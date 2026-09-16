<script lang="ts">
  import { flowStore, DELTA_LABELS } from '../stores/flowStore.svelte';
  let baselineId = $state('');
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
  {#if flowStore.compare}
    <details><summary>변경 {flowStore.activeDeltaChanges.length}건</summary>
      {#each flowStore.activeDeltaChanges as change}
        <p>{DELTA_LABELS[change.kind]} · {flowStore.steps.find(s => s.stepId === change.targetStepId)?.name || flowStore.baseline?.semanticMap.steps.find(s => s.stepId === change.targetStepId)?.name || '대상 미확인'}</p>
      {/each}
    </details>
  {/if}
</section>
<style>
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
