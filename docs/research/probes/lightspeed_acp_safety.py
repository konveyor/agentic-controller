"""Offline diagnostic: real Lightspeed-built graph inside official AgentServerACP.

Run with an isolated environment containing the pinned official deepagents_acp
package, DeepAgents 0.7.22, LangGraph 1.2.14, and agent-client-protocol 0.10.1.
Place the selected Lightspeed source tree's ``src`` directory on PYTHONPATH,
alongside any staged official ACP package. No provider credentials are needed.

Example:
    PYTHONPATH=/path/to/staged-acp:/path/to/lightspeed/src \
        /path/to/isolated/python lightspeed_acp_safety.py > trace.json

Models, classification decisions, tool output, and ACP client are scripted;
Lightspeed preparation, graph, middleware, classifier orchestration, query
driver, and official ACP server are real. Capture uses a diagnostic patch at
graph construction, not a supported production factory. No paid model, real
MCP server, shell tool, cluster, or UI is called. Temporary workspace removed
on exit. The fixture credential text is synthetic.
"""
import asyncio, json, os, tempfile
from types import SimpleNamespace
from unittest.mock import patch
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

class Captured(BaseException): pass

async def case(decision, target="official"):
    trace=[]
    captured={}
    class Model(FakeMessagesListChatModel):
        def bind_tools(self, _tools, **kwargs): return self
        def _generate(self,messages,stop=None,run_manager=None,**kwargs):
            trace.append({'event':'main_model','contains_tool_result':any(isinstance(m,ToolMessage) for m in messages),'raw_redaction_fixture':any('OFFLINE-REDACTION-PROBE' in str(m.content) for m in messages),'redacted':any('[REDACTED:credential]' in str(m.content) for m in messages)})
            return super()._generate(messages,stop,run_manager,**kwargs)
        def _stream(self,messages,stop=None,run_manager=None,**kwargs):
            response=self._generate(messages,stop,run_manager,**kwargs).generations[0].message
            chunks=[{'name':c['name'],'args':json.dumps(c['args']),'id':c['id'],'index':i,'type':'tool_call_chunk'} for i,c in enumerate(response.tool_calls)]
            yield ChatGenerationChunk(message=AIMessageChunk(content=response.content,tool_call_chunks=chunks,chunk_position='last'))
    model=Model(responses=[AIMessage(content='',tool_calls=[{'name':'lookup_probe','args':{},'id':'probe-call','type':'tool_call'}]), AIMessage(content='Done')])
    class Classifier:
        async def classify(self, request, **kwargs):
            trace.append({'event':'classifier','marker': 'TOOL-INSPECTION-PROBE' in request.content,'raw_redaction_fixture':'OFFLINE-REDACTION-PROBE' in request.content,'redacted':'[REDACTED:credential]' in request.content})
            if decision=='classifier_error': raise ValueError('scripted classifier failure')
            return ClassifierDecision(injectionDetected=decision=='reject',category='unknown' if decision=='reject' else 'none')
    @tool
    def lookup_probe()->str:
        """Return an offline inspection marker."""
        trace.append({'event':'tool'})
        return 'TOOL-INSPECTION-PROBE password=OFFLINE-REDACTION-PROBE'
    create=deepagents.create_deep_agent
    def capture(**kwargs):
        kwargs['tools']=[lookup_probe]
        captured['graph']=create(**kwargs)
        captured['middleware']=[type(x).__name__ for x in kwargs['middleware']]
        if target=='official': raise Captured()
        return captured['graph']
    creates=iter([model,SimpleNamespace(profile={},disable_streaming=False)])
    route=ResolvedModelRoute(route='anthropic',model='offline-model',create_model=lambda *a:next(creates),model_parameters=ModelParameters(model='offline-model'),telemetry_provider='anthropic',structured_output_method='function_calling',create_classifier_client=lambda *a:Classifier())
    provider=DeepAgentsProvider(route)
    with tempfile.TemporaryDirectory(prefix='ls-acp-graph-') as cwd:
        try:
            with patch('deepagents.create_deep_agent',capture):
                async for event in provider.query(ProviderQueryOptions(prompt='x',system_prompt='Probe',max_turns=50,cwd=cwd)):
                    if event.type=='tool_result':
                        trace.append({'event':'viewer','kind':'normalized_tool_result','marker':'TOOL-INSPECTION-PROBE' in event.output,'raw_redaction_fixture':'OFFLINE-REDACTION-PROBE' in event.output,'redacted':'[REDACTED:credential]' in event.output})
            outcome='end_turn'
        except Captured: pass
        except BaseException as exc: outcome=type(exc).__name__
        if target=='lightspeed':
            return {'target':target,'decision':decision,'outcome':outcome,'trace':trace}
        assert 'graph' in captured
        class Client:
            async def session_update(self,**kwargs):
                update=kwargs['update']
                data=update.model_dump(mode='json',exclude_none=True)
                trace.append({'event':'viewer','kind':data.get('session_update'),'marker':'TOOL-INSPECTION-PROBE' in json.dumps(data),'raw_redaction_fixture':'OFFLINE-REDACTION-PROBE' in json.dumps(data),'redacted':'[REDACTED:credential]' in json.dumps(data)})
        server=AgentServerACP(captured['graph'])
        server.on_connect(Client())
        session=await server.new_session(cwd=cwd,mcp_servers=[])
        try:
            result=await server.prompt(prompt=[text_block('Call lookup_probe then finish')],session_id=session.session_id)
            outcome=result.stop_reason
        except BaseException as exc:
            outcome=type(exc).__name__
    return {'target':target,'decision':decision,'middleware':captured['middleware'],'outcome':outcome,'trace':trace}

async def main():
    source=__import__('lightspeed_agentic.providers.deepagents',fromlist=['']).__file__
    cases=[await case(x,target) for target in ['official','lightspeed'] for x in ['pass','reject','classifier_error']]
    for result in cases:
        trace=result['trace']
        indices=[i for i,e in enumerate(trace) if e['event']=='viewer' and e.get('marker')]
        first_classifier=next(i for i,e in enumerate(trace) if e['event']=='classifier')
        assert result['outcome']==('end_turn' if result['decision']=='pass' else 'ToolResultSafetyInspectionFailed')
        assert sum(e['event']=='main_model' for e in trace)==(2 if result['decision']=='pass' else 1)
        if result['target']=='official': assert len(indices)==1 and indices[0]<first_classifier
        elif result['decision']=='pass': assert len(indices)==1 and indices[0]>first_classifier
        else: assert not indices
    print(json.dumps({'lightspeed_source':source,'assertions':'passed','cases':cases},indent=2))
asyncio.run(main())
