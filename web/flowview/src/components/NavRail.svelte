<script lang="ts">
  import { flowStore, LAYER_LABELS } from '../stores/flowStore.svelte';

  const steps = $derived(flowStore.steps);
  const selectedStepId = $derived(flowStore.selectedStepId);

  function handleSelect(stepId: string) {
    flowStore.select(stepId);
    const targetEl = document.querySelector(`[data-card="${stepId}"], [data-process="${stepId}"]`);
    targetEl?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
</script>

<nav class="rail" aria-label="흐름의 처리 단계">
  <h2>처리 순서</h2>
  <div id="step-nav">
    {#each steps as step (step.stepId)}
      {@const isSelected = selectedStepId === step.stepId}
      {@const layerLabel = LAYER_LABELS[step.layer] || step.layer || '계층 미확인'}
      <button
        type="button"
        class="step-link"
        data-select={step.stepId}
        aria-pressed={isSelected}
        onclick={() => handleSelect(step.stepId)}
      >
        <span class="idx">{String(step.ordinal || 1).padStart(2, '0')}</span>
        <span>
          <strong>{step.name}</strong>
          <small>{layerLabel}</small>
        </span>
      </button>
    {/each}
  </div>
  <p class="rail-note">
    단계를 선택하면 중앙의 같은 단계로 이동합니다.<br><br>
    연결은 분석에서 확인한 관계만 표시합니다.
  </p>
</nav>

<style>
  .rail {
    position: sticky;
    top: 70px;
    padding-top: 20px;
    max-height: calc(100vh - 85px);
    overflow: auto;
  }
  .rail h2 {
    font-size: 11px;
    letter-spacing: .6px;
    margin-bottom: 12px;
  }
  .step-link {
    width: 100%;
    display: flex;
    text-align: left;
    gap: 10px;
    border-color: transparent;
    background: transparent;
    margin-bottom: 4px;
    padding: 10px 8px;
    font-size: 11px;
    cursor: pointer;
    border-radius: 5px;
    border: 1px solid transparent;
  }
  .step-link .idx {
    font: 10px/1.9 ui-monospace, monospace;
    color: #666;
  }
  .step-link strong {
    display: block;
    font-size: 11px;
    font-weight: 600;
  }
  .step-link small {
    display: block;
    font-size: 10px;
    color: #666;
  }
  .step-link[aria-pressed=true] {
    background: #eee;
    border-color: #ccc;
    color: #171717;
  }
  .step-link[aria-pressed=true] small {
    color: #444;
  }
  .rail-note {
    font-size: 10px;
    color: #666;
    padding: 14px 8px;
    border-top: 1px solid #ddd;
    margin-top: 16px;
    line-height: 1.8;
  }
</style>
