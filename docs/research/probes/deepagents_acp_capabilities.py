"""Offline probes of unmodified pinned AgentServerACP; no model/network calls."""
import asyncio
import importlib.metadata
import json
import os
import socket
import tempfile
from pathlib import Path

os.environ["LANGSMITH_TRACING"] = "false"
os.environ["LANGCHAIN_TRACING_V2"] = "false"


class OfflineSocket(socket.socket):
    def connect(self, *args, **kwargs):
        raise RuntimeError("network connections are disabled in this probe")

    def connect_ex(self, *args, **kwargs):
        raise RuntimeError("network connections are disabled in this probe")


socket.socket = OfflineSocket

from acp import text_block
from acp.agent.router import build_agent_router
from acp.connection import Connection
from acp.schema import AllowedOutcome, RequestPermissionResponse, SessionMode, SessionModeState
from langchain.agents import create_agent
from langchain.agents.middleware import HumanInTheLoopMiddleware, ModelCallLimitMiddleware
from langchain_core.callbacks import AsyncCallbackHandler
from langchain_core.messages import AIMessage
from langchain_core.tools import tool
from langgraph.checkpoint.memory import MemorySaver
from langgraph.graph import StateGraph, MessagesState, START, END
from langgraph.types import interrupt
from deepagents.backends import LocalShellBackend
from deepagents_acp.server import AgentServerACP
from langchain_core.language_models.fake_chat_models import GenericFakeChatModel as BaseFakeChatModel


class GenericFakeChatModel(BaseFakeChatModel):
    """Use LangChain's scripted model without requiring a real tool binding."""

    def bind_tools(self, tools, **kwargs):
        return self


class Client:
    def __init__(self, outcome="approve"):
        self.events, self.requests, self.outcome = [], [], outcome
        self.permission_started = asyncio.Event()
        self.permission_release = None

    async def session_update(self, session_id, update, **kwargs):
        self.events.append(update.model_dump(by_alias=True, exclude_none=True))

    async def request_permission(self, **kwargs):
        self.requests.append(kwargs)
        self.permission_started.set()
        if self.permission_release is not None:
            await self.permission_release.wait()
        return RequestPermissionResponse(outcome=AllowedOutcome(outcome="selected", option_id=self.outcome))


def model(*messages):
    return GenericFakeChatModel(messages=iter(messages), disable_streaming=True)


async def server(graph, *, load=False, client=None, modes=None):
    s = AgentServerACP(graph, load_sessions=load, modes=modes)
    c = client or Client()
    s.on_connect(c)
    session = await s.new_session(cwd="/private/tmp", mcp_servers=[])
    return s, c, session.session_id


async def ask(s, sid, text="hello", **kwargs):
    return await s.prompt([text_block(text)], session_id=sid, **kwargs)


def state_graph(node, saver=None):
    builder = StateGraph(MessagesState)
    builder.add_node("work", node)
    builder.add_edge(START, "work")
    builder.add_edge("work", END)
    return builder.compile(checkpointer=saver or MemorySaver())


