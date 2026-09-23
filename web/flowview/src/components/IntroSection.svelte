<script lang="ts">
  import { flowStore } from '../stores/flowStore.svelte';

  interface Props {
    onSubmitQuery?: (query: string) => void;
  }

  let { onSubmitQuery }: Props = $props();

  let queryText = $state('');

  const flowTitle = $derived(flowStore.flowTitle);
  const steps = $derived(flowStore.steps);
  const compare = $derived(flowStore.compare);
  const deltaChanges = $derived(compare ? flowStore.activeDeltaChanges : []);

  const scopeText = $derived.by(() => {
    if (!steps.length) return '';
    return `범위 · 총 ${steps.length}단계`;
  });

  const changeText = $derived.by(() => {
    if (!compare || !deltaChanges.length) return '';
    return `변경 · 검증된 의미 변경 ${deltaChanges.length}건이 감지되었습니다.`;
  });

  function onFormSubmit(event: SubmitEvent) {
    event.preventDefault();
    const trimmed = queryText.trim();
    if (!trimmed) return;
    if (onSubmitQuery) {
      onSubmitQuery(trimmed);
    } else {
      // Update URL search query
      const url = new URL(window.location.href);
      url.searchParams.set('request', trimmed);
      url.searchParams.delete('entrySymbol');
      window.history.replaceState(null, '', url.toString());
    }
  }
</script>

<section class="intro" aria-label="흐름 요청">
  <div class="eyebrow">CODEFLOW · FLOWVIEW / CODE COMPREHENSION</div>
  <h1 id="flow-title">{flowTitle}</h1>
  <p id="intro-note">호출한 코드에서 다음 구현까지. 조건과 변경을 같은 위치에서 확인합니다.</p>

  {#if scopeText || changeText}
    <div class="flow-summary" id="flow-summary" aria-label="현재 흐름 요약">
      {#if scopeText}<p id="flow-scope">{scopeText}</p>{/if}
      {#if changeText}<p id="flow-change">{changeText}</p>{/if}
    </div>
  {/if}

  <form class="request" id="request-form" onsubmit={onFormSubmit}>
    <label for="query-input" hidden>이해할 코드 흐름</label>
    <input
      id="query-input"
      aria-label="이해할 코드 흐름"
      placeholder="어떤 흐름을 이해하고 싶나요?"
      bind:value={queryText}
      required
    />
    <button id="query-submit" type="submit">흐름 보기</button>
  </form>
</section>

<style>
  .intro {
    padding: 24px 30px 18px;
  }
  .eyebrow {
    font-size: 10px;
    letter-spacing: 1.4px;
    font-weight: 750;
    color: var(--muted, #666666);
  }
  .intro h1 {
    font-size: 27px;
    letter-spacing: -0.8px;
    margin: 6px 0;
    font-weight: 800;
  }
  .intro p {
    font-size: 12px;
    color: #666666;
  }
  .flow-summary {
    font-size: 12px;
    color: #555555;
    margin-top: 10px;
  }
  .flow-summary p + p {
    margin-top: 4px;
  }
  .request {
    display: flex;
    gap: 8px;
    max-width: 850px;
    margin-top: 15px;
  }
  .request input {
    width: 100%;
    min-width: 0;
    padding: 9px 12px;
    border: 1px solid #cccccc;
    border-radius: 5px;
    background: #ffffff;
    font-size: 12px;
    color: inherit;
  }
  .request input:focus-visible {
    outline: 2px solid #111111;
    outline-offset: 2px;
  }
  .request button {
    white-space: nowrap;
    font-size: 12px;
    background: #171717;
    color: #ffffff;
    border: 1px solid #171717;
    padding: 6px 14px;
    border-radius: 5px;
    cursor: pointer;
  }
  .request button:hover {
    background: #333333;
  }

  @media (max-width: 650px) {
    .intro {
      padding: 20px 15px;
    }
    .intro h1 {
      font-size: 23px;
    }
    .request {
      margin-left: 0;
      margin-right: 0;
    }
  }
</style>
