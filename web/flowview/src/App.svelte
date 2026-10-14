<script lang="ts">
  import ConditionPathControls from './components/ConditionPathControls.svelte';
  import StepConnections from './components/StepConnections.svelte';
  import NoticeBar from './components/NoticeBar.svelte';
  import RequestedFlowBlockPanel from './components/RequestedFlowBlockPanel.svelte';
  import RequestedFlowSegmentRail from './components/RequestedFlowSegmentRail.svelte';
  import RequestedFlowCategorySequence from './components/RequestedFlowCategorySequence.svelte';
  import CodeFlowPanel from './components/CodeFlowPanel.svelte';
  import ContextAside from './components/ContextAside.svelte';
  import CandidateChoices from './components/CandidateChoices.svelte';
  import { flowStore } from './stores/flowStore.svelte';
  import { summarizeUnknownBoundaries } from './stores/flowNavigation';

  const codeFocusedStudyLayout = new URLSearchParams(window.location.search).get('studyLayout') === 'code-focused';
  const purposeOverview = $derived(!!flowStore.data?.requestedFlowOverview);
  const unknowns = $derived(flowStore.data?.semanticMap?.unknowns || []);
  const unknownSummaries = $derived(summarizeUnknownBoundaries(unknowns));
  const analysisLimitations = $derived([
    flowStore.data?.resolutionLimitation,
    flowStore.data?.codeReadingPathLimitation,
    flowStore.data?.requestedFlowOverviewLimitation,
    flowStore.data?.sourceContextMissing ? '소스 근거 미확인 · 재분석 필요' : '',
    unknowns.length > 0 ? `미확인 연결 ${unknowns.length}건` : '',
    flowStore.data?.flowSequence?.summaryLimitations?.length ? `요약 한계 ${flowStore.data.flowSequence.summaryLimitations.length}건` : '',
  ].filter((item): item is string => !!item));
  let olderViewsOpen = $state(false);

  function mostRecentViews() {
    const latestByTitle = new Set<string>();
    return flowStore.views.filter(view => {
      const title = view.title?.trim() || '저장된 흐름';
      if (latestByTitle.has(title)) return false;
      latestByTitle.add(title);
      return true;
    });
  }

  const latestViews = $derived(mostRecentViews());
  const hiddenViewCount = $derived(flowStore.views.length - latestViews.length);
  const displayedViews = $derived(olderViewsOpen ? flowStore.views : latestViews);

  function onQuerySubmit(query: string) {
    const event = new CustomEvent('codeflow:query', { detail: { query } });
    window.dispatchEvent(event);
  }

  function onFormSubmit(e: SubmitEvent) {
    e.preventDefault();
    const input = document.getElementById('query-input') as HTMLInputElement | null;
    const q = input?.value?.trim();
    if (q) {
      onQuerySubmit(q);
    }
  }
</script>

