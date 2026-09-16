<script lang="ts">
  import { flowStore, LAYER_LABELS, EDGE_LABELS } from '../stores/flowStore.svelte';
  import type { Step } from '../types/flow';

  const steps = $derived(flowStore.sceneSteps);
  const selectedStepId = $derived(flowStore.selectedStepId);
  const matchingStepIds = $derived(flowStore.matchingStepIds);

  function getStepRelations(step: Step) {
    return (flowStore.data?.semanticMap?.edges || []).filter(e => e.fromStepId === step.stepId);
  }

  function handleSelect(stepId: string) {
    flowStore.select(stepId);
  }
</script>

<div id="process-flow" class="flow-panel">
  {#if !steps.length}
    <p class="empty">이 요청에서 확인된 처리 단계가 없습니다.</p>
  {:else}
    {#each steps as step (step.stepId)}
      {@const isSelected = selectedStepId === step.stepId}
      {@const layerLabel = LAYER_LABELS[step.layer] || step.layer || '계층 미확인'}
      {@const rels = getStepRelations(step)}

      <article
        class="process-card"
        class:selected={isSelected}
        class:outside-focus={matchingStepIds !== null && !matchingStepIds.has(step.stepId)}
        class:condition-match={matchingStepIds !== null && matchingStepIds.has(step.stepId)}
        data-process={step.stepId}
      >
        <button
          type="button"
          class="process-select"
          data-select={step.stepId}
          onclick={() => handleSelect(step.stepId)}
        >
          <span class="card-ordinal">{String(step.ordinal || 1).padStart(2, '0')}</span>
          <span>
            <strong>{step.name}</strong>
            <small>{step.technicalName || ''}</small>
          </span>
          <span class="tag">{layerLabel}</span>
        </button>

        {#if step.branch}
          <div class="process-condition">조건 · {step.branch}</div>
        {/if}

        {#if step.sideEffect}
          <p class="process-effect">{step.sideEffect}</p>
        {/if}

        <div class="relation">
          {#if !rels.length}
            <span class="unresolved">분석에서 확인한 다음 연결 없음</span>
          {:else}
            {#each rels as edge (edge.toStepId + edge.kind)}
              {@const target = flowStore.steps.find(s => s.stepId === edge.toStepId)}
              {@const label = EDGE_LABELS[edge.kind] || '연결'}
              {#if target && edge.resolutionStatus === 'resolved'}
                <span>{label} →</span>
                <button type="button" data-select={target.stepId} onclick={() => handleSelect(target.stepId)}>
                  {target.technicalName || target.name}
                </button>
              {:else}
                <span class="unresolved">
                  {label}: {edge.toSymbolPath || target?.technicalName || '대상'} · 연결 미확인
                </span>
              {/if}
            {/each}
          {/if}
        </div>
      </article>
    {/each}
  {/if}
</div>

<style>
  .flow-panel {
    height: auto;
    overflow: visible;
    padding: 2px 6px 2px 2px;
  }
  .process-card {
    border: 1px solid #ccc;
    border-radius: 7px;
    margin-bottom: 18px;
    background: white;
    overflow: hidden;
    scroll-margin-top: 75px;
  }
  .process-card.selected {
    border: 2px solid #171717;
  }
  .process-card.outside-focus {
    border-style: dashed;
  }
  .process-card.outside-focus .process-select {
    background: #f5f5f5;
  }
  .process-card.condition-match {
    box-shadow: inset 4px 0 #171717;
  }
  .process-select {
    display: flex;
    gap: 12px;
    width: 100%;
    border: 0;
    border-radius: 0;
    text-align: left;
    padding: 16px;
    align-items: start;
    background: transparent;
    cursor: pointer;
  }
  .card-ordinal {
    font: 11px/1.8 ui-monospace, monospace;
    color: #666;
    min-width: 20px;
  }
  .process-select strong {
    display: block;
    font-size: 14px;
  }
  .process-select small {
    display: block;
    font-size: 10px;
    color: #666;
    overflow-wrap: anywhere;
  }
  .tag {
    margin-left: auto;
    font-size: 10px;
    border: 1px solid #ccc;
    padding: 2px 6px;
    border-radius: 4px;
    white-space: nowrap;
  }
  .process-condition {
    margin: 0 15px 12px;
    padding: 9px 12px;
    border-left: 3px solid #171717;
    background: #f3f3f3;
    font: 11px/1.8 ui-monospace, monospace;
    overflow-wrap: anywhere;
  }
  .process-effect {
    padding: 0 15px 12px;
    font-size: 11px;
    margin: 0;
  }
  .relation {
    padding: 10px 13px;
    border-top: 1px solid #ddd;
    background: #fafafa;
    font-size: 11px;
    display: flex;
    align-items: center;
    gap: 7px;
    flex-wrap: wrap;
  }
  .relation button {
    font-size: 10px;
    padding: 3px 7px;
    background: white;
    cursor: pointer;
  }
  .relation .unresolved {
    color: #666;
  }
  .empty {
    padding: 55px 15px;
    text-align: center;
    color: #666;
    font-size: 13px;
  }
</style>
