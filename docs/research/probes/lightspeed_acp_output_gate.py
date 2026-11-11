"""Disposable live-only graph proxy experiment; official server is unmodified.

Run with current Lightspeed src and pinned ACP staging on PYTHONPATH. No
network/model calls. The in-memory ledger is deliberately NOT durable replay
proof. Graph, inspector orchestration, redaction and acceptance callbacks are real.
"""
import asyncio
import json
import os
import socket
import tempfile
from types import SimpleNamespace
from unittest.mock import patch

os.environ["LANGSMITH_TRACING"]="false"
os.environ["LANGCHAIN_TRACING_V2"]="false"
class OfflineSocket(socket.socket):
    def connect(self,*args,**kwargs): raise RuntimeError("offline probe")
    def connect_ex(self,*args,**kwargs): raise RuntimeError("offline probe")
socket.socket=OfflineSocket

import deepagents
from acp import text_block
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage, AIMessageChunk, ToolMessage
from langchain_core.outputs import ChatGenerationChunk
from langchain_core.tools import tool
from deepagents_acp.server import AgentServerACP
from lightspeed_agentic.providers.deepagents import DeepAgentsProvider
from lightspeed_agentic.providers.model_router import ModelParameters, ResolvedModelRoute
from lightspeed_agentic.inspection.models import ClassifierDecision
from lightspeed_agentic.types import ProviderQueryOptions

try:
    from lightspeed_agentic.providers.tool_result_middleware import tool_result_signature
except ImportError:
    from lightspeed_agentic.inspection.middleware import ToolResultInspectionMiddleware
    tool_result_signature=ToolResultInspectionMiddleware._signature


def signature(message):
    return tool_result_signature(message.name or "", "error" if message.status=="error" else "result", message.tool_call_id or "", message.content)


class OutputGate:
    """Transform graph streams, never reimplement ACP prompt handling.

    Exact approvals are scoped to a source graph namespace and current invocation.
    Acknowledgements release only currently buffered occurrences; they never
    grant future duplicate content general trust. Unknown/missing records fail
    closed. aget_state/history are intentionally untouched in this live probe.
    """
    def __init__(self,graph,trace,*,tamper=None):
        self.graph,self.trace,self.tamper=graph,trace,tamper
        self.ledger=[]

    def __getattr__(self,name): return getattr(self.graph,name)
    @property
    def checkpointer(self): return self.graph.checkpointer
    @checkpointer.setter
    def checkpointer(self,value): self.graph.checkpointer=value

    async def astream(self,value,*,config,stream_mode,subgraphs=True,**kwargs):
        pending=[]
        boundary=0
        modes=list(dict.fromkeys([*stream_mode,"custom"]))
        try:
            async for frame in self.graph.astream(value,config=config,stream_mode=modes,subgraphs=True,**kwargs):
                namespace,kind,data=frame
                namespace=tuple(namespace)
                if kind=="messages" and isinstance(data[0],ToolMessage):
                    message=data[0]
                    if self.tamper=="content":
                        message=message.model_copy(update={"content":str(message.content)+" CHANGED-AFTER-INSPECTION-SIGNATURE"})
                        frame=(namespace,kind,(message,data[1]))
                    self.trace.append({"event":"buffer","namespace":namespace,"call_id":message.tool_call_id,"signature":signature(message)})
                    pending.append((namespace,message,frame))
                    continue
                if kind=="custom" and isinstance(data,dict) and "lightspeed.accepted_tool_results" in data:
                    boundary+=1
                    accepted=set(data["lightspeed.accepted_tool_results"])
                    if self.tamper=="namespace": namespace=("unrelated-source",)
                    self.trace.append({"event":"acceptance","namespace":namespace,"signatures":list(accepted),"boundary":boundary})
                    remaining=[]
                    release=[]
                    for source,message,buffered in pending:
                        sig=signature(message)
                        if source==namespace and sig in accepted:
                            self.ledger.append({"thread":config["configurable"]["thread_id"],"namespace":source,"signature":sig,"message_id":message.id,"boundary":boundary})
                            release.append(buffered)
                        else: remaining.append((source,message,buffered))
                    pending=remaining
                    # All siblings are classified before this single acknowledgement.
                    for approved in release:
                        self.trace.append({"event":"release","namespace":approved[0],"call_id":approved[2][0].tool_call_id,"signature":signature(approved[2][0])})
                        yield approved
                    continue
                if kind in stream_mode:
                    yield frame
            if pending:
                raise RuntimeError("unaccepted tool result remains at stream completion")
        finally:
            if pending:
                self.trace.append({"event":"discard","count":len(pending)})


