import { describe, it, expect } from 'vitest';
import { buildFlowRelations, incomingRelationSummary, validateFlowReferences, sourceWindow, navigationRole, navigationTitle, buildNavigationTitles, executionNavigationLabel, selectedCodeLimitations, type FlowRelation } from '../stores/flowNavigation';
import { samplePayload } from '../stores/sampleData';

describe('flow navigation evidence', () => {
  it('keeps branches and a shared target without inventing adjacent links', () => {
    const data = samplePayload(1);
    const [a,b,c,d] = data.semanticMap.steps;
    data.semanticMap.edges = [
      {fromStepId:a.stepId,toStepId:b.stepId,kind:'branch',resolutionStatus:'resolved'},
      {fromStepId:a.stepId,toStepId:c.stepId,kind:'branch',resolutionStatus:'resolved'},
      {fromStepId:b.stepId,toStepId:d.stepId,kind:'call',resolutionStatus:'resolved'},
      {fromStepId:c.stepId,toStepId:d.stepId,kind:'call',resolutionStatus:'resolved'},
      {fromStepId:d.stepId,toStepId:'missing',kind:'async',resolutionStatus:'unresolved',toSymbolPath:'remote'},
    ];
    const relations = buildFlowRelations(data);
    expect(relations).toHaveLength(5);
    expect(relations.filter(r=>r.toStep?.stepId === d.stepId)).toHaveLength(2);
    expect(relations[4].resolved).toBe(false);
    expect(relations[4].toFrame).toBeNull();
    expect(relations.some(r=>r.fromStep?.stepId===b.stepId && r.toStep?.stepId===c.stepId)).toBe(false);
  });
  it('rejects duplicate parents and missing representative references', () => {
    const data = samplePayload(1);
    data.flowSequence!.frames[1].stepRefs.push(data.semanticMap.steps[0].stepId);
    expect(validateFlowReferences(data)).toBeTruthy();
    const valid = samplePayload(1);
    valid.flowSequence!.frames[0].primaryStepRef='missing';
    expect(validateFlowReferences(valid)).toBeTruthy();
  });
});

it('keeps the selected source line visible in sparse source bundles', () => {
  const lines = Array.from({length: 40}, (_, i) => ({lineNumber: i + 200}));
  expect(sourceWindow(lines, 225).map(l => l.lineNumber)).toContain(225);
});

it('uses a known function instead of a raw code statement in navigation', () => {
  expect(navigationTitle('const payload = { items: cart }', 'HomePage.checkout')).toBe('HomePage.checkout');
  expect(navigationTitle('setStatus("ready")', 'HomePage.checkout')).toBe('HomePage.checkout');
  expect(navigationTitle('주문 가능 여부 결정', 'CheckoutService.validate')).toBe('주문 가능 여부 결정');
});

it('labels a break by its verified control target', () => {
  const step = {stepId:'break',kind:'break',name:'break;',layer:'service'};
  const target = {stepId:'next',kind:'call',name:'finish()',layer:'service'};
  const switchExit = [{fromStepId:'break',toStepId:'next',kind:'switch_exit',resolutionStatus:'resolved'}];
  expect(navigationRole(step, switchExit)).toBe('분기 종료');
  expect(buildNavigationTitles([step, target], switchExit).get('break')).toBe('분기 종료');
  expect(navigationRole(step, [])).toBe('반복 종료');
});

it('distinguishes a verified parallel wait from an ordinary shared destination', () => {
  const parallel: FlowRelation[] = [
    {edge:{fromStepId:'a',toStepId:'wait',kind:'parallel_wait',resolutionStatus:'resolved'}, fromStep:null, toStep:null, fromFrame:null, toFrame:null, resolved:true, id:'a'},
    {edge:{fromStepId:'b',toStepId:'wait',kind:'parallel_wait',resolutionStatus:'resolved'}, fromStep:null, toStep:null, fromFrame:null, toFrame:null, resolved:true, id:'b'},
  ];
  expect(incomingRelationSummary(parallel)).toBe('2개 병렬 작업 완료 대기');
  expect(incomingRelationSummary([{...parallel[0], edge:{...parallel[0].edge, kind:'control_flow'}} , parallel[1]])).toBe('2개 선행 연결 · 동시 완료를 뜻하지 않음');
});

it('preserves summary limitations separately from source status and validates targets', () => {
  const data = samplePayload(1);
  const before = data.flowSequence!.frames.map(frame => frame.status);
  data.flowSequence!.summaryLimitations = [{code:'grouping_evidence_missing',message:'묶음 근거 부족',frameRefs:[data.flowSequence!.frames[0].frameID]}];
  expect(validateFlowReferences(data)).toBeNull();
  expect(data.flowSequence!.frames.map(frame => frame.status)).toEqual(before);
  data.flowSequence!.summaryLimitations[0].frameRefs = ['missing'];
  expect(validateFlowReferences(data)).toBeTruthy();
});

