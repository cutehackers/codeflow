// Deliberately authored UI contract data. This is not an analyzer output or runtime trace.
export function flowHierarchyFixture(viewId = 'hierarchy') {
  const definitions = [
    ['request', '결제 요청 접수', 'presentation', 'const order = readOrder(request);'],
    ['normalize', '요청 금액 정규화', 'presentation', 'const amount = normalizeAmount(order.amount);'],
    ['validate', '재고와 금액 확인', 'application', 'if (amount > 0 && hasStock(order)) {'],
    ['reserve', '재고 예약', 'application', '  await reserveInventory(order);'],
    ['reject', '요청 거절 기록', 'application', '  await recordRejection(order);'],
    ['persist', '처리 결과 저장', 'repository', 'await saveOrderResult(order);'],
    ['respond', '결과 응답', 'presentation', 'return response(order.status);'],
    ['notify', '알림 서비스 인계', 'external', 'await notifications.publish(order);'],
  ];
  const steps = definitions.map(([stepId, name, layer], index) => ({stepId, name, layer, ordinal:index+1, structuralIdentity:stepId, technicalName:`checkout.${stepId}`, anchor:{repoRelativePath:'checkout.ts',enclosingSymbolPath:'checkout'}, ...(stepId === 'validate' ? {branch:'amount > 0 && hasStock(order)'} : {})}));
  const lines = definitions.map((d,index)=>({lineNumber:200+index,text:d[3]}));
  const frame = (id,title,role,stepRefs) => ({frameID:id,frameMatchKey:id,ordinal:0,title,role,stepRefs,primaryStepRef:stepRefs[0],status:'verified',text:'화면 검증용으로 작성한 처리 묶음',sourceAnchor:{repoRelativePath:'checkout.ts',enclosingSymbolPath:'checkout'}});
  const frames = [frame('request-gateway','요청 준비','entry',['request','normalize']), frame('validation-gateway','주문 가능 여부 결정','decision',['validate','reserve','reject']), frame('persistence-gateway','처리 결과 보존','effect',['persist']), frame('response-gateway','사용자에게 결과 전달','result',['respond']), frame('notification-gateway','외부 알림','boundary',['notify'])].map((f,i)=>({...f,ordinal:i+1}));
  const edge = (fromStepId,toStepId,kind='successor',resolutionStatus='resolved')=>({fromStepId,toStepId,kind,resolutionStatus});
  const edges = [edge('request','normalize'),edge('normalize','validate','call'),edge('validate','reserve','branch'),edge('validate','reject','branch'),edge('reserve','persist'),edge('reject','persist'),edge('persist','respond','return'),edge('persist','notify','async'),edge('notify','remote','async','unresolved')];
  edges.at(-1).toSymbolPath = 'NotificationService.receive';
  const data = {viewId,flowId:'checkout',request:{request:'결제 요청 흐름'},sourceNotice:'화면 검증용 데이터 · 실제 분석 결과나 실행 기록이 아닙니다.',semanticMap:{generationId:viewId,summary:{requested:'결제 요청: 준비 → 판단 → 저장 → 응답'},steps,edges,unknowns:[{subject:'NotificationService.receive',reason:'소스 연결 미확인'}]},flowSequence:{schemaId:'codeflow.flow-sequence',schemaVersion:1,generationId:viewId,computedBasisId:viewId,flowID:'checkout',snapshotID:viewId,frames},sourceFiles:{'checkout.ts':lines},flowContexts:Object.fromEntries(steps.map((s,i)=>[s.stepId,{stepId:s.stepId,canonicalPath:'checkout.ts',precision:'exact',generationId:viewId,snapshotId:viewId,displayedLines:lines.map(l=>({...l,isHit:l.lineNumber === 200+i}))}]))};
  if (viewId === 'missing-source') { data.sourceFiles={}; data.flowContexts={}; data.sourceContextMissing=true; }
  if (viewId === 'invalid') data.flowSequence.frames[0].stepRefs.push('not-in-map');
  if (viewId === 'baseline') data.semanticMap.summary.requested='비교 기준: 결제 요청';
  if (viewId === 'cycle') data.semanticMap.edges.push(edge('notify','validate','cycle'));
  if (viewId === 'many') {
    for(let i=0;i<25;i++) {
      const stepId=`additional-${i}`;
      data.semanticMap.steps.push({...steps[0],stepId,name:`긴 이름의 처리 ${i+1}: 요청한 흐름의 여러 계층에서 수행하는 처리`,structuralIdentity:stepId});
      data.flowSequence.frames.push({...frame(stepId,`추가 처리 ${i+1}`,'process',[stepId]),ordinal:i+6});
    }
  }
  return data;
}
