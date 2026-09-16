<script lang="ts">
  import { flowStore, GATEWAY_ROLES, LAYER_LABELS, EDGE_LABELS } from '../stores/flowStore.svelte';
  import type { FlowContext, DisplayedLine } from '../types/flow';
  import type { StoryboardFrame } from '../types/storyboard';

  const frames = $derived(flowStore.selectedFrame ? [{ ...flowStore.selectedFrame, primaryStepRef: flowStore.selectedStepId || flowStore.selectedFrame.primaryStepRef }] : []);
  const selectedFrameId = $derived(flowStore.selectedFrameId);
  const selectedStepId = $derived(flowStore.selectedStepId);
  const compare = $derived(flowStore.compare);
  const expandedSet = $derived(flowStore.expanded);
  const matchingStepIds = $derived(flowStore.matchingStepIds);
  const deltaChanges = $derived(compare ? (flowStore.data?.semanticDelta?.changes || []) : []);

  function getDelta(stepId: string) {
    if (!compare) return undefined;
    return deltaChanges.find(c => c.targetStepId === stepId);
  }

  function getContext(stepId: string, isBaseline = false): FlowContext | null {
    const data = isBaseline ? flowStore.baseline : flowStore.data;
    if (!data?.flowContexts) return null;
    const step = flowStore.selectedStep;
    const match = isBaseline ? data.semanticMap.steps.find(s => s.stepId === stepId || (s.structuralIdentity && s.structuralIdentity === step?.structuralIdentity)) : null;
    const context = data.flowContexts[match?.stepId || stepId];
    if (!context) return null;
    const source = data.sourceFiles?.[context.canonicalPath || ''];
    if (!source?.length) return context;
    const known = new Map(context.displayedLines.map(line => [line.lineNumber, line]));
    const lines = source.map(line => known.get(line.lineNumber) || line);
    if (expandedSet.has(stepId)) return {...context, displayedLines: lines};
    const focus = context.displayedLines.find(line => line.isHit)?.lineNumber || context.displayedLines[0]?.lineNumber || 1;
    return {...context, displayedLines: lines.slice(Math.max(0,focus-5), Math.max(0,focus-5)+16)};
  }

  function getDisplayedLines(context: FlowContext | null, isExpanded: boolean): DisplayedLine[] {
    if (!context?.displayedLines) return [];
    const all = context.displayedLines;
    if (isExpanded || all.length <= 16) return all;
    const hit = all.findIndex(l => l.isHit);
    const start = Math.max(0, (hit < 0 ? 0 : hit) - 4);
    return all.slice(start, start + 16);
  }

  function getStepRelations(stepId: string) {
    return (flowStore.data?.semanticMap?.edges || []).filter(e => e.fromStepId === stepId);
  }

  function handleSelect(frameId: string) {
    if (flowStore.selectedFrameId !== frameId) flowStore.select(frameId);
    const storyEl = document.querySelector(`[data-story-frame="${frameId}"], [data-story-step="${flowStore.selectedStepId}"]`);

  }
</script>

