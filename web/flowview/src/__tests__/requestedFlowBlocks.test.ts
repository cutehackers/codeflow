import {describe, expect, it, beforeEach, vi} from 'vitest';
import {render} from 'svelte/server';
import {samplePayload} from '../stores/sampleData';
import {flowStore} from '../stores/flowStore.svelte';
import {validateFlowReferences} from '../stores/flowNavigation';
import {blockSourceLines, blockLineParts, blockDetailAlternatives} from '../stores/requestedFlowBlocks';
import App from '../App.svelte';
import RequestedFlowBlockPanel from '../components/RequestedFlowBlockPanel.svelte';
import RequestedFlowSegmentRail from '../components/RequestedFlowSegmentRail.svelte';

export function purposePayload() {
  const data = samplePayload(1);
  data.viewId = 'purpose-view';
  data.flowResolution = {schemaVersion:1,status:'resolved',rawRequest:'How is checkout processed?'};
  const steps = data.semanticMap.steps;
  steps.forEach((step,index) => {step.anchor = {repoRelativePath:index === 2 ? 'effects.ts' : 'checkout.ts',enclosingSymbolPath:index === 2 ? 'applyEffect' : 'processCheckout'};});
  data.sourceFiles = {
    'checkout.ts':[{lineNumber:1,text:'const amount = request.amount;'},{lineNumber:2,text:'const total = amount + fee;'},{lineNumber:3,text:'return total;'}],
    'effects.ts':[{lineNumber:1,text:'persist(total);'}],
  };
  const block = (id: string, indexes: number[], path: string, range: [number,number]) => ({blockId:id,repoRelativePath:path,symbolPath:indexes[0]===2?'applyEffect':'processCheckout',lineRange:range,stepIds:indexes.map(index=>steps[index].stepId),highlights:indexes.map((index,position)=>({stepId:steps[index].stepId,lineRange:[range[0]+position,range[0]+position] as [number,number],byteRange:[index*30,index*30+10] as [number,number],lineSelections:[{lineNumber:range[0]+position,startColumn:0,endColumn:data.sourceFiles![path][position].text.length}]}))});
  data.requestedFlowOverview = {schemaId:'https://codeflow.local/schemas/requested-flow-overview.schema.json',schemaVersion:1,request:data.flowResolution.rawRequest,snapshotId:data.flowSequence!.snapshotID,computedBasisId:data.flowSequence!.computedBasisId,segments:[{segmentId:'prepare',purpose:'결제 금액을 준비하고 기록한다',inclusionReason:'입력과 계산 결과의 기록을 함께 읽는다',stepIds:steps.slice(0,3).map(step=>step.stepId),entryStepId:steps[0].stepId,blocks:[block('prepare-source',[0,1],'checkout.ts',[1,2]),block('prepare-effect',[2],'effects.ts',[1,1])]}],relations:[],boundaries:[]};
  return data;
}

