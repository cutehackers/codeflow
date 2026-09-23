<script lang="ts">
  import { conditionOutcomeLabel } from '../stores/conditionNavigation';
  import { buildNavigationTitles, incomingRelationSummary } from '../stores/flowNavigation';
  import { flowStore, GATEWAY_ROLES, EDGE_LABELS } from '../stores/flowStore.svelte';
  const relations = $derived(flowStore.relations);
  const crossRelations = $derived(relations.filter(r => r.fromFrame && (!r.resolved || r.fromFrame.frameID !== r.toFrame?.frameID)));
  const navigationTitles = $derived(buildNavigationTitles(flowStore.steps, flowStore.data?.semanticMap.edges || []));
</script>
<section class="overview" aria-label="요청 흐름의 관문과 연결">
  <header>
    <div><p class="eyebrow">FLOWSEQUENCE</p><h1>{flowStore.flowTitle}</h1><p class="caption">{flowStore.frames.length}개 관문 · {flowStore.steps.length}개 실행 단계 · {flowStore.data?.sourceContextMissing ? '입력된 연결 · 소스 근거 미확인' : '소스에서 확인한 연결'}</p></div>
    <button aria-expanded={flowStore.overviewOpen} aria-controls="flow-overview" onclick={() => flowStore.overviewOpen = !flowStore.overviewOpen}>{flowStore.overviewOpen ? '개요 접기' : '전체 흐름 보기'}</button>
  </header>
  {#if !flowStore.overviewOpen}<p class="selection">현재 관문 · {flowStore.selectedFrame?.title}</p>{/if}
  {#if flowStore.overviewOpen}
    <div class="track" id="flow-overview" data-navigation-panel>
      {#each flowStore.frames as frame, index (frame.frameID)}
        {@const outgoing = crossRelations.filter(r=>r.fromFrame?.frameID === frame.frameID)}
        {@const incoming = crossRelations.filter(r=>r.resolved && r.toFrame?.frameID === frame.frameID)}
        {@const incomingSummary = incomingRelationSummary(incoming)}
        <article class:path-match={!!flowStore.matchingStepIds && frame.stepRefs.some(id => flowStore.matchingStepIds!.has(id))} class:active={flowStore.selectedFrameId === frame.frameID}>
          <button class="frame" data-flow-frame={frame.frameID} aria-pressed={flowStore.selectedFrameId === frame.frameID} onclick={() => flowStore.select(frame.frameID)}>
            <span class="number">{String(index+1).padStart(2,'0')} <span>{GATEWAY_ROLES[frame.role] || frame.role}</span></span>
            <strong>{frame.title}</strong>
            <span class="caption">실행 타임라인 {frame.stepRefs.length}단계</span>
          </button>
          {#if incomingSummary}<p class="incoming">↳ {incomingSummary}</p>{/if}
          <ul aria-label={`${frame.title}의 후속 연결`}>
            {#each outgoing as relation (relation.id)}
              <li class:unknown={!relation.resolved}>
                <button class="relation" aria-pressed={flowStore.selectedRelationId === relation.id} onclick={() => flowStore.selectRelation(flowStore.selectedRelationId === relation.id ? null : relation.id)}>
                  <small>{relation.fromStep ? navigationTitles.get(relation.fromStep.stepId) || relation.fromStep.name : '출발 코드 미확인'}</small>
                  <span>{relation.resolved ? '→' : '⋯'} {EDGE_LABELS[relation.edge.kind] || relation.edge.kind}{#if relation.edge.conditions?.[0]} · {conditionOutcomeLabel(relation.edge.conditions[0].outcome)}{/if}</span>
                  <strong>{relation.resolved ? relation.toFrame?.title || (relation.toStep ? navigationTitles.get(relation.toStep.stepId) || relation.toStep.name : '') : '다음 연결 미확인'}</strong>
                </button>
              </li>
            {/each}
          </ul>
          {#if !outgoing.length}<p class="caption terminal">{incoming.some(relation => relation.edge.kind === 'finally') ? '정리 뒤 종료 결과 미확인' : frame.role === 'result' ? '확인된 결과' : frame.role === 'boundary' ? '다음 연결 미확인' : '관문 밖 연결 정보 없음'}</p>{/if}
        </article>
      {/each}
    </div>
    <p class="legend">번호는 읽기 순서입니다. 조건·동기화 근거가 없는 연결을 실행 순서로 추정하지 않습니다.</p>
  {/if}
  {#if flowStore.selectedRelation}
    {@const relation = flowStore.selectedRelation}
    <div class="relation-focus" role="status">
      <div><strong>직접 연결 강조</strong><p>{relation.fromStep?.name} → {relation.resolved ? relation.toStep?.name : relation.edge.toSymbolPath || '대상 미확인'}</p>
      {#if relation.fromStep?.branch}<p>출발 단계의 조건 · {relation.fromStep.branch}</p>{/if}
      {#if relation.edge.conditions?.[0]}<small>{conditionOutcomeLabel(relation.edge.conditions[0].outcome)}일 때의 연결</small>{:else}<small>이 조건이 어느 후속 경로에 해당하는지는 현재 연결 데이터만으로 확정하지 않습니다.</small>{/if}</div>
      <button onclick={() => {if(relation.fromStep) flowStore.select(relation.fromStep.stepId)}}>출발 코드</button>
      {#if relation.resolved && relation.toStep}<button onclick={() => flowStore.select(relation.toStep!.stepId)}>도착 코드</button>{/if}
      <button onclick={() => flowStore.selectRelation(null)}>강조 해제</button>
    </div>
  {/if}
</section>
<style>
  .overview{margin:20px 30px;border:1px solid #ccc;border-radius:10px;background:#fff;overflow:hidden}
  header{display:flex;align-items:flex-start;justify-content:space-between;gap:20px;padding:24px}.eyebrow{font-size:10px;letter-spacing:2px;color:#666}h1{font-size:20px;line-height:1.5;margin:8px 0;overflow-wrap:anywhere}.caption{font-size:11px;color:#666}header button{white-space:nowrap;font-size:11px}
  .track{display:grid;grid-template-columns:repeat(auto-fit,minmax(205px,1fr));gap:12px;padding:0 24px 20px;max-height:360px;overflow:auto}
  article{border:1px solid #ddd;border-radius:6px;min-width:0;align-self:start}.active{border-color:#171717;box-shadow:0 0 0 1px #171717}.frame{width:100%;border:0;border-radius:5px 5px 0 0;text-align:left;padding:14px;background:#fafafa}.frame strong{display:block;font-size:13px;line-height:1.55;margin:8px 0;overflow-wrap:anywhere}.frame[aria-pressed=true]{background:#171717;color:white}.frame[aria-pressed=true] .caption{color:#ddd}.number{display:flex;justify-content:space-between;font:11px ui-monospace,monospace}
  ul{list-style:none;margin:0;padding:0 10px}.relation{font-size:11px;text-align:left;width:100%;border:0;border-top:1px solid #ddd;border-radius:0;padding:10px 4px;background:transparent}.relation span{display:block;font-size:10px;margin-bottom:3px}.relation strong{font-weight:500;overflow-wrap:anywhere}.relation[aria-pressed=true]{background:#eee;color:#111;outline:1px solid #111}.unknown{border-bottom:1px dashed #999}.incoming{font-size:10px;padding:8px 12px;background:#f5f5f5}.terminal{padding:10px 14px}.legend{padding:12px 24px;border-top:1px solid #eee;color:#666;font-size:11px}.selection{padding:0 24px 18px;font-size:12px}
  .path-match{border-left:4px solid #111}
  .relation-focus{display:flex;align-items:center;gap:8px;flex-wrap:wrap;border-top:1px solid #aaa;padding:16px 24px;background:#f5f5f5;font-size:12px}.relation-focus div{flex:1;min-width:180px}.relation-focus small{color:#666}.relation-focus button{font-size:11px}
  @media(max-width:800px){.overview{margin:12px 15px}header{padding:16px;gap:10px}h1{font-size:17px}.track{grid-template-columns:repeat(auto-fit,minmax(180px,1fr));padding:0 16px 16px;max-height:300px}}
  @media(max-width:360px){.overview{margin:8px 10px}header{padding:12px;gap:8px;flex-wrap:wrap}header > div{flex:1 1 0;min-width:0}header button{margin-left:auto}.track{grid-template-columns:minmax(0,1fr);padding:0 10px 12px}.relation-focus{padding:12px}.relation-focus div{min-width:0}.legend,.selection{padding-left:12px;padding-right:12px}}
</style>
