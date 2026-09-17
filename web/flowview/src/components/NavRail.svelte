<script lang="ts">
  import { flowStore, GATEWAY_ROLES, LAYER_LABELS } from '../stores/flowStore.svelte';

  const frames = $derived(flowStore.frames);
  const selectedFrameId = $derived(flowStore.selectedFrameId);
  const selectedStepId = $derived(flowStore.selectedStepId);
  const compare = $derived(flowStore.compare);
  const deltaChanges = $derived(compare ? (flowStore.data?.semanticDelta?.changes || []) : []);

  function onFrameSelect(frameID: string) {
    flowStore.select(frameID);
    const targetEl = document.querySelector(`[data-card="${frameID}"], [data-card="${flowStore.selectedStepId}"], [data-process="${flowStore.selectedStepId}"]`);
    targetEl?.scrollIntoView({ behavior: 'smooth', block: 'start' });
    const frameEl = document.querySelector(`[data-flow-frame="${frameID}"], [data-flow-step="${flowStore.selectedStepId}"]`);
    frameEl?.scrollIntoView({ behavior: 'smooth', inline: 'center' });
  }
</script>

<nav class="rail" aria-label="FlowSequence">
  <h2>FlowSequence</h2>
  <div id="step-nav">
    {#each frames as frame, index (frame.frameID)}
      {@const isSelected = selectedFrameId === frame.frameID || (!selectedFrameId && selectedStepId === frame.primaryStepRef)}
      {@const roleName = GATEWAY_ROLES[frame.role] || (frame.architecture ? `${frame.role.toUpperCase()} (${LAYER_LABELS[frame.architecture] || frame.architecture})` : frame.role.toUpperCase())}
      {@const delta = deltaChanges.find(c => c.targetStepId === frame.primaryStepRef)}
      {@const isSurgery = compare && (delta?.kind === 'added_behavior' || delta?.kind === 'changed_rule')}
      <button
        type="button"
        class="step-link"
        class:is-surgery={isSurgery}
        data-select={frame.primaryStepRef}
        data-frame={frame.frameID}
        aria-pressed={isSelected}
        onclick={() => onFrameSelect(frame.frameID)}
      >
        <span class="idx">FRAME {String(frame.ordinal || index + 1).padStart(2, '0')}</span>
        <span>
          <strong>
            {#if isSurgery}⚡ {/if}
            {#if frame.isRecursion}<span class="badge-recursion">↺ RECURSION</span> {/if}
            {frame.title}
          </strong>
          <small>{roleName}</small>
        </span>
      </button>
      {#if flowStore.selectedFrame?.frameID === frame.frameID && frame.stepRefs.length > 1}
        <details class="scene-details">
          <summary>실행 타임라인 ({frame.stepRefs.length}단계)</summary>
          {#each flowStore.sceneSteps as step (step.stepId)}
            <button class="step-link" aria-pressed={selectedStepId === step.stepId} onclick={() => flowStore.select(step.stepId)}>{step.name}</button>
          {/each}
        </details>
      {/if}
    {/each}
  </div>
  <p class="rail-note">
    스토리보드 관문을 선택하면 중앙의 해당 코드 위치로 이동합니다.<br><br>
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
  .badge-recursion {
    display: inline-block;
    font-size: 9px;
    font-weight: 700;
    color: #b45309;
    background: #fef3c7;
    border: 1px solid #fcd34d;
    padding: 1px 4px;
    border-radius: 3px;
    margin-right: 4px;
  }
  .rail-note {
    font-size: 10px;
    color: #666;
    padding: 14px 8px;
    border-top: 1px solid #ddd;
    line-height: 1.5;
  }
</style>
