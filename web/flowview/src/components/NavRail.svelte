<script lang="ts">
  import { timelineConnectionLabels } from '../stores/timelineConnections';
  import { conditionOutcomeLabel } from '../stores/conditionNavigation';
  import { buildNavigationTitles, codeReadingRelationDetailOwners, codeReadingRelationDetailSteps, executionNavigationLabel, implementationSymbol, navigationRole, navigationTitle, omittedCodeReadingBranches, partitionCodeReadingNavigation } from '../stores/flowNavigation';
  import { flowStore, GATEWAY_ROLES } from '../stores/flowStore.svelte';
  import type { FlowSequenceFrame } from '../types/flow_sequence';
  const navigationTitles = $derived(buildNavigationTitles(flowStore.steps, flowStore.data?.semanticMap.edges || []));
  const navigation = $derived(partitionCodeReadingNavigation(flowStore.flowSequence, flowStore.data?.codeReadingPath));
  const omittedBranches = $derived(omittedCodeReadingBranches(flowStore.data?.codeReadingPath, flowStore.data?.semanticMap.edges || []));
  const detailOwners = $derived(flowStore.data?.codeReadingPath
    ? codeReadingRelationDetailOwners(flowStore.data.codeReadingPath, flowStore.data.semanticMap.edges, flowStore.steps)
    : new Map<string, string>());
  const executionStepCount = $derived(navigation.reading.reduce((count, frame) => count + frame.stepRefs.length, 0));
  let otherOpen = $state(false);
  const otherVisible = $derived(otherOpen || navigation.other.some(frame => frame.frameID === flowStore.selectedFrameId));
  const stepRoles: Record<string, string> = {call: '호출', return: '반환', throw: '예외 발생', branch: '조건 판단', guard: '조건 검사', decision: '판단', mutation: '상태 변경', external_effect: '외부 효과', user_action: '시작', result: '결과'};
  function onSelectedFlowFocus() {
    const panel = document.getElementById('selected-flow');
    panel?.focus();
    panel?.scrollIntoView({block:'start'});
  }
