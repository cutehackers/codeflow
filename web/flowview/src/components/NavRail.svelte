<script lang="ts">
  import { timelineConnectionLabels } from '../stores/timelineConnections';
  import { buildNavigationTitles, executionNavigationLabel, implementationSymbol, navigationRole, navigationTitle } from '../stores/flowNavigation';
  import { flowStore, GATEWAY_ROLES } from '../stores/flowStore.svelte';
  const navigationTitles = $derived(buildNavigationTitles(flowStore.steps, flowStore.data?.semanticMap.edges || []));
  const stepRoles: Record<string, string> = {call: '호출', return: '반환', throw: '예외 발생', branch: '조건 판단', guard: '조건 검사', decision: '판단', mutation: '상태 변경', external_effect: '외부 효과', user_action: '시작', result: '결과'};
  function onSelectedFlowFocus() {
    const panel = document.getElementById('selected-flow');
    panel?.focus();
    panel?.scrollIntoView({block:'start'});
  }
</script>
<nav class="rail" aria-label="관문과 실행 타임라인" id="execution-navigation" data-navigation-panel>
  <header><h2>실행 탐색</h2><span>{flowStore.steps.length}단계</span></header>
  <button class="disclosure" onclick={onSelectedFlowFocus}>선택한 흐름으로 이동</button>
  {#each flowStore.frames as frame, index (frame.frameID)}
    {@const primaryStep = flowStore.steps.find(step => step.stepId === frame.primaryStepRef)}
    {@const frameLabel = primaryStep ? executionNavigationLabel(primaryStep, navigationTitles.get(primaryStep.stepId) || navigationTitle(primaryStep.name, implementationSymbol(primaryStep))) : {title: frame.title, detail: ''}}
    {@const connections = timelineConnectionLabels(frame.stepRefs, flowStore.data?.semanticMap.edges || [])}
    <section class:active={flowStore.selectedFrameId === frame.frameID}>
      <button class="gateway" data-frame={frame.frameID} aria-pressed={flowStore.selectedFrameId === frame.frameID} onclick={() => flowStore.select(frame.frameID)}>
        <span class="ordinal">{String(index+1).padStart(2,'0')} · {GATEWAY_ROLES[frame.role] || frame.role}</span>
        <strong>{frameLabel.title}</strong>
        {#if frameLabel.detail}<small class="symbol">{frameLabel.detail}</small>{/if}
      </button>
      <button class="disclosure" aria-expanded={flowStore.expandedFrames.has(frame.frameID)} aria-controls={`timeline-${frame.frameID}`} onclick={() => flowStore.toggleFrame(frame.frameID)}>
        <span aria-hidden="true">{flowStore.expandedFrames.has(frame.frameID) ? '−' : '+'}</span> 실행 타임라인 {frame.stepRefs.length}단계
      </button>
      {#if flowStore.expandedFrames.has(frame.frameID)}
        <ol id={`timeline-${frame.frameID}`}>
          {#each flowStore.stepsForFrame(frame) as step (step.stepId)}
            {@const stepLabel = executionNavigationLabel(step, navigationTitles.get(step.stepId) || navigationTitle(step.name, implementationSymbol(step)))}
            <li>{#if connections.has(step.stepId)}<p class="connection">{connections.get(step.stepId)}</p>{/if}<button class="detail" data-step={step.stepId} data-flow-step={step.stepId} aria-pressed={flowStore.selectedStepId === step.stepId} onclick={() => flowStore.select(step.stepId)}>
              <span class="step-role">{#if flowStore.matchingStepIds?.has(step.stepId)}경로 포함 · {/if}{stepRoles[step.kind || ''] || navigationRole(step, flowStore.data?.semanticMap.edges || [])}</span>
              {stepLabel.title}
              {#if stepLabel.detail}<small class="symbol">{stepLabel.detail}</small>{/if}
            </button></li>
          {/each}
        </ol>
      {/if}
      {#if flowStore.data?.flowSequence?.summaryLimitations?.some(item => item.frameRefs.includes(frame.frameID))}<p class="boundary">처리 묶음 미확인</p>{/if}
      {#if frame.role === 'boundary'}<p class="boundary">다음 연결 미확인</p>{/if}
    </section>
  {/each}
</nav>
<style>
  .rail{position:sticky;top:16px;max-height:calc(100vh - 32px);overflow:auto;padding:20px 8px 24px 0;min-width:0}
  header{display:flex;justify-content:space-between;align-items:center;margin-bottom:18px}h2{font-size:14px}header span{font-size:11px;color:#666}
  section{border-left:2px solid #ddd;padding:0 0 12px 10px;margin-bottom:12px}.active{border-color:#111}
  .gateway{display:block;width:100%;text-align:left;border:0;padding:9px;background:transparent}.gateway strong{display:block;font-size:13px;overflow-wrap:anywhere;line-height:1.5}
  .gateway[aria-pressed=true]{background:#171717;color:#fff}.ordinal{display:block;font-size:10px;margin-bottom:5px}
  .disclosure{border:0;background:transparent;font-size:11px;padding:9px;text-align:left;width:100%}.disclosure span{display:inline-block;width:12px}
  ol{padding-left:20px;margin:4px 0;font-size:11px}.detail{text-align:left;width:100%;border:0;background:transparent;padding:8px;overflow-wrap:anywhere}.detail[aria-pressed=true]{color:#111;background:#eee;box-shadow:inset 2px 0 #111}
  .connection{font-size:10px;line-height:1.5;border-left:1px solid #aaa;padding:4px 8px;margin:5px 0 0;color:#555}
  .step-role{display:block;font-size:10px;margin-bottom:4px;color:#666}
  .symbol{font-family:ui-monospace,monospace;font-size:10px;overflow-wrap:anywhere}
  small{display:block;color:inherit;margin-top:4px}.boundary{font-size:11px;border:1px dashed #999;padding:5px}
  @media(max-width:800px){.rail{position:static;max-height:45vh;padding:12px 0}}
</style>