beforeEach(() => {flowStore.data = null; flowStore.baseline = null; flowStore.compare = false;});
describe('purpose code block reading', () => {
  it('keeps the processing overview visible when opening source detail', () => {
    vi.stubGlobal('window', {location:{search:''}});
    const data = purposePayload();
    data.flowResolution!.rawRequest = '인증 흐름을 보여줘';
    data.requestedFlowOverview!.request = data.flowResolution!.rawRequest;
    data.requestedFlowOverview!.segments[0].purpose = '메시지 검증 · 결과 조회 · 성공/실패 처리';
    flowStore.adopt(data,true);
    flowStore.home = false;
    const initial = render(App).body;
    expect(initial).toContain('메시지 검증 · 결과 조회 · 성공/실패 처리');
    expect(initial).toContain('소스 펼치기');
    expect(initial).not.toContain('id="code-flow"');
    expect(initial).not.toContain('문장별 탐색');
    flowStore.openSegmentDetail(data.semanticMap.steps[0].stepId);
    const detail = render(App).body;
    expect(detail).toContain('id="requested-flow-overview"');
    expect(detail).toContain('처리 과정의 소스 상세');
    expect(detail).toContain('id="code-flow"');
    const segment = data.requestedFlowOverview!.segments[0];
    flowStore.selectSegment(segment.segmentId, 'prepare-effect');
    flowStore.closeSegmentDetail();
    expect(flowStore.selectedBlockId).toBe('prepare-effect');
    expect(flowStore.explorationMode).toBe('overview');
    flowStore.toggleReadingRelation('block-source:prepare-effect');
    const navigation = flowStore.captureNavigationState();
    flowStore.expandedReadingRelations = new Set();
    expect(flowStore.applyNavigationState(navigation)).toBe(true);
    expect(render(RequestedFlowBlockPanel).body).toContain('class="block-source-detail" open');
    vi.unstubAllGlobals();
  });

  it('keeps a nested included statement accessible when its exact range overlaps another statement',()=>{
    const data=purposePayload();
    const block=data.requestedFlowOverview!.segments[0].blocks[0];
    block.highlights[1].lineRange=[1,1];
    block.highlights[1].lineSelections=[{lineNumber:1,startColumn:6,endColumn:12}];
    expect(blockDetailAlternatives(data,block).map(item=>item.stepId)).toEqual([block.stepIds[1]]);
    expect(flowStore.adopt(data,true)).toBe(true);
    const body=render(RequestedFlowBlockPanel).body;
    expect(body).toContain('겹친 코드의 문장 선택');
    for (const id of block.stepIds) expect(body).toContain(`data-block-detail="${id}"`);
    const original=purposePayload();
    expect(blockDetailAlternatives(original,original.requestedFlowOverview!.segments[0].blocks[0])).toEqual([]);
  });
  it('opens multiple statements and files together without rendering the detail card', () => {
    const data = purposePayload();
    expect(validateFlowReferences(data)).toBeNull();
    expect(flowStore.adopt(data,true)).toBe(true);
    expect(flowStore.explorationMode).toBe('overview');
    expect(flowStore.selectedSegmentId).toBe('prepare');
    const body = render(RequestedFlowBlockPanel).body;
    expect(body).toContain('const amount = request.amount;');
    expect(body).toContain('const total = amount + fee;');
    expect(body).toContain('persist(total);');
    expect(body.match(/data-code-block=/g)).toHaveLength(2);
    expect(body).not.toContain('class="code-card');
    expect(body).toContain('주변 코드');
    expect(body).not.toContain('detail-actions');
    expect(body.match(/class="source-statement/g)).toHaveLength(3);
    expect(body).toContain(`data-block-detail="${data.semanticMap.steps[1].stepId}"`);
  });

  it('selected block statement overrides previous detail and returns to the origin block', () => {
    const data = purposePayload();
    flowStore.adopt(data,true);
    flowStore.openSegmentDetail(data.semanticMap.steps[0].stepId,'prepare-source');
    flowStore.select(data.semanticMap.steps.at(-1)!.stepId);
    flowStore.setExplorationMode('overview');
    flowStore.toggleBlock('prepare-effect');
    flowStore.openSegmentDetail(data.semanticMap.steps[2].stepId,'prepare-effect');
    expect(flowStore.selectedStepId).toBe(data.semanticMap.steps[2].stepId);
    flowStore.select(data.semanticMap.steps[3].stepId);
    flowStore.setExplorationMode('overview');
    expect(flowStore.selectedSegmentId).toBe('prepare');
    expect(flowStore.selectedBlockId).toBe('prepare-effect');
    expect(flowStore.expandedBlocks.has('prepare-effect')).toBe(true);
    const state = flowStore.captureNavigationState();
    expect(flowStore.applyNavigationState({...state,blockId:'missing'})).toBe(false);
    expect(flowStore.selectedBlockId).toBe('prepare-effect');
  });

  it('keeps every included line visible when a long block is collapsed and marks omitted context', () => {
    const data = purposePayload();
    data.sourceFiles!['checkout.ts'] = Array.from({length:90},(_,index)=>({lineNumber:index+1,text:`line ${index+1}`}));
    const block = data.requestedFlowOverview!.segments[0].blocks[0];
    block.lineRange = [1,90];
    block.highlights = [{stepId:block.stepIds[0],lineRange:[40,45],byteRange:[1,2],lineSelections:[{lineNumber:40,startColumn:0,endColumn:7}]}];
    const visible = blockSourceLines(data,block,false);
    expect(visible.filter(item=>item.line.lineNumber>=40 && item.line.lineNumber<=45)).toHaveLength(6);
    expect(visible.some(item=>item.omittedBefore)).toBe(true);
    expect(blockSourceLines(data,block,true)).toHaveLength(90);
  });

  it('uses supplied exact columns and leaves surrounding text outside the included statement', () => {
    const data = purposePayload();
    const block = data.requestedFlowOverview!.segments[0].blocks[0];
    block.highlights[0].lineSelections[0] = {lineNumber:1,startColumn:6,endColumn:12};
    expect(blockLineParts(data,block,data.sourceFiles!['checkout.ts'][0])).toEqual([{text:'const '},{text:'amount',stepId:block.stepIds[0]},{text:' = request.amount;'}]);
  });

  it('accepts an empty line inside a verified multiline statement without inventing source text', () => {
    const data = purposePayload();
    const block = data.requestedFlowOverview!.segments[0].blocks[0];
    data.sourceFiles!['checkout.ts'][1].text = '';
    block.highlights[0].lineRange = [1,2];
    block.highlights[0].lineSelections.push({lineNumber:2,startColumn:0,endColumn:0});
    block.highlights[1].lineSelections = [{lineNumber:2,startColumn:0,endColumn:0}];
    expect(validateFlowReferences(data)).toBeNull();
    expect(flowStore.adopt(data,true)).toBe(true);
    const body = render(RequestedFlowBlockPanel).body;
    expect(body).toContain('data-block-line="2"');
    expect(blockLineParts(data,block,data.sourceFiles!['checkout.ts'][1]).every(part => part.text === '')).toBe(true);
    block.highlights[0].lineSelections[0].endColumn = 0;
    expect(validateFlowReferences(data)).toContain('문장 강조');
  });

  it('rejects another snapshot or unavailable block source without replacing the current view', () => {
    const data = purposePayload();
    flowStore.adopt(data,true);
    const wrong = purposePayload();
    wrong.requestedFlowOverview!.snapshotId = 'different';
    expect(flowStore.adopt(wrong,true)).toBe(false);
    const missing = purposePayload();
    delete missing.sourceFiles!['effects.ts'];
    expect(flowStore.adopt(missing,true)).toBe(false);
    expect(flowStore.data).toBe(data);
  });

  it('displays real condition outcomes, repeat and unknown boundaries without invented progress', () => {
    const data = purposePayload();
    const [first,second] = data.semanticMap.steps;
    data.requestedFlowOverview!.relations = [{derived:true,fromSegmentId:'prepare',toSegmentId:'prepare',fromStepId:first.stepId,toStepId:second.stepId,kind:'loop_back',status:'verified',edgeIndexes:[0],edges:[{fromStepId:first.stepId,toStepId:second.stepId,kind:'loop_back',resolutionStatus:'resolved',conditions:[{stepId:first.stepId,outcome:'falsy'}]}]}];
    data.requestedFlowOverview!.boundaries = [{fromStepId:first.stepId,target:'service#dynamic',reason:'unresolved_dynamic_call'}];
    flowStore.data = data;
    const body = render(RequestedFlowSegmentRail).body;
    expect(body).toContain('조건 거짓');
    expect(body).toContain('다음 반복의 조건 재평가');
    expect(body).toContain('service#dynamic');
    expect(body).toContain('실행 시 결정되는 호출 대상을 확인하지 못했습니다.');
  });
});
