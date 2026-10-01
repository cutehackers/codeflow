<script lang="ts">
  import {flowStore} from '../stores/flowStore.svelte';
  import {blockSourceLines, blockLineParts, blockDetailAlternatives} from '../stores/requestedFlowBlocks';
  const segment = $derived(flowStore.selectedSegment);
  const data = $derived(flowStore.data);
</script>
<div id="requested-flow-blocks" data-code-focus>
  {#if segment && data}
    <header><div class="segment-heading"><h2>{#if flowStore.selectedCategory}<span class="breadcrumb">{flowStore.selectedCategory.title} / </span>{/if}{segment.purpose}</h2><button data-navigation-focus={`segment-detail:${segment.segmentId}`} onclick={() => flowStore.openSegmentDetail()}>구간 상세</button></div><p>{segment.inclusionReason}</p><p class="legend">강조 코드 선택: 문장별 탐색 · 나머지: 주변 코드</p>
    </header>
    {#each segment.blocks as block (block.blockId)}
      {@const expanded = flowStore.expandedBlocks.has(block.blockId)}
      {@const lines = blockSourceLines(data, block, expanded)}
      {@const alternatives = blockDetailAlternatives(data, block)}
      <article data-code-block={block.blockId} class:selected={flowStore.selectedBlockId === block.blockId}>
        <div class="block-source-heading"><h3>{block.repoRelativePath} · {block.symbolPath}</h3><span>행 {block.lineRange[0]}–{block.lineRange[1]}</span></div>
        {#if lines.length}
          <pre class="source"><span class="source-lines">{#each lines as item}{@const parts = blockLineParts(data, block, item.line)}{#if item.omittedBefore}<span class="omission">… 주변 코드 생략 · 코드 블록 펼치기로 읽기</span>{/if}<span class="line" class:hit={parts.some(part => part.stepId)} data-block-line={item.line.lineNumber}><span class="line-number">{item.line.lineNumber}</span><code>{#each parts as part}{#if part.stepId}<button class="source-statement" data-block-detail={part.stepId} data-navigation-focus={`block-detail:${block.blockId}:${part.stepId}:${item.line.lineNumber}`} aria-label={`${flowStore.steps.find(step => step.stepId === part.stepId)?.name || '포함 문장'} · ${item.line.lineNumber}행 문장별 탐색`} onclick={() => flowStore.openSegmentDetail(part.stepId, block.blockId)}><mark data-block-step={part.stepId}>{part.text}</mark></button>{:else}{part.text}{/if}{/each}</code></span>{/each}</span></pre>
        {:else}<p>이 분석 시점의 코드 블록을 읽을 수 없습니다.</p>{/if}
        {#if block.lineRange[1] - block.lineRange[0] >= 24}<button data-navigation-focus={`block-expand:${block.blockId}`} aria-expanded={expanded} onclick={() => {flowStore.selectedBlockId = block.blockId; flowStore.toggleBlock(block.blockId);}}>{expanded ? '코드 블록 접기' : '코드 블록 펼치기'}</button>{/if}
        {#if alternatives.length}<details class="overlap-details" open={flowStore.expandedReadingRelations.has(`block-overlap:${block.blockId}`)} ontoggle={event=>{if ((event.currentTarget as HTMLDetailsElement).open !== flowStore.expandedReadingRelations.has(`block-overlap:${block.blockId}`)) flowStore.toggleReadingRelation(`block-overlap:${block.blockId}`);}}><summary>겹친 코드의 문장 선택 · {alternatives.length}개</summary>{#each alternatives as highlight}<button data-block-detail={highlight.stepId} data-navigation-focus={`block-detail:${block.blockId}:${highlight.stepId}`} onclick={()=>flowStore.openSegmentDetail(highlight.stepId,block.blockId)}>행 {highlight.lineRange[0]}–{highlight.lineRange[1]} · {flowStore.steps.find(step=>step.stepId===highlight.stepId)?.name || '포함 문장'} · 문장별 탐색</button>{/each}</details>{/if}
      </article>
    {/each}
  {/if}
</div>
<style>
  .block-source-heading{display:flex;flex-wrap:wrap;align-items:baseline;gap:4px 10px}.block-source-heading h3{margin:0;font-size:11px}.block-source-heading span{font-size:10px;color:#666;white-space:nowrap}.breadcrumb{font-size:11px;font-weight:normal}
  header,article{padding:12px;border:1px solid #ddd;margin-bottom:12px;min-width:0}.segment-heading{display:flex;align-items:center;flex-wrap:wrap;gap:8px;justify-content:space-between}h2{font-size:17px;margin:4px 0}h3{font-size:12px;overflow-wrap:anywhere;margin:0 0 4px}p,.legend{font-size:12px;line-height:1.6;margin:6px 0}.legend,.breadcrumb{color:#666}.legend{font-size:10px}.breadcrumb{margin:0 .4em 0 0}article.selected{border-color:#888}.source{overflow-x:auto;background:#f7f7f7;padding:10px;font-size:12px;line-height:1.8;white-space:pre;margin:8px 0}.source-lines{display:inline-grid;min-width:100%}.line{display:flex}.line.hit{background:#fff1a8}.line.hit:has(.source-statement:hover),.line.hit:has(.source-statement:focus-visible){background:#ffe477}.line-number{display:inline-block;min-width:4em;flex-shrink:0;color:#777;user-select:none}.line code{white-space:pre}.omission{display:block;color:#666}mark{background:transparent;color:inherit}button{font-size:11px;padding:6px;max-width:100%;overflow-wrap:anywhere;text-align:left}.source-statement{display:inline;border:0;border-radius:0;padding:0;margin:0;background:transparent;color:inherit;font:inherit;white-space:pre;max-width:none;cursor:pointer}.source-statement:focus-visible{outline:2px solid #333;outline-offset:2px}
</style>
