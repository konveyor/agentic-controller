"""Disposable offline checkpoint/display-view experiment; not production code."""
import asyncio, copy, hashlib, json, os, pickle, tempfile
from collections import defaultdict
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
import deepagents
import importlib.util
_gate_spec=importlib.util.spec_from_file_location("live_output_gate",Path(__file__).with_name("lightspeed_acp_output_gate.py"))
_gate_module=importlib.util.module_from_spec(_gate_spec);_gate_spec.loader.exec_module(_gate_module)
OutputGate=_gate_module.OutputGate
from acp import text_block
from deepagents_acp.server import AgentServerACP
from langchain.agents.middleware import AgentMiddleware
from langchain_core.language_models.fake_chat_models import FakeMessagesListChatModel
from langchain_core.messages import AIMessage, AIMessageChunk, ToolMessage, RemoveMessage
from langchain_core.outputs import ChatGenerationChunk
from langchain_core.tools import tool
from langgraph.checkpoint.memory import InMemorySaver
from lightspeed_agentic.providers.deepagents import DeepAgentsProvider
from lightspeed_agentic.providers.model_router import ModelParameters, ResolvedModelRoute
from lightspeed_agentic.inspection.models import ClassifierDecision
from lightspeed_agentic.types import ProviderQueryOptions

class Captured(BaseException): pass

def signature(message):
    try:
        from lightspeed_agentic.providers.tool_result_middleware import tool_result_signature
        return tuple(tool_result_signature(message.name or '', 'error' if message.status=='error' else 'result', message.tool_call_id or '', message.content))
    except ImportError:
        from lightspeed_agentic.inspection.middleware import ToolResultInspectionMiddleware
        return tuple(ToolResultInspectionMiddleware._signature(message.name or '', 'error' if message.status=='error' else 'result', message.tool_call_id or '', message.content))

def record_message(message):
    return {'id':message.id,'type':message.type,'name':getattr(message,'name',None),'call_id':getattr(message,'tool_call_id',None),'content':message.content,'additional_kwargs':message.additional_kwargs}

def ledger_key(thread, namespace, message):
    return (thread, tuple(namespace), signature(message))

class HistoryView:
    """Display-only checkpoint history view; graph invocation/current state unmodified."""
    def __init__(self, graph, released, merge_guard=True):
        self.graph=graph; self.released=released;self.merge_guard=merge_guard
    def __getattr__(self,name): return getattr(self.graph,name)
    async def aget_state_history(self, config, *args, **kwargs):
        thread=config['configurable']['thread_id']
        snapshots=[s async for s in self.graph.aget_state_history(config,*args,**kwargs)]
        merged={}
        for snapshot in reversed(snapshots):
            for m in snapshot.values.get('messages',[]):merged[m.id]=m
        authorized_ids={m.id for m in merged.values() if isinstance(m,ToolMessage) and ledger_key(thread,(),m) in self.released}
        for snapshot in snapshots:
            values=dict(snapshot.values)
            values['messages']=[m for m in values.get('messages',[]) if not isinstance(m,ToolMessage) or (ledger_key(thread,(),m) in self.released and (not self.merge_guard or m.id in authorized_ids))]
            yield snapshot._replace(values=values)

class Client:
    def __init__(self): self.events=[]
    async def session_update(self,**kwargs):
        self.events.append(kwargs['update'].model_dump(mode='json',exclude_none=True))

class JournalPublisher:
    def __init__(self, server, transport):self.server=server;self.transport=transport
    def __getattr__(self,name):return getattr(self.transport,name)
    async def session_update(self,**kwargs):
        self.server.display_journal[kwargs['session_id']].append(copy.deepcopy(kwargs))
        await self.transport.session_update(**kwargs)

class JournalServer(AgentServerACP):
    """Only wrap publication and replace replay; inherited prompt loop is unchanged."""
    def __init__(self,*args,**kwargs):
        super().__init__(*args,**kwargs);self.display_journal=defaultdict(list)
    def on_connect(self, conn):
        self.raw_transport=conn
        super().on_connect(JournalPublisher(self,conn))
    async def _replay_session(self,session_id,agent):
        for event in list(self.display_journal[session_id]):
            await self.raw_transport.session_update(**copy.deepcopy(event))

