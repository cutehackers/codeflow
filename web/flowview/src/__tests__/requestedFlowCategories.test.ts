import {beforeEach,describe,expect,it} from 'vitest';
import {render} from 'svelte/server';
import {samplePayload} from '../stores/sampleData';
import {flowStore} from '../stores/flowStore.svelte';
import RequestedFlowCategorySequence from '../components/RequestedFlowCategorySequence.svelte';
import RequestedFlowSegmentRail from '../components/RequestedFlowSegmentRail.svelte';
import {validateFlowReferences} from '../stores/flowNavigation';

function categoryPayload(viewId='category-view') {
  const data=samplePayload(1);
  data.viewId=viewId;
  data.flowResolution={schemaVersion:1,status:'resolved',rawRequest:'How is checkout processed?'};
  const steps=data.semanticMap.steps.slice(0,4);
  steps.forEach(step=>{step.anchor={repoRelativePath:'checkout.ts',enclosingSymbolPath:'processCheckout'};});
  data.sourceFiles={'checkout.ts':steps.map((step,index)=>({lineNumber:index+1,text:`processPart${index}();`}))};
  data.requestedFlowOverview={schemaId:'https://codeflow.local/schemas/requested-flow-overview.schema.json',schemaVersion:2,request:data.flowResolution.rawRequest,snapshotId:data.flowSequence!.snapshotID,computedBasisId:data.flowSequence!.computedBasisId,categories:[{categoryId:'prepare',title:'입력과 결제 준비',segmentIds:['segment-0','segment-1']},{categoryId:'complete',title:'결제와 결과 처리',segmentIds:['segment-2','segment-3']}],segments:steps.map((step,index)=>({segmentId:`segment-${index}`,purpose:`처리 목적 ${index}`,inclusionReason:'요청한 결제 처리를 읽는다',stepIds:[step.stepId],entryStepId:step.stepId,blocks:[{blockId:`block-${index}`,repoRelativePath:'checkout.ts',symbolPath:'processCheckout',lineRange:[index+1,index+1],stepIds:[step.stepId],highlights:[{stepId:step.stepId,lineRange:[index+1,index+1],byteRange:[index*20,index*20+10],lineSelections:[{lineNumber:index+1,startColumn:0,endColumn:data.sourceFiles!['checkout.ts'][index].text.length}]}]}]})),relations:[],boundaries:[]};
  return data;
}