<div class="app-container">
  <header class="entry-header">
    <button class="brand" type="button" onclick={() => window.showFlowHome()}>CodeFlow<span aria-hidden="true" class="brand-divider">/</span><span class="brand-surface" aria-hidden="true">FlowView</span></button>
    {#if !flowStore.home}<strong>{flowStore.flowTitle}</strong>{/if}
  </header>
  {#if flowStore.home}
    <section class="home" aria-label="FlowView 시작">
      <div class="home-intro">
        <div>
          <p class="home-eyebrow">코드 이해를 시작하는 곳</p>
          <h1>궁금한 흐름에서,<br />구현의 핵심까지.</h1>
          <p class="home-description">이해하고 싶은 기능을 알려주세요.<br />처리의 연결을 따라가며, 판단의 근거를 코드에서 확인합니다.</p>
        </div>
        <ol class="reading-guide" aria-label="FlowView에서 코드를 읽는 순서">
          <li><span aria-hidden="true">01</span><div><strong>전체 흐름</strong><p>시작과 주요 판단, 처리 결과를 파악합니다.</p></div></li>
          <li><span aria-hidden="true">02</span><div><strong>실행 단계</strong><p>관문을 펼쳐 내부 함수와 연결을 살펴봅니다.</p></div></li>
          <li><span aria-hidden="true">03</span><div><strong>코드 근거</strong><p>해당 구현을 읽고 원래 흐름으로 돌아옵니다.</p></div></li>
        </ol>
      </div>
      <div class="request-card" aria-busy={flowStore.busy}>
        <form id="request-form" onsubmit={onFormSubmit}>
          <label for="query-input">어떤 흐름을 살펴볼까요?</label>
          <div class="request-input-row">
            <input id="query-input" type="text" placeholder="기능 설명 또는 파일#심볼" aria-describedby="request-hint" required />
            <button id="request-submit" type="submit" disabled={flowStore.busy}>{flowStore.busy ? '분석 중…' : '흐름 보기'}<span aria-hidden="true">→</span></button>
          </div>
          <p id="request-hint">이해하려는 처리 목적을 적거나, 분석을 시작할 함수의 위치를 입력하세요.</p>
        </form>
        <div class="request-status" class:visible={!!flowStore.notice || flowStore.busy}>
          <p role="status">{flowStore.notice}</p>
          {#if flowStore.overviewDetailPending}<button onclick={() => window.openFlowDetail()}>중간 결과 확인</button>{/if}
          {#if flowStore.busy}<button onclick={() => window.cancelFlowRequest()}>취소</button>{/if}
        </div>
      </div>
      {#if flowStore.errorCode === 'no_entrypoints_found'}
        <div class="recovery-box" role="region" aria-label="진입점 복구 안내">
          <p><strong>진입점이 발견되지 않았습니다.</strong> 아래 3가지 방법으로 분석을 진행할 수 있습니다:</p>
          <ol class="recovery-list">
            <li>
              <strong>1. 직접 진입 심볼 입력</strong>
              <p>상단 입력창에 메인 함수나 엔트리포인트를 직접 입력하세요. (예: <code>main.go#main</code>, <code>server.ts#bootstrap</code>)</p>
            </li>
            <li>
              <strong>2. CodeGraph 색인 실행</strong>
              <p>터미널에서 <code>codegraph index</code>를 실행하여 전역 호출 그래프를 생성하세요.</p>
            </li>
            <li>
              <strong>3. 프로젝트 최상위 심볼 선택</strong>
              {#if flowStore.candidates.length}
                <CandidateChoices candidates={flowStore.candidates} request={flowStore.pendingRequest} />
              {:else}
                <p class="muted">감지된 공개 심볼이 없습니다. 진입 심볼을 직접 입력해 주세요.</p>
              {/if}
            </li>
          </ol>
        </div>
      {:else if flowStore.candidates.length}
        <p>요청과 연결된 코드를 확인할 진입점을 선택하세요. 선택 전에는 새 흐름을 저장하지 않습니다.</p>
        <CandidateChoices candidates={flowStore.candidates} request={flowStore.pendingRequest} />
      {/if}
      <section class="view-library" aria-label="저장된 FlowView">
        <header class="library-header"><div><h2>다시 이어서 읽기</h2><p>저장된 분석에서 탐색을 이어갑니다.</p></div><span class="view-count">{flowStore.views.length}개 FlowView</span></header>
        {#if flowStore.listError}
          <div class="library-empty"><p role="alert">{flowStore.listError}</p><button onclick={() => window.showFlowHome()}>목록 다시 불러오기</button></div>
        {:else if !flowStore.views.length}
          <div class="library-empty"><strong>첫 번째 흐름을 열어보세요.</strong><p>위에서 분석한 흐름은 여기에 보관됩니다.<br />연결된 에이전트에게 CodeFlow 분석을 요청할 수도 있습니다.</p></div>
        {:else}
          <ul class="view-list">
            {#each displayedViews as view, index (view.viewId)}
              <li><button class="view-card" onclick={() => window.openFlowView(view.viewId)}>
                <span class="view-card-top"><span>{olderViewsOpen ? `FLOWVIEW ${String(index+1).padStart(2,'0')}` : '최근 분석'}</span><span aria-hidden="true">↗</span></span>
                <strong>{view.title || '저장된 흐름'}</strong>
                <span class="view-card-bottom"><span>{view.savedAt ? new Date(view.savedAt).toLocaleString() : '저장된 분석'}</span><span>이어서 읽기 <span aria-hidden="true">→</span></span></span>
              </button></li>
            {/each}
          </ul>
          {#if hiddenViewCount > 0}
            <button class="view-history-toggle" aria-expanded={olderViewsOpen} onclick={() => olderViewsOpen = !olderViewsOpen}>{olderViewsOpen ? '최근 흐름만 보기' : `이전 분석 ${hiddenViewCount}개 보기`}</button>
          {/if}
        {/if}
      </section>
    </section>
  {:else}
  <!-- 1. FLOW RESOLUTION AREA -->
  <section class="request-panel" class:compact={purposeOverview} aria-label="요청 영역">
    <div class="request-summary">
      <div class="request-title-row">
        <span class="request-tag">요청 흐름</span>
        <strong class="request-raw">{flowStore.flowResolution?.rawRequest || flowStore.data?.request?.request || flowStore.flowTitle}</strong>
        {#if flowStore.flowResolution?.status}
          <span class="status-badge" class:resolved={flowStore.flowResolution.status === 'resolved'}>
            {flowStore.flowResolution.status === 'resolved' ? '진입점 확인' : flowStore.flowResolution.status}
          </span>
        {/if}
      </div>
      {#if purposeOverview}
        <div class="overview-request-context">
          {#if analysisLimitations.length}<span>분석 한계: {analysisLimitations.join(' · ')}</span>{/if}
          <details><summary>진입점과 분석 근거</summary>
            <p>진입점: <code>{flowStore.flowResolution?.entrySymbolPath || flowStore.data?.request?.entrySymbol || ''}</code></p>
            {#if flowStore.data?.requestedFocus}<p>선택한 구현: <code>{flowStore.data.requestedFocus.symbolPath}</code> · {flowStore.data.requestedFocus.reason}</p>{/if}
            {#each flowStore.flowResolution?.evidence || [] as evidence}<p>{evidence.description}{#if evidence.sourceAnchor} · <code>{evidence.sourceAnchor.repoRelativePath}#{evidence.sourceAnchor.enclosingSymbolPath}</code>{/if}</p>{/each}
            <p>요청과 코드의 의미 연결은 선택자의 판단입니다. 표시된 심볼과 코드 범위는 검증했습니다.</p>
          </details>
        </div>
      {:else}<div class="request-details">
        <span class="request-detail"><strong>진입점:</strong> <code>{flowStore.flowResolution?.entrySymbolPath || flowStore.data?.request?.entrySymbol || ''}</code></span>
        {#if flowStore.data?.requestedFocus}
          <span class="request-detail"><strong>선택한 구현:</strong> <code>{flowStore.data.requestedFocus.symbolPath}</code> · {flowStore.data.requestedFocus.reason}</span>
        {/if}
        {#if flowStore.flowResolution?.evidence?.length}
          {#each flowStore.flowResolution.evidence as evidence}
            <span class="request-detail"><strong>후보 정보:</strong> {evidence.description}{#if evidence.sourceAnchor} · 검증한 코드 <code>{evidence.sourceAnchor.repoRelativePath}#{evidence.sourceAnchor.enclosingSymbolPath}</code>{/if}</span>
          {/each}
          <span class="request-detail">요청과 코드의 의미 연결은 선택자의 판단입니다. CodeFlow는 표시된 심볼과 코드 범위를 검증했습니다.</span>
        {/if}
        {#if analysisLimitations.length}
          {#each analysisLimitations as limitation}
            <span class="request-detail"><strong>분석 한계:</strong> {limitation}</span>
          {/each}
        {:else}
          <span class="request-detail"><strong>분석 한계:</strong> 처리 목적별 Overview 생성 전</span>
        {/if}
      </div>
      {/if}
    </div>
  </section>

  <!-- Live Notice & Status Controls -->
  <NoticeBar />

  {#if purposeOverview}
    {#if flowStore.data?.requestedFlowOverview?.categories?.length}<RequestedFlowCategorySequence />{/if}
    <main class="layout" class:code-focused-study={codeFocusedStudyLayout}>
      <RequestedFlowSegmentRail />
      <section class="center purpose-reading" id="selected-flow" tabindex="-1" aria-label="선택한 처리 과정">
        <div class="view-stage purpose-reading" id="source-panel" data-navigation-panel><RequestedFlowBlockPanel /></div>
        {#if flowStore.explorationMode === 'statements'}
          <details class="segment-source-detail" open ontoggle={event => {if (!(event.currentTarget as HTMLDetailsElement).open) flowStore.closeSegmentDetail();}}>
            <summary>처리 과정의 소스 상세</summary>
            <button onclick={() => flowStore.closeSegmentDetail()}>상세 닫기</button>
            <ConditionPathControls /><StepConnections /><CodeFlowPanel />
          </details>
        {/if}
        {#if unknowns.length > 0}<div id="boundaries" class="boundaries"><details><summary>확인하지 못한 연결 {unknowns.length}건</summary>{#each unknownSummaries as summary}<p>{summary.description}</p>{/each}</details></div>{/if}
      </section>
      <ContextAside />
    </main>
  {:else}
    <section class="request-panel" role="status" aria-label="중간 결과">
      <h2>중간 결과 · 처리 목적별 Overview 필요</h2>
      <p>분석은 저장되었지만 처리 과정의 개요가 아직 없습니다. 에이전트가 코드 근거를 확인하고 Overview를 생성해야 합니다.</p>
      {#if flowStore.data?.requestedFlowOverviewLimitation}<p>{flowStore.data.requestedFlowOverviewLimitation}</p>{/if}
    </section>
  {/if}

  {/if}

  <!-- Footer -->
  <footer class="footer">
    <span>소스 근거 기반 · 실행 기록 없음</span>
    <span>처리 목적 → 처리 과정 → 소스 상세</span>
  </footer>
</div>

<style>
  .entry-header { display:flex; align-items:center; gap:24px; padding:20px 30px; border-bottom:1px solid #e8e8e8; }
  .view-stage.purpose-reading{padding:0;border:0;background:transparent}
  .center.purpose-reading{padding-top:0}
  .request-panel.compact{padding-top:12px;padding-bottom:12px}.overview-request-context{display:flex;flex-wrap:wrap;gap:8px 18px;margin-top:8px;font-size:11px;color:#555}.overview-request-context details{min-width:0;overflow-wrap:anywhere}.overview-request-context summary{cursor:pointer}.overview-request-context code{font-size:10px}
  .entry-header > strong { font-size:12px; overflow-wrap:anywhere; }
  .brand { display:flex; align-items:center; gap:12px; font-size:17px; font-weight:750; letter-spacing:-.5px; padding:4px 0; border:0; background:transparent; }
  .brand:hover { background:transparent; }
  .brand-divider { font-weight:300; color:#aaa; }
  .brand-surface { font-size:12px; font-weight:500; color:#666; letter-spacing:0; }
  .home { max-width:1100px; margin:0 auto; padding:60px 30px 48px; }
  .home-intro { display:grid; grid-template-columns:minmax(0,1.3fr) minmax(0,1fr); align-items:center; gap:70px; margin-bottom:40px; }
  .home-eyebrow { font-size:11px; color:#666; margin-bottom:18px; }
  .home h1 { font-size:clamp(30px,3.4vw,46px); line-height:1.28; font-weight:650; letter-spacing:-1.8px; }
  .home-description { font-size:13px; line-height:1.9; color:#666; margin-top:20px; word-break:keep-all; }
  .reading-guide { list-style:none; padding:0 0 0 30px; margin:0; border-left:1px solid #ddd; }
  .reading-guide li { display:flex; gap:18px; padding:14px 0; }
  .reading-guide li > span { font:11px ui-monospace,monospace; color:#626262; padding-top:4px; }
  .reading-guide strong { font-size:13px; font-weight:650; }
  .reading-guide p { color:#666; font-size:11px; margin-top:3px; word-break:keep-all; }
  .request-card { padding:26px; border:1px solid #ccc; border-radius:10px; background:#fff; box-shadow:0 4px 16px #00000005; }
  .home form label { display:block; font-size:13px; font-weight:650; margin-bottom:12px; }
  .request-input-row { display:flex; gap:12px; }
  .home input { flex:1; min-width:0; width:100%; padding:14px 16px; border:1px solid #d5d5d5; border-radius:6px; background:#fafafa; font-size:14px; color:#171717; }
  .home input::placeholder { color:#777; }
  #request-submit { display:flex; justify-content:center; align-items:center; gap:22px; padding:12px 20px; background:#171717; color:#fff; border:1px solid #171717; white-space:nowrap; }
  #request-submit:hover:not(:disabled) { background:#333; }
  #request-hint { font-size:11px; color:#666; margin-top:10px; }
  .request-status { display:flex; justify-content:space-between; align-items:center; gap:12px; font-size:12px; }
  .request-status.visible { margin-top:16px; padding-top:14px; border-top:1px solid #eee; }
  .view-library { margin-top:52px; }
  .library-header { display:flex; justify-content:space-between; gap:16px; align-items:center; margin-bottom:20px; }
  .library-header h2 { font-size:18px; letter-spacing:-.5px; }
  .library-header p { font-size:12px; color:#666; margin-top:4px; }
  .view-count { font:11px ui-monospace,monospace; color:#666; white-space:nowrap; }
  .view-list { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:14px; list-style:none; padding:0; margin:0; }
  .view-card { display:flex; flex-direction:column; text-align:left; padding:20px; width:100%; height:100%; background:#fff; border:1px solid #ddd; border-radius:8px; }
  .view-card:hover { border-color:#555; background:#f5f5f5; }
  .view-card-top,.view-card-bottom { display:flex; justify-content:space-between; gap:12px; width:100%; color:#666; font-size:10px; }
  .view-card-top { font-family:ui-monospace,monospace; letter-spacing:.7px; }
  .view-card strong { display:block; font-size:15px; line-height:1.6; margin:16px 0 22px; overflow-wrap:anywhere; }
  .view-card-bottom { margin-top:auto; padding-top:12px; border-top:1px solid #eee; flex-wrap:wrap; }
  .view-history-toggle { display:block; margin:16px auto 0; font-size:12px; }
  .library-empty { border:1px dashed #ccc; border-radius:8px; padding:36px 24px; text-align:center; background:#f7f7f7; }
  .library-empty strong { font-size:14px; }.library-empty p { font-size:12px; color:#666; margin:8px 0; line-height:1.9; }
  @media(max-width:700px) {
    .home { padding:32px 20px; }.home-intro { grid-template-columns:1fr; gap:24px; margin-bottom:28px; }
    .reading-guide { border-left:0; border-top:1px solid #ddd; padding:12px 0 0; }.reading-guide li { padding:7px 0; }
    .request-card { padding:20px; }.request-input-row { flex-direction:column; }.view-list { grid-template-columns:1fr; }
    .view-library { margin-top:32px; }.entry-header { padding:16px 20px; }
  }
  @media(max-width:360px) {
    .entry-header { gap:8px; padding:12px 10px; }
    .entry-header > strong { flex:1 1 0; min-width:0; }
  }

  .layout {
    display: grid;
    grid-template-columns: 230px minmax(0, 1fr) 240px;
    margin: 0 30px;
    border-top: 1px solid var(--line, #dddddd);
    gap: 24px;
    align-items: start;
  }
  @media (min-width: 1151px) {
    .layout.code-focused-study {
      grid-template-columns: 260px minmax(0, 1fr);
    }
    .layout.code-focused-study :global(.context) {
      grid-column: 1 / -1;
      position: static;
      max-height: none;
      border-top: 1px solid var(--line, #dddddd);
    }
  }
  .center {
    min-width: 0;
    border-left: 1px solid var(--line, #dddddd);
    border-right: 1px solid var(--line, #dddddd);
    padding: 19px 24px 28px;
  }
  .view-stage {
    height: auto;
  }
  .boundaries {
    margin-top: 18px;
    padding-top: 15px;
    border-top: 1px solid var(--line, #dddddd);
    font-size: 11px;
    color: var(--muted, #666666);
  }
  .boundaries p {
    margin-top: 6px;
    overflow-wrap: anywhere;
  }
  .footer {
    padding: 22px 30px;
    font-size: 10px;
    color: var(--muted, #666666);
    display: flex;
    justify-content: space-between;
  }

  @media (min-width: 1600px) {
    .layout, .footer {
      max-width: 1540px;
      margin-left: auto;
      margin-right: auto;
    }
    .layout {
      grid-template-columns: 220px minmax(0, 1fr) 290px;
    }
  }
  @media (max-width: 1150px) {
    .layout {
      grid-template-columns: 175px minmax(0, 1fr);
      gap: 18px;
    }
    .center {
      border-right: 0;
      padding-right: 0;
    }
  }
  @media (max-width: 650px) {
    .layout {
      display: block;
      margin-left: 15px;
      margin-right: 15px;
    }
    .center {
      border-left: 0;
      border-right: 0;
      padding-left: 0;
      padding-right: 0;
    }
    .footer {
      padding: 20px 15px;
      gap: 15px;
    }
  }
  .recovery-box {
    margin: 16px 0;
    padding: 16px;
    border: 1px solid #dcdcdc;
    border-radius: 6px;
    background: #fafafa;
  }
  .recovery-list {
    margin: 12px 0 0 16px;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .recovery-list li p {
    margin: 4px 0 0 0;
    font-size: 12px;
    color: #555;
  }
  .request-panel {
    margin: 16px 30px 0;
    border: 1px solid var(--line, #dddddd);
    background: #ffffff;
    border-radius: 6px;
    padding: 12px 16px;
    box-sizing: border-box;
    min-width: 0;
    max-width: 100%;
    overflow-wrap: anywhere;
    word-break: break-word;
  }
  .request-summary {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .request-title-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    min-width: 0;
  }
  .request-tag {
    font-size: 10px;
    letter-spacing: 0.5px;
    font-weight: 700;
    padding: 2px 6px;
    background: #171717;
    color: #ffffff;
    border-radius: 4px;
    flex-shrink: 0;
  }
  .request-raw {
    font-size: 13px;
    font-weight: 600;
    overflow-wrap: anywhere;
    word-break: break-word;
    min-width: 0;
  }
  .status-badge {
    font-size: 10px;
    padding: 2px 6px;
    border: 1px solid #171717;
    border-radius: 4px;
    color: #171717;
    flex-shrink: 0;
  }
  .request-details {
    display: flex;
    flex-wrap: wrap;
    gap: 16px;
    font-size: 11px;
    color: #444444;
    min-width: 0;
  }
  .request-detail {
    min-width: 0;
    overflow-wrap: anywhere;
    word-break: break-word;
  }
  .request-detail strong {
    color: #171717;
    margin-right: 4px;
  }
  .request-detail code {
    font-family: ui-monospace, monospace;
    font-size: 11px;
    background: #f4f4f4;
    padding: 1px 4px;
    border-radius: 3px;
    white-space: normal;
    overflow-wrap: anywhere;
    word-break: break-all;
  }
  @media(max-width:800px){
    .request-panel {
      margin: 12px 15px 0;
      padding: 10px 12px;
    }
    .request-details {
      flex-direction: column;
      gap: 6px;
    }
  }
  @media(max-width:360px){
    .request-panel {
      margin: 8px 10px 0;
      padding: 8px 10px;
    }
    .layout {
      margin-left: 10px;
      margin-right: 10px;
    }
  }
</style>
