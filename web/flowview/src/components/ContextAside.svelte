<script lang="ts">
  import { flowStore, LAYER_LABELS, EDGE_LABELS } from '../stores/flowStore.svelte';
  import BlastRadiusRadar from './BlastRadiusRadar.svelte';

  const selectedStep = $derived(flowStore.selectedStep);
  const viewMode = $derived(flowStore.viewMode);

  const context = $derived.by(() => {
    if (!selectedStep || !flowStore.data?.flowContexts) return null;
    return flowStore.data.flowContexts[selectedStep.stepId] || null;
  });

  const incomingCallers = $derived.by(() => {
    if (!selectedStep) return [];
    return (flowStore.data?.semanticMap?.edges || []).filter(
      e => e.toStepId === selectedStep.stepId && e.resolutionStatus === 'resolved' && ['call', 'calls', 'resolved_cross_file'].includes(e.kind)
    ).map(e => {
      const from = flowStore.steps.find(s => s.stepId === e.fromStepId);
      return from ? { stepId: from.stepId, label: from.technicalName || from.name } : null;
    }).filter(Boolean);
  });

  function inspectCode() {
    flowStore.setViewMode('code');
  }

  function onStepSelect(stepId: string) {
    flowStore.saveNavigationState();
    flowStore.select(stepId);
  }
</script>

<aside class="context" id="context" aria-label="선택한 코드의 맥락">
  <!-- 2. BLAST RADIUS RADAR -->
  <BlastRadiusRadar />

  <!-- STEP CONTEXT DETAILS -->
  <div id="context-detail">
    {#if !selectedStep}
      <p class="caption">단계를 선택해 조건과 다음 처리를 확인하세요.</p>
    {:else if viewMode === 'process'}
      <div class="eyebrow muted">선택한 처리</div>
      <h2>{selectedStep.name}</h2>
      <p class="caption">{LAYER_LABELS[selectedStep.layer] || selectedStep.layer || '계층 미확인'}</p>

      {#if selectedStep.branch}
        <section class="context-section">
          <h3>판단 조건</h3>
          <p>{selectedStep.branch}</p>
        </section>
      {/if}

      {#if selectedStep.stateDelta}
        <section class="context-section">
          <h3>상태 변화</h3>
          <p>{selectedStep.stateDelta.before} → {selectedStep.stateDelta.after}</p>
        </section>
      {/if}

      {#if selectedStep.sideEffect}
        <section class="context-section">
          <h3>외부 처리</h3>
          <p>{selectedStep.sideEffect}</p>
        </section>
      {/if}

      <section class="context-section">
        <h3>구현 확인</h3>
        <p class="caption mono">{context?.canonicalPath || selectedStep.anchor?.repoRelativePath || '소스 위치 미확인'}</p>
        <button type="button" id="inspect-code" onclick={inspectCode}>이 단계의 코드 보기 →</button>
      </section>
      <p class="caption">소스에서 확인한 관계입니다. 실제 실행 순서와 결과는 확인하지 않았습니다.</p>
    {:else}
      <div class="eyebrow muted">선택한 코드</div>
      <h2>{selectedStep.name}</h2>
      <p class="caption mono">{selectedStep.technicalName || ''}</p>

      {#if selectedStep.branch}
        <section class="context-section">
          <h3>분기 조건</h3>
          <p>{selectedStep.branch}</p>
        </section>
      {/if}

      {#if selectedStep.sideEffect}
        <section class="context-section">
          <h3>외부 처리</h3>
          <p>{selectedStep.sideEffect}</p>
        </section>
      {/if}

      {#if selectedStep.stateDelta}
        <section class="context-section">
          <h3>코드에 나타난 상태 변경</h3>
          <p>{selectedStep.stateDelta.before} → {selectedStep.stateDelta.after}</p>
        </section>
      {/if}

      <section class="context-section">
        <h3>이곳을 호출한 코드</h3>
        {#if incomingCallers.length}
          {#each incomingCallers as caller (caller!.stepId)}
            <button type="button" data-select={caller!.stepId} onclick={() => onStepSelect(caller!.stepId)}>
              ← {caller!.label}
            </button>
          {/each}
        {:else}
          <p class="caption">분석에서 확인한 선행 연결 없음</p>
        {/if}
      </section>

      <details>
        <summary>소스 범위와 설명</summary>
        <p>{context?.sourceLimitation || '선택한 분석과 같은 시점의 소스입니다. 실제 실행 여부와 결과는 확인하지 않았습니다.'}</p>
      </details>
    {/if}
  </div>
</aside>

<style>
  .context {
    padding-top: 20px;
    position: sticky;
    top: 70px;
    max-height: calc(100vh - 85px);
    overflow: auto;
    min-width: 0;
  }
  .eyebrow {
    font-size: 10px;
    letter-spacing: 1.4px;
    font-weight: 750;
  }
  .muted {
    color: var(--muted, #666);
  }
  .caption {
    font-size: 10px;
    color: var(--muted, #666);
    margin: 4px 0;
  }
  .mono {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  h2 {
    font-size: 13px;
    margin: 8px 0 4px;
  }
  .context-section {
    border-top: 1px solid var(--line, #ddd);
    padding: 14px 0;
    margin-top: 12px;
  }
  .context-section h3 {
    font-size: 11px;
    margin: 0 0 6px;
  }
  .context-section p {
    font-size: 11px;
    line-height: 1.85;
    overflow-wrap: anywhere;
    margin: 0;
  }
  .context-section button {
    font-size: 10px;
    text-align: left;
    margin-top: 6px;
    display: block;
    cursor: pointer;
  }
  details {
    font-size: 11px;
    margin-top: 13px;
  }
  details p {
    margin-top: 8px;
  }
</style>
