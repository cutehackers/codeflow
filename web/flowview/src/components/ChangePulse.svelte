<script lang="ts">
  import { flowStore, DELTA_LABELS } from '../stores/flowStore.svelte';

  const changes = $derived(flowStore.data?.semanticDelta?.changes || []);

  function onStepSelect(targetStepId?: string) {
    if (!targetStepId) return;
    flowStore.select(targetStepId);
    const targetEl = document.querySelector(`[data-card="${targetStepId}"], [data-process="${targetStepId}"]`);
    targetEl?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
</script>

{#if flowStore.compare && changes.length > 0}
  <section id="change-pulse" class="change-pulse" aria-label="검증된 의미 변경">
    <h2>CHANGE PULSE · 검증된 의미 변경</h2>
    <div id="pulse-list" class="pulse-list">
      {#each changes as change (change.changeId || change.targetStepId + change.kind)}
        {@const label = DELTA_LABELS[change.kind] || change.kind}
        {@const isPriority = ['added_behavior', 'changed_rule', 'removed_behavior'].includes(change.kind)}
        <button
          type="button"
          class="tag pulse-item"
          class:pulse-low-priority={!isPriority}
          data-kind={change.kind}
          data-select={change.targetStepId}
          onclick={() => onStepSelect(change.targetStepId)}
        >
          <b>{label}</b>
          <span>{change.summary || change.description || ''}</span>
        </button>
      {/each}
    </div>
  </section>
{/if}

<style>
  .change-pulse {
    margin: 0 30px 18px;
    padding: 13px;
    border: 1px solid #bbbbbb;
    background: #ffffff;
    border-radius: 6px;
  }
  .change-pulse h2 {
    font-size: 11px;
    letter-spacing: 0.6px;
    margin-bottom: 8px;
  }
  .pulse-list {
    display: flex;
    gap: 7px;
    flex-wrap: wrap;
  }
  .pulse-item {
    font-size: 10px;
    text-align: left;
    cursor: pointer;
    background: #ffffff;
    border: 1px solid #cccccc;
    padding: 2px 6px;
    border-radius: 4px;
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: inherit;
  }
  .pulse-item:hover {
    background: #f0f0f0;
    border-color: #999999;
  }
  .pulse-item b {
    margin-right: 2px;
  }
  .pulse-item[data-kind="removed_behavior"],
  .pulse-item[data-kind="ambiguous_move"] {
    border-style: dashed;
  }
  .pulse-item.pulse-low-priority {
    opacity: 0.85;
    border-style: dotted;
  }

  @media (max-width: 650px) {
    .change-pulse {
      margin-left: 15px;
      margin-right: 15px;
    }
  }
</style>
