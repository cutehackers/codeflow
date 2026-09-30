import { describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import RequestedFlowOverview from '../components/RequestedFlowOverview.svelte';
import { flowStore } from '../stores/flowStore.svelte';
import { samplePayload } from '../stores/sampleData';

describe('Requested Flow Overview rendering', () => {
  it('discloses branch outcomes, joins, failures, loops and unknown calls beside folded code', () => {
    const data = samplePayload(1);
    const [entry, decision, result, failure] = data.semanticMap.steps;
    data.semanticMap.edges = [
      {fromStepId:entry.stepId,toStepId:decision.stepId,kind:'control_flow',resolutionStatus:'resolved'},
      {fromStepId:decision.stepId,toStepId:result.stepId,kind:'control_flow',resolutionStatus:'resolved',conditions:[{stepId:decision.stepId,outcome:'truthy'}]},
      {fromStepId:decision.stepId,toStepId:failure.stepId,kind:'failure',resolutionStatus:'resolved',conditions:[{stepId:decision.stepId,outcome:'falsy'}]},
      {fromStepId:decision.stepId,toStepId:entry.stepId,kind:'loop_back',resolutionStatus:'resolved'},
      {fromStepId:failure.stepId,toStepId:result.stepId,kind:'control_flow',resolutionStatus:'resolved'},
      {fromStepId:decision.stepId,toStepId:'unknown',kind:'call',resolutionStatus:'unresolved',toSymbolPath:'service#unconfirmed'},
    ];
    data.codeReadingPath = {nodes:[entry,result].map(step=>({nodeId:step.stepId,stepId:step.stepId,symbol:step.technicalName,inclusionReason:step.name,role:'transform',previousNodeIds:step===result?[entry.stepId]:[]})),relations:[{fromNodeId:entry.stepId,toNodeId:result.stepId,edgeIndexes:[0,1]}]} as any;
    flowStore.data = data;
    flowStore.expandedReadingRelations = new Set();
    const {body} = render(RequestedFlowOverview);
    expect(body).toContain('조건 참');
    expect(body).toContain('조건 거짓');
    expect(body).toContain('실패');
    expect(body).toContain('다음 반복의 조건 재평가');
    expect(body).toContain(`data-overview-join="${result.stepId}"`);
    expect(body).toContain('2개 확인된 선행 연결');
    expect(body).toContain('경유 코드 1단계 펼치기');
    expect(body).toContain('연결 미확인 · service#unconfirmed');
    expect(body).not.toContain('확인된 연결 → service#unconfirmed');
    expect(body).not.toContain(`data-relation-step="${decision.stepId}"`);
    expect(body).toContain(`data-branch-target="${failure.stepId}"`);
  });
  it('keeps a long linear relation compact until its intermediary code is expanded', () => {
    const data = samplePayload(1);
    const entry = data.semanticMap.steps[0];
    const result = data.semanticMap.steps.at(-1)!;
    const middle = Array.from({length:80}, (_, index) => ({...entry,stepId:`intermediate-${index}`,name:`중간 처리 ${index}`}));
    data.semanticMap.steps = [entry,...middle,result];
    data.semanticMap.edges = data.semanticMap.steps.slice(0,-1).map((step,index)=>({fromStepId:step.stepId,toStepId:data.semanticMap.steps[index+1].stepId,kind:'control_flow',resolutionStatus:'resolved'}));
    data.codeReadingPath = {nodes:[entry,result].map(step=>({nodeId:step.stepId,stepId:step.stepId,symbol:step.technicalName,inclusionReason:step.name,role:'transform',previousNodeIds:step===result?[entry.stepId]:[]})),relations:[{fromNodeId:entry.stepId,toNodeId:result.stepId,edgeIndexes:data.semanticMap.edges.map((_,index)=>index)}]} as any;
    flowStore.data = data;
    flowStore.expandedReadingRelations = new Set();
    const collapsed = render(RequestedFlowOverview).body;
    expect(collapsed).not.toContain('다음 처리');
    expect(collapsed).toContain('경유 코드 80단계 펼치기');
    expect(collapsed).not.toContain('data-relation-step=');
    flowStore.expandedReadingRelations = new Set([`${entry.stepId}\u0000${result.stepId}`]);
    const expanded = render(RequestedFlowOverview).body;
    expect((expanded.match(/data-relation-step=/g)||[]).length).toBe(80);
  });

});
