<script lang="ts">
  import { flowStore, GATEWAY_ROLES, LAYER_LABELS } from '../stores/flowStore.svelte';
  import type { FlowSequenceFrame } from '../types/flow_sequence';

  const frames = $derived(flowStore.frames);
  const selectedFrameId = $derived(flowStore.selectedFrameId);
  const selectedStepId = $derived(flowStore.selectedStepId);
  const compare = $derived(flowStore.compare);
  const deltaChanges = $derived(compare ? (flowStore.data?.semanticDelta?.changes || []) : []);

  const modifiedCount = $derived(
    deltaChanges.filter(c => c.kind === 'changed_rule').length
  );
  const addedCount = $derived(
    deltaChanges.filter(c => c.kind === 'added_behavior').length
  );

  const statusPillText = $derived.by(() => {
    if (compare && (addedCount > 0 || modifiedCount > 0)) {
      const parts = [];
      if (addedCount > 0) parts.push(`+${addedCount} Step Added`);
      if (modifiedCount > 0) parts.push(`${modifiedCount} Rule Modified`);
      return parts.join(' · ');
    }
    return frames.length ? `${frames.length}개 주요 장면` : '확인된 단계 없음';
  });

  function getDelta(stepId: string) {
    if (!compare) return undefined;
    return deltaChanges.find(c => c.targetStepId === stepId);
  }

  function onFrameSelect(frameID: string) {
    flowStore.select(frameID);
    const cardEl = document.querySelector(`[data-card="${frameID}"], [data-card="${flowStore.selectedStepId}"], [data-process="${flowStore.selectedStepId}"]`);
    cardEl?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }
</script>

<section class="flow-sequence-section" id="flow-sequence" aria-label="FlowSequence 주요 관문">
  <div class="section-head">
    <div class="section-head-title">
      <strong>2. MACRO CONTEXT FLOWSEQUENCE</strong>
      <span class="muted">· 시작부터 결과까지 ({frames.length ? `${frames.length}개 주요 장면` : '대기 중'})</span>
    </div>
    <span class="pill" id="flow-sequence-status-pill">{statusPillText}</span>
  </div>

  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="flow-sequence-track" id="flow-sequence-track" role="region" aria-label="FlowSequence 단계 목록" tabindex="0">
    {#if !frames.length}
      <p class="muted" style="font-size:11px;padding:8px 0">흐름을 선택하면 단계별 시퀀스 카드가 표시됩니다.</p>
    {:else}
      {#each frames as frame, index (frame.frameID)}
        {@const isSelected = selectedFrameId === frame.frameID || (!selectedFrameId && selectedStepId === frame.primaryStepRef)}
        {@const delta = getDelta(frame.primaryStepRef)}
        {@const roleName = GATEWAY_ROLES[frame.role] || (frame.architecture ? `${frame.role.toUpperCase()} (${LAYER_LABELS[frame.architecture] || frame.architecture})` : frame.role.toUpperCase())}
        {@const desc = frame.text || (frame.condition ? `조건 · ${frame.condition}` : (frame.collapsedDetail ? `${frame.collapsedDetail.count}개 내부 단계 접힘` : '다음 구현 연결 및 처리'))}
        {@const isSurgery = compare && (delta?.kind === 'added_behavior' || delta?.kind === 'changed_rule')}

        <button
          type="button"
          class="flow-card"
          class:active-selected={isSelected}
          class:surgery-badge={isSurgery && isSelected}
          data-flow-step={frame.primaryStepRef}
          data-flow-frame={frame.frameID}
          aria-pressed={isSelected}
          onclick={() => onFrameSelect(frame.frameID)}
        >
          <div>
            <div class="flow-header">
              <span class="flow-step-num">
                FRAME {String(frame.ordinal || index + 1).padStart(2, '0')} · {roleName}
              </span>
              {#if compare && delta}
                {#if delta.kind === 'changed_rule'}
                  <span class="step-tag mod">~ RULE CHG</span>
                {:else if delta.kind === 'added_behavior'}
                  <span class="step-tag add">+ NEW SURGERY</span>
                {:else if delta.kind === 'removed_behavior'}
                  <span class="step-tag del">- REMOVED</span>
                {:else}
                  <span class="step-tag mod">~ MOD</span>
                {/if}
              {:else if frame.status === 'partial'}
                <span class="step-tag mod">PARTIAL</span>
              {:else if frame.status === 'unknown'}
                <span class="step-tag del">UNKNOWN</span>
              {/if}
            </div>
            <div class="flow-title">
              {#if isSurgery}⚡ {/if}{frame.title}
            </div>
            <div class="flow-desc">{desc}</div>
          </div>
          <div class="flow-footer">
            {#if isSurgery}⚡ {/if}{frame.technicalAnchor || ''}
          </div>
        </button>
      {/each}
    {/if}
  </div>
</section>

<style>
  .flow-sequence-section {
    min-width: 0;
    margin: 0 30px 16px;
    border: 1.5px solid var(--ink, #171717);
    border-radius: 8px;
    padding: 12px 16px;
    background: var(--paper, #ffffff);
  }
  .section-head {
    flex-wrap: wrap;
    gap: 8px;
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    border-bottom: 1px solid var(--line, #dddddd);
    padding-bottom: 6px;
    margin-bottom: 10px;
    font-size: 11px;
  }
  .section-head-title {
    flex-wrap: wrap;
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
  .flow-sequence-track {
    display: flex;
    flex-direction: row;
    flex-wrap: nowrap;
    overflow-x: auto;
    gap: 10px;
    padding-top: 6px;
    padding-bottom: 6px;
    scrollbar-width: thin;
    overscroll-behavior-x: contain;
  }
  .flow-card {
    overflow-wrap: anywhere;
    flex: 0 0 215px;
    min-width: 200px;
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
  .flow-card:hover {
    border-color: var(--ink, #171717);
    background: #ffffff;
  }
  .flow-card.active-selected {
    border: 2px solid var(--ink, #171717);
    background: var(--paper, #ffffff);
    box-shadow: 2px 2px 0 var(--ink, #171717);
  }
  .flow-card.active-selected::after {
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
  .flow-card.surgery-badge::after {
    content: "ACTIVE SURGERY";
    background: var(--ink, #171717);
  }
  .flow-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 4px;
  }
  .flow-step-num {
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
  .flow-title {
    font-size: 12px;
    font-weight: 800;
    line-height: 1.35;
    margin: 2px 0 4px;
    color: var(--ink, #171717);
  }
  .flow-desc {
    font-size: 10.5px;
    color: var(--muted, #666666);
    line-height: 1.4;
  }
  .flow-footer {
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
  .flow-card.active-selected .flow-footer {
    color: var(--ink, #171717);
    font-weight: 800;
  }

  @media (max-width: 650px) {
    .flow-sequence-section {
    min-width: 0;
      margin-left: 15px;
      margin-right: 15px;
    }
  }
</style>