class Captured(BaseException): pass


async def case(name,*,siblings=1,decision="pass",cancel=None,tamper=None,nested=False,cross_namespace=False):
    trace=[]
    captured={}
    classifier_started=asyncio.Event()
    classifier_release=asyncio.Event()
    class Model(FakeMessagesListChatModel):
        def bind_tools(self,_tools,**kwargs): return self
        def _generate(self,messages,stop=None,run_manager=None,**kwargs):
            trace.append({"event":"model","tool_result_ids":[m.tool_call_id for m in messages if isinstance(m,ToolMessage)]})
            return super()._generate(messages,stop,run_manager,**kwargs)
        def _stream(self,messages,stop=None,run_manager=None,**kwargs):
            response=self._generate(messages,stop,run_manager,**kwargs).generations[0].message
            chunks=[{"name":c["name"],"args":json.dumps(c["args"]),"id":c["id"],"index":i,"type":"tool_call_chunk"} for i,c in enumerate(response.tool_calls)]
            yield ChatGenerationChunk(message=AIMessageChunk(content=response.content,tool_call_chunks=chunks,chunk_position="last"))
    calls=[{"name":"lookup_probe","args":{"marker":f"RESULT-{i}"},"id":f"probe-{i}","type":"tool_call"} for i in range(siblings)]
    replies=[AIMessage(content="",tool_calls=calls),AIMessage(content="Done")]
    if nested:
        replies=[AIMessage(content="",tool_calls=[{"name":"task","args":{"description":"Call lookup_probe once then finish","subagent_type":"general-purpose"},"id":"delegate","type":"tool_call"}]),*replies,AIMessage(content="Outer done")]
        if cross_namespace:
            root_calls=json.loads(json.dumps(calls))
            if decision=="root_payload_reject": root_calls[0]["args"]["marker"]="ROOT-RESULT-REJECT"
            replies[0].tool_calls.extend(root_calls)
    model=Model(responses=replies)
    class Classifier:
        result_observations=0
        async def classify(self,request,**kwargs):
            trace.append({"event":"classifier","content":request.content})
            classifier_started.set()
            if cancel: await classifier_release.wait()
            if decision=="technical_failure": raise ValueError("scripted classifier failure")
            if "RESULT-0" in request.content: self.result_observations+=1
            rejected=decision=="reject" or decision=="mixed" and "RESULT-1" in request.content or decision=="root_payload_reject" and "ROOT-RESULT-REJECT" in request.content
            return ClassifierDecision(injectionDetected=rejected,category="unknown" if rejected else "none")
    @tool
    async def lookup_probe(marker:str)->str:
        """Return a controlled local result."""
        trace.append({"event":"tool","marker":marker})
        return f"{marker} password=OFFLINE-CREDENTIAL"
    original=deepagents.create_deep_agent
    def capture(**kwargs):
        kwargs["tools"]=[lookup_probe]
        captured["graph"]=original(**kwargs)
        raise Captured()
    creates=iter([model,SimpleNamespace(profile={},disable_streaming=False)])
    route=ResolvedModelRoute(route="anthropic",model="offline-model",create_model=lambda*a:next(creates),model_parameters=ModelParameters(model="offline-model"),telemetry_provider="anthropic",structured_output_method="function_calling",create_classifier_client=lambda*a:Classifier())
    provider=DeepAgentsProvider(route)
    with tempfile.TemporaryDirectory(prefix="acp-output-gate-") as cwd:
        try:
            with patch("deepagents.create_deep_agent",capture):
                async for _ in provider.query(ProviderQueryOptions(prompt="probe",system_prompt="Probe",max_turns=50,cwd=cwd)): pass
        except Captured: pass
        proxy=OutputGate(captured["graph"],trace,tamper=tamper)
        class Client:
            async def session_update(self,**kwargs):
                data=kwargs["update"].model_dump(mode="json",exclude_none=True)
                trace.append({"event":"viewer","update":data})
        server=AgentServerACP(lambda context:proxy)
        server.on_connect(Client())
        session=await server.new_session(cwd=cwd,mcp_servers=[])
        worker=asyncio.create_task(server.prompt(prompt=[text_block("Run probe")],session_id=session.session_id))
        if cancel:
            await asyncio.wait_for(classifier_started.wait(),5)
            if cancel=="official_flag": await server.cancel(session_id=session.session_id)
            else: worker.cancel()
            await asyncio.sleep(.05)
            trace.append({"event":"cancel_observed","prompt_done":worker.done()})
            classifier_release.set()
        try:
            response=await asyncio.wait_for(worker,8)
            outcome=response.stop_reason
        except BaseException as exc: outcome=type(exc).__name__
    viewers=[e for e in trace if e["event"]=="viewer" and e["update"].get("session_update")=="tool_call_update" and "RESULT-" in json.dumps(e["update"])]
    return {"name":name,"outcome":outcome,"result_updates":len(viewers),"ledger":proxy.ledger,"trace":trace}