beforeEach(()=>{flowStore.data=null;flowStore.baseline=null;flowStore.compare=false;});
describe('coarse Requested Flow category selection',()=>{
  it('shows a category connection only when the overview preserves a verified execution relation',()=>{
    const data=categoryPayload();
    expect(flowStore.adopt(data,true)).toBe(true);
    const relation={fromSegmentId:'segment-0',toSegmentId:'segment-2',fromStepId:data.semanticMap.steps[0].stepId,toStepId:data.semanticMap.steps[2].stepId,kind:'control_flow',status:'verified',edgeIndexes:[0],edges:[{fromStepId:data.semanticMap.steps[0].stepId,toStepId:data.semanticMap.steps[2].stepId,kind:'control_flow',resolutionStatus:'resolved'}]};
    data.requestedFlowOverview!.relations=[relation];
    expect(render(RequestedFlowCategorySequence).body).toContain('처리 목적 0 → 처리 목적 2');
    relation.status='boundary';
    expect(render(RequestedFlowCategorySequence).body).not.toContain('data-category-connection');
    data.requestedFlowOverview!.relations=[];
    expect(render(RequestedFlowCategorySequence).body).not.toContain('→');
  });
  it('keeps all category children available across category boundaries and rejects invalid membership',()=>{
    const data=categoryPayload();
    expect(flowStore.adopt(data,true)).toBe(true);
    const first=render(RequestedFlowSegmentRail).body;
    expect(first.match(/data-segment=/g)).toHaveLength(4);
    expect(first).toContain('data-segment-category="prepare"');
    expect(first).toContain('data-segment-category="complete"');
    expect(first).toContain('data-segment="segment-0"');
    expect(first).toContain('data-segment="segment-2"');
    flowStore.selectSegment('segment-2');
    expect(flowStore.selectedCategory?.categoryId).toBe('complete');
    const second=render(RequestedFlowSegmentRail).body;
    expect(second).toContain('data-segment="segment-2"');
    expect(second).toContain('data-segment="segment-0"');
    expect(second.match(/data-segment=/g)).toHaveLength(4);
    for (const mutation of [
      (candidate:typeof data)=>{delete candidate.requestedFlowOverview!.categories;},
      (candidate:typeof data)=>{candidate.requestedFlowOverview!.categories![0].segmentIds.pop();},
      (candidate:typeof data)=>{candidate.requestedFlowOverview!.categories![1].segmentIds.push('segment-0');},
      (candidate:typeof data)=>{candidate.requestedFlowOverview!.categories![0].segmentIds.push('missing');},
      (candidate:typeof data)=>{candidate.requestedFlowOverview!.schemaVersion=1;},
      (candidate:typeof data)=>{candidate.requestedFlowOverview!.categories![1].title=candidate.requestedFlowOverview!.categories![0].title;},
    ]) {
      const candidate=categoryPayload('invalid-view');
      mutation(candidate);
      expect(validateFlowReferences(candidate)).not.toBeNull();
      expect(flowStore.adopt(candidate,true)).toBe(false);
      expect(flowStore.data).toBe(data);
      expect(flowStore.selectedSegmentId).toBe('segment-2');
    }
  });
  it('derives the selected category from its child and remembers each category last child',()=>{
    expect(flowStore.adopt(categoryPayload(),true)).toBe(true);
    expect(flowStore.selectedCategory?.categoryId).toBe('prepare');
    expect(flowStore.selectCategory('complete')).toBe(true);
    expect(flowStore.selectedSegmentId).toBe('segment-2');
    flowStore.selectSegment('segment-3');
    flowStore.selectCategory('prepare');
    expect(flowStore.selectedSegmentId).toBe('segment-0');
    flowStore.selectSegment('segment-1');
    flowStore.selectCategory('complete');
    expect(flowStore.selectedSegmentId).toBe('segment-3');
    flowStore.selectCategory('prepare');
    expect(flowStore.selectedSegmentId).toBe('segment-1');
    expect(flowStore.selectedCategory?.segmentIds).toEqual(['segment-0','segment-1']);
  });

  it('rejects an unknown category or invalid child without changing the current reading state',()=>{
    const data=categoryPayload();
    expect(flowStore.adopt(data,true)).toBe(true);
    flowStore.selectSegment('segment-1');
    const origin=flowStore.captureNavigationState();
    expect(flowStore.selectCategory('missing')).toBe(false);
    data.requestedFlowOverview!.categories![1].segmentIds.push('missing');
    expect(flowStore.selectCategory('complete')).toBe(false);
    expect(flowStore.captureNavigationState()).toEqual(origin);
    expect(flowStore.applyNavigationState({...origin,categorySegmentSelections:{prepare:'missing'}})).toBe(false);
    expect(flowStore.selectedSegmentId).toBe('segment-1');
  });

  it('returns from detail to the originating child and keeps category memory',()=>{
    const data=categoryPayload();
    expect(flowStore.adopt(data,true)).toBe(true);
    const baseline=categoryPayload('baseline-view');
    flowStore.baseline=baseline;
    flowStore.compare=true;
    flowStore.selectSegment('segment-1');
    flowStore.selectCategory('complete');
    flowStore.selectSegment('segment-3');
    flowStore.openSegmentDetail(data.requestedFlowOverview!.segments[3].entryStepId,'block-3');
    flowStore.select(data.requestedFlowOverview!.segments[0].entryStepId);
    flowStore.setExplorationMode('overview');
    expect(flowStore.selectedCategory?.categoryId).toBe('complete');
    expect(flowStore.selectedSegmentId).toBe('segment-3');
    expect(flowStore.selectedBlockId).toBe('block-3');
    expect(flowStore.baseline).toBe(baseline);
    expect(flowStore.compare).toBe(true);
    flowStore.selectCategory('prepare');
    expect(flowStore.selectedSegmentId).toBe('segment-1');
  });

  it('restores separate per-View child memories through the existing navigation snapshots',()=>{
    const first=categoryPayload('first-view');
    const second=categoryPayload('second-view');
    expect(flowStore.adopt(first,true)).toBe(true);
    flowStore.selectSegment('segment-1');
    flowStore.selectSegment('segment-3');
    const firstState=flowStore.captureNavigationState();
    expect(flowStore.adopt(second,true)).toBe(true);
    flowStore.selectSegment('segment-2');
    const secondState=flowStore.captureNavigationState();
    expect(flowStore.adopt(first,true)).toBe(true);
    expect(flowStore.applyNavigationState(firstState)).toBe(true);
    flowStore.selectCategory('prepare');
    expect(flowStore.selectedSegmentId).toBe('segment-1');
    flowStore.selectCategory('complete');
    expect(flowStore.selectedSegmentId).toBe('segment-3');
    expect(flowStore.adopt(second,true)).toBe(true);
    expect(flowStore.applyNavigationState(secondState)).toBe(true);
    flowStore.selectCategory('prepare');
    expect(flowStore.selectedSegmentId).toBe('segment-0');
    flowStore.selectCategory('complete');
    expect(flowStore.selectedSegmentId).toBe('segment-2');
  });

  it('shows category titles, child counts and the active child without implying execution arrows',()=>{
    expect(flowStore.adopt(categoryPayload(),true)).toBe(true);
    flowStore.selectSegment('segment-1');
    const body=render(RequestedFlowCategorySequence).body;
    expect(body).toContain('aria-label="상위 처리 분류"');
    expect(body).toContain('입력과 결제 준비');
    expect(body).toContain('결제와 결과 처리');
    expect(body.match(/2개 처리 구간/g)).toHaveLength(2);
    expect(body).toContain('읽는 구간: 처리 목적 1');
    expect(body).toContain('data-category="prepare"');
    expect(body).toContain('data-navigation-focus="category:prepare"');
    expect(body).not.toContain('→');
    const legacy=categoryPayload();
    legacy.requestedFlowOverview!.schemaVersion=1;
    delete legacy.requestedFlowOverview!.categories;
    expect(flowStore.adopt(legacy,true)).toBe(true);
    expect(flowStore.selectedCategory).toBeNull();
    expect(render(RequestedFlowCategorySequence).body).not.toContain('<nav');
  });
});
