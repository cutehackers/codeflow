import {describe,expect,it,beforeEach} from 'vitest';
import {render} from 'svelte/server';
import {samplePayload} from '../stores/sampleData';
import {flowStore} from '../stores/flowStore.svelte';
import {partitionRequestedFlowRelations, requestedFlowJoins, requestedFlowSegmentContextStepIds, type RequestedFlowRelation} from '../stores/requestedFlowRelations';
import RequestedFlowSegmentRail from '../components/RequestedFlowSegmentRail.svelte';

function sequencePayload() {
  const data = samplePayload(1);
  data.semanticMap.steps = Array.from({length:201},(_,index)=>({stepId:`step-${index}`,name:`처리${index}`,anchor:{repoRelativePath:'flow.ts'}}));
  data.semanticMap.edges = data.semanticMap.steps.slice(0,-1).map((step,index)=>({fromStepId:step.stepId,toStepId:data.semanticMap.steps[index+1].stepId,kind:'control_flow',resolutionStatus:'resolved'}));
  const relations: RequestedFlowRelation[] = data.semanticMap.edges.map((edge,index)=>({derived:true,fromSegmentId:'process',toSegmentId:'process',fromStepId:edge.fromStepId,toStepId:edge.toStepId,kind:edge.kind,status:'verified',edgeIndexes:[index],edges:[edge]}));
  data.requestedFlowOverview = {schemaId:'overview',schemaVersion:1,request:'request',snapshotId:'snapshot',computedBasisId:'basis',segments:[{segmentId:'process',purpose:'입력 값을 처리한다',inclusionReason:'계산을 함께 읽는다',entryStepId:'step-0',stepIds:data.semanticMap.steps.map(step=>step.stepId),blocks:[]}],relations,boundaries:[]};
  return data;
}

