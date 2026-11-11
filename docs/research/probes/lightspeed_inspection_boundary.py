"""Offline inspection-boundary invariants; synthetic messages only."""
import asyncio,json
from types import SimpleNamespace
from langchain_core.messages import AIMessage,ToolMessage
from lightspeed_agentic.providers.tool_result_middleware import ToolResultInspectionMiddleware,tool_result_signature
from lightspeed_agentic.inspection.errors import ToolResultSafetyInspectionFailed

async def scenario(reject_second):
    calls=[{'name':'lookup','args':{},'id':'one'},{'name':'lookup','args':{},'id':'two'}]
    results=[ToolMessage(content='SYNTHETIC-PASS',name='lookup',tool_call_id='one',id='message-one'),ToolMessage(content='SYNTHETIC-SECOND',name='lookup',tool_call_id='two',id='message-two')]
    messages=[AIMessage(content='',tool_calls=calls),*results]
    approvals=[];model_inputs=[];classified=[]
    async def classify(name,kind,content,call_id):
        classified.append(call_id)
        return SimpleNamespace(passed=not(reject_second and call_id=='two'))
    middleware=ToolResultInspectionMiddleware(classify,on_accepted=lambda m:approvals.append(tuple(m)))
    async def handler(request):model_inputs.append(request.messages);return 'model response'
    request=SimpleNamespace(messages=messages,override=lambda **kw:SimpleNamespace(**kw))
    failed=False
    try:await middleware.awrap_model_call(request,handler)
    except ToolResultSafetyInspectionFailed:failed=True
    cached=[x[2] for x in middleware._classified_signatures]
    assert classified==['one','two']
    if reject_second:
        assert failed and not approvals and not model_inputs and 'one' in cached
    else:
        assert not failed and len(approvals)==1 and len(approvals[0])==2
        assert all('<tool_data ' in m.content for m in model_inputs[0] if isinstance(m,ToolMessage))
        assert results[0].content=='SYNTHETIC-PASS'
    signature=tool_result_signature('lookup','result','one',results[0].content)
    assert signature!=tool_result_signature('lookup','result','one','SYNTHETIC-CHANGED')
    assert signature!=tool_result_signature('lookup','error','one',results[0].content)
    return {'mixed_boundary_rejected':reject_second,'classifier_cache_calls':sorted(cached),'completed_boundary_callbacks':len(approvals),'model_calls':len(model_inputs),'source_content_unchanged':results[0].content=='SYNTHETIC-PASS','payload_and_status_change_signature':True}

async def main():
    data={'source':'a23005e8f9ed0edc6e430072f50e4698590de71f','assertions':'passed','cases':[await scenario(False),await scenario(True)]}
    print(json.dumps(data,indent=2))
asyncio.run(main())
