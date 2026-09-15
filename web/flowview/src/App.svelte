<script lang="ts">
  import HeaderBar from './components/HeaderBar.svelte';
  import IntroSection from './components/IntroSection.svelte';
  import NoticeBar from './components/NoticeBar.svelte';
  import ChangePulse from './components/ChangePulse.svelte';
  import MacroStoryboard from './components/MacroStoryboard.svelte';
  import ViewToolbar from './components/ViewToolbar.svelte';
  import NavRail from './components/NavRail.svelte';
  import CodeFlowPanel from './components/CodeFlowPanel.svelte';
  import ProcessFlowPanel from './components/ProcessFlowPanel.svelte';
  import ContextAside from './components/ContextAside.svelte';
  import { flowStore } from './stores/flowStore.svelte';

  const viewMode = $derived(flowStore.viewMode);
  const steps = $derived(flowStore.steps);
  const hasData = $derived(steps.length > 0);
  const branchSteps = $derived(steps.filter(s => !!s.branch));
  const unknowns = $derived(flowStore.data?.semanticMap?.unknowns || []);

  function handleConditionChange(event: Event) {
    const target = event.target as HTMLSelectElement;
    flowStore.setConditionFilter(target.value || null);
  }

  function handleQuerySubmit(query: string) {
    // In live SSE or standalone mode, dispatched to main.ts listener via window custom event or query fn
    const event = new CustomEvent('codeflow:query', { detail: { query } });
    window.dispatchEvent(event);
  }
</script>

<div class="app-container">
  <!-- Top Navigation Bar -->
  <HeaderBar />

  <!-- Intro & User Query Form -->
  <IntroSection onSubmitQuery={handleQuerySubmit} />

  <!-- Live Notice & Status Controls -->
  <NoticeBar />

  <!-- Change Pulse (Verified Semantic Changes) -->
  <ChangePulse />

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
          <strong>확인하지 못한 처리</strong>
          {#each unknowns as u}
            <p>{typeof u === 'string' ? u : u.reason || u.subject || '처리 경계 미확인'}</p>
          {/each}
        </div>
      {/if}
    </section>

    <!-- Right Aside (with 2. BLAST RADIUS RADAR) -->
    <ContextAside />
  </main>

  <!-- Footer -->
  <footer class="footer">
    <span>소스 근거 기반 · 실행 기록 없음</span>
    <span>정적 FlowView와 통일된 차세대 프론트엔드</span>
  </footer>
</div>

<style>
  .layout {
    display: grid;
    grid-template-columns: 210px minmax(0, 1fr) 260px;
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
      grid-template-columns: 220px minmax(0, 1fr) 275px;
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
