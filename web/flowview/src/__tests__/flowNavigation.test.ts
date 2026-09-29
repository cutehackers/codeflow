import { describe, it, expect } from 'vitest';
import { buildFlowRelations, incomingRelationSummary, validateFlowReferences, sourceWindow, navigationRole, navigationTitle, buildNavigationTitles, executionNavigationLabel, selectedCodeLimitations, summarizeUnknownBoundaries, partitionExecutionNavigation, partitionCodeReadingNavigation, codeReadingRelationDetailSteps, codeReadingRelationDetailOwners, omittedCodeReadingBranches, requestedFocusFrame, type FlowRelation } from '../stores/flowNavigation';
import { samplePayload } from '../stores/sampleData';

it('keeps unproven invocation code accessible outside the requested execution path', () => {
  const data = samplePayload(1);
  const frames = data.flowSequence!.frames;
  const original = frames.map(frame => ({id: frame.frameID, refs: [...frame.stepRefs]}));
  data.flowSequence!.summaryLimitations = [{
    code: 'invocation_unproven',
    message: '실행 관계 미확인',
    frameRefs: [frames[1].frameID],
  }];
  const sections = partitionExecutionNavigation(data.flowSequence);
  expect(sections.execution.map(frame => frame.frameID)).toEqual(frames.filter(frame => frame !== frames[1]).map(frame => frame.frameID));
  expect(sections.unproven.map(frame => frame.frameID)).toEqual([frames[1].frameID]);
  expect(frames.map(frame => ({id: frame.frameID, refs: frame.stepRefs}))).toEqual(original);
  expect(partitionExecutionNavigation({...data.flowSequence!, summaryLimitations: []}).unproven).toEqual([]);
});

it('opens a verified requested implementation and rejects stale focus references', () => {
  const data = samplePayload(1);
  const entry = data.semanticMap.steps.find(step => data.flowSequence!.frames[1].stepRefs.includes(step.stepId))!;
  data.requestedFocus = {
    symbolPath: 'service.ts#applyMove', reason: '요청한 이동 처리를 구현한다',
    callStepId: data.semanticMap.steps[0].stepId, entryStepId: entry.stepId,
    snapshotId: data.flowSequence!.snapshotID, evidenceRefs: ['source-proof'],
  };
  expect(validateFlowReferences(data)).toBeNull();
  expect(requestedFocusFrame(data.flowSequence!, data.requestedFocus.entryStepId)?.frameID).toBe(data.flowSequence!.frames[1].frameID);
  data.requestedFocus.snapshotId = 'stale-snapshot';
  expect(validateFlowReferences(data)).toBeTruthy();
});

it('shows curator-selected code points first while retaining every other gateway', () => {
  const sequence = samplePayload(1).flowSequence!;
  const selectedFrames = [sequence.frames[0], sequence.frames[2]];
  const path = {nodes: selectedFrames.map(frame => ({frameId: frame.frameID}))} as any;
  const navigation = partitionCodeReadingNavigation(sequence, path);
  expect(navigation.reading.map(frame => frame.frameID)).toEqual(selectedFrames.map(frame => frame.frameID));
  expect(navigation.other.map(frame => frame.frameID)).toEqual(sequence.frames.filter(frame => !selectedFrames.includes(frame)).map(frame => frame.frameID));
  expect([...navigation.reading, ...navigation.other, ...navigation.unproven]).toHaveLength(sequence.frames.length);
});

it('keeps verified intermediate steps accessible below a short reading relation', () => {
  const data = samplePayload(1);
  const [entry, decision, effect, result] = data.semanticMap.steps.slice(0, 4);
  const path = {nodes: [{stepId: entry.stepId}, {stepId: result.stepId}]} as any;
  const relation = {fromNodeId: entry.stepId, toNodeId: result.stepId, edgeIndexes: [0, 1, 2]} as any;
  const edges = [
    {fromStepId: entry.stepId, toStepId: decision.stepId},
    {fromStepId: decision.stepId, toStepId: effect.stepId},
    {fromStepId: effect.stepId, toStepId: result.stepId},
  ] as any;
  expect(codeReadingRelationDetailSteps(relation, path, edges, data.semanticMap.steps).map(step => step.stepId))
    .toEqual([decision.stepId, effect.stepId]);
  path.relations = [relation, {...relation, fromNodeId: 'other', edgeIndexes: [1, 2]}];
  expect(codeReadingRelationDetailOwners(path, edges, data.semanticMap.steps).get(effect.stepId))
    .toBe(`${entry.stepId}\u0000${result.stepId}`);
});

