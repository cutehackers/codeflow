<script lang="ts">
  import { flowStore, GATEWAY_ROLES, LAYER_LABELS } from '../stores/flowStore.svelte';
  import type { Step } from '../types/flow';

  const steps = $derived(flowStore.steps);
  const selectedStepId = $derived(flowStore.selectedStepId);
  const deltaChanges = $derived(flowStore.data?.semanticDelta?.changes || []);

  const modifiedCount = $derived(
    deltaChanges.filter(c => c.kind === 'changed_rule').length
  );
  const addedCount = $derived(
    deltaChanges.filter(c => c.kind === 'added_behavior').length
  );

  const statusPillText = $derived.by(() => {
    if (addedCount > 0 || modifiedCount > 0) {
      const parts = [];
      if (addedCount > 0) parts.push(`+${addedCount} Step Added`);
      if (modifiedCount > 0) parts.push(`${modifiedCount} Rule Modified`);
      return parts.join(' · ');
    }
    return steps.length ? `${steps.length}개 관문 확인됨` : '확인된 단계 없음';
  });

  function getDelta(stepId: string) {
    return deltaChanges.find(c => c.targetStepId === stepId);
  }

  function handleSelect(stepId: string) {
    flowStore.select(stepId);
    const cardEl = document.querySelector(`[data-card="${stepId}"], [data-process="${stepId}"]`);
    cardEl?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
</script>

<section class="macro-storyboard-section" id="macro-storyboard" aria-label="매크로 비즈니스 스토리보드">
  <div class="section-head">
    <div class="section-head-title">
      <strong>2. MACRO CONTEXT STORYBOARD</strong>
      <span class="muted">· 전체 비즈니스 흐름 조망 ({steps.length ? `${steps.length}개 관문 엔드투엔드 시퀀스` : '대기 중'})</span>
    </div>
    <span class="pill" id="storyboard-status-pill">{statusPillText}</span>
  </div>

  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="storyboard-track" id="storyboard-track" role="region" aria-label="스토리보드 단계 목록" tabindex="0">
    {#if !steps.length}
      <p class="muted" style="font-size:11px;padding:8px 0">흐름을 선택하면 단계별 시퀀스 카드가 표시됩니다.</p>
    {:else}
      {#each steps as step, index (step.stepId)}
        {@const isSelected = selectedStepId === step.stepId}
        {@const delta = getDelta(step.stepId)}
        {@const roleName = GATEWAY_ROLES[step.layer] || LAYER_LABELS[step.layer] || step.layer.toUpperCase()}
        {@const desc = step.description || (step.branch ? `조건 · ${step.branch}` : (step.sideEffect || '다음 구현 연결 및 계층 처리'))}
        {@const isSurgery = delta?.kind === 'added_behavior' || delta?.kind === 'changed_rule'}

        <button
          type="button"
          class="story-card"
          class:active-selected={isSelected}
          class:surgery-badge={isSurgery && isSelected}
          data-story-step={step.stepId}
          aria-pressed={isSelected}
          onclick={() => handleSelect(step.stepId)}
        >
          <div>
            <div class="story-header">
              <span class="story-step-num">
                FRAME {String(step.ordinal || index + 1).padStart(2, '0')} · {roleName}
              </span>
              {#if delta}
                {#if delta.kind === 'changed_rule'}
                  <span class="step-tag mod">~ RULE CHG</span>
                {:else if delta.kind === 'added_behavior'}
                  <span class="step-tag add">+ NEW SURGERY</span>
                {:else if delta.kind === 'removed_behavior'}
                  <span class="step-tag del">- REMOVED</span>
                {:else}
                  <span class="step-tag mod">~ MOD</span>
                {/if}
              {/if}
            </div>
            <div class="story-title">
              {#if isSurgery}⚡ {/if}{step.name}
            </div>
            <div class="story-desc">{desc}</div>
          </div>
          <div class="story-footer">
            {#if isSurgery}⚡ {/if}{step.technicalName || ''}
          </div>
        </button>
      {/each}
    {/if}
  </div>
</section>

<style>
  .macro-storyboard-section {
    margin: 0 30px 18px;
    border: 2px solid var(--ink, #171717);
    border-radius: 9px;
    padding: 12px 16px;
    background: var(--paper, #ffffff);
    box-shadow: 3px 3px 0 var(--soft, #dddddd);
  }
  .section-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    border-bottom: 1px solid var(--line, #dddddd);
    padding-bottom: 6px;
    margin-bottom: 10px;
    font-size: 11px;
  }
  .section-head-title {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .section-head-title strong {
    font-size: 11px;
    letter-spacing: 0.8px;
    font-weight: 800;
  }
  .muted {
    color: var(--muted, #666666);
  }
  .pill {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 10px;
    font-weight: 800;
    padding: 3px 10px;
    border-radius: 999px;
    background: #ffffff;
    color: var(--ink, #171717);
    border: 1.5px solid var(--ink, #171717);
  }
  .storyboard-track {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
    gap: 10px;
    padding-top: 6px;
  }
  .story-card {
    border: 1px solid var(--line, #dddddd);
    border-radius: 7px;
    padding: 10px 12px;
    background: #fafaf8;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    min-height: 110px;
    text-align: left;
    cursor: pointer;
    position: relative;
    transition: border-color .15s, box-shadow .15s, background .15s;
    font-family: inherit;
    color: inherit;
  }
  .story-card:hover {
    border-color: var(--ink, #171717);
    background: #ffffff;
  }
  .story-card.active-selected {
    border: 2px solid var(--ink, #171717);
    background: var(--paper, #ffffff);
    box-shadow: 2px 2px 0 var(--ink, #171717);
  }
  .story-card.active-selected::after {
    content: "SELECTED";
    position: absolute;
    top: -9px;
    right: 8px;
    background: var(--ink, #171717);
    color: #ffffff;
    font-size: 8px;
    font-weight: 900;
    padding: 2px 7px;
    border-radius: 999px;
    line-height: 1.2;
    white-space: nowrap;
    z-index: 2;
  }
  .story-card.surgery-badge::after {
    content: "ACTIVE SURGERY";
    background: var(--ink, #171717);
  }
  .story-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 4px;
  }
  .story-step-num {
    font-family: ui-monospace, monospace;
    font-size: 9px;
    font-weight: 800;
    color: var(--muted, #666666);
  }
  .step-tag {
    font-size: 8px;
    font-weight: 850;
    padding: 1.5px 6px;
    border-radius: 3px;
    white-space: nowrap;
  }
  .step-tag.mod {
    background: #ffffff;
    color: #171717;
    border: 1px solid #171717;
  }
  .step-tag.add {
    background: #171717;
    color: #ffffff;
    border: 1px solid #171717;
  }
  .step-tag.del {
    background: #ffe3e3;
    color: #c92a2a;
    border: 1px solid #ffa8a8;
  }
  .story-title {
    font-size: 12px;
    font-weight: 800;
    line-height: 1.35;
    margin: 2px 0 4px;
    color: var(--ink, #171717);
  }
  .story-desc {
    font-size: 10.5px;
    color: var(--muted, #666666);
    line-height: 1.4;
  }
  .story-footer {
    margin-top: 8px;
    padding-top: 5px;
    border-top: 1px dashed var(--line, #dddddd);
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 9.5px;
    font-weight: 700;
    color: var(--muted, #666666);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .story-card.active-selected .story-footer {
    color: var(--ink, #171717);
    font-weight: 800;
  }

  @media (max-width: 900px) {
    .macro-storyboard-section {
      margin-left: 15px;
      margin-right: 15px;
    }
    .storyboard-track {
      grid-template-columns: 1fr;
    }
  }
</style>
