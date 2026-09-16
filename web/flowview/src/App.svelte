<script lang="ts">
  import NoticeBar from './components/NoticeBar.svelte';
  import MacroStoryboard from './components/MacroStoryboard.svelte';
  import ViewToolbar from './components/ViewToolbar.svelte';
  import NavRail from './components/NavRail.svelte';
  import CodeFlowPanel from './components/CodeFlowPanel.svelte';
  import ProcessFlowPanel from './components/ProcessFlowPanel.svelte';
  import ContextAside from './components/ContextAside.svelte';
  import { flowStore } from './stores/flowStore.svelte';

  const viewMode = $derived(flowStore.viewMode);
  const steps = $derived(flowStore.steps);
  const branchSteps = $derived(flowStore.sceneSteps.filter(s => !!s.branch));
  const unknowns = $derived(flowStore.data?.semanticMap?.unknowns || []);

  function boundaryDescription(value: {reason?: string; subject?: string} | string) {
    const reason = typeof value === 'string' ? value : value.reason || '';
    const subject = typeof value === 'string' ? '' : value.subject || '';
    let description = '소스 근거가 부족해 처리 연결을 확인하지 못했습니다.';
    if (reason.includes('outside the selected')) description = '선택한 코드 범위 밖의 호출입니다.';
    else if (reason.includes('multiple canonical')) description = '호출 대상을 하나로 확인하지 못했습니다.';
    else if (reason.includes('closure')) description = '요청한 흐름의 끝까지 연결을 확인하지 못했습니다.';
    return subject ? `${subject} · ${description}` : description;
  }

  function handleConditionChange(event: Event) {
    const target = event.target as HTMLSelectElement;
    flowStore.setConditionFilter(target.value || null);
  }

  function handleQuerySubmit(query: string) {
    const event = new CustomEvent('codeflow:query', { detail: { query } });
    window.dispatchEvent(event);
  }

  function handleFormSubmit(e: SubmitEvent) {
    e.preventDefault();
    const input = document.getElementById('query-input') as HTMLInputElement | null;
    const q = input?.value?.trim();
    if (q) {
      handleQuerySubmit(q);
    }
  }
</script>

