<script lang="ts">
  import { flowStore, DELTA_LABELS } from '../stores/flowStore.svelte';
  import { implementationSymbol, navigationTitle } from '../stores/flowNavigation';
  let baselineId = $state('');
  $effect(() => { baselineId = flowStore.baseline?.viewId || ''; });
</script>
<section class="notice" aria-label="분석 안내">
  <span role="status">{flowStore.notice}</span>
  <div class="controls">
    {#if flowStore.savedNavigationState}<button onclick={() => flowStore.restoreNavigationState()}>원래 장면 복귀</button>{/if}
    {#if flowStore.busy}
      <button onclick={() => window.cancelFlowRequest()}>취소</button>
    {:else}
      <button id="reanalyze" onclick={() => window.fetchTaskView(flowStore.flowTitle, '', '', true)}>다시 분석</button>
    {/if}
    <label for="baseline">비교 기준</label>
    <select id="baseline" bind:value={baselineId} disabled={flowStore.busy}>
      <option value="">분석 선택</option>
      {#each flowStore.views.filter(v => v.viewId !== flowStore.data?.viewId) as view (view.viewId)}
        <option value={view.viewId}>{view.title} · {view.savedAt ? new Date(view.savedAt).toLocaleString() : '저장된 분석'}</option>
      {/each}
    </select>
    <button id="compare" disabled={!baselineId || !flowStore.data?.viewId || flowStore.busy} onclick={() => window.compareFlowViews(baselineId)}>변경 전후 비교</button>
    {#if flowStore.compare}<button onclick={() => { flowStore.compare = false; }}>비교 닫기</button>{/if}
    {#if flowStore.pending?.viewId}<button onclick={() => window.openFlowView(flowStore.pending!.viewId!)}>새 분석 열기</button>{/if}
  </div>
  {#each flowStore.data?.flowSequence?.summaryLimitations || [] as limitation}
    <details class="summary-limitation">
      <summary>요약 한계 · {limitation.frameRefs.length}개 관문</summary>
      <p>{limitation.message} 코드를 펼쳐 처리 내용을 확인할 수 있습니다.</p>
      {#each limitation.frameRefs as ref}
        {@const frame = flowStore.frames.find(item => item.frameID === ref)}
        {#if frame}<button onclick={() => { flowStore.select(ref); if (!flowStore.expandedFrames.has(ref)) flowStore.toggleFrame(ref); }}>{frame.ordinal}. {navigationTitle(frame.title, implementationSymbol(flowStore.steps.find(step => step.stepId === frame.primaryStepRef)))}</button>{/if}
      {/each}
    </details>
  {/each}
  {#if flowStore.compare}
    <details><summary>변경 {flowStore.activeDeltaChanges.length}건</summary>
      {#each flowStore.activeDeltaChanges as change}
        <p>{DELTA_LABELS[change.kind]} · {flowStore.steps.find(s => s.stepId === change.targetStepId)?.name || flowStore.baseline?.semanticMap.steps.find(s => s.stepId === change.targetStepId)?.name || '대상 미확인'}</p>
      {/each}
    </details>
  {/if}
</section>
<style>
  .summary-limitation{flex-basis:100%;border-top:1px dashed #999;padding-top:9px;overflow-wrap:anywhere}.summary-limitation summary{cursor:pointer}.summary-limitation button{margin:3px;max-width:100%;overflow-wrap:anywhere;text-align:left}
  .notice {
    margin: 16px 30px 14px;
    border: 1px solid var(--line, #dddddd);
    background: #ffffff;
    border-radius: 6px;
    padding: 9px 13px;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 14px;
    font-size: 11px;
    min-height: 44px;
    box-sizing: border-box;
  }
  .notice .controls {
    margin-left: auto;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    min-width: 0;
  }
  .notice select { max-width: min(240px, 100%); }
  .notice button {
    font-size: 10px;
    padding: 4px 8px;
    cursor: pointer;
    border: 1px solid #cccccc;
    background: #ffffff;
    border-radius: 5px;
    color: inherit;
  }
  .notice button:hover {
    background: #eeeeee;
    border-color: #888888;
  }
  .notice button:disabled {
    opacity: 0.45;
    cursor: default;
  }

  @media (max-width: 650px) {
    .notice {
      margin-left: 15px;
      margin-right: 15px;
    }
  }
</style>
