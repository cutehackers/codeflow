<script lang="ts">
  import { flowStore } from '../stores/flowStore.svelte';
  import { conditionOutcomeLabel, type ConditionSelection } from '../stores/conditionNavigation';
  const choices = $derived(flowStore.conditionChoices);
  const selected = $derived(flowStore.conditionSelections);
  const legacy = $derived(flowStore.steps.filter(step => step.branch && (!step.kind || ['branch', 'guard'].includes(step.kind)) && !choices.has(step.stepId)));
  function onConditionAdd(event: Event) {
    const select = event.currentTarget as HTMLSelectElement;
    if (!select.value) return;
    const choice: ConditionSelection = JSON.parse(select.value);
    flowStore.chooseCondition(choice.stepId, choice.outcome);
    select.value = '';
  }
</script>
<section class="condition-controls" aria-label="조건별 경로 탐색" id="condition-bar">
  <header><strong>조건별 경로 탐색</strong>{#if selected.length || flowStore.conditionFilter}<button onclick={() => flowStore.setConditionFilter(null)}>전체 조건 해제</button>{/if}</header>
  {#if selected.length}
    <ol aria-label="선택한 경로 조건">
      {#each selected as choice (choice.stepId)}
        {@const step = flowStore.steps.find(step => step.stepId === choice.stepId)}
        <li>
          <span>{step?.branch || step?.name || choice.stepId}</span>
          <select aria-label={`${step?.name}의 조건 결과`} value={choice.outcome} data-navigation-focus={`condition:${choice.stepId}`} onchange={event => flowStore.chooseCondition(choice.stepId, event.currentTarget.value as ConditionSelection['outcome'])}>
            {#each choices.get(choice.stepId) || [] as outcome}<option value={outcome}>{conditionOutcomeLabel(outcome)}</option>{/each}
          </select>
          <button onclick={() => {flowStore.saveNavigationState(); flowStore.select(choice.stepId)}}>근거 보기</button>
          <button aria-label={`${step?.name} 조건 해제`} onclick={() => flowStore.removeCondition(choice.stepId)}>해제</button>
        </li>
      {/each}
    </ol>
  {/if}
  <label for="condition-path-add">{selected.length ? '하위 조건 추가' : '탐색할 조건과 결과'}</label>
  <select id="condition-path-add" onchange={onConditionAdd} disabled={!choices.size}>
    <option value="">{choices.size ? '조건 결과 선택' : '조건 결과별 연결 정보 없음'}</option>
    {#each flowStore.steps.filter(step => choices.has(step.stepId) && !selected.some(choice => choice.stepId === step.stepId)) as step (step.stepId)}
      <optgroup label={step.branch || step.name} disabled={!!selected.length && !flowStore.conditionReachability.steps.has(step.stepId)}>
        {#each choices.get(step.stepId) || [] as outcome}<option value={JSON.stringify({stepId:step.stepId,outcome})}>{conditionOutcomeLabel(outcome)} · {step.name}</option>{/each}
      </optgroup>
    {/each}
  </select>
  <p>선택한 조건에서 확인된 연결을 따라갈 수 있는 범위를 표시합니다. 실제 실행 결과나 조건 조합의 실행 가능성을 뜻하지 않습니다.</p>
  {#if selected.length && flowStore.conditionReachability.repeated}<p role="status">다음 반복에서는 조건을 다시 평가합니다. 반복 이후의 경로는 이 선택으로 확정하지 않습니다. 종료 연결과 다른 단계는 계속 탐색할 수 있습니다.</p>{/if}
  {#if selected.length && flowStore.conditionReachability.limited}<p role="status">조건이나 후속 연결이 미확인인 지점에서는 강조를 멈춥니다.</p>{/if}
  {#if legacy.length}
    <details><summary>조건 결과가 없는 분석의 직접 연결</summary>
      <label for="condition-focus">직접 연결 강조</label>
      <select id="condition-focus" value={flowStore.conditionFilter || ''} onchange={event => flowStore.setConditionFilter(event.currentTarget.value || null)}>
        <option value="">선택 해제</option>
        {#each legacy as step (step.stepId)}<option value={step.stepId}>{step.branch || step.name}</option>{/each}
      </select>
      <p>이 분석은 조건별 결과가 없어 직접 연결된 단계만 강조합니다.</p>
    </details>
  {/if}
  {#if flowStore.matchingStepIds && flowStore.selectedStepId && !flowStore.matchingStepIds.has(flowStore.selectedStepId)}<p role="status">현재 선택은 강조 경로 밖입니다.</p>{/if}
</section>
<style>
  .condition-controls{border:1px solid #ccc;border-radius:6px;padding:14px;margin-bottom:16px;font-size:12px;min-width:0}header{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:12px}ol{padding-left:22px}li{margin:8px 0;display:flex;flex-wrap:wrap;gap:6px;align-items:center}li span{flex:1 1 160px;overflow-wrap:anywhere}select{max-width:100%;font-size:12px;padding:6px;background:#fff;color:#111;border:1px solid #999;border-radius:4px}label{display:block;margin:8px 0 5px}button{font-size:11px}p{font-size:11px;color:#555;line-height:1.6;margin-top:10px}details{border-top:1px solid #ddd;margin-top:12px;padding-top:10px}summary{cursor:pointer}
</style>