it('identifies other confirmed outcomes from decisions inside summarized relations', () => {
  const edges = [
    {fromStepId: 'entry', toStepId: 'decision', kind: 'control_flow', resolutionStatus: 'resolved'},
    {fromStepId: 'decision', toStepId: 'success', kind: 'control_flow', resolutionStatus: 'resolved', conditions: [{stepId: 'decision', outcome: 'truthy'}]},
    {fromStepId: 'decision', toStepId: 'failure', kind: 'control_flow', resolutionStatus: 'resolved', conditions: [{stepId: 'decision', outcome: 'falsy'}]},
    {fromStepId: 'decision', toStepId: 'unknown', kind: 'control_flow', resolutionStatus: 'unresolved', conditions: [{stepId: 'decision', outcome: 'nullish'}]},
  ] as any;
  const path = {nodes: [{stepId: 'entry'}, {stepId: 'success'}], relations: [{fromNodeId: 'entry', toNodeId: 'success', edgeIndexes: [0, 1]}]} as any;
  expect(omittedCodeReadingBranches(path, edges).get('decision')).toEqual([{edgeIndex: 2, outcome: 'falsy', toStepId: 'failure'}]);
  expect(omittedCodeReadingBranches(null, edges).size).toBe(0);
});

it('restores a reading path that revisits an earlier selected loop condition', () => {
  const data = samplePayload(1);
  const steps = data.semanticMap.steps.slice(0, 4);
  data.semanticMap.steps = steps;
  data.flowSequence!.frames = data.flowSequence!.frames.slice(0, 4);
  data.flowResolution = {schemaVersion: 1, status: 'resolved', rawRequest: 'process loop'};
  const kinds = ['call', 'branch', 'mutation', 'return'];
  const roles = ['entry', 'decision', 'state_change', 'outcome'];
  for (const [index, step] of steps.entries()) {
    step.kind = kinds[index];
    const anchor = {...step.anchor!, byteRange: [index * 10, index * 10 + 5], fileHash: 'file', spanHash: step.stepId} as any;
    step.anchor = anchor;
    data.flowContexts![step.stepId] = {
      ...data.flowContexts![step.stepId], precision: 'exact', sourceValidationStatus: 'verified',
      statement: {byteRange: anchor.byteRange},
    };
  }
  const [entry, condition, body, result] = steps.map(step => step.stepId);
  data.semanticMap.edges = [
    {fromStepId: entry, toStepId: condition, kind: 'control_flow', resolutionStatus: 'resolved'},
    {fromStepId: condition, toStepId: body, kind: 'control_flow', resolutionStatus: 'resolved'},
    {fromStepId: body, toStepId: condition, kind: 'loop_back', resolutionStatus: 'resolved'},
    {fromStepId: condition, toStepId: result, kind: 'control_flow', resolutionStatus: 'resolved'},
  ];
  const links = [[entry, condition, 'control_flow', [0]], [condition, body, 'control_flow', [1]], [body, result, 'conditional', [2, 3]]] as const;
  data.codeReadingPath = {
    schemaId: 'https://codeflow.local/schemas/code-reading-path.schema.json', schemaVersion: 2,
    request: 'process loop', snapshotId: data.flowSequence!.snapshotID, computedBasisId: data.flowSequence!.computedBasisId,
    nodes: steps.map((step, index) => ({
      nodeId: step.stepId, stepId: step.stepId, frameId: data.flowSequence!.frames[index].frameID,
      symbol: `${step.anchor!.repoRelativePath}#${step.anchor!.enclosingSymbolPath}`, sourceAnchor: step.anchor!,
      role: roles[index], requestPart: 'loop', inclusionReason: 'Verified loop code',
      previousNodeIds: index ? [steps[index - 1].stepId] : [], nextNodeIds: index < 3 ? [steps[index + 1].stepId] : [],
      status: 'verified', evidenceRefs: ['proof'], codeViewAvailable: true,
    })),
    relations: links.map(([fromNodeId, toNodeId, kind, edgeIndexes]) => ({fromNodeId, toNodeId, kind, status: 'verified', edgeIndexes: [...edgeIndexes]})),
  } as any;
  expect(validateFlowReferences(data)).toBeNull();
  data.semanticMap.edges[2].kind = 'control_flow';
  expect(validateFlowReferences(data)).toBeTruthy();
});