<div id="code-flow" class="flow-panel">
  {#if !frames.length}
    <p class="empty">이 요청에서 확인된 처리 단계가 없습니다.</p>
  {:else}
    {#each frames as frame, index (frame.frameId)}
      {@const isSelected = selectedFrameId === frame.frameId || (!selectedFrameId && selectedStepId === frame.primaryStepRef)}
      {@const isExpanded = expandedSet.has(frame.primaryStepRef)}
      {@const currContext = getContext(frame.primaryStepRef, false)}
      {@const baseContext = compare ? getContext(frame.primaryStepRef, true) : null}
      {@const currLines = getDisplayedLines(currContext, isExpanded)}
      {@const baseLines = getDisplayedLines(baseContext, isExpanded)}
      {@const roleName = GATEWAY_ROLES[frame.role] || (frame.architecture ? `${frame.role.toUpperCase()} (${LAYER_LABELS[frame.architecture] || frame.architecture})` : frame.role.toUpperCase())}
      {@const path = currContext?.canonicalPath || frame.sourceAnchor?.repoRelativePath || ''}
      {@const rels = getStepRelations(frame.primaryStepRef)}
      {@const delta = getDelta(frame.primaryStepRef)}
      {@const isSurgery = compare && (delta?.kind === 'added_behavior' || delta?.kind === 'changed_rule')}
      {@const desc = frame.narrative || (frame.condition ? `조건 · ${frame.condition}` : '다음 구현 연결 및 처리')}

      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <article
        class="code-card"
        class:selected={isSelected}
        class:outside-focus={matchingStepIds !== null && !matchingStepIds.has(frame.primaryStepRef)}
        class:condition-match={matchingStepIds !== null && matchingStepIds.has(frame.primaryStepRef)}
        data-card={frame.frameId}
        data-step-card={frame.primaryStepRef}
        onclick={() => handleSelect(frame.frameId)}
      >
        <header class="card-head">
          <div class="card-head-top">
            <span class="card-frame-badge">FRAME {String(frame.ordinal || index + 1).padStart(2, '0')} · {roleName}</span>
            {#if isSurgery}
              <span class="surgery-badge">⚡ ACTIVE SURGERY</span>
            {/if}
            {#if compare && delta}
              {#if delta.kind === 'changed_rule'}
                <span class="delta-tag mod">~ RULE CHG</span>
              {:else if delta.kind === 'added_behavior'}
                <span class="delta-tag add">+ NEW SURGERY</span>
              {:else if delta.kind === 'removed_behavior'}
                <span class="delta-tag del">- REMOVED</span>
              {/if}
            {:else if frame.status === 'partial'}
              <span class="delta-tag mod">PARTIAL</span>
            {:else if frame.status === 'unknown'}
              <span class="delta-tag del">UNKNOWN</span>
            {/if}
          </div>
          <div class="card-head-body">
            <h3>{#if isSurgery}⚡ {/if}{frame.title}</h3>
            <p class="card-narrative">{desc}</p>
            {#if flowStore.selectedStep?.stepId !== flowStore.selectedFrame?.primaryStepRef}<p>{flowStore.selectedStep?.name}</p>{/if}
            <div class="path">{path} · {frame.technicalAnchor || frame.sourceAnchor?.enclosingSymbolPath || ''}</div>
          </div>
        </header>

        {#if frame.collapsedDetail && frame.collapsedDetail.count > 0}
          <div class="collapsed-banner">
            <small>＋ {frame.collapsedDetail.count}개 내부 처리 단계 묶음 ({frame.collapsedDetail.reason})</small>
          </div>
        {/if}

        {#if compare && baseContext}
          <div class="compare-grid">
            <div>
              <div class="compare-title">이전 · 선택한 분석</div>
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

        {#if currContext?.sourceLimitation}<p class="code-note">{currContext.sourceLimitation}</p>{/if}
        <div class="code-tools">
          <button type="button" data-expand={frame.primaryStepRef} onclick={(e) => { e.stopPropagation(); flowStore.toggleExpand(frame.primaryStepRef); }}>
            {isExpanded ? '코드 접기' : '코드 더 보기'}
          </button>
          <span class="precision">
            {currContext?.precision === 'exact' ? '선택 문장 강조' : '주변 코드 · 정확한 문장 범위 미확인'}
          </span>
        </div>

        {#if rels.length > 0}
          <div class="step-relations">
            <strong>직접 연결:</strong>
            {#each rels as rel}
              <span class="rel-tag">{EDGE_LABELS[rel.kind] || rel.kind} → {rel.toSymbolPath || rel.toStepId}</span>
            {/each}
          </div>
        {/if}
      </article>
    {/each}
  {/if}
</div>

<style>
  .flow-panel {
    padding-bottom: 40px;
  }
  .empty {
    padding: 30px 0;
    color: #666666;
    font-size: 12px;
  }
  .code-card {
    border: 1px solid var(--line, #dddddd);
    background: #ffffff;
    border-radius: 8px;
    margin-bottom: 16px;
    padding: 14px 16px;
    transition: border-color .15s, box-shadow .15s;
    cursor: pointer;
  }
  .code-card:hover {
    border-color: #999999;
  }
  .code-card.selected {
    border: 2px solid var(--ink, #171717);
    box-shadow: 2px 2px 0 var(--ink, #171717);
  }
  .code-card.outside-focus {
    opacity: 0.35;
  }
  .code-card.condition-match {
    border-color: #2b8a3e;
    background: #fbfefb;
  }
  .card-head {
    border-bottom: 1px solid #eeeeee;
    padding-bottom: 8px;
    margin-bottom: 10px;
  }
  .card-head-top {
    display: flex;
    gap: 8px;
    align-items: center;
    margin-bottom: 4px;
    font-size: 10px;
  }
  .card-frame-badge {
    font-family: ui-monospace, monospace;
    font-weight: 800;
    color: var(--muted, #666666);
  }
  .surgery-badge {
    background: #171717;
    color: #ffffff;
    padding: 1.5px 6px;
    border-radius: 3px;
    font-weight: 850;
    font-size: 8.5px;
  }
  .delta-tag {
    padding: 1.5px 6px;
    border-radius: 3px;
    font-weight: 800;
    font-size: 8.5px;
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
    margin: 3px 0 4px;
    font-size: 13px;
    font-weight: 800;
  }
  .card-narrative {
    font-size: 11px;
    color: #555555;
    line-height: 1.4;
    margin: 0 0 4px;
  }
  .path {
    font-family: ui-monospace, monospace;
    font-size: 9.5px;
    color: #888888;
  }
  .collapsed-banner {
    background: #f7f7f5;
    border: 1px dashed #d5d5d0;
    border-radius: 4px;
    padding: 4px 8px;
    margin-bottom: 10px;
    color: #666666;
    font-size: 10px;
  }
  .source {
    margin: 0;
    background: #f9f9f9;
    border: 1px solid #e0e0e0;
    border-radius: 5px;
    padding: 8px 10px;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px;
    line-height: 1.55;
    overflow-x: auto;
  }
  .source-empty {
    font-size: 11px;
    color: #888888;
    padding: 12px;
    background: #fafaf8;
    border: 1px dashed #dddddd;
    border-radius: 5px;
  }
  .line {
    display: flex;
    gap: 12px;
  }
  .ln {
    width: 28px;
    text-align: right;
    color: #aaaaaa;
    user-select: none;
    font-size: 10px;
  }
  .txt {
    white-space: pre;
    color: #222222;
  }
  .line.hit {
    background: #fff8d6;
    font-weight: 700;
  }
  .line.struct {
    background: #f1f7ff;
  }
  .code-note {
    font-size: 10px;
    color: #888888;
    margin-top: 4px;
  }
  .code-tools {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 8px;
    font-size: 10px;
  }
  .code-tools button {
    background: #ffffff;
    border: 1px solid #cccccc;
    padding: 3px 8px;
    border-radius: 4px;
    font-size: 10px;
    cursor: pointer;
  }
  .code-tools button:hover {
    background: #f0f0f0;
  }
  .precision {
    color: #888888;
  }
  .step-relations {
    margin-top: 8px;
    padding-top: 6px;
    border-top: 1px dotted #e0e0e0;
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    align-items: center;
    font-size: 9.5px;
  }
  .step-relations strong {
    color: #666666;
  }
  .rel-tag {
    background: #f0f0f0;
    padding: 1.5px 6px;
    border-radius: 3px;
    color: #444444;
  }
  .compare-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
  }
  .compare-title {
    font-size: 10px;
    font-weight: 700;
    margin-bottom: 4px;
    color: #555555;
  }
</style>