async def main():
    cases=[]
    for name,kwargs in [
        ("single_pass",{}),
        ("single_reject",{"decision":"reject"}),
        ("single_technical_failure",{"decision":"technical_failure"}),
        ("siblings_pass",{"siblings":2}),
        ("siblings_mixed_reject",{"siblings":2,"decision":"mixed"}),
        ("exact_content_mismatch",{"tamper":"content"}),
        ("namespace_mismatch",{"tamper":"namespace"}),
        ("cancel_flag_during_classifier",{"cancel":"official_flag"}),
        ("cancel_task_during_classifier",{"cancel":"task"}),
        ("nested_pass",{"nested":True}),
        ("nested_reject",{"nested":True,"decision":"reject"}),
        ("child_accept_must_not_release_identical_root_result",{"nested":True,"cross_namespace":True}),
        ("child_pass_root_payload_reject",{"nested":True,"cross_namespace":True,"decision":"root_payload_reject"}),
    ]:
        result=await case(name,**kwargs)
        cases.append(result)
    by_name={c["name"]:c for c in cases}
    assert by_name["single_pass"]["result_updates"]==1
    assert by_name["siblings_pass"]["result_updates"]==2
    for name in ("single_reject","single_technical_failure","siblings_mixed_reject","nested_reject"):
        assert by_name[name]["outcome"]=="ToolResultSafetyInspectionFailed"
        assert by_name[name]["ledger"]==[] and by_name[name]["result_updates"]==0
    for name in ("exact_content_mismatch","namespace_mismatch"):
        assert by_name[name]["outcome"]=="RuntimeError"
        assert by_name[name]["ledger"]==[] and by_name[name]["result_updates"]==0
    for name in ("cancel_flag_during_classifier","cancel_task_during_classifier"):
        assert by_name[name]["result_updates"]==0
    nested=by_name["nested_pass"]
    assert any(e["namespace"] for e in nested["ledger"])
    assert any(not e["namespace"] for e in nested["ledger"])
    identical=by_name["child_accept_must_not_release_identical_root_result"]
    trace=identical["trace"]
    parent_accept=max(i for i,e in enumerate(trace) if e["event"]=="acceptance" and not e["namespace"])
    parent_release=[i for i,e in enumerate(trace) if e["event"]=="release" and not e["namespace"]]
    assert parent_release and all(i>parent_accept for i in parent_release)
    assert any(e["event"]=="release" and e["namespace"] for e in trace[:parent_accept])
    rejected_root=by_name["child_pass_root_payload_reject"]
    assert rejected_root["outcome"]=="ToolResultSafetyInspectionFailed"
    assert len(rejected_root["ledger"])==1 and rejected_root["ledger"][0]["namespace"]
    assert not any(e["event"]=="viewer" and "ROOT-RESULT-REJECT password=" in json.dumps(e) for e in rejected_root["trace"])
    print(json.dumps({"source":__import__("lightspeed_agentic.providers.deepagents",fromlist=[""]).__file__,"cases":cases},indent=2))


if __name__=="__main__":
    asyncio.run(main())
