<script lang="ts">
  import { flowStore } from '../stores/flowStore.svelte';

  const viewMode = $derived(flowStore.viewMode);

  function setView(mode: 'code' | 'process') {
    flowStore.setViewMode(mode);
  }
</script>

<section class="view-toolbar" aria-label="흐름 보기 선택">
  <div role="group" aria-label="중앙 보기" class="view-buttons">
    <button
      id="view-code"
      type="button"
      aria-pressed={viewMode === 'code'}
      aria-controls="code-flow"
      onclick={() => setView('code')}
    >
      코드 흐름
    </button>
    <button
      id="view-process"
      type="button"
      aria-pressed={viewMode === 'process'}
      aria-controls="process-flow"
      onclick={() => setView('process')}
    >
      처리 흐름
    </button>
  </div>
  <p>같은 흐름 · 같은 선택 · 다른 보기</p>
</section>

<style>
  .view-toolbar {
    margin: 0 30px 16px;
    display: flex;
    gap: 12px;
    align-items: center;
    flex-wrap: wrap;
    position: sticky;
    top: 0;
    z-index: 5;
    background: #fafafa;
    padding: 10px 0;
    border-bottom: 1px solid #dddddd;
  }
  .view-buttons {
    display: flex;
    gap: 6px;
  }
  .view-toolbar button {
    font-size: 12px;
    padding: 6px 12px;
    border-radius: 5px;
    border: 1px solid #cccccc;
    background: #ffffff;
    cursor: pointer;
    color: inherit;
  }
  .view-toolbar button:hover {
    background: #eeeeee;
  }
  .view-toolbar button[aria-pressed=true] {
    background: #171717;
    color: #ffffff;
    border-color: #171717;
  }
  .view-toolbar p {
    font-size: 11px;
    color: #666666;
    margin: 0;
  }

  @media (max-width: 650px) {
    .view-toolbar {
      margin-left: 15px;
      margin-right: 15px;
    }
  }
</style>
