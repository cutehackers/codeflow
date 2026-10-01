<script lang="ts">
  import {flowStore} from '../stores/flowStore.svelte';
  import {conditionOutcomeLabel} from '../stores/conditionNavigation';
  const overview = $derived(flowStore.data?.requestedFlowOverview);
  const selectedCategory = $derived(flowStore.selectedCategory);
  const connections = $derived.by(()=>{
    const seen = new Set<string>();
    return (overview?.relations || []).filter(relation=>{
      if (relation.status !== 'verified' || !relation.fromSegmentId || !relation.toSegmentId || !selectedCategory?.segmentIds.includes(relation.fromSegmentId) || selectedCategory.segmentIds.includes(relation.toSegmentId)) return false;
      const key=JSON.stringify([relation.fromSegmentId,relation.toSegmentId,relation.edgeIndexes]);
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    });
  });
</script>
{#if overview?.categories?.length}
  <nav id="requested-flow-categories" class="category-sequence" data-navigation-panel aria-label="상위 처리 분류">
    <p>전체 흐름 · 범주로 바로 이동하거나 아래 처리 구간을 이어서 읽습니다.</p>
    <div class="category-buttons">
      {#each overview.categories as category (category.categoryId)}
        <button
          type="button"
          data-category={category.categoryId}
          data-navigation-focus={`category:${category.categoryId}`}
          aria-pressed={selectedCategory?.categoryId === category.categoryId}
          aria-controls="requested-flow-blocks"
          title={category.title}
          onclick={() => flowStore.selectCategory(category.categoryId)}
        >
          <strong>{category.title}</strong>
          <span>{category.segmentIds.length}개 처리 구간</span>
          {#if selectedCategory?.categoryId === category.categoryId}<span class="current">읽는 구간: {flowStore.selectedSegment?.purpose}</span>{/if}
        </button>
      {/each}
    </div>
    {#if connections.length}<div class="connections" aria-label="범주 사이 확인된 연결"><span>확인된 연결</span>{#each connections as relation}<button type="button" data-category-connection={relation.toSegmentId} data-navigation-focus={`category-connection:${relation.fromStepId}:${relation.toStepId}`} onclick={()=>flowStore.selectSegment(relation.toSegmentId!)}>{overview.segments.find(segment=>segment.segmentId===relation.fromSegmentId)?.purpose} → {overview.segments.find(segment=>segment.segmentId===relation.toSegmentId)?.purpose}{#each relation.edges.flatMap(edge=>edge.conditions || []) as condition} · {conditionOutcomeLabel(condition.outcome)}{/each}</button>{/each}</div>{/if}
  </nav>
{/if}
<style>
  .category-sequence{margin:16px 30px;min-width:0;max-width:calc(100% - 60px);overflow-x:auto;padding:0 0 10px;box-sizing:border-box}.category-sequence p{font-size:11px;color:#555;margin:0 0 8px;line-height:1.6}.category-buttons{display:flex;gap:8px;min-width:0}.category-buttons button{flex:0 0 190px;max-width:230px;min-height:72px;padding:10px 12px;text-align:left;white-space:normal;overflow-wrap:anywhere;border:1px solid #ccc;border-radius:5px;background:#fff;color:#171717;cursor:pointer}.category-buttons button[aria-pressed=true]{border-color:#171717;background:#eee;color:#171717}.category-buttons button:focus-visible{outline:2px solid #171717;outline-offset:2px}.category-buttons strong,.category-buttons span{display:block}.category-buttons strong{font-size:13px;line-height:1.5}.category-buttons span{font-size:11px;line-height:1.6}.current{margin-top:4px}.category-buttons strong,.category-buttons .current{display:-webkit-box;-webkit-box-orient:vertical;-webkit-line-clamp:2;line-clamp:2;overflow:hidden}.connections{display:flex;flex-wrap:wrap;align-items:center;gap:6px;margin-top:8px;font-size:10px;color:#555}.connections button{font:inherit;text-align:left;background:#fff;color:#333;border:1px solid #ddd;border-radius:4px;padding:4px 6px;overflow-wrap:anywhere;max-width:100%;cursor:pointer}@media(max-width:650px){.category-sequence{margin-left:15px;margin-right:15px;max-width:calc(100% - 30px)}.category-buttons button{flex-basis:160px}}
</style>
