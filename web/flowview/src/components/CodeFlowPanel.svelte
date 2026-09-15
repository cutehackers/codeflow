<script lang="ts">
  import { flowStore, GATEWAY_ROLES, LAYER_LABELS, EDGE_LABELS } from '../stores/flowStore.svelte';
  import type { Step, FlowContext, DisplayedLine } from '../types/flow';

  const steps = $derived(flowStore.steps);
  const selectedStepId = $derived(flowStore.selectedStepId);
  const compare = $derived(flowStore.compare);
  const expandedSet = $derived(flowStore.expanded);
  const matchingStepIds = $derived(flowStore.matchingStepIds);
  const deltaChanges = $derived(flowStore.data?.semanticDelta?.changes || []);

  function getDelta(stepId: string) {
    return deltaChanges.find(c => c.targetStepId === stepId);
  }

  function getContext(step: Step, isBaseline = false): FlowContext | null {
    const data = isBaseline ? flowStore.baseline : flowStore.data;
    if (!data?.flowContexts) return null;
    return data.flowContexts[step.stepId] || null;
  }

  function getDisplayedLines(context: FlowContext | null, isExpanded: boolean): DisplayedLine[] {
    if (!context?.displayedLines) return [];
    const all = context.displayedLines;
    if (isExpanded || all.length <= 16) return all;
    const hit = all.findIndex(l => l.isHit);
    const start = Math.max(0, (hit < 0 ? 0 : hit) - 4);
    return all.slice(start, start + 16);
  }

  function getStepRelations(step: Step) {
    return (flowStore.data?.semanticMap?.edges || []).filter(e => e.fromStepId === step.stepId);
  }

  function handleSelect(stepId: string) {
    flowStore.select(stepId);
    const storyEl = document.querySelector(`[data-story-step="${stepId}"]`);
    storyEl?.scrollIntoView({ behavior: 'smooth', inline: 'center' });
  }
</script>

