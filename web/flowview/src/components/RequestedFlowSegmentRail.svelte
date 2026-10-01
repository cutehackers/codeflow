<script lang="ts">
  import {flowStore, EDGE_LABELS} from '../stores/flowStore.svelte';
  import {partitionRequestedFlowRelations, requestedFlowJoins, type RequestedFlowJoin, type RequestedFlowRelation} from '../stores/requestedFlowRelations';
  import {conditionOutcomeLabel} from '../stores/conditionNavigation';
  const overview = $derived(flowStore.data?.requestedFlowOverview);
  const segments = $derived(overview?.segments || []);
  const groups = $derived(overview?.categories?.length ? overview.categories.map(category => ({category, segments:category.segmentIds.flatMap(id => segments.find(segment => segment.segmentId === id) || [])})) : [{category:null,segments}]);
  const selected = $derived(flowStore.selectedSegment || segments[0]);
  const intermediateJoins = $derived(flowStore.data ? requestedFlowJoins(flowStore.data, flowStore.steps.filter(step=>!overview?.segments.some(segment=>segment.stepIds.includes(step.stepId))).map(step=>step.stepId)) : []);
  const titles = $derived(new Map(flowStore.steps.map(step => [step.stepId, step.name])));
  function onSegmentKeydown(event: KeyboardEvent) {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
    const direction = event.key === 'ArrowDown' || event.key === 'ArrowRight' ? 1 : event.key === 'ArrowUp' || event.key === 'ArrowLeft' ? -1 : 0;
    if (!direction) return;
    event.preventDefault();
    const current = event.currentTarget as HTMLButtonElement;
    const buttons = Array.from(current.closest('nav')!.querySelectorAll<HTMLButtonElement>('[data-segment]'));
    const next = buttons[buttons.indexOf(current) + direction];
    if (!next?.dataset.segment) return;
    flowStore.selectSegment(next.dataset.segment);
    next.focus({preventScroll:true});
    next.scrollIntoView({block:'nearest',inline:'nearest'});
  }
  function openStep(stepId: string, segmentId?: string) {
    if (segmentId && flowStore.selectedSegmentId !== segmentId) flowStore.selectSegment(segmentId);
    flowStore.openSegmentDetail(stepId);
  }
  const reasons: Record<string, string> = {unverified_execution_relation:'후속 실행을 확인하지 못했습니다.', unresolved_dynamic_call:'실행 시 결정되는 호출 대상을 확인하지 못했습니다.', no_evidence:'연결을 뒷받침할 코드 근거가 없습니다.'};
  function onDisclosureToggle(event: Event, key: string) {
    const open = (event.currentTarget as HTMLDetailsElement).open;
    if (open !== flowStore.expandedReadingRelations.has(key)) flowStore.toggleReadingRelation(key);
  }
  function relationSignals(relations: RequestedFlowRelation[], stepIds: string[]) {
    const outcomes = [...new Set(relations.flatMap(relation => relation.edges.flatMap(edge => edge.conditions?.map(condition => conditionOutcomeLabel(condition.outcome)) || [])))];
    if (relations.some(relation => relation.status === 'verified' && relation.edges.some(edge => ['loop_back','loop_reentry','loop_exit_back','await_loop_back'].includes(edge.kind)))) outcomes.push('반복');
    if (flowStore.data && requestedFlowJoins(flowStore.data, stepIds).length) outcomes.push('합류');
    if (flowStore.steps.some(step => stepIds.includes(step.stepId) && ['failure','throw'].includes(step.kind || '')) || relations.some(relation => relation.status === 'verified' && relation.edges.some(edge => ['failure','error','throw','exception'].includes(edge.kind)))) outcomes.push('실패');
    if (overview?.boundaries.some(boundary => stepIds.includes(boundary.fromStepId)) || relations.some(relation => relation.status !== 'verified')) outcomes.push('연결 미확인');
    return outcomes.join(' · ');
  }
  function boundaryGroups(stepIds: string[]) {
    const groups = new Map<string, {target?: string; reason: string; stepIds: string[]}>();
    for (const boundary of overview?.boundaries.filter(item => stepIds.includes(item.fromStepId)) || []) {
      const key = JSON.stringify([boundary.target, boundary.reason]);
      const group = groups.get(key) || {target:boundary.target, reason:boundary.reason, stepIds:[]};
      if (!group.stepIds.includes(boundary.fromStepId)) group.stepIds.push(boundary.fromStepId);
      groups.set(key, group);
    }
    return [...groups.values()];
  }
