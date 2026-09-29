<script lang="ts">
  let { candidates, request }: { candidates: string[]; request: string } = $props();
  let preview = $state<{
    entrySymbolPath: string;
    snapshotId: string;
    source: { path: string; startLine: number; endLine: number; code: string };
  } | null>(null);
  let loading = $state('');
  let error = $state('');
  let previewTicket = 0;

  async function onCandidatePreview(entry: string) {
    const ticket = ++previewTicket;
    preview = null;
    error = '';
    loading = entry;
    const url = new URL('/api/candidate/preview', location.origin);
    url.searchParams.set('entrySymbol', entry);
    const token = new URLSearchParams(location.search).get('token') || '';
    try {
      const response = await fetch(url, { headers: token ? { 'X-CodeFlow-Token': token } : {} });
      if (!response.ok) throw new Error(`코드 확인 실패 (${response.status})`);
      const result = await response.json();
      if (ticket === previewTicket && result.entrySymbolPath === entry && result.snapshotId && result.source?.code) preview = result;
    } catch (cause) {
      if (ticket === previewTicket) error = cause instanceof Error ? cause.message : '코드를 확인하지 못했습니다.';
    } finally {
      if (ticket === previewTicket) loading = '';
    }
  }
</script>

<div class="candidate-choices">
  {#each candidates as candidate}
    <div class="candidate-choice">
      <code>{candidate}</code>
      <button type="button" onclick={() => onCandidatePreview(candidate)} disabled={loading === candidate}>코드 미리 보기</button>
      <button type="button" disabled={preview?.entrySymbolPath !== candidate} onclick={() => window.fetchTaskView(request, candidate, '', false, preview!.snapshotId)}>이 진입점 선택</button>
    </div>
  {/each}
  {#if loading}<p role="status">진입 코드를 확인하는 중…</p>{/if}
  {#if error}<p role="alert">{error}</p>{/if}
  {#if preview}
    <div class="candidate-source" aria-label="후보 진입 코드">
      <p><strong>{preview.source.path}</strong> · {preview.source.startLine}–{preview.source.endLine}줄</p>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <pre tabindex="0"><code>{preview.source.code}</code></pre>
      <p>이 코드가 요청한 처리의 시작점인지 확인한 뒤 선택하세요.</p>
    </div>
  {/if}
</div>

<style>
  .candidate-choices { display: grid; gap: 12px; }
  .candidate-choice { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .candidate-choice code { overflow-wrap: anywhere; }
  .candidate-source pre { max-height: 360px; overflow: auto; padding: 12px; border: 1px solid var(--line, #ddd); background: #fff; white-space: pre; }
</style>