def visible_tool_results(client):
    return [e for e in client.events if e.get('session_update')=='tool_call_update' and e.get('content')]

def checkpoint_fingerprint(graph, config):
    return None

async def case(scenario, driver="direct"):
    trace=[]; captured={}; released={}; accepted_records=[]; pending={}
    inspection_started=asyncio.Event(); unblock=asyncio.Event()
    saver=InMemorySaver()
    class Model(FakeMessagesListChatModel):
        def bind_tools(self,_tools,**kwargs): return self
        def _generate(self,messages,stop=None,run_manager=None,**kwargs):
            trace.append({'event':'model','tool_contents':[m.content for m in messages if isinstance(m,ToolMessage)]})
            return super()._generate(messages,stop,run_manager,**kwargs)
        def _stream(self,messages,stop=None,run_manager=None,**kwargs):
            response=self._generate(messages,stop,run_manager,**kwargs).generations[0].message
            chunks=[{'name':c['name'],'args':json.dumps(c['args']),'id':c['id'],'index':i,'type':'tool_call_chunk'} for i,c in enumerate(response.tool_calls)]
            yield ChatGenerationChunk(message=AIMessageChunk(content=response.content,tool_call_chunks=chunks,chunk_position='last'))
    calls=[{'name':'lookup_a','args':{},'id':'call-a','type':'tool_call'}]
    if scenario=='mixed_siblings': calls.append({'name':'lookup_b','args':{},'id':'call-b','type':'tool_call'})
    replies=[AIMessage(content='',tool_calls=calls),AIMessage(content='Finished'),AIMessage(content='Follow-up finished')]
    if scenario.startswith('nested'):
        replies=[AIMessage(content='',tool_calls=[{'name':'task','args':{'description':'Call lookup_a then report','subagent_type':'general-purpose'},'id':'delegate','type':'tool_call'}]),AIMessage(content='',tool_calls=calls),AIMessage(content='CHILD-REPORT-REPLAY'),AIMessage(content='Root finished')]
    model=Model(responses=replies)
    class Classifier:
        async def classify(self,request,**kwargs):
            trace.append({'event':'classify','tool':request.tool_name,'content':request.content})
            inspection_started.set()
            if scenario=='pending_cancel': await unblock.wait()
            if scenario=='classifier_error': raise ValueError('offline classifier failure')
            reject=scenario in {'reject','removed_after_reject'} or (scenario=='nested_parent_reject' and request.tool_name=='task') or (scenario=='mixed_siblings' and request.tool_name=='lookup_b')
            return ClassifierDecision(injectionDetected=reject,category='unknown' if reject else 'none')
    @tool
    def lookup_a()->str:
        """Return the first offline fixture."""
        trace.append({'event':'tool_return','tool':'lookup_a','content':'REPLAY-A password=OFFLINE-CREDENTIAL'})
        return 'REPLAY-A password=OFFLINE-CREDENTIAL'
    @tool
    def lookup_b()->str:
        """Return the rejected sibling offline fixture."""
        trace.append({'event':'tool_return','tool':'lookup_b','content':'REPLAY-B-REJECTED'})
        return 'REPLAY-B-REJECTED'
    class Transform(AgentMiddleware):
        async def awrap_tool_call(self,request,handler):
            result=await handler(request)
            if isinstance(result,ToolMessage):
                result=result.model_copy(update={'content':'TRANSFORMED-REPLAY-A password=OFFLINE-CREDENTIAL'})
            return result
    create=deepagents.create_deep_agent
    def capture(**kwargs):
        kwargs['tools']=[lookup_a,lookup_b]
        kwargs['checkpointer']=saver
        if scenario=='transformed': kwargs['middleware']=[*kwargs['middleware'],Transform()]
        captured['inspection']=kwargs['middleware'][0]
        captured['kwargs']=kwargs
        captured['graph']=create(**kwargs)
        raise Captured()
    creates=iter([model,SimpleNamespace(profile={},disable_streaming=False)])
    route=ResolvedModelRoute(route='anthropic',model='offline',create_model=lambda *a:next(creates),model_parameters=ModelParameters(model='offline'),telemetry_provider='anthropic',structured_output_method='function_calling',create_classifier_client=lambda *a:Classifier())
    provider=DeepAgentsProvider(route)
    with tempfile.TemporaryDirectory(prefix='replay-boundary-') as cwd:
        try:
            with patch('deepagents.create_deep_agent',capture):
                async for _ in provider.query(ProviderQueryOptions(prompt='capture',system_prompt='Offline fixture',max_turns=50,cwd=cwd)): pass
        except Captured: pass
        graph=captured['graph']
        seed=AgentServerACP(graph,load_sessions=True); seed.on_connect(Client())
        sid=(await seed.new_session(cwd=cwd,mcp_servers=[])).session_id
        config=seed._session_config(sid)
        async def run_prompt(text):
            async for namespace,kind,data in graph.astream({'messages':[{'role':'user','content':text}]},config=config,stream_mode=['messages','custom','updates'],subgraphs=True):
                if kind=='messages' and isinstance(data[0],ToolMessage):
                    m=data[0]; pending[(tuple(namespace),signature(m))]=m
                    trace.append({'event':'tool_stream','message':record_message(m)})
                elif kind=='custom' and isinstance(data,dict) and 'lightspeed.accepted_tool_results' in data:
                    signatures={tuple(x) for x in data['lightspeed.accepted_tool_results']}
                    record={'event':'accepted','namespace':list(namespace),'signatures':[list(x) for x in signatures]}
                    accepted_records.append(record);trace.append(record)
                    for key,m in list(pending.items()):
                        ns,sig=key
                        if ns==tuple(namespace) and sig in signatures:
                            released[ledger_key(sid,namespace,m)]={'message_id':m.id,'content':m.content,'source':'consumed acceptance for streamed result'}
                            pending.pop(key)
        mid=None
        gate=None
        if driver in {'combined','journal'}:
            gate=OutputGate(graph,trace)
            live_server=(JournalServer if driver=='journal' else AgentServerACP)(lambda _ctx:gate,load_sessions=True);live_client=Client();live_server.on_connect(live_client)
            await live_server.load_session(cwd=cwd,session_id=sid,mcp_servers=[])
            task=asyncio.create_task(live_server.prompt([text_block('Read offline fixture')],session_id=sid))
        else:task=asyncio.create_task(run_prompt('Read offline fixture'))
        if scenario=='pending_cancel':
            await asyncio.wait_for(inspection_started.wait(),5)
            mid=await graph.aget_state(config)
            task.cancel()
        try: await task; outcome='end_turn'
        except BaseException as exc: outcome=type(exc).__name__
        if driver in {'combined','journal'}:
            released={(e['thread'],tuple(e['namespace']),tuple(e['signature'])):{'message_id':e['message_id'],'boundary':e['boundary'],'source':'OutputGate consumed live acceptance'} for e in gate.ledger}
            accepted_records=[e for e in trace if e['event']=='acceptance']
        after=await graph.aget_state(config)
        tool_messages=[m for m in after.values.get('messages',[]) if isinstance(m,ToolMessage)]
        before_mutation=[record_message(m) for m in tool_messages]
        if scenario=='modified_after_pass':
            m=tool_messages[0]
            await graph.aupdate_state(config,{'messages':[m.model_copy(update={'content':'ALTERED-AFTER-ACCEPTANCE'})]})
        if scenario in {'removed_after_pass','removed_after_reject'}:
            await graph.aupdate_state(config,{'messages':[RemoveMessage(id=tool_messages[0].id)]})
        if scenario=='repeat_prompt':
            await run_prompt('Follow-up: explain the prior result')
        latest=await graph.aget_state(config)
        snapshots=[s async for s in graph.aget_state_history(config)]
        # Fresh server objects simulate a reconnect. Same checkpoint storage is intentionally retained.
        async def replay(agent):
            c=Client();server=AgentServerACP(lambda _ctx:agent,load_sessions=True);server.on_connect(c)
            await server.load_session(cwd=cwd,session_id=sid,mcp_servers=[])
            return c
        plain=await replay(graph)
        filtered=await replay(HistoryView(graph,released))
        naive=await replay(HistoryView(graph,released,merge_guard=False))
        empty=await replay(HistoryView(graph,{}))
        def plain_dicts(value):
            if isinstance(value,dict):return {k:plain_dicts(v) for k,v in value.items()}
            if isinstance(value,list):return [plain_dicts(v) for v in value]
            if isinstance(value,tuple):return tuple(plain_dicts(v) for v in value)
            return value
        storage_bytes=pickle.dumps({name:plain_dicts(getattr(saver,name)) for name in ['storage','writes','blobs']})
        new_saver=InMemorySaver()
        for name,value in pickle.loads(storage_bytes).items():getattr(new_saver,name).update(value)
        old_inspection=captured['inspection']
        middleware_options={'on_accepted':old_inspection._on_accepted}
        if hasattr(old_inspection,'_redaction_rules'):
            middleware_options.update(redaction_rules=old_inspection._redaction_rules,on_tool_result=old_inspection._on_tool_result)
        if hasattr(old_inspection,'_trusted_tool_names'):middleware_options['trusted_tool_names']=old_inspection._trusted_tool_names
        fresh_inspection=type(old_inspection)(old_inspection._inspector,**middleware_options)
        fresh_kwargs={**captured['kwargs'],'checkpointer':new_saver,'middleware':[fresh_inspection,*captured['kwargs']['middleware'][1:]]}
        fresh_graph=create(**fresh_kwargs)
        serialized_ledger=json.dumps([{'thread':k[0],'namespace':list(k[1]),'signature':list(k[2]),'entry':v} for k,v in released.items()])
        restored_ledger={(e['thread'],tuple(e['namespace']),tuple(e['signature'])):e['entry'] for e in json.loads(serialized_ledger)}
        fresh_plain=await replay(fresh_graph)
        fresh_empty=await replay(HistoryView(fresh_graph,{}))
        fresh_restored=await replay(HistoryView(fresh_graph,restored_ledger))
        assert not fresh_inspection._classified_signatures
        assert visible_tool_results(fresh_plain)==visible_tool_results(plain)
        assert not visible_tool_results(fresh_empty)
        assert visible_tool_results(fresh_restored)==visible_tool_results(filtered)
        post_view=await graph.aget_state(config)
        unchanged=[record_message(m) for m in latest.values.get('messages',[])]==[record_message(m) for m in post_view.values.get('messages',[])]
        history_tool_variants={}
        for s in reversed(snapshots):
            for m in s.values.get('messages',[]):
                if isinstance(m,ToolMessage):
                    history_tool_variants.setdefault(m.id,[])
                    if m.content not in history_tool_variants[m.id]:history_tool_variants[m.id].append(m.content)
        state_keys=set().union(*(s.values.keys() for s in snapshots))
        metadata_keys=set().union(*((s.metadata or {}).keys() for s in snapshots))
        result={'scenario':scenario,'driver':driver,'outcome':outcome,'trace':trace,'accepted_notifications':sum(bool(r['signatures']) for r in accepted_records),'all_custom_acceptance_events':len(accepted_records),'released_entries':len(released),'middleware_pass_cache_size':len(captured['inspection']._classified_signatures),'checkpoint_tool_messages_before_mutation':before_mutation,'latest_tool_messages':[record_message(m) for m in latest.values.get('messages',[]) if isinstance(m,ToolMessage)],'history_tool_variants':history_tool_variants,'checkpoint_state_keys':sorted(state_keys),'checkpoint_metadata_keys':sorted(metadata_keys),'pending_snapshot_tool_messages':None if mid is None else [record_message(m) for m in mid.values.get('messages',[]) if isinstance(m,ToolMessage)],'plain_replay_tool_results':visible_tool_results(plain),'filtered_replay_tool_results':visible_tool_results(filtered),'naive_filtered_replay_tool_results':visible_tool_results(naive),'empty_ledger_replay_tool_results':visible_tool_results(empty),'view_did_not_mutate_state':unchanged,'fresh_checkpoint_store_plain_tool_results':visible_tool_results(fresh_plain),'fresh_checkpoint_store_empty_ledger_tool_results':visible_tool_results(fresh_empty),'fresh_checkpoint_store_restored_ledger_tool_results':visible_tool_results(fresh_restored),'fresh_middleware_pass_cache_size':len(fresh_inspection._classified_signatures),'combined_live_tool_results':None if driver not in {'combined','journal'} else visible_tool_results(live_client)}
        if driver=='journal':
            before_journal=len(live_server.display_journal[sid])
            journal_client=Client();live_server.on_connect(journal_client)
            await live_server.load_session(cwd=cwd,session_id=sid,mcp_servers=[])
            result['journal_replay_tool_results']=visible_tool_results(journal_client)
            result['journal_replay_matches_emitted_events']=journal_client.events==live_client.events
            result['journal_did_not_append_on_replay']=before_journal==len(live_server.display_journal[sid])
            assert result['journal_replay_matches_emitted_events'] and result['journal_did_not_append_on_replay']
            assert len(visible_tool_results(journal_client))==(1 if scenario=='pass' else 0)
        # A model follow-up after filtered replay must see the original graph/checkpoint history.
        if scenario=='pass':
            followup=AgentServerACP(lambda _ctx:HistoryView(graph,released),load_sessions=True);followup.on_connect(Client())
            await followup.load_session(cwd=cwd,session_id=sid,mcp_servers=[])
            followup_result=await followup.prompt([text_block('Explain previous fixture')],session_id=sid)
            result['followup_after_view']=followup_result.stop_reason
            result['followup_model_tool_contents']=trace[-1].get('tool_contents') if trace[-1]['event']=='model' else [e['tool_contents'] for e in trace if e['event']=='model'][-1]
        assert unchanged
        assert len(visible_tool_results(plain))==(2 if scenario=='mixed_siblings' else 1),(scenario,plain.events,result['checkpoint_tool_messages_before_mutation'])
        safe_expected=1 if scenario in {'pass','transformed','repeat_prompt','removed_after_pass','nested_pass'} else 0
        assert len(visible_tool_results(filtered))==safe_expected,(scenario,visible_tool_results(filtered))
        assert not visible_tool_results(empty)
        if scenario=='mixed_siblings': assert not any(r['signatures'] for r in accepted_records) and captured['inspection']._classified_signatures
        if scenario in {'reject','removed_after_reject','classifier_error','pending_cancel','mixed_siblings'}:assert not released
        if scenario=='modified_after_pass':assert len(history_tool_variants[tool_messages[0].id])==2
        if scenario=='removed_after_pass':assert not result['latest_tool_messages']
        if scenario=='nested_parent_reject':assert len(released)==1 and all(k[1] for k in released)
        if driver in {'combined','journal'}:assert len(visible_tool_results(live_client))==(1 if scenario=='pass' else 0)
        if scenario=='pass':assert result['followup_after_view']=='end_turn' and result['followup_model_tool_contents']
        return result

async def main():
    scenarios=['pass','reject','classifier_error','pending_cancel','mixed_siblings','transformed','modified_after_pass','removed_after_pass','removed_after_reject','repeat_prompt','nested_pass','nested_parent_reject']
    results=[await case(s) for s in scenarios]
    results.extend([await case('pass','combined'),await case('reject','combined')])
    results.extend([await case(s,'journal') for s in ['pass','reject','classifier_error','pending_cancel','mixed_siblings']])
    print(json.dumps({'source':__import__('lightspeed_agentic.providers.deepagents',fromlist=['']).__file__,'assertions':'passed','cases':results},indent=2,default=str))
if __name__=='__main__':
    asyncio.run(main())