it('distinguishes calls and assignment results using confirmed target symbols', () => {
  const steps = [
    {stepId:'call',ordinal:1,kind:'call',name:'adjust(total)',layer:'service',anchor:{enclosingSymbolPath:'processOrder'}},
    {stepId:'body',ordinal:2,kind:'return',name:'return value + 1;',layer:'service',anchor:{enclosingSymbolPath:'applyAdjustment01'}},
    {stepId:'assign',ordinal:3,kind:'mutation',name:'total = adjust(total)',layer:'service',assignmentSourceOrdinal:1,anchor:{enclosingSymbolPath:'processOrder'}},
  ];
  const edges = [{fromStepId:'call',toStepId:'body',kind:'call',resolutionStatus:'resolved'}];
  const titles = buildNavigationTitles(steps, edges);
  expect(titles.get('call')).toBe('applyAdjustment01 호출');
  expect(titles.get('assign')).toBe('applyAdjustment01 결과 반영');
  expect(buildNavigationTitles(steps, [{...edges[0],resolutionStatus:'unresolved'}]).get('call')).toBe('processOrder');
  expect(buildNavigationTitles(steps, [{...edges[0],kind:'control_flow'}]).get('call')).toBe('processOrder');
  expect(buildNavigationTitles(steps, [{...edges[0],toStepId:'missing'}]).get('call')).toBe('processOrder');
  expect(buildNavigationTitles(steps, [...edges,edges[0]]).get('call')).toBe('applyAdjustment01 호출');
});

it('keeps purpose titles and distinguishes conditions without inventing business meaning', () => {
  const steps = [
    {stepId:'a',kind:'guard',name:'if (!request.authenticated) return',branch:'!request.authenticated',layer:'service',anchor:{enclosingSymbolPath:'processOrder'}},
    {stepId:'b',kind:'guard',name:'if (!request.authorized) return',branch:'!request.authorized',layer:'service',anchor:{enclosingSymbolPath:'processOrder'}},
    {stepId:'purpose',kind:'call',name:'주문 가능 여부 결정',layer:'service',anchor:{enclosingSymbolPath:'processOrder'}},
  ];
  const titles = buildNavigationTitles(steps, []);
  expect(titles.get('a')).toBe('조건: !request.authenticated');
  expect(titles.get('b')).toBe('조건: !request.authorized');
  expect(titles.get('purpose')).toBe('주문 가능 여부 결정');
});


describe('selected code limitations', () => {
  it('shows referenced limitations without global telemetry or unrelated failures', () => {
    const data = samplePayload(1);
    const step = data.semanticMap.steps[0];
    step.rules = ['boundary:await-rejection'];
    data.semanticMap.unknowns = [
      {subject: 'await-rejection', reason: '대기 실패 경로를 확인하지 못했습니다.'},
      {subject: 'await-rejection', reason: '대기 실패 경로를 확인하지 못했습니다.'},
      {subject: 'causalObservationClosure', reason: 'internal closure status'},
      {subject: 'another-step', reason: 'unrelated failure'}
    ];
    expect(selectedCodeLimitations(data, step)).toEqual(['대기 실패 경로를 확인하지 못했습니다.']);
    expect(selectedCodeLimitations(data, data.semanticMap.steps[1])).toEqual([]);
  });
  it('resolves an unresolved edge subject and translates reason codes', () => {
    const data = samplePayload(1);
    const step = data.semanticMap.steps[0];
    data.semanticMap.edges = [{fromStepId:step.stepId,toStepId:'',toSymbolPath:'external-target',kind:'unknown_edge',resolutionStatus:'unresolved'}];
    data.semanticMap.unknowns = [{subject:'external-target',reason:'unresolved_dynamic_call'}];
    expect(selectedCodeLimitations(data,step)).toEqual(['실행 시 결정되는 호출 대상을 확인하지 못했습니다.']);
  });
});


it('uses implementation context for mutation syntax while preserving purpose names', () => {
  const names = ['count++', '++count', 'order.total += 1', 'order.status = "paid"', 'delete order.pending', '[first, second] = values'];
  const steps = names.map((name,index)=>({stepId:String(index),kind:'mutation',name,layer:'service',anchor:{enclosingSymbolPath:'checkout'}}));
  const titles=buildNavigationTitles(steps,[]);
  for (const step of steps) expect(titles.get(step.stepId)).toBe('상태 변경');
  expect(navigationTitle('재시도 횟수 갱신','checkout')).toBe('재시도 횟수 갱신');
});

it('uses the known owner as an execution rail mutation title without restoring source code', () => {
  const step = {stepId:'mutation',kind:'mutation',name:'order.status = "paid"',layer:'service',anchor:{enclosingSymbolPath:'CheckoutService.complete'}};
  expect(executionNavigationLabel(step, buildNavigationTitles([step], []).get(step.stepId)!)).toEqual({title:'CheckoutService.complete', detail:'상태 변경'});
});