<div class="app-container">
  <header class="entry-header">
    <button type="button" onclick={() => window.showFlowHome()}>CodeFlow</button>
    {#if !flowStore.home}<strong>{flowStore.flowTitle}</strong>{/if}
  </header>
  {#if flowStore.home}
    <section class="home" aria-label="FlowView 시작">
      <h1>어떤 코드 흐름을 이해하고 싶나요?</h1>
      <form id="request-form" onsubmit={handleFormSubmit}>
        <label for="query-input">흐름 요청</label>
        <input id="query-input" type="text" placeholder="처리 목적 또는 진입 심볼" required />
        <button id="request-submit" type="submit" disabled={flowStore.busy}>흐름 보기</button>
      </form>
      <p role="status">{flowStore.notice}</p>
      {#if flowStore.busy}<button onclick={() => window.cancelFlowRequest()}>취소</button>{/if}
      {#if flowStore.candidates.length}
        <p>분석할 진입점을 선택하세요.</p>
        {#each flowStore.candidates as candidate}<button onclick={() => window.fetchTaskView('', candidate)}>{candidate}</button>{/each}
      {/if}
      <h2>기존 FlowView</h2>
      {#if flowStore.listError}
        <p role="alert">{flowStore.listError}</p>
        <button onclick={() => window.showFlowHome()}>목록 다시 불러오기</button>
      {:else if !flowStore.views.length && !flowStore.legacyFlows.length}
        <p>아직 분석한 흐름이 없습니다.</p>
        <p>위에서 흐름을 입력하거나, MCP가 연결된 에이전트에게 “이 프로젝트의 원하는 기능을 CodeFlow로 분석하고 FlowView를 열어줘”라고 요청하세요.</p>
      {:else}
        <ul>
          {#each flowStore.views as view (view.viewId)}
            <li><button onclick={() => window.openFlowView(view.viewId)}>{view.title || '저장된 흐름'}{view.savedAt ? ` · ${new Date(view.savedAt).toLocaleString()}` : ''}</button></li>
          {/each}
          {#each flowStore.legacyFlows as flow (flow.flowId)}
            <li><button onclick={() => window.openFlowView(flow.flowId, true)}>{flow.title} · 기존 흐름</button></li>
          {/each}
        </ul>
      {/if}
    </section>
  {:else}
  <!-- Live Notice & Status Controls -->
  <NoticeBar />

  <!-- 2. MACRO CONTEXT STORYBOARD -->
  <MacroStoryboard />

  <!-- View Switch Toolbar -->
  <ViewToolbar />

  <!-- Main 3-Column Workbench -->
  <main class="layout">
    <!-- Left Navigation Rail -->
    <NavRail />

    <!-- Center Viewport -->
    <section class="center" aria-label="선택한 흐름">
      <div class="center-head">
        <h2>{viewMode === 'code' ? '코드 흐름' : '처리 흐름'}</h2>
        <span>{viewMode === 'code' ? '선택 문장 · 조건 · 호출 연결' : '판단 조건 · 상태 변화 · 부수 효과'}</span>
      </div>

      <div class="condition-bar" id="condition-bar">
        <label for="condition-focus">조건 위치 </label>
        <select
          id="condition-focus"
          disabled={!branchSteps.length}
          value={flowStore.conditionFilter || ''}
          onchange={handleConditionChange}
        >
          <option value="">전체 흐름</option>
          {#each branchSteps as step (step.stepId)}
            <option value={step.stepId}>
              {step.name}: {step.branch}
            </option>
          {/each}
        </select>
        <p>조건과 직접 연결된 단계를 강조합니다. 실행 경로를 가정하지 않습니다.</p>
      </div>

      <div class="view-stage">
        {#if viewMode === 'code'}
          <CodeFlowPanel />
        {:else}
          <ProcessFlowPanel />
        {/if}
      </div>

      {#if unknowns.length > 0}
        <div id="boundaries" class="boundaries">
          <details>
            <summary>확인하지 못한 연결 {unknowns.length}건</summary>
            {#each [...new Set(unknowns.map(boundaryDescription))] as description}<p>{description}</p>{/each}
          </details>
        </div>
      {/if}
    </section>

    <!-- Right Aside (with 2. BLAST RADIUS RADAR) -->
    <ContextAside />
  </main>

  {/if}

  <!-- Footer -->
  <footer class="footer">
    <span>소스 근거 기반 · 실행 기록 없음</span>
    <span>정적 FlowView와 통일된 차세대 프론트엔드</span>
  </footer>
</div>

<style>
  .entry-header { display:flex; align-items:center; gap:16px; padding:16px 30px; }
  .home { max-width:850px; margin:40px auto; padding:24px; }
  .home h1 { font-size:24px; margin-bottom:24px; }
  .home h2 { margin-top:32px; }
  .home form { display:flex; flex-wrap:wrap; gap:10px; align-items:center; }
  .home input { flex:1; min-width:180px; padding:10px; border:1px solid #bbb; border-radius:5px; }
  .home li { margin:8px 0; }

  .layout {
    display: grid;
    grid-template-columns: 210px minmax(0, 1fr) 275px;
    margin: 0 30px;
    border-top: 1px solid var(--line, #dddddd);
    gap: 24px;
    align-items: start;
  }
  .center {
    min-width: 0;
    border-left: 1px solid var(--line, #dddddd);
    border-right: 1px solid var(--line, #dddddd);
    padding: 19px 24px 28px;
  }
  .center-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    margin-bottom: 17px;
  }
  .center-head h2 {
    font-size: 14px;
    font-weight: 700;
  }
  .center-head span {
    font-size: 10px;
    color: var(--muted, #666666);
  }
  .condition-bar {
    margin-bottom: 15px;
    font-size: 11px;
  }
  .condition-bar label {
    font-weight: 600;
  }
  .condition-bar select {
    font: inherit;
    background: #ffffff;
    color: #171717;
    border: 1px solid #bbbbbb;
    padding: 6px;
    max-width: 100%;
    border-radius: 4px;
  }
  .condition-bar p {
    font-size: 10px;
    color: var(--muted, #666666);
    margin-top: 5px;
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
</style>