it('groups repeated unknowns without showing internal callback identities', () => {
  const callbackReason = '콜백을 전달한 코드는 확인했지만 실제 호출 시점과 실행 여부는 확인되지 않았습니다.';
  const summaries = summarizeUnknownBoundaries([
    {subject:'lib/controller.dart#unresolved_dynamic.notifyListeners',reason:'unresolved_dynamic_call'},
    {subject:'lib/controller.dart#unresolved_dynamic.notifyListeners',reason:'unresolved_dynamic_call'},
    {subject:'lib/controller.dart#unresolved_dynamic.callback_123',reason:callbackReason},
    {subject:'lib/controller.dart#unresolved_dynamic.callback_456',reason:callbackReason},
    {subject:'lib/controller.dart#Controller.retry',reason:'재귀 호출이 현재 분석 경로에 있어 다음 실행 문장을 펼치지 못했습니다.'},
  ]);
  expect(summaries).toEqual([
    {description:'lib/controller.dart · notifyListeners 호출 · 코드에서 다음 처리와의 연결을 확인하지 못했습니다.',count:2},
    {description:'lib/controller.dart · 전달된 콜백 · 실제 호출 시점과 실행 여부를 확인하지 못했습니다.',count:2},
    {description:'lib/controller.dart · Controller.retry · 재귀 호출의 다음 실행 문장은 분석 범위 때문에 펼치지 못했습니다.',count:1},
  ]);
});

describe('flow navigation evidence', () => {
  it('shows a verified synchronous callee return without a caller bypass', () => {
    const data = samplePayload(1);
    const [call, entry, returned, next] = data.semanticMap.steps;
    call.kind = 'call'; call.ordinal = 1; call.invocationId = 'caller';
    entry.kind = 'guard'; entry.ordinal = 2; entry.invocationId = 'callee'; entry.callerStepOrdinal = 1;
    returned.kind = 'return'; returned.ordinal = 3; returned.invocationId = 'callee'; returned.callerStepOrdinal = 1;
    next.kind = 'mutation'; next.ordinal = 4; next.invocationId = 'caller';
    data.semanticMap.edges = [
      {fromStepId: call.stepId, toStepId: entry.stepId, kind: 'resolved_cross_file', resolutionStatus: 'resolved'},
      {fromStepId: entry.stepId, toStepId: returned.stepId, kind: 'control_flow', resolutionStatus: 'resolved'},
      {fromStepId: call.stepId, toStepId: next.stepId, kind: 'control_flow', resolutionStatus: 'resolved'},
      {fromStepId: returned.stepId, toStepId: next.stepId, kind: 'return', resolutionStatus: 'resolved'},
    ];
    const relations = buildFlowRelations(data);
    expect(relations).toHaveLength(3);
    expect(relations.some(relation => relation.edge.fromStepId === call.stepId && relation.edge.toStepId === next.stepId)).toBe(false);
    expect(relations.some(relation => relation.edge.fromStepId === returned.stepId && relation.edge.toStepId === next.stepId)).toBe(true);
  });
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

it('shows every selected line of a long source statement before expansion', () => {
  const lines = Array.from({length: 40}, (_, i) => ({lineNumber: i + 1, isHit: i >= 10 && i <= 30}));
  const visible = sourceWindow(lines, 11);
  expect(visible[0].lineNumber).toBeLessThan(11);
  expect(visible.at(-1)?.lineNumber).toBeGreaterThan(31);
  expect(visible.filter(line => line.isHit)).toHaveLength(21);
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