</script>
{#snippet renderOtherBranches(stepID: string)}
  {#each omittedBranches.get(stepID) || [] as branch (branch.edgeIndex)}
    {@const target = flowStore.steps.find(step => step.stepId === branch.toStepId)}
    {#if target}
      {@const targetLabel = executionNavigationLabel(target, navigationTitles.get(target.stepId) || navigationTitle(target.name, implementationSymbol(target)))}
      <button class="reading-alternative" data-branch-target={target.stepId} onclick={() => flowStore.select(target.stepId)}>
        다른 확인된 분기 · {conditionOutcomeLabel(branch.outcome)} → {targetLabel.title}
      </button>
    {/if}
  {/each}
{/snippet}
{#snippet renderFrame(frame: FlowSequenceFrame)}
    {@const pathNodes = flowStore.data?.codeReadingPath?.nodes.filter(node => node.frameId === frame.frameID) || []}
    {@const primaryStep = flowStore.steps.find(step => step.stepId === frame.primaryStepRef)}
    {@const frameLabel = primaryStep ? executionNavigationLabel(primaryStep, navigationTitles.get(primaryStep.stepId) || navigationTitle(primaryStep.name, implementationSymbol(primaryStep))) : {title: frame.title, detail: ''}}
    {@const gatewayTitle = frame.title && frame.title !== '확인되지 않은 처리 목적' ? frame.title : frameLabel.title}
    {@const gatewayDetail = gatewayTitle === frameLabel.title ? frameLabel.detail : [frameLabel.title, frameLabel.detail].filter(Boolean).join(' · ')}
    {@const connections = timelineConnectionLabels(frame.stepRefs, flowStore.data?.semanticMap.edges || [])}
    <section class:active={flowStore.selectedFrameId === frame.frameID}>
      <button class="gateway" data-frame={frame.frameID} aria-pressed={flowStore.selectedFrameId === frame.frameID} onclick={() => flowStore.select(pathNodes[0]?.stepId || frame.frameID)}>
        <span class="ordinal">{String(frame.ordinal).padStart(2,'0')} · {GATEWAY_ROLES[frame.role] || frame.role}</span>
        <strong>{gatewayTitle}</strong>
        {#if gatewayDetail}<small class="symbol">{gatewayDetail}</small>{/if}
      </button>
      {#each pathNodes as node (node.nodeId)}
        <button class="reading-node" data-flow-step={node.stepId} aria-pressed={flowStore.selectedStepId === node.stepId} onclick={() => flowStore.select(node.stepId)}>
          <strong>{node.symbol}</strong><span>이 항목을 보는 이유: {node.inclusionReason} · 요청: {node.requestPart}</span>
          {#if node.previousNodeIds.length > 1}<span>합류: {node.previousNodeIds.length}개 확인된 경로</span>{/if}
          {#if node.nextNodeIds.length > 1}<span>분기: {node.nextNodeIds.length}개 확인된 경로</span>{/if}
        </button>
        {@render renderOtherBranches(node.stepId)}
        {#if flowStore.data?.codeReadingPath}
          {#each flowStore.data.codeReadingPath.relations.filter(relation => relation.fromNodeId === node.stepId) as relation (`${relation.fromNodeId}:${relation.toNodeId}`)}
            {@const detailSteps = codeReadingRelationDetailSteps(relation, flowStore.data.codeReadingPath, flowStore.data.semanticMap.edges, flowStore.steps)}
            {@const relationKey = `${relation.fromNodeId}\u0000${relation.toNodeId}`}
            {#if detailSteps.length > 0}
              <div class="reading-relation-detail" data-reading-relation={`${relation.fromNodeId}:${relation.toNodeId}`}>
                <button class="disclosure" aria-expanded={flowStore.expandedReadingRelations.has(relationKey)} aria-controls={`reading-detail-${relation.fromNodeId}-${relation.toNodeId}`} onclick={() => flowStore.toggleReadingRelation(relationKey)}>
                  다음 핵심 코드까지의 실제 처리 {detailSteps.length}단계 {flowStore.expandedReadingRelations.has(relationKey) ? '접기' : '펼치기'}
                </button>
                {#if flowStore.expandedReadingRelations.has(relationKey)}
                  <ol id={`reading-detail-${relation.fromNodeId}-${relation.toNodeId}`}>
                    {#each detailSteps as step (step.stepId)}
                      {@const stepLabel = executionNavigationLabel(step, navigationTitles.get(step.stepId) || navigationTitle(step.name, implementationSymbol(step)))}
                      <li><button class="detail" data-relation-step={step.stepId} aria-pressed={flowStore.selectedStepId === step.stepId} onclick={() => flowStore.select(step.stepId)}>
                        <span class="step-role">{stepRoles[step.kind || ''] || navigationRole(step, flowStore.data?.semanticMap.edges || [])}</span>
                        {stepLabel.title}
                        {#if stepLabel.detail}<small class="symbol">{stepLabel.detail}</small>{/if}
                      </button>{#if detailOwners.get(step.stepId) === relationKey}{@render renderOtherBranches(step.stepId)}{/if}</li>
                    {/each}
                  </ol>
                {/if}
              </div>
            {/if}
          {/each}
        {/if}
      {/each}
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
      {#if flowStore.data?.flowSequence?.summaryLimitations?.some(item => item.code === 'source_code_unavailable' && item.frameRefs.includes(frame.frameID))}
        <p class="boundary">상세 코드 미표시 · 읽기 경계</p>
      {:else if flowStore.data?.flowSequence?.summaryLimitations?.some(item => item.code === 'invocation_unproven' && item.frameRefs.includes(frame.frameID))}
        <p class="boundary">실행 연결 미확인</p>
      {:else if flowStore.data?.flowSequence?.summaryLimitations?.some(item => item.code !== 'request_relevance_unproven' && item.frameRefs.includes(frame.frameID))}<p class="boundary">처리 묶음 미확인</p>{/if}
      {#if frame.role === 'boundary'}<p class="boundary">다음 연결 미확인</p>{/if}
    </section>
{/snippet}
<nav class="rail" aria-label="관문과 실행 타임라인" id="execution-navigation" data-navigation-panel>
  <header><h2>{flowStore.data?.codeReadingPath ? '코드 읽기 경로' : '실행 탐색'}</h2><span>{flowStore.data?.codeReadingPath ? `${flowStore.data.codeReadingPath.nodes.length}개 코드 지점` : `${navigation.reading.length}관문 · ${executionStepCount}단계`}</span></header>
  <button class="disclosure" onclick={onSelectedFlowFocus}>선택한 흐름으로 이동</button>
  {#each navigation.reading as frame (frame.frameID)}
    {@render renderFrame(frame)}
  {/each}
  {#if navigation.other.length > 0}
    <button class="disclosure other-toggle" aria-expanded={otherVisible} aria-controls="other-execution" onclick={() => otherOpen = !otherVisible}>
      다른 확인된 코드 {navigation.other.length}관문 {otherVisible ? '접기' : '보기'}
    </button>
    <div id="other-execution" hidden={!otherVisible}>
      {#if otherVisible}
        {#each navigation.other as frame (frame.frameID)}
          {@render renderFrame(frame)}
        {/each}
      {/if}
    </div>
  {/if}
  {#if navigation.unproven.length > 0}
    <button class="disclosure unproven-toggle" aria-expanded={flowStore.unprovenOpen} aria-controls="unproven-invocations" onclick={() => flowStore.unprovenOpen = !flowStore.unprovenOpen}>
      실행 연결 미확인 코드 {navigation.unproven.length}관문 {flowStore.unprovenOpen ? '접기' : '보기'}
    </button>
    <div id="unproven-invocations" hidden={!flowStore.unprovenOpen}>
      {#if flowStore.unprovenOpen}
        <p class="boundary">코드는 확인했지만 요청한 흐름에서 실행됐다는 근거는 없습니다.</p>
        {#each navigation.unproven as frame (frame.frameID)}
          {@render renderFrame(frame)}
        {/each}
      {/if}
    </div>
  {/if}
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
  .unproven-toggle{border:1px solid #999;width:100%;text-align:left;margin:8px 0}
  .other-toggle{border:1px solid #999;width:100%;text-align:left;margin:8px 0}.reading-node{display:block;width:calc(100% - 16px);margin:0 8px 6px;padding:6px;text-align:left;border:1px solid #ddd;background:#fff;font-size:11px;line-height:1.5;color:#444;overflow-wrap:anywhere}.reading-node strong,.reading-node span{display:block}.reading-node[aria-pressed=true]{border-color:#111}
  .reading-relation-detail{margin:0 8px 8px 16px;border-left:1px solid #ccc;padding-left:8px;font-size:11px}.reading-relation-detail ol{margin-top:0}
  .reading-alternative{display:block;width:calc(100% - 24px);margin:0 8px 6px 16px;padding:5px;text-align:left;border:1px dashed #999;background:transparent;color:#444;font-size:10px;line-height:1.5;overflow-wrap:anywhere}
  @media(max-width:800px){.rail{position:static;max-height:45vh;padding:12px 0}}
</style>