</script>
{#snippet connection(relation: RequestedFlowRelation, segmentId?: string)}
  {@const disclosureKey = `relation-detail:${segmentId || 'intermediate'}:${relation.fromStepId}:${relation.toStepId}:${relation.edgeIndexes.join(',')}`}
  <div class="relation" data-segment-relation={`${relation.fromStepId}:${relation.toStepId}`}>
    <p>{EDGE_LABELS[relation.kind] || relation.kind}{relation.status === 'verified' ? ' · 확인된 연결' : ' · 연결 미확인'}</p>
    {#each relation.edges.filter(edge => !['control_flow','successor'].includes(edge.kind) || edge.conditions?.length) as edge}<p>{titles.get(edge.fromStepId)} · {EDGE_LABELS[edge.kind] || edge.kind}{#each edge.conditions || [] as condition} · {conditionOutcomeLabel(condition.outcome)}{/each}</p>{/each}
    {#if relation.toSegmentId && relation.status === 'verified'}<button data-navigation-focus={`segment-relation:${relation.fromStepId}:${relation.toStepId}`} onclick={() => flowStore.selectSegment(relation.toSegmentId!)}>{overview?.segments.find(item => item.segmentId === relation.toSegmentId)?.purpose} · 코드 블록 열기</button>{/if}
    <details open={flowStore.expandedReadingRelations.has(disclosureKey)} ontoggle={event=>onDisclosureToggle(event,disclosureKey)}><summary>연결의 코드 읽기 · {relation.edgeIndexes.length}개 확인 근거</summary>{#each relation.edges as edge}<button data-navigation-focus={`${segmentId ? 'segment-edge' : 'intermediate'}:${edge.fromStepId}:${edge.toStepId}`} onclick={() => openStep(edge.fromStepId, segmentId)}>{titles.get(edge.fromStepId)} · 문장별 탐색</button>{/each}{#if titles.has(relation.toStepId)}<button data-navigation-focus={`relation-target:${segmentId || 'intermediate'}:${relation.fromStepId}:${relation.toStepId}`} onclick={() => openStep(relation.toStepId, segmentId)}>{titles.get(relation.toStepId)} · 문장별 탐색</button>{/if}</details>
  </div>
{/snippet}
{#snippet joinConnection(join: RequestedFlowJoin, segmentId?: string)}
  <div class="relation" data-segment-join={join.stepId}>
    <p>{`${join.parallel ? '병렬 작업 완료 대기' : '합류'} · ${titles.get(join.stepId)} · ${join.predecessors.length}개 확인된 선행 연결${join.parallel ? '' : ' · 동시 완료를 뜻하지 않음'}`}</p>
    <p>선행 코드: {join.predecessors.map(id => titles.get(id)).join(' · ')}</p>
    {#each join.predecessors as predecessor}<button data-navigation-focus={`segment-join:${predecessor}:${join.stepId}`} onclick={() => openStep(predecessor,segmentId)}>{titles.get(predecessor)} · 문장별 탐색</button>{/each}
    <button data-navigation-focus={`segment-join-target:${join.stepId}`} onclick={() => openStep(join.stepId,segmentId)}>{titles.get(join.stepId)} · 합류 코드 문장별 탐색</button>
  </div>
{/snippet}
{#snippet routineConnections(relations: RequestedFlowRelation[], key: string, segmentId?: string)}
  {#if relations.length}
    <button class="disclosure" data-navigation-focus={`routine:${key}`} aria-expanded={flowStore.expandedReadingRelations.has(key)} onclick={() => flowStore.toggleReadingRelation(key)}>기타 내부 연결 {relations.length}개 {flowStore.expandedReadingRelations.has(key) ? '접기' : '펼치기'}</button>
    {#if flowStore.expandedReadingRelations.has(key)}<div data-routine-relations={key}>{#each relations as relation}{@render connection(relation,segmentId)}{/each}</div>{/if}
  {/if}
{/snippet}
<nav id="requested-flow-overview" class="rail" data-navigation-panel aria-label="처리 구간">
  <h2>처리 구간</h2><p class="keyboard-hint">선택 후 ↑ ↓ 또는 ← →로 이동</p>
  {#each groups as group}
    {#if group.category}<h3 data-segment-category={group.category.categoryId}>{group.category.title}</h3>{/if}
    <ol class="segment-list">
    {#each group.segments as segment (segment.segmentId)}
      {@const signals = relationSignals(overview?.relations.filter(relation => relation.fromSegmentId === segment.segmentId) || [], segment.stepIds)}
      <li><button class="reading-node" onkeydown={onSegmentKeydown} data-segment={segment.segmentId} data-navigation-focus={`segment:${segment.segmentId}`} aria-pressed={flowStore.selectedSegmentId === segment.segmentId} onclick={() => flowStore.selectSegment(segment.segmentId)}><strong>{segment.purpose}</strong><small>{segment.blocks.length}개 코드 블록</small>{#if signals}<span class="signals">{signals}</span>{/if}</button></li>
    {/each}
    </ol>
  {/each}
  {#if selected}
    {@const joins = flowStore.data ? requestedFlowJoins(flowStore.data,selected.stepIds) : []}
    {@const relations = flowStore.data ? partitionRequestedFlowRelations(flowStore.data, overview?.relations.filter(relation => relation.fromSegmentId === selected.segmentId) || []) : {primary:[],routine:[]}}
    <details class="context-detail" data-segment-context={selected.segmentId} open={flowStore.expandedReadingRelations.has(`segment-context:${selected.segmentId}`)} ontoggle={event=>onDisclosureToggle(event,`segment-context:${selected.segmentId}`)}>
      <summary>판단과 연결 상세</summary>
      {#each joins as join (join.stepId)}{@render joinConnection(join,selected.segmentId)}{/each}
      {#each relations.primary as relation}{@render connection(relation,selected.segmentId)}{/each}
      {@render routineConnections(relations.routine,`segment-routine:${selected.segmentId}`,selected.segmentId)}
      {#each boundaryGroups(selected.stepIds) as boundary}<div class="boundary"><p>{boundary.target} · {reasons[boundary.reason] || boundary.reason}</p>{#each boundary.stepIds as stepId}<button data-navigation-focus={`segment-boundary:${stepId}:${boundary.target}`} onclick={() => openStep(stepId, selected.segmentId)}>마지막 확인 코드 · {titles.get(stepId)} · 문장별 탐색</button>{/each}</div>{/each}
    </details>
  {/if}
  {#if intermediateJoins.length || overview?.relations.some(relation => !relation.fromSegmentId) || overview?.boundaries.some(boundary => !overview.segments.some(segment => segment.stepIds.includes(boundary.fromStepId)))}
    {@const relations = flowStore.data ? partitionRequestedFlowRelations(flowStore.data, overview?.relations.filter(relation => !relation.fromSegmentId) || []) : {primary:[],routine:[]}}
    {@const intermediateIds = flowStore.steps.filter(step=>!overview?.segments.some(segment=>segment.stepIds.includes(step.stepId))).map(step=>step.stepId)}
    <details class="context-detail" aria-label="경유 처리의 판단과 연결" open={flowStore.expandedReadingRelations.has('intermediate-context')} ontoggle={event=>onDisclosureToggle(event,'intermediate-context')}><summary>경유 처리 · {relationSignals([...relations.primary,...relations.routine],intermediateIds)} · 상세</summary>
      {#each intermediateJoins as join (join.stepId)}{@render joinConnection(join)}{/each}
      {#each relations.primary as relation}{@render connection(relation)}{/each}
      {@render routineConnections(relations.routine,'intermediate-routine')}
      {#each boundaryGroups(flowStore.steps.filter(step=>!overview?.segments.some(segment=>segment.stepIds.includes(step.stepId))).map(step=>step.stepId)) as boundary}<div class="boundary"><p>{boundary.target} · {reasons[boundary.reason] || boundary.reason}</p>{#each boundary.stepIds as stepId}<button onclick={() => openStep(stepId)}>마지막 확인 코드 · {titles.get(stepId)} · 문장별 탐색</button>{/each}</div>{/each}
    </details>
  {/if}
</nav>
<style>
  .rail{position:sticky;top:16px;max-height:calc(100vh - 32px);overflow:auto;padding:12px 8px 24px 4px;scroll-padding:4px;min-width:0;font-size:11px;line-height:1.6}h2{font-size:14px}.keyboard-hint{color:#666;font-size:10px;margin:4px 0 12px}h3{font-size:11px;color:#666;margin:16px 0 6px}.segment-list{list-style:none;padding:0;margin:6px 0 12px}.segment-list li{margin:0 0 6px}.reading-node{width:100%;text-align:left;padding:12px;border:1px solid #ddd;background:#fff;color:#171717;border-radius:5px}.reading-node{scroll-margin:4px}.reading-node:focus-visible{outline:2px solid #171717;outline-offset:2px}.reading-node strong,.reading-node span,.reading-node small{display:block}.reading-node small{color:#666;margin-top:4px}.signals{color:#6c5322;font-size:10px;margin-top:6px}.reading-node[aria-pressed=true]{border-color:#111;background:#eee;color:#171717}button{max-width:100%;overflow-wrap:anywhere;text-align:left;font-size:11px;margin:4px 0}.relation,.boundary{margin:8px 0;padding:8px;border:1px solid #ddd}.boundary{border-style:dashed}.context-detail{border-top:1px solid #ddd;padding:12px 0}.context-detail summary{cursor:pointer}details button{display:block}@media(max-width:800px){.rail{position:static;max-height:45vh}}
</style>