<div id="code-flow" class="flow-panel">
  {#if !steps.length}
    <p class="empty">이 요청에서 확인된 처리 단계가 없습니다.</p>
  {:else}
    {#each steps as step, index (step.stepId)}
      {@const isSelected = selectedStepId === step.stepId}
      {@const isExpanded = expandedSet.has(step.stepId)}
      {@const currContext = getContext(step, false)}
      {@const baseContext = compare ? getContext(step, true) : null}
      {@const currLines = getDisplayedLines(currContext, isExpanded)}
      {@const baseLines = getDisplayedLines(baseContext, isExpanded)}
      {@const roleName = GATEWAY_ROLES[step.layer] || LAYER_LABELS[step.layer] || step.layer.toUpperCase()}
      {@const path = currContext?.canonicalPath || step.anchor?.repoRelativePath || ''}
      {@const rels = getStepRelations(step)}
      {@const delta = getDelta(step.stepId)}
      {@const isSurgery = delta?.kind === 'added_behavior' || delta?.kind === 'changed_rule'}
      {@const desc = step.description || (step.branch ? `조건 · ${step.branch}` : (step.sideEffect || '다음 구현 연결 및 계층 처리'))}

      <article
        class="code-card"
        class:selected={isSelected}
        class:outside-focus={matchingStepIds !== null && !matchingStepIds.has(step.stepId)}
        class:condition-match={matchingStepIds !== null && matchingStepIds.has(step.stepId)}
        data-card={step.stepId}
      >
        <header class="card-head">
          <div class="card-head-top">
            <span class="card-frame-badge">FRAME {String(step.ordinal || index + 1).padStart(2, '0')} · {roleName}</span>
            {#if isSurgery}
              <span class="surgery-badge">⚡ ACTIVE SURGERY</span>
            {/if}
            {#if delta}
              {#if delta.kind === 'changed_rule'}
                <span class="delta-tag mod">~ RULE CHG</span>
              {:else if delta.kind === 'added_behavior'}
                <span class="delta-tag add">+ NEW SURGERY</span>
              {:else if delta.kind === 'removed_behavior'}
                <span class="delta-tag del">- REMOVED</span>
              {/if}
            {/if}
          </div>
          <div class="card-head-body">
            <h3>{#if isSurgery}⚡ {/if}{step.name}</h3>
            <p class="card-narrative">{desc}</p>
            <div class="path">{path} · {step.technicalName || step.anchor?.enclosingSymbolPath || ''}</div>
          </div>
        </header>

        {#if compare && baseContext}
          <div class="compare-grid">
            <div>
              <div class="compare-title">이전 · 최초 표시 코드</div>
              <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
              <pre class="source" tabindex="0" aria-label="이전 코드"><code>{#each baseLines as line (line.lineNumber)}<span class="line" class:hit={line.isHit}><span class="ln">{line.lineNumber}</span><span class="txt">{line.text}</span></span>{/each}</code></pre>
            </div>
            <div>
              <div class="compare-title">현재 코드</div>
              <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
              <pre class="source" tabindex="0" aria-label="현재 코드"><code>{#each currLines as line (line.lineNumber)}<span class="line" class:hit={line.isHit}><span class="ln">{line.lineNumber}</span><span class="txt">{line.text}</span></span>{/each}</code></pre>
            </div>
          </div>
        {:else if currContext && currLines.length}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre class="source" tabindex="0" aria-label="소스 코드"><code>{#each currLines as line (line.lineNumber)}<span class="line" class:hit={line.isHit} class:struct={line.isStruct}><span class="ln">{line.lineNumber}</span><span class="txt">{#if line.selection}{line.selection.before}<mark>{line.selection.text}</mark>{line.selection.after}{:else}{line.text}{/if}</span></span>{/each}</code></pre>
          {#if !isExpanded && (currContext.displayedLines?.length || 0) > currLines.length}
            <p class="code-note">관련 코드 {currLines.length}줄 표시 · 코드 더 보기로 전체 문맥 확인</p>
          {/if}
        {:else}
          <p class="source-empty">이 분석에 연결된 소스가 없습니다. 코드 내용을 추정하지 않습니다.</p>
        {/if}

        <div class="code-tools">
          <button type="button" data-expand={step.stepId} onclick={() => flowStore.toggleExpand(step.stepId)}>
            {isExpanded ? '코드 접기' : '코드 더 보기'}
          </button>
          <button type="button" data-callable={step.stepId}>함수 전체</button>
          <span class="precision">
            {currContext?.precision === 'exact' ? '선택 문장 강조' : '주변 코드 · 정확한 문장 범위 미확인'}
          </span>
        </div>

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
  .code-card {
    border: 1px solid #d4d4d4;
    border-radius: 7px;
    background: white;
    margin-bottom: 18px;
    overflow: hidden;
    scroll-margin-top: 75px;
  }
  .code-card.selected {
    border: 2px solid #171717;
  }
  .code-card.outside-focus {
    border-style: dashed;
  }
  .code-card.outside-focus .card-head {
    background: #f5f5f5;
  }
  .code-card.condition-match {
    box-shadow: inset 4px 0 #171717;
  }
  .card-head {
    padding: 12px 16px;
    border-bottom: 1px solid #ddd;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .card-head-top {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .card-frame-badge {
    font-family: ui-monospace, monospace;
    font-size: 9.5px;
    font-weight: 800;
    color: #444;
    background: #eee;
    padding: 2px 7px;
    border-radius: 4px;
    letter-spacing: .3px;
  }
  .surgery-badge {
    font-family: ui-monospace, monospace;
    font-size: 9px;
    font-weight: 900;
    background: #171717;
    color: #fff;
    padding: 2px 7px;
    border-radius: 999px;
    letter-spacing: .4px;
  }
  .delta-tag {
    font-size: 8.5px;
    font-weight: 800;
    padding: 1.5px 6px;
    border-radius: 3px;
    white-space: nowrap;
  }
  .delta-tag.mod {
    background: #ffffff;
    color: #171717;
    border: 1px solid #171717;
  }
  .delta-tag.add {
    background: #171717;
    color: #ffffff;
    border: 1px solid #171717;
  }
  .delta-tag.del {
    background: #ffe3e3;
    color: #c92a2a;
    border: 1px solid #ffa8a8;
  }
  .card-head-body h3 {
    font-size: 13.5px;
    font-weight: 750;
    margin: 3px 0 2px;
    color: #171717;
  }
  .card-narrative {
    font-size: 11px;
    color: #555;
    margin: 2px 0 4px;
    line-height: 1.45;
  }
  .card-head .path {
    font: 10px/1.8 ui-monospace, monospace;
    color: #777;
    overflow-wrap: anywhere;
  }
  .source {
    margin: 0;
    padding: 10px 0;
    background: #fff;
    font-size: 12px;
    line-height: 1.5;
    overflow-x: auto;
    tab-size: 2;
    white-space: normal;
  }
  .source code {
    display: block;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  }
  .source .line {
    display: flex;
    align-items: baseline;
    min-width: max-content;
    min-height: 20px;
    line-height: 20px;
    padding-right: 17px;
  }
  .source .ln {
    color: #666;
    flex: 0 0 44px;
    width: 44px;
    text-align: right;
    padding-right: 14px;
    user-select: none;
    font-size: 10px;
    border-left: 3px solid transparent;
    line-height: 20px;
    white-space: nowrap;
  }
  .source .txt {
    white-space: pre;
    font-family: inherit;
    line-height: 20px;
  }
  .source .struct .txt {
    font-weight: 600;
  }
  .source .hit {
    background: #eee;
  }
  .source .hit .ln {
    border-left-color: #171717;
    color: #171717;
  }
  .source mark {
    background: #ddd;
    color: #111;
    font-weight: 700;
  }
  .code-tools {
    padding: 7px 12px;
    border-top: 1px solid #eee;
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    align-items: center;
  }
  .code-tools button {
    font-size: 10px;
    padding: 3px 7px;
    cursor: pointer;
  }
  .code-tools .precision {
    font-size: 10px;
    color: #666;
    margin-left: auto;
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
  .source-empty {
    padding: 16px;
    color: #666;
    font-size: 12px;
  }
  .code-note {
    padding: 7px 14px;
    font-size: 10px;
    color: #666;
    border-top: 1px solid #eee;
    margin: 0;
  }
  .compare-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
  }
  .compare-grid > div + div {
    border-left: 1px solid #ccc;
  }
  .compare-title {
    font-size: 10px;
    padding: 5px 13px;
    background: #f4f4f4;
    border-bottom: 1px solid #ddd;
  }
  .empty {
    padding: 55px 15px;
    text-align: center;
    color: #666;
    font-size: 13px;
  }
</style>