beforeEach(()=>{flowStore.expandedReadingRelations=new Set();});
describe('purpose relation disclosure',()=>{
  it('marks an omitted execution boundary in its preceding purpose without crossing other segments or unverified edges',()=>{
    const data=sequencePayload();
    const overview=data.requestedFlowOverview!;
    overview.segments[0].stepIds=['step-0'];
    overview.segments.push({...overview.segments[0],segmentId:'next-purpose',entryStepId:'step-2',stepIds:['step-2']});
    overview.relations=overview.relations.slice(0,4);
    overview.relations[3].status='boundary';
    overview.boundaries=[{fromStepId:'step-1',target:'dynamicTarget',reason:'unresolved_dynamic_call'}];
    expect(requestedFlowSegmentContextStepIds(overview,'process')).toEqual(['step-0','step-1']);
    expect(requestedFlowSegmentContextStepIds(overview,'next-purpose')).toEqual(['step-2','step-3']);
    flowStore.data=data;
    flowStore.selectedSegmentId='process';
    const body=render(RequestedFlowSegmentRail).body;
    expect(body).toMatch(/data-segment="process"[^]*?연결 미확인/);
    expect(body).toContain('segment-boundary:step-1:dynamicTarget');
    overview.relations[0].status='boundary';
    expect(requestedFlowSegmentContextStepIds(overview,'process')).toEqual(['step-0']);
  });
  it('preserves open connection disclosures and advertises omitted repetitions and failures',()=>{
    const data=sequencePayload();
    data.requestedFlowOverview!.segments[0].stepIds=['step-0'];
    const relations=data.requestedFlowOverview!.relations.slice(1,3);
    relations.forEach(relation=>{delete relation.fromSegmentId;delete relation.toSegmentId;});
    relations[0].edges[0].kind='loop_back';
    relations[1].edges[0].kind='failure';
    data.requestedFlowOverview!.relations=relations;
    flowStore.data=data;
    const body=render(RequestedFlowSegmentRail).body;
    expect(body).toMatch(/경유 처리 · 반복 · 실패 · 상세/);
    flowStore.toggleReadingRelation('intermediate-context');
    const expanded=render(RequestedFlowSegmentRail).body;
    expect(expanded).toMatch(/aria-label="경유 처리의 판단과 연결"[^>]*open/);
    const snapshot=flowStore.captureNavigationState();
    expect(snapshot.expandedReadingRelations).toContain('intermediate-context');
  });
  it('collapses a long routine sequence without dropping its graph facts or detail access',()=>{
    const data=sequencePayload();
    const relations=data.requestedFlowOverview!.relations;
    const partition=partitionRequestedFlowRelations(data,relations);
    expect(partition.primary).toHaveLength(0);
    expect(partition.routine).toEqual(relations);
    flowStore.data=data;
    const collapsed=render(RequestedFlowSegmentRail).body;
    expect(collapsed).toContain('기타 내부 연결 200개 펼치기');
    expect(collapsed).not.toContain('data-segment-relation=');
    expect(collapsed).not.toContain('처리100');
    flowStore.toggleReadingRelation('segment-routine:process');
    const expanded=render(RequestedFlowSegmentRail).body;
    expect(expanded.match(/data-segment-relation=/g)).toHaveLength(200);
    expect(expanded).toContain('segment-edge:step-100:step-101');
  });

  it('keeps proposals, branches, loops, joins, boundaries and cross-file handoffs visible',()=>{
    const data=sequencePayload();
    const original=data.requestedFlowOverview!.relations.slice(0,8);
    const relations=structuredClone(original);
    relations[0].derived=false;
    relations[1].edges[0].conditions=[{stepId:relations[1].fromStepId,outcome:'falsy'}];
    relations[2].edges[0].kind='loop_back';
    const joinEdge={fromStepId:'step-100',toStepId:relations[3].toStepId,kind:'control_flow',resolutionStatus:'resolved'};
    data.requestedFlowOverview!.relations.push({derived:true,fromSegmentId:'process',toSegmentId:'process',fromStepId:joinEdge.fromStepId,toStepId:joinEdge.toStepId,kind:joinEdge.kind,status:'verified',edgeIndexes:[200],edges:[joinEdge]});
    relations[4].status='boundary';
    data.semanticMap.steps.find(step=>step.stepId===relations[5].fromStepId)!.anchor!.repoRelativePath='effects.ts';
    relations[6].toSegmentId='finish';
    relations[7].edges[0].kind='failure';
    expect(partitionRequestedFlowRelations(data,relations).primary).toEqual(relations);
  });

  it('names both branch predecessors and their internal merge in the default overview',()=>{
    const data=sequencePayload();
    data.semanticMap.steps=data.semanticMap.steps.slice(0,5);
    data.semanticMap.steps[1].name='성공한 결제를 기록한다';
    data.semanticMap.steps[2].name='실패한 결제를 정리한다';
    data.semanticMap.steps[3].name='결제 응답을 반환한다';
    data.semanticMap.edges=[
      {fromStepId:'step-0',toStepId:'step-1',kind:'control_flow',resolutionStatus:'resolved',conditions:[{stepId:'step-0',outcome:'truthy'}]},
      {fromStepId:'step-0',toStepId:'step-2',kind:'control_flow',resolutionStatus:'resolved',conditions:[{stepId:'step-0',outcome:'falsy'}]},
      {fromStepId:'step-1',toStepId:'step-3',kind:'control_flow',resolutionStatus:'resolved'},
      {fromStepId:'step-2',toStepId:'step-3',kind:'control_flow',resolutionStatus:'resolved'},
    ];
    data.requestedFlowOverview!.relations=data.semanticMap.edges.map((edge,index)=>({derived:true,fromSegmentId:'process',toSegmentId:'process',fromStepId:edge.fromStepId,toStepId:edge.toStepId,kind:edge.kind,status:'verified',edgeIndexes:[index],edges:[edge]}));
    flowStore.data=data;
    const body=render(RequestedFlowSegmentRail).body;
    expect(body).toContain('data-segment-join="step-3"');
    expect(body).toContain('합류 · 결제 응답을 반환한다 · 2개 확인된 선행 연결 · 동시 완료를 뜻하지 않음');
    expect(body).toContain('선행 코드: 성공한 결제를 기록한다 · 실패한 결제를 정리한다');
    expect(body).toContain('segment-join:step-1:step-3');
    expect(body).toContain('segment-join:step-2:step-3');
    data.semanticMap.edges.slice(2).forEach(edge=>edge.kind='parallel_wait');
    const parallel=render(RequestedFlowSegmentRail).body;
    expect(parallel).toContain('병렬 작업 완료 대기 · 결제 응답을 반환한다');
    expect(parallel).not.toContain('동시 완료를 뜻하지 않음');
  });

  it('ignores raw caller bypass and resolved nonexecution edges when identifying joins',()=>{
    const data=sequencePayload();
    const after='step-3';
    const callerBypass={fromStepId:'step-0',toStepId:after,kind:'control_flow',resolutionStatus:'resolved'};
    const calleeReturn={fromStepId:'step-2',toStepId:after,kind:'return',resolutionStatus:'resolved'};
    const nonexecution={fromStepId:'step-1',toStepId:after,kind:'callback_argument',resolutionStatus:'resolved'};
    data.semanticMap.edges=[callerBypass,calleeReturn,nonexecution];
    data.requestedFlowOverview!.relations=[
      {derived:true,fromSegmentId:'process',toSegmentId:'process',fromStepId:calleeReturn.fromStepId,toStepId:after,kind:'return',status:'verified',edgeIndexes:[1],edges:[calleeReturn]},
      {derived:true,fromSegmentId:'process',toSegmentId:'process',fromStepId:nonexecution.fromStepId,toStepId:after,kind:'callback_argument',status:'boundary',edgeIndexes:[2],edges:[nonexecution]},
    ];
    expect(requestedFlowJoins(data,data.requestedFlowOverview!.segments[0].stepIds)).toEqual([]);
    flowStore.data=data;
    expect(render(RequestedFlowSegmentRail).body).not.toContain('data-segment-join=');
  });

  it('shows an omitted intermediate merge and its verified predecessors by default',()=>{
    const data=sequencePayload();
    data.semanticMap.steps[1].name='성공 처리';
    data.semanticMap.steps[2].name='실패 처리';
    data.semanticMap.steps[3].name='공통 응답';
    const edges=[
      {fromStepId:'step-1',toStepId:'step-3',kind:'control_flow',resolutionStatus:'resolved'},
      {fromStepId:'step-2',toStepId:'step-3',kind:'control_flow',resolutionStatus:'resolved'},
    ];
    data.requestedFlowOverview!.segments[0].stepIds=['step-0'];
    data.requestedFlowOverview!.relations=edges.map((edge,index)=>({derived:true,fromStepId:edge.fromStepId,toStepId:edge.toStepId,kind:edge.kind,status:'verified',edgeIndexes:[index],edges:[edge]}));
    flowStore.data=data;
    const body=render(RequestedFlowSegmentRail).body;
    expect(body).toContain('data-segment-join="step-3"');
    expect(body).toContain('합류 · 공통 응답 · 2개 확인된 선행 연결 · 동시 완료를 뜻하지 않음');
    expect(body).toContain('선행 코드: 성공 처리 · 실패 처리');
  });

  it('collapses routine intermediate facts while preserving their condition outcomes',()=>{
    const data=sequencePayload();
    const relations=data.requestedFlowOverview!.relations;
    relations.forEach(relation=>{delete relation.fromSegmentId;delete relation.toSegmentId;});
    relations[99].edges[0].conditions=[{stepId:relations[99].fromStepId,outcome:'truthy'}];
    flowStore.data=data;
    const body=render(RequestedFlowSegmentRail).body;
    expect(body).toContain('기타 내부 연결 199개 펼치기');
    expect(body).toContain('조건 참');
    expect(body.match(/data-segment-relation=/g)).toHaveLength(1);
    expect(body).toContain('intermediate:step-99:step-100');
  });
});
