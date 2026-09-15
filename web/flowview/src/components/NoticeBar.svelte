<script lang="ts">
  import { flowStore } from '../stores/flowStore.svelte';

  const notice = $derived(flowStore.notice);
  const paused = $derived(flowStore.paused);
  const compare = $derived(flowStore.compare);
  const hasBaseline = $derived(!!flowStore.baseline);
  const hasPending = $derived(!!flowStore.pending);

  function handleCompare() {
    flowStore.toggleCompare();
  }

  function handlePause() {
    flowStore.togglePause();
  }

  function handleApply() {
    if (flowStore.pending) {
      flowStore.adopt(flowStore.pending, true);
    }
  }
</script>

<section class="notice" aria-label="코드 갱신 안내">
  <span id="live-notice" role="status" aria-live="polite">{notice}</span>
  <div class="controls">
    <button
      id="compare"
      type="button"
      aria-pressed={compare}
      disabled={!hasBaseline}
      onclick={handleCompare}
    >
      이전 코드 비교
    </button>
    <button
      id="pause"
      type="button"
      aria-pressed={paused}
      onclick={handlePause}
    >
      읽기 고정
    </button>
    {#if hasPending}
      <button
        id="apply"
        type="button"
        onclick={handleApply}
      >
        새 변경 적용
      </button>
    {/if}
  </div>
</section>

<style>
  .notice {
    margin: 0 30px 19px;
    border: 1px solid var(--line, #dddddd);
    background: #ffffff;
    border-radius: 6px;
    padding: 9px 13px;
    display: flex;
    align-items: center;
    gap: 14px;
    font-size: 11px;
    min-height: 44px;
    box-sizing: border-box;
  }
  .notice .controls {
    margin-left: auto;
    display: flex;
    gap: 6px;
    flex-shrink: 0;
  }
  .notice button {
    font-size: 10px;
    padding: 4px 8px;
    cursor: pointer;
    border: 1px solid #cccccc;
    background: #ffffff;
    border-radius: 5px;
    color: inherit;
  }
  .notice button:hover {
    background: #eeeeee;
    border-color: #888888;
  }
  .notice button:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .notice button[aria-pressed=true] {
    background: #171717;
    color: #ffffff;
    border-color: #171717;
  }

  @media (max-width: 650px) {
    .notice {
      margin-left: 15px;
      margin-right: 15px;
      flex-wrap: wrap;
    }
  }
</style>