async def main():
    out = {"versions": {n: importlib.metadata.version(n) for n in ("deepagents", "langchain", "langgraph", "agent-client-protocol")}}
    saver = MemorySaver()
    graph = create_agent(model(AIMessage(content="first"), AIMessage(content="handoff")), checkpointer=saver)
    s, c, sid = await server(graph, load=True)
    await ask(s, sid, "task")
    await ask(s, sid, "write handoff")
    state = await graph.aget_state({"configurable": {"thread_id": sid}})
    restarted = AgentServerACP(graph, load_sessions=True)
    replay_client = Client()
    restarted.on_connect(replay_client)
    await restarted.load_session(cwd="/private/tmp", session_id=sid, mcp_servers=[])
    bad_cwd = None
    try:
        await restarted.load_session(cwd="/different", session_id=sid, mcp_servers=[])
    except Exception as exc:
        bad_cwd = type(exc).__name__
    out["multi_prompt_and_load"] = {"history": [(m.type, m.content) for m in state.values["messages"]], "replayed_updates": [e["sessionUpdate"] for e in replay_client.events], "different_cwd": bad_cwd}

    effects = []
    @tool
    async def guarded(value: str) -> str:
        """Record a controlled local effect."""
        effects.append(value)
        return value
    for decision in ("approve", "reject"):
        graph = create_agent(model(AIMessage(content="", tool_calls=[{"name":"guarded","args":{"value":decision},"id":"call1"}]), AIMessage(content="done")), tools=[guarded], middleware=[HumanInTheLoopMiddleware(interrupt_on={"guarded":True})], checkpointer=MemorySaver())
        s, c, sid = await server(graph, client=Client(decision))
        response = await ask(s, sid)
        out["permission_" + decision] = {"requests":len(c.requests), "effect_executed":decision in effects, "stop":response.stop_reason}

    modes = SessionModeState(current_mode_id="approve", available_modes=[SessionMode(id="approve",name="Approve"),SessionMode(id="auto",name="Auto")])
    factory_modes = []
    def factory(context):
        factory_modes.append(context.mode)
        return create_agent(model(AIMessage(content="done")), checkpointer=MemorySaver())
    s, c, sid = await server(factory, modes=modes)
    await s.set_session_mode(mode_id="auto", session_id=sid)
    out["approval_floor"] = {"factory_modes_after_client_downgrade":factory_modes, "selected":s._session_modes[sid]}

    @tool
    async def ask_user(question: str) -> str:
        """Ask a question through a plain LangGraph interrupt."""
        return interrupt(question)
    graph = create_agent(model(AIMessage(content="",tool_calls=[{"name":"ask_user","args":{"question":"Which database?"},"id":"q1"}])),tools=[ask_user],checkpointer=MemorySaver())
    s, c, sid = await server(graph)
    try:
        await ask(s,sid)
    except Exception as exc:
        out["plain_ask_user_interrupt"] = {"error":type(exc).__name__,"code":getattr(exc,"code",None),"message":str(exc),"permission_requests":len(c.requests)}

    started, release = asyncio.Event(), asyncio.Event()
    async def block(state):
        started.set()
        await release.wait()
        return {"messages":[AIMessage(content="unblocked")]}
    s,c,sid = await server(state_graph(block))
    pending = asyncio.create_task(ask(s,sid))
    await started.wait()
    steer_result = await build_agent_router(s)("_goose/unstable/session/steer",{"sessionId":sid,"prompt":[{"type":"text","text":"change direction"}]},False)
    await s.cancel(session_id=sid)
    await asyncio.sleep(0.10)
    still_waiting = not pending.done()
    release.set()
    response = await pending
    out["active_cancel_and_steer"] = {"steer_result":steer_result,"prompt_still_blocked_100ms_after_cancel":still_waiting,"eventual_stop":response.stop_reason}

    permission_client=Client()
    permission_client.permission_release=asyncio.Event()
    graph=create_agent(model(AIMessage(content="",tool_calls=[{"name":"guarded","args":{"value":"aftercancel"},"id":"wait1"}]),AIMessage(content="done")),tools=[guarded],middleware=[HumanInTheLoopMiddleware(interrupt_on={"guarded":True})],checkpointer=MemorySaver())
    s,c,sid=await server(graph,client=permission_client)
    pending=asyncio.create_task(ask(s,sid))
    await c.permission_started.wait()
    await s.cancel(session_id=sid)
    await asyncio.sleep(.1)
    still_waiting=not pending.done()
    c.permission_release.set()
    response=await pending
    out["cancel_waiting_permission"]={"still_waiting_after_cancel":still_waiting,"eventual_stop":response.stop_reason,"effect_executed":"aftercancel" in effects}

    with tempfile.TemporaryDirectory(prefix="official-acp-shell-",dir="/private/tmp") as tmp:
        started_file=Path(tmp,"started")
        effect_file=Path(tmp,"completed")
        backend=LocalShellBackend(root_dir=tmp,virtual_mode=False,inherit_env=True)
        async def shell(state):
            await backend.aexecute(f"touch {started_file}; sleep 0.6; touch {effect_file}")
            return {"messages":[AIMessage(content="shell finished")]}
        s,c,sid=await server(state_graph(shell))
        pending=asyncio.create_task(ask(s,sid))
        while not started_file.exists():
            await asyncio.sleep(.01)
        await s.cancel(session_id=sid)
        await asyncio.sleep(.1)
        still_waiting=not pending.done()
        response=await pending
        out["shell_cancel"]={"prompt_still_waiting":still_waiting,"shell_effect_after_cancel":effect_file.exists(),"eventual_stop":response.stop_reason}

    count=[]
    @tool
    async def count_call() -> str:
        """Count a controlled tool call."""
        count.append(1)
        return "ok"
    usage={"input_tokens":10,"output_tokens":2,"total_tokens":12}
    messages=[AIMessage(content="",tool_calls=[{"name":"count_call","args":{},"id":f"c{i}"}],usage_metadata=usage) for i in range(2)]
    messages.append(AIMessage(content="done",usage_metadata=usage))
    graph=create_agent(model(*messages),tools=[count_call],checkpointer=MemorySaver())
    s,c,sid=await server(graph)
    response=await ask(s,sid,field_meta={"lightspeed":{"maxTurns":1}})
    state=await graph.aget_state({"configurable":{"thread_id":sid}})
    out["usage_and_requested_budget"]={"requested_maxTurns":1,"actual_model_messages":sum(m.type=="ai" for m in state.values["messages"]),"tools_executed":len(count),"usage_in_checkpoint":[m.usage_metadata for m in state.values["messages"] if m.type=="ai"],"response":response.model_dump(by_alias=True,exclude_none=True),"usage_updates":[e for e in c.events if e["sessionUpdate"]=="usage_update"]}

    graph=create_agent(model(AIMessage(content="partial",response_metadata={"stop_reason":"max_tokens"})),checkpointer=MemorySaver())
    s,c,sid=await server(graph)
    response=await ask(s,sid)
    out["native_cutoff_without_graph_middleware"]={"response":response.model_dump(by_alias=True,exclude_none=True)}

    # Existing graph/SDK seams can supply accounting and stop calls without a
    # second ACP implementation; generic server still needs outcome projection.
    observations=[]
    class Observer(AsyncCallbackHandler):
        async def on_llm_end(self,response,**kwargs):
            observations.extend(g.message.usage_metadata for choices in response.generations for g in choices)
    class ConfiguredServer(AgentServerACP):
        def _session_config(self,sid):
            return {**super()._session_config(sid),"callbacks":[Observer()]}
    before=len(count)
    messages=[AIMessage(content="",tool_calls=[{"name":"count_call","args":{},"id":"limit1"}],usage_metadata=usage),AIMessage(content="should not be consumed")]
    graph=create_agent(model(*messages),tools=[count_call],middleware=[ModelCallLimitMiddleware(run_limit=1)],checkpointer=MemorySaver())
    s=ConfiguredServer(graph)
    c=Client()
    s.on_connect(c)
    session=await s.new_session(cwd="/private/tmp",mcp_servers=[])
    response=await ask(s,session.session_id)
    state=await graph.aget_state({"configurable":{"thread_id":session.session_id}})
    out["existing_graph_middleware_and_callback_seams"]={"completed_model_calls_observed":len(observations),"observed_usage":observations,"tools_executed":len(count)-before,"checkpoint_run_model_call_count":state.values.get("run_model_call_count"),"response":response.model_dump(by_alias=True,exclude_none=True),"terminal_message":state.values["messages"][-1].content}

    active=0
    maximum_active=0
    both_started=asyncio.Event()
    release_both=asyncio.Event()
    async def overlap(state):
        nonlocal active,maximum_active
        active+=1
        maximum_active=max(maximum_active,active)
        if active==2:
            both_started.set()
        await release_both.wait()
        active-=1
        return {"messages":[AIMessage(content="finished")]}
    s,c,sid=await server(state_graph(overlap))
    first=asyncio.create_task(ask(s,sid,"one"))
    await asyncio.sleep(.05)
    second=asyncio.create_task(ask(s,sid,"two"))
    try:
        await asyncio.wait_for(both_started.wait(),.5)
    finally:
        release_both.set()
        await asyncio.gather(first,second)
    out["concurrent_same_session"]={"maximum_active_graph_invocations":maximum_active}

    async def fails(state):
        raise RuntimeError("fixture-sensitive-provider-detail")
    s,c,sid=await server(state_graph(fails))
    frames=[]
    class Sink:
        async def send(self,payload):
            frames.append(payload)
    conn=Connection.__new__(Connection)
    conn._handler=build_agent_router(s)
    conn._sender=Sink()
    conn._observers=[]
    try:
        await conn._run_request({"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"sessionId":sid,"prompt":[{"type":"text","text":"fail"}]}})
    except Exception as exc:
        out["sdk_error_projection"]={"error_type":type(exc).__name__,"wire_error":frames[-1]["error"]}
    assert len(out["multi_prompt_and_load"]["history"]) == 4
    assert out["permission_approve"]["effect_executed"] is True
    assert out["permission_reject"]["effect_executed"] is False
    assert out["approval_floor"]["selected"] == "auto"
    assert out["plain_ask_user_interrupt"]["code"] == -32600
    assert out["active_cancel_and_steer"]["steer_result"] is None
    assert out["active_cancel_and_steer"]["prompt_still_blocked_100ms_after_cancel"] is True
    assert out["cancel_waiting_permission"]["still_waiting_after_cancel"] is True
    assert out["shell_cancel"]["shell_effect_after_cancel"] is True
    assert out["usage_and_requested_budget"]["actual_model_messages"] == 3
    assert out["usage_and_requested_budget"]["usage_updates"] == []
    assert out["native_cutoff_without_graph_middleware"]["response"]["stopReason"] == "end_turn"
    assert out["existing_graph_middleware_and_callback_seams"]["completed_model_calls_observed"] == 1
    assert out["existing_graph_middleware_and_callback_seams"]["tools_executed"] == 1
    assert out["concurrent_same_session"]["maximum_active_graph_invocations"] == 2
    assert out["sdk_error_projection"]["wire_error"]["data"]["details"] == "fixture-sensitive-provider-detail"
    print(json.dumps(out,indent=2))


asyncio.run(main())
