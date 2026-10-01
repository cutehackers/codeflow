<script lang="ts">
  import { requestedFlowOverview, buildNavigationTitles } from '../stores/flowNavigation';
  import { conditionOutcomeLabel } from '../stores/conditionNavigation';
  import { flowStore, EDGE_LABELS } from '../stores/flowStore.svelte';
  const points = $derived(requestedFlowOverview(flowStore.data));
  const titles = $derived(buildNavigationTitles(flowStore.steps, flowStore.data?.semanticMap.edges || []));
  const roles: Record<string, string> = {entry:'시작', decision:'판단', transform:'값 처리', state_change:'상태 변경', effect:'외부 효과', handoff:'처리 인계', outcome:'결과', boundary:'분석 경계'};
</script>
<nav id="requested-flow-overview" class="rail" data-navigation-panel aria-label="선별 지점 읽기">
  <header><h2>선별 지점 읽기</h2><span>{points.length}개 코드 지점</span></header>
  <p>요청에 맞춰 선별한 코드입니다. 연결은 소스에서 확인한 관계이며 실제 실행 기록이 아닙니다.</p>
  {#each points as point (point.node.nodeId)}
    <section>
      <button class="reading-node" data-flow-step={point.node.stepId} data-navigation-focus={`overview:${point.node.nodeId}`} aria-pressed={flowStore.selectedStepId === point.node.stepId} onclick={() => flowStore.select(point.node.stepId)}>
        <small>{roles[point.node.role]}</small><strong>{point.node.inclusionReason}</strong>
        <span>요청: {point.node.requestPart}</span><code>{point.node.symbol}</code>
      </button>
      {#each point.joins as join (join.step.stepId)}
        <div class="boundary" data-overview-join={join.step.stepId}>
          <p>합류 · {titles.get(join.step.stepId)} · {join.incoming.length}개 확인된 선행 연결 · 동시 완료를 뜻하지 않음</p>
          {#each join.incoming as relation (relation.id)}
            <button data-join-source={relation.edge.fromStepId} data-navigation-focus={`overview-join:${relation.id}`} onclick={() => flowStore.select(relation.edge.fromStepId)}>← {titles.get(relation.edge.fromStepId)}{#each relation.edge.conditions || [] as condition} · {conditionOutcomeLabel(condition.outcome)}{/each}</button>
          {/each}
        </div>
      {/each}
      {#each point.outgoing as connection (`${connection.relation.fromNodeId}:${connection.relation.toNodeId}`)}
        {@const key = `${connection.relation.fromNodeId}\u0000${connection.relation.toNodeId}`}
        <div data-reading-relation={`${connection.relation.fromNodeId}:${connection.relation.toNodeId}`}>
          <p>확인된 연결 → {connection.target?.inclusionReason}</p>
          {#each connection.contextEdges as edge}
            <p>{EDGE_LABELS[edge.kind] || edge.kind}{#each edge.conditions || [] as condition} · {titles.get(condition.stepId)} · {conditionOutcomeLabel(condition.outcome)}{/each}{#if ['loop_back','loop_reentry','loop_exit_back','await_loop_back'].includes(edge.kind)} · 반복 재방문: {titles.get(edge.toStepId)}{/if}</p>
          {/each}
          {#if connection.details.length}
            <button class="disclosure" data-navigation-focus={`overview-relation:${key}`} aria-expanded={flowStore.expandedReadingRelations.has(key)} onclick={() => flowStore.toggleReadingRelation(key)}>경유 코드 {connection.details.length}단계 {flowStore.expandedReadingRelations.has(key) ? '접기' : '펼치기'}</button>
            {#if flowStore.expandedReadingRelations.has(key)}
              {#each connection.edges as edge}
                <p class="detail-connection">{titles.get(edge.fromStepId)} → {titles.get(edge.toStepId)} · {EDGE_LABELS[edge.kind] || edge.kind}{#each edge.conditions || [] as condition} · {conditionOutcomeLabel(condition.outcome)}{/each}</p>
              {/each}
              {#each connection.details as step (step.stepId)}
                <button class="detail" data-relation-step={step.stepId} data-flow-step={step.stepId} data-navigation-focus={`overview-detail:${step.stepId}`} aria-pressed={flowStore.selectedStepId === step.stepId} onclick={() => flowStore.select(step.stepId)}>{titles.get(step.stepId)} · 코드 열기</button>
              {/each}
            {/if}
          {/if}
        </div>
      {/each}
      {#each point.boundaries as relation (relation.id)}
        <div class="boundary">
          <p>{titles.get(relation.edge.fromStepId)} · {EDGE_LABELS[relation.edge.kind] || relation.edge.kind}{#each relation.edge.conditions || [] as condition} · {titles.get(condition.stepId)} · {conditionOutcomeLabel(condition.outcome)}{/each}</p>
          {#if relation.resolved && relation.toStep}
            <button data-branch-target={relation.toStep.stepId} data-navigation-focus={`overview-boundary:${relation.id}`} onclick={() => flowStore.select(relation.toStep!.stepId)}>다른 확인된 연결 → {titles.get(relation.toStep.stepId)}</button>
          {:else}<p>연결 미확인 · {relation.edge.toSymbolPath || relation.toStep?.name || '대상 미확인'}</p>{/if}
        </div>
      {/each}
      {#each point.limitations as limitation}<p class="boundary">{limitation}</p>{/each}
    </section>
  {/each}
</nav>
<style>
  .rail{position:sticky;top:16px;max-height:calc(100vh - 32px);overflow:auto;padding:20px 8px 24px 0;min-width:0;font-size:11px;line-height:1.6}header{display:flex;justify-content:space-between;gap:8px}h2{font-size:14px}p{margin:8px 0;color:#555}section{border-top:1px solid #ddd;padding:12px 0}.reading-node,.detail,.disclosure{display:block;width:100%;text-align:left;margin:6px 0;padding:8px;overflow-wrap:anywhere}.reading-node strong,.reading-node span,.reading-node code,.reading-node small{display:block}.reading-node[aria-pressed=true]{border-color:#111;background:#eee}.boundary{border:1px dashed #999;padding:6px;margin:8px 0}.boundary button{width:100%;text-align:left;overflow-wrap:anywhere}.detail{font-size:11px}@media(max-width:800px){.rail{position:static;max-height:45vh}}
</style>
