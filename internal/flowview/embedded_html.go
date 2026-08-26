package flowview

// IndexHTML is the embedded single-page FlowView — flow-first review surface:
// business flow explanations, 5-lane architecture map, execution timeline,
// symbol-scoped code evidence, causal impact and honest unknowns.
const IndexHTML = `<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>CodeFlow — FlowView</title>
  <style>
    :root{color-scheme:light;--ink:#111;--muted:#666;--line:#d0d0cc;--soft:#f4f4f2;--paper:#fff;--warn:#fff9db;--accent:#222;--active-bg:#1a1a1a;--focus-ring:#444}
    *{box-sizing:border-box}html{scroll-behavior:smooth}
    body{margin:0;background:var(--paper);color:var(--ink);font:14px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif}
    button,a,input{font:inherit}button{color:inherit}
    button:focus-visible,a:focus-visible,input:focus-visible{outline:2px solid var(--focus-ring);outline-offset:2px}
    code,pre,.mono{font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace}
    .shell{width:min(1360px,100%);margin:auto;padding:24px 28px 80px}
    
    /* ---- Header & Flow Context ---- */
    .top-header{padding-bottom:16px;border-bottom:3px solid var(--ink)}
    .brand-line{display:flex;align-items:center;flex-wrap:wrap;gap:8px;margin-bottom:8px}
    .brand-eyebrow{font-size:11px;font-weight:900;letter-spacing:.14em;color:var(--ink)}
    .flow-title{margin:4px 0 8px;font-size:clamp(22px,3.2vw,32px);font-weight:800;letter-spacing:-.03em;line-height:1.2}
    .flow-desc{margin:0 0 12px;color:var(--muted);font-size:14px;line-height:1.6;max-width:920px}
    .crumb-line{display:flex;align-items:center;flex-wrap:wrap;gap:8px 12px;padding-top:8px;border-top:1px solid var(--line);font-size:12px;color:var(--muted)}
    .crumb-label{font-weight:700;color:var(--ink);font-size:11px;text-transform:uppercase;letter-spacing:.05em}
    .crumb-line code{font-size:12px;color:var(--ink);background:var(--soft);padding:2px 6px;border-radius:4px}
    .crumb-line .sep{color:var(--line)}
    .crumb-line .spacer{margin-left:auto;display:flex;gap:8px;align-items:center}
    
    .badge{display:inline-flex;align-items:center;min-height:22px;padding:1px 8px;border:1px solid var(--ink);border-radius:999px;font-size:11px;font-weight:800}
    .warn-badge{background:var(--warn);border-color:var(--ink);color:var(--ink)}
    .queue-banner{margin:14px 0 0;padding:10px 14px;border-left:4px solid var(--ink);background:var(--warn);display:flex;justify-content:space-between;gap:10px;align-items:center;border-radius:0 6px 6px 0}
    
    /* ---- Flow Tabs ---- */
    .flow-tabs-section{margin:16px 0 0}
    .section-subhead{display:flex;align-items:baseline;justify-content:space-between;margin-bottom:8px}
    .subhead-title{font-size:12px;font-weight:800;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}
    .flow-tabs{display:flex;gap:10px;overflow-x:auto;padding-bottom:8px;scrollbar-width:thin;overscroll-behavior-x:contain}
    .flow-tab{flex:0 0 auto;width:260px;text-align:left;padding:12px 14px;border:1px solid var(--line);border-radius:9px;background:var(--paper);cursor:pointer;transition:border-color .15s,box-shadow .15s}
    .flow-tab:hover:not(.active){border-color:var(--ink);background:#fafafa}
    .flow-tab.active{background:var(--ink);color:var(--paper);border-color:var(--ink);box-shadow:3px 3px 0 var(--line)}
    .flow-tab .tab-title{font-size:13px;font-weight:800;line-height:1.3;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
    .flow-tab .tab-desc{margin-top:4px;font-size:11px;line-height:1.45;color:var(--muted);display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;height:32px}
    .flow-tab.active .tab-desc{color:#bbb}
    .flow-tab .tab-meta{margin-top:8px;display:flex;gap:6px;align-items:center;font-size:10px;color:var(--muted)}
    .flow-tab.active .tab-meta{color:#aaa}
    .flow-tab .tab-meta .badge{font-size:10px;min-height:18px;padding:0 6px}
    .flow-tab.active .tab-meta .badge{border-color:#fff;color:#fff}
    
    /* ---- 5-Lane Architecture Map ---- */
    .map-panel{margin:20px 0 0;border:1px solid var(--ink);border-radius:10px;overflow:hidden}
    .map-head{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:12px;padding:12px 16px;border-bottom:1px solid var(--ink);background:var(--soft)}
    .map-head h2{margin:0;font-size:15px;font-weight:800}
    .map-head p{margin:2px 0 0;color:var(--muted);font-size:12px}
    .legend{display:flex;align-items:center;flex-wrap:wrap;gap:8px 14px;color:var(--muted);font-size:11px;font-weight:700}
    .legend .status::before{content:"○";margin-right:4px}
    .legend .status[data-status="fresh"]::before{content:"●"}
    .legend .status[data-status="stale"]::before{content:"◐"}
    .legend .status[data-status="orphaned"]::before{content:"?";font-weight:900}
    .map-scroll{overflow-x:auto;overscroll-behavior-x:contain;background:var(--paper)}
    .lane{display:grid;grid-template-columns:160px minmax(max-content,1fr);min-width:max-content;border-bottom:1px solid var(--line)}
    .lane:last-child{border-bottom:0}
    .lane-label{position:sticky;left:0;z-index:2;display:grid;align-content:center;width:160px;padding:12px 14px;border-right:1px solid var(--ink);background:var(--paper);color:var(--muted);font-size:11px;font-weight:800}
    .lane-track{display:grid;grid-template-columns:repeat(var(--cols),minmax(152px,1fr));gap:8px;min-width:calc(var(--cols) * 160px);padding:10px;background-image:linear-gradient(to right,transparent calc(100% - 1px),#ebebea 0);background-size:160px 100%}
    .node{display:grid;align-content:start;gap:3px;min-height:62px;padding:8px 10px;border:1px solid var(--ink);border-radius:7px;background:var(--paper);text-align:left;cursor:pointer;font-size:11px;transition:transform .1s,box-shadow .1s}
    .node:hover:not([aria-pressed="true"]){transform:translateY(-1px);box-shadow:2px 2px 0 var(--line)}
    .node strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:11px}
    .node small{color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:10px}
    .node[aria-pressed="true"]{background:var(--ink);color:var(--paper);box-shadow:3px 3px 0 var(--line)}
    .node[aria-pressed="true"] small{color:#ccc}
    .node[data-status="stale"]{border-style:dashed}
    .node[data-status="orphaned"]{border-style:dashed;color:var(--muted)}
    
    /* ---- Workbench Layout (Timeline + Detail) ---- */
    .workbench{display:grid;grid-template-columns:360px minmax(0,1fr);gap:24px;margin-top:24px;align-items:start}
    
    /* ---- Execution Timeline (Left Pane) ---- */
    .timeline-pane{border:1px solid var(--line);border-radius:10px;padding:16px;background:var(--soft)}
    .timeline-head{display:flex;align-items:flex-start;justify-content:space-between;gap:8px;padding-bottom:12px;margin-bottom:12px;border-bottom:1px solid var(--line)}
    .timeline-head h2{margin:0;font-size:15px;font-weight:800}
    .timeline-sub{margin:2px 0 0;font-size:11px;color:var(--muted)}
    .timeline-controls{display:flex;flex-direction:column;align-items:flex-end;gap:4px}
    .timeline-note{font-size:11px;color:var(--muted);font-weight:700}
    .timeline-list{list-style:none;margin:0;padding:0;display:grid;gap:8px}
    
    .timeline-item{position:relative;width:100%;text-align:left;padding:10px 12px;border:1px solid var(--line);border-radius:8px;background:var(--paper);cursor:pointer;display:grid;grid-template-columns:30px minmax(0,1fr);gap:10px;align-items:start;transition:border-color .15s,box-shadow .15s}
    .timeline-item:hover:not([aria-current="step"]){border-color:var(--ink)}
    .timeline-item[aria-current="step"]{border-color:var(--ink);background:var(--paper);box-shadow:3px 3px 0 var(--ink)}
    .timeline-num{display:grid;place-items:center;width:26px;height:26px;border:1px solid var(--ink);border-radius:50%;background:var(--paper);font:800 10px ui-monospace,monospace;flex:0 0 auto}
    .timeline-item[aria-current="step"] .timeline-num{background:var(--ink);color:var(--paper)}
    .timeline-body{min-width:0}
    .timeline-tags{display:flex;align-items:center;flex-wrap:wrap;gap:4px;margin-bottom:4px}
    .timeline-tag{font-size:9px;font-weight:800;text-transform:uppercase;padding:1px 5px;border-radius:4px;border:1px solid var(--line);color:var(--muted)}
    .timeline-tag.layer-tag{border-color:var(--ink);color:var(--ink)}
    .timeline-tag.kind-tag{background:var(--soft)}
    .timeline-title{font-size:12px;font-weight:800;line-height:1.3;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--ink)}
    .timeline-summary{margin-top:3px;font-size:11px;color:var(--muted);line-height:1.4;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
    
    /* ---- Detail Pane (Right Pane) ---- */
    .detail-pane{min-width:0}
    .detail-nav{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}
    .nav-pos{min-width:0}
    .pos-badge{font-size:11px;font-weight:800;color:var(--muted);text-transform:uppercase;letter-spacing:.05em}
    .pos-title{margin:2px 0 0;font-size:18px;font-weight:800;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
    .nav-actions{display:flex;gap:6px;flex:0 0 auto}
    
    .btn{min-height:32px;padding:5px 12px;border:1px solid var(--ink);border-radius:6px;background:var(--paper);cursor:pointer;font-size:12px;font-weight:700}
    .btn:hover:not(:disabled){background:var(--ink);color:var(--paper)}
    .btn:disabled{border-color:var(--line);color:var(--line);cursor:default}
    .btn-sm{font-size:11px;padding:3px 8px;border:1px solid var(--line);border-radius:5px;background:var(--paper);cursor:pointer}
    .btn-sm:hover{border-color:var(--ink)}
    .btn-primary{background:var(--ink);color:var(--paper)}
    
    /* ---- Card ---- */
    .card{border:1px solid var(--ink);border-radius:10px;padding:18px;box-shadow:5px 5px 0 var(--soft);background:var(--paper)}
    .card-chips{display:flex;align-items:center;flex-wrap:wrap;gap:6px;margin-bottom:14px}
    
    .impact{display:grid;grid-template-columns:minmax(0,1fr) 26px minmax(0,1fr) 26px minmax(0,1fr);margin-bottom:16px;border:1px solid var(--ink);border-radius:8px;overflow:hidden}
    .impact .cell{display:grid;align-content:center;gap:3px;min-height:68px;padding:10px 14px;background:var(--paper)}
    .impact .cell small{color:var(--muted);font-size:10px;font-weight:800;letter-spacing:.08em}
    .impact .cell[data-kind="state"][data-changed="true"]{background:var(--ink);color:var(--paper)}
    .impact .cell[data-kind="state"][data-changed="true"] small{color:#ccc}
    .impact .arr{display:grid;place-items:center;border-inline:1px solid var(--ink);font-size:16px;background:var(--soft)}
    
    .chain-box{display:flex;align-items:center;flex-wrap:wrap;gap:6px;margin-bottom:14px}
    .pill{display:inline-flex;align-items:center;gap:6px;padding:4px 10px;border:1px solid var(--line);border-radius:999px;background:var(--paper);font-size:11px}
    
    /* ---- Code Panel ---- */
    .code-panel{min-width:0;border:1px solid var(--line);border-radius:8px;overflow:hidden;margin-top:14px}
    .code-toolbar{display:flex;align-items:center;flex-wrap:wrap;gap:8px 12px;padding:8px 12px;border-bottom:1px solid var(--line);background:var(--soft);font-size:11px}
    .code-toolbar .path{min-width:0;overflow-wrap:anywhere;color:var(--muted)}
    .code-toolbar .path b{color:var(--ink)}
    .code-toolbar .modes{display:flex;gap:4px;margin-left:auto}
    .code-toolbar .modes button{padding:3px 8px;border:1px solid var(--line);border-radius:5px;background:var(--paper);cursor:pointer;font-size:10px}
    .code-toolbar .modes button[aria-pressed="true"]{border-color:var(--ink);background:var(--ink);color:var(--paper)}
    .code-note{padding:6px 12px;border-bottom:1px solid var(--line);background:var(--warn);font-size:11px}
    .code-wrap{max-height:560px;overflow:auto;background:#fafafa}
    .code{padding:8px 0;font:13px/1.65 ui-monospace,monospace}
    .line{display:grid;grid-template-columns:46px 26px minmax(max-content,1fr)}
    .num{color:#999;text-align:right;padding-right:10px;border-right:1px solid #e0e0e0;user-select:none}
    .gut{position:relative}
    .gut .marker{position:absolute;left:4px;top:50%;transform:translateY(-50%);display:grid;place-items:center;width:17px;height:17px;border:1px solid var(--ink);border-radius:50%;background:var(--paper);font:800 9px ui-monospace,monospace;cursor:pointer;padding:0}
    .gut .marker[aria-pressed="true"]{background:var(--ink);color:var(--paper)}
    .src{padding:0 12px;white-space:pre}
    .line.hit{background:#e9e9e4;box-shadow:inset 3px 0 var(--ink)}
    .line.peer{background:#f3f3f0}
    .evidence{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px;padding:8px 12px;border-top:1px solid var(--line);font-size:11px;color:var(--muted)}
    .vscode{display:inline-flex;padding:4px 9px;border:1px solid var(--ink);border-radius:5px;background:var(--ink);color:var(--paper);text-decoration:none;font-weight:800;font-size:11px}
    .vscode:hover{background:var(--paper);color:var(--ink)}
    
    .approval-bar{margin-top:14px}
    .edit-form{margin-top:10px;border:1px solid var(--ink);padding:12px;border-radius:8px;display:grid;gap:8px}
    .input-text{width:100%;border:1px solid var(--line);padding:7px 10px;border-radius:6px}
    .input-text:focus{border-color:var(--ink)}
    
    /* ---- Unknowns Panel ---- */
    .unknowns-panel{margin-top:24px;border:1px solid var(--ink);border-radius:10px;padding:16px 18px;background:var(--paper)}
    .unknowns-panel h2{margin:0 0 6px;font-size:15px;font-weight:800}
    .unknowns-panel p{margin:0 0 12px;color:var(--muted);font-size:12px}
    .unknowns-panel ul{margin:0;padding-left:18px}
    .unknowns-panel li+li{margin-top:8px}
    .unknowns-panel .why{display:block;color:var(--muted);font-size:12px}
    
    /* ---- Modal ---- */
    .modal{position:fixed;inset:0;background:rgba(0,0,0,0.4);display:none;align-items:center;justify-content:center;z-index:100}
    .modal-box{background:var(--paper);border:1px solid var(--ink);border-radius:10px;width:520px;max-width:92vw;padding:18px;box-shadow:0 8px 30px rgba(0,0,0,0.15)}
    [hidden]{display:none!important}
    
    @media(max-width:960px){
      .workbench{grid-template-columns:1fr}
      .impact{grid-template-columns:minmax(0,1fr)}
      .impact .arr{min-height:24px;border-inline:0;border-block:1px solid var(--ink);transform:rotate(90deg)}
      .lane{grid-template-columns:110px minmax(max-content,1fr)}
      .lane-label{width:110px}
    }
  </style>
</head>
<body>
<main class="shell" id="flowview">
  <!-- 1. Header & Business Flow Explanation (Above Map) -->
  <header class="top-header">
    <div class="brand-line">
      <span class="brand-eyebrow">CODEFLOW · FLOWVIEW</span>
      <span class="badge" id="flow-badge">—</span>
      <span id="truncated-chip" class="badge warn-badge" style="display:none" title="조건이 복잡하거나 호출 깊이가 깊어 일부 구간만 추적했습니다.">일부 구간만 추적됨</span>
    </div>
    <h1 id="flow-title" class="flow-title">흐름을 불러오는 중…</h1>
    <p id="flow-desc" class="flow-desc"></p>
    
    <div class="crumb-line" data-region="breadcrumb">
      <span class="crumb-label">진입 심볼</span>
      <code id="bc-entry">—</code>
      <span class="sep">·</span>
      <span class="crumb-label">파일</span>
      <code id="bc-file">—</code>
      <span class="sep">·</span>
      <span class="crumb-label">흐름 ID</span>
      <code id="bc-flow">—</code>
      <span class="sep">·</span>
      <span class="crumb-label">전체 단계</span>
      <span id="flow-basis" style="font-weight:700;color:var(--ink)">0단계</span>
      <span class="spacer"><span id="snapshot-status"></span></span>
    </div>
  </header>

  <div id="queue-banner" class="queue-banner" style="display:none">
    <span><b>승인 큐:</b> <span id="queue-count">0</span>개 단계 재승인 필요</span>
    <button class="btn" onclick="scrollToFirstStale()">검토</button>
  </div>

  <!-- Business Flow Tabs (비즈니스 흐름 전환 및 설명) -->
  <section class="flow-tabs-section" id="flow-tabs-section" aria-label="비즈니스 흐름 목록">
    <div class="section-subhead">
      <span class="subhead-title">비즈니스 흐름 목록</span>
      <span class="subhead-count" id="flows-count" style="font-size:11px;color:var(--muted)"></span>
    </div>
    <nav class="flow-tabs" id="flow-tabs"></nav>
  </section>

  <!-- 2. 5-Lane Architecture Map -->
  <section class="map-panel" data-region="map" aria-label="5레인 아키텍처 맵">
    <div class="map-head">
      <div>
        <h2>Architecture Map (5-Lane)</h2>
        <p>비즈니스 흐름이 화면에서 외부 연동까지 전달되는 5계층 구조입니다.</p>
      </div>
      <div class="legend" aria-label="신뢰 범례">
        <span class="status" data-status="fresh">확인됨</span>
        <span class="status" data-status="stale">재확인 필요</span>
        <span class="status" data-status="orphaned">찾을 수 없음</span>
      </div>
    </div>
    <div class="map-scroll" id="map-scroll"><div id="map-lanes"></div></div>
  </section>

  <!-- 3. Business Flow Timeline & Workbench -->
  <section class="workbench" aria-label="비즈니스 흐름 실행 타임라인 및 상세 근거">
    <!-- 좌측: 비즈니스 흐름 실행 타임라인 -->
    <aside class="timeline-pane">
      <div class="timeline-head">
        <div>
          <h2>실행 타임라인</h2>
          <p class="timeline-sub">비즈니스 흐름의 순차적 실행 단계</p>
        </div>
        <div class="timeline-controls">
          <span class="timeline-note" id="timeline-note"></span>
          <button class="btn-sm" id="timeline-toggle" onclick="toggleTimelineFilter()" style="display:none">전체 보기</button>
        </div>
      </div>
      <ol class="timeline-list" id="timeline-list"></ol>
    </aside>

    <!-- 우측: 선택된 단계의 상세 증거 및 코드 -->
    <section class="detail-pane" data-region="detail">
      <div class="detail-nav">
        <div class="nav-pos">
          <span class="pos-badge" id="position">—</span>
          <h3 id="detail-title" class="pos-title">—</h3>
        </div>
        <div class="nav-actions">
          <button class="btn" id="prev" onclick="prevStep()">← 이전</button>
          <button class="btn" id="next" onclick="nextStep()">다음 →</button>
          <button class="btn" onclick="openSwitcher()">⌘K</button>
        </div>
      </div>

      <!-- Detail Card -->
      <article class="card" aria-live="polite">
        <div class="card-chips" id="detail-chips"></div>

        <!-- Causal Impact Box -->
        <div class="impact" aria-label="코드에서 상태와 화면 결과까지의 영향">
          <div class="cell" data-kind="code">
            <small>CODE Δ (실행 동작)</small>
            <strong id="impact-code">—</strong>
          </div>
          <div class="arr" aria-hidden="true">→</div>
          <div class="cell" data-kind="state" id="impact-state-cell">
            <small>STATE Δ (상태 변화)</small>
            <strong id="impact-state">—</strong>
          </div>
          <div class="arr" aria-hidden="true">→</div>
          <div class="cell" data-kind="result">
            <small>VISIBLE RESULT (결과)</small>
            <strong id="impact-result">—</strong>
          </div>
        </div>

        <!-- Business Rules Row -->
        <div id="rules-row" class="chain-box" style="display:none"></div>

        <!-- Cross-layer Delegation Edges Row -->
        <div id="edges-row" class="chain-box" style="display:none"></div>

        <!-- Code Panel -->
        <section class="code-panel" aria-label="선택 단계 코드 근거">
          <div class="code-toolbar">
            <span class="path" id="code-path">—</span>
            <div class="modes" role="group" aria-label="코드 뷰 범위">
              <button id="mode-symbol" aria-pressed="true" onclick="setViewMode('symbol')">함수 단위</button>
              <button id="mode-focus" aria-pressed="false" onclick="setViewMode('focus')">단계 근거만</button>
            </div>
          </div>
          <div class="code-note" id="code-note" hidden>주변 코드 · 심볼 범위 미확정 — 문장 주변 근거만 표시합니다.</div>
          <div class="code-wrap"><div class="code" id="code">—</div></div>
          <div class="evidence">
            <span class="mono" id="code-range">—</span>
            <a class="vscode" id="vscode-link" href="#">↗ VS Code에서 열기</a>
          </div>
        </section>

        <!-- Inline Approval -->
        <div class="approval-bar">
          <button class="btn" onclick="toggleEdit()">인라인 승인</button>
        </div>
        <div id="edit-form" style="display:none" class="edit-form">
          <input id="edit-name" class="input-text" placeholder="단계 이름">
          <input id="edit-rules" class="input-text" placeholder="비즈니스 규칙 (쉼표 구분)">
          <button class="btn btn-primary" onclick="submitApproval()">승인 완료</button>
        </div>
      </article>
    </section>
  </section>

  <!-- Unknowns Panel -->
  <section class="unknowns-panel" id="unknowns-panel" hidden aria-label="아직 타임라인에 연결되지 않은 동작">
    <h2>아직 타임라인에 연결되지 않은 동작</h2>
    <p>현재 코드에서 확인된 내용과 빠진 연결을 함께 표시합니다.</p>
    <ul id="unknowns-list"></ul>
  </section>
</main>

<div id="switcher-modal" class="modal" onclick="if(event.target===this)closeSwitcher()">
  <div class="modal-box">
    <div style="font-weight:800;font-size:15px;margin-bottom:10px">비즈니스 흐름 전환 (⌘K)</div>
    <input id="switcher-input" class="input-text" placeholder="흐름 검색 (제목, 심볼)..." oninput="filterFlows()">
    <div id="switcher-list" style="max-height:320px;overflow:auto;margin-top:10px;display:grid;gap:6px"></div>
  </div>
</div>

<script>
const params=new URLSearchParams(location.search),token=params.get('token')||'';
let currentFlowId=params.get('flow')||'',cachedFlows=[],currentSpec=null,selected=0,viewMode='symbol';
let showAllTimeline=false;

async function api(path,opts={}){
  const u=new URL(path,location.origin);
  if(token)u.searchParams.set('token',token);
  const h=Object.assign({},opts.headers);
  if(token)h['X-CodeFlow-Token']=token;
  const r=await fetch(u.toString(),Object.assign({},opts,{headers:h}));
  if(!r.ok)throw new Error(r.status+' '+r.statusText);
  return r;
}

function esc(s){
  return (s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

const LAYER_ORDER=['ui','application','state','data','external'];
const LAYER_LABELS={ui:'화면(UI)',application:'흐름 제어(Application)',state:'상태(State)',data:'데이터(Data)',external:'외부 연동(External)'};
const KIND_LABELS={guard:'조건 확인',mutation:'상태 변경',call:'기능 실행',branch:'흐름 분기'};
const EDGE_LABELS={resolved_cross_file:'내부 위임',boundary_call:'외부 연동'};
const FRESH_LABEL={fresh:'확인됨',stale:'재확인 필요',orphaned:'찾을 수 없음'};

function isCoreStep(st){
  if(!st)return false;
  if(st.kind==='mutation'||st.kind==='call')return true;
  if(st.stateDelta||st.sideEffect)return true;
  return false;
}

function layerLabelShort(st){
  if(!st||!st.layer)return'';
  const lane=(currentSpec.lanes||[]).find(l=>l.id===st.layer);
  if(lane&&lane.label)return lane.label.split('(')[0];
  return (LAYER_LABELS[st.layer]||st.layer).split('(')[0];
}

function nodeSub(st){
  if(st.stateDelta)return st.stateDelta.before+' → '+st.stateDelta.after;
  if(st.sideEffect)return '외부 연동 · '+st.sideEffect;
  if(st.branch)return '분기 · '+st.branch;
  return '변경 없음';
}

function stepSummary(st){
  if(st.stateDelta)return '상태 변경: '+st.stateDelta.before+' → '+st.stateDelta.after;
  if(st.sideEffect)return '외부 연동: '+st.sideEffect;
  if(st.branch)return '조건 분기: '+st.branch;
  if(st.rules&&st.rules.length)return '규칙: '+st.rules.join(', ');
  return st.anchor?st.anchor.repoRelativePath.split('/').pop():'';
}

function lens(st){return st.codeLens||{};}
function focusStart(st){return lens(st).startLine||1;}
function focusEnd(st){return lens(st).endLine||focusStart(st);}
function viewRange(st){
  const l=lens(st);
  if(l.viewStartLine&&l.viewEndLine)return{start:l.viewStartLine,end:l.viewEndLine,known:true};
  const s=focusStart(st),e=focusEnd(st);
  return{start:Math.max(1,s-12),end:e+12,known:false};
}

async function init(){
  try{
    const r=await api('/api/flows');
    const d=await r.json();
    cachedFlows=d.flows||[];
    if(!cachedFlows.length){
      document.getElementById('flow-title').textContent='발행된 비즈니스 흐름 없음';
      document.getElementById('flow-desc').textContent='codeflow publish를 실행하여 비즈니스 흐름을 발행하세요.';
      return;
    }
    if(!currentFlowId||!cachedFlows.some(f=>f.flowId===currentFlowId)){
      currentFlowId=cachedFlows[0].flowId;
    }
    await loadFlow(currentFlowId);
  }catch(e){
    document.getElementById('flow-title').textContent='오류: '+e.message;
  }
}

async function loadFlow(id){
  currentFlowId=id;
  try{
    const r=await api('/api/flow?id='+encodeURIComponent(id));
    currentSpec=await r.json();
    selected=0;
    viewMode='symbol';
    renderAll();
  }catch(e){
    document.getElementById('flow-title').textContent='흐름 로드 오류: '+e.message;
  }
}

function renderAll(){
  renderHeader();
  renderStale();
  renderFlowTabs();
  renderMap();
  renderTimeline();
  selectStep(nearestCoreIndex(selected),false);
}

function coreIndices(){
  if(!currentSpec)return [];
  const a=[];
  currentSpec.steps.forEach((st,i)=>{
    if(showAllTimeline||isCoreStep(st))a.push(i);
  });
  return a;
}

function nearestCoreIndex(i){
  const c=coreIndices();
  if(!c.length)return Math.max(0,Math.min((currentSpec.steps.length-1),i));
  if(c.includes(i))return i;
  let best=c[0],d=Math.abs(c[0]-i);
  for(const x of c){
    const nd=Math.abs(x-i);
    if(nd<d){d=nd;best=x;}
  }
  return best;
}

function renderHeader(){
  const s=currentSpec;
  if(!s)return;
  const idx=cachedFlows.find(x=>x.flowId===s.flowId)||{};
  const entry=s.entrySymbolPath||idx.entrySymbolPath||'';
  
  document.getElementById('flow-title').textContent=s.title||idx.title||'(제목 없는 흐름)';
  const desc=s.description||idx.description||'';
  const de=document.getElementById('flow-desc');
  if(desc){
    de.textContent=desc;
    de.style.display='block';
  }else{
    de.textContent='비즈니스 흐름의 코드 증거와 계층 간 위임 관계를 확인합니다.';
    de.style.display='block';
  }
  
  document.getElementById('truncated-chip').style.display=s.truncated?'inline-flex':'none';
  const hash=entry.indexOf('#');
  document.getElementById('bc-entry').textContent=hash>=0?entry.slice(hash+1):entry;
  document.getElementById('bc-file').textContent=hash>=0?entry.slice(0,hash):'—';
  document.getElementById('bc-flow').textContent=s.flowId.slice(0,16)+'@'+s.basisSha.slice(0,8);
  document.getElementById('flow-basis').textContent=s.steps.length+'단계';
  document.getElementById('flow-badge').textContent=s.flowId.slice(0,12);
  
  const hasStale=s.steps.some(x=>x.freshness==='stale');
  document.getElementById('snapshot-status').innerHTML=hasStale?'<span class="badge warn-badge">재승인 필요</span>':'<span class="badge">검증 완료</span>';
}

function renderStale(){
  let stale=0;
  currentSpec.steps.forEach(x=>{if(x.freshness==='stale')stale++;});
  document.getElementById('queue-banner').style.display=stale?'flex':'none';
  document.getElementById('queue-count').textContent=stale;
}

function renderFlowTabs(){
  const el=document.getElementById('flow-tabs');
  const countEl=document.getElementById('flows-count');
  if(!el)return;
  if(countEl)countEl.textContent='총 '+cachedFlows.length+'개 흐름';
  if(cachedFlows.length<=1){
    document.getElementById('flow-tabs-section').style.display='none';
    return;
  }
  document.getElementById('flow-tabs-section').style.display='block';
  
  el.innerHTML=cachedFlows.map(f=>{
    const active=f.flowId===currentFlowId;
    const desc=esc(f.description||'');
    const entryShort=esc((f.entrySymbolPath||'').split('#').pop()||'');
    return '<button class="flow-tab'+(active?' active':'')+'" onclick="loadFlow(\''+esc(f.flowId)+'\')" aria-current="'+(active?'true':'false')+'">'+
      '<div class="tab-title">'+esc(f.title)+'</div>'+
      (desc?'<div class="tab-desc">'+desc+'</div>':'<div class="tab-desc" style="color:var(--muted)">'+entryShort+'</div>')+
      '<div class="tab-meta"><span>'+f.stepCount+'단계</span><span style="margin-left:auto" class="badge">'+esc(f.flowId.slice(0,8))+'</span></div>'+
    '</button>';
  }).join('');
}

function renderMap(){
  const lanes=(Array.isArray(currentSpec.lanes)&&currentSpec.lanes.length?currentSpec.lanes:LAYER_ORDER.filter(l=>currentSpec.steps.some(st=>(st.layer||'application')===l)).map(l=>({id:l,label:LAYER_LABELS[l]})));
  document.getElementById('map-lanes').innerHTML=lanes.map(l=>{
    const inLayer=currentSpec.steps.map((st,i)=>({st,i})).filter(x=>(x.st.layer||'application')===l.id);
    return '<div class="lane"><div class="lane-label">'+esc(l.label)+'</div><div class="lane-track" style="--cols:'+currentSpec.steps.length+'">'+
      inLayer.map(x=>'<button class="node" style="grid-column:'+(x.i+1)+'" data-map-step="'+x.i+'" data-status="'+esc(x.st.freshness)+'" aria-pressed="false"><strong>'+esc(x.st.name)+'</strong><small>'+esc(nodeSub(x.st))+'</small></button>').join('')+
    '</div></div>';
  }).join('');
  document.querySelectorAll('[data-map-step]').forEach(n=>n.addEventListener('click',()=>selectStep(Number(n.dataset.mapStep))));
}

function renderTimeline(){
  const core=coreIndices();
  const note=document.getElementById('timeline-note');
  const tog=document.getElementById('timeline-toggle');
  const list=document.getElementById('timeline-list');
  if(!currentSpec.steps.length){
    list.innerHTML='';
    if(note)note.textContent='';
    if(tog)tog.style.display='none';
    return;
  }
  const total=currentSpec.steps.length,shown=core.length;
  const filtered=shown<total&&!showAllTimeline;
  if(note)note.textContent=filtered?'핵심 '+shown+'개 · 전체 '+total+'개':total+'개 단계'+(showAllTimeline&&shown<total?' (전체)':'');
  if(tog){
    if(total>shown){
      tog.style.display='inline-block';
      tog.textContent=showAllTimeline?'핵심만 보기':'전체 보기';
    }else tog.style.display='none';
  }
  
  const idxs=filtered?core:currentSpec.steps.map((_,i)=>i);
  list.innerHTML=idxs.map(i=>{
    const st=currentSpec.steps[i];
    const lyr=layerLabelShort(st);
    const kindText=st.kind&&KIND_LABELS[st.kind];
    const fresh=FRESH_LABEL[st.freshness]||st.freshness;
    return '<li class="timeline-item" data-hstep="'+i+'" data-status="'+esc(st.freshness)+'" aria-current="'+(i===selected?'step':'false')+'">'+
      '<div class="timeline-num">'+String(i+1).padStart(2,'0')+'</div>'+
      '<div class="timeline-body">'+
        '<div class="timeline-tags">'+
          (lyr?'<span class="timeline-tag layer-tag">'+esc(lyr)+'</span>':'')+
          (kindText?'<span class="timeline-tag kind-tag">'+esc(kindText)+'</span>':'')+
          '<span class="timeline-tag">'+esc(fresh)+'</span>'+
        '</div>'+
        '<div class="timeline-title">'+esc(st.name)+'</div>'+
        '<div class="timeline-summary">'+esc(stepSummary(st))+'</div>'+
      '</div>'+
    '</li>';
  }).join('');
  
  document.querySelectorAll('[data-hstep]').forEach(n=>n.addEventListener('click',()=>selectStep(Number(n.dataset.hstep))));
}

function toggleTimelineFilter(){
  showAllTimeline=!showAllTimeline;
  renderTimeline();
  const c=coreIndices();
  if(!c.includes(selected))selectStep(c[0]||0,false);
  else selectStep(selected,false);
}

function trustChip(st){
  const detail=(st.provenance||'')+' · 신뢰도 '+Math.round((st.confidence||0)*100)+'%';
  return '<span class="badge" title="'+esc(detail)+'">'+esc(FRESH_LABEL[st.freshness]||st.freshness)+'</span>';
}

function changeChip(st){
  if(st.stateDelta)return '<span class="badge" style="background:var(--ink);color:var(--paper)">상태 변경</span>';
  if(st.sideEffect)return '<span class="badge">외부 연동</span>';
  if(st.branch)return '<span class="badge">조건 분기</span>';
  return '<span class="badge" style="border-color:var(--line);color:var(--muted)">변경 없음</span>';
}

function layerChip(st){
  const lyr=layerLabelShort(st);
  return lyr?'<span class="badge" style="border-color:var(--ink);font-weight:800">'+esc(lyr)+'</span>':'';
}

function renderDetail(){
  const st=currentSpec.steps[selected];
  if(!st)return;
  const c=coreIndices();
  const pos=c.indexOf(selected);
  const corePos=pos>=0?String(pos+1).padStart(2,'0')+' / '+String(c.length).padStart(2,'0'):String(selected+1).padStart(2,'0')+' / '+String(currentSpec.steps.length).padStart(2,'0');
  
  document.getElementById('position').textContent=c.length!==currentSpec.steps.length&&c.includes(selected)?'핵심 단계 '+corePos:'단계 '+corePos;
  document.getElementById('detail-title').textContent=st.name;
  document.getElementById('detail-chips').innerHTML=layerChip(st)+(st.kind&&KIND_LABELS[st.kind]?'<span class="badge" style="background:var(--ink);color:var(--paper)">'+KIND_LABELS[st.kind]+'</span>':'')+trustChip(st)+changeChip(st);
  
  // Impact box
  document.getElementById('impact-code').textContent=st.name;
  const hasDelta=!!st.stateDelta;
  document.getElementById('impact-state-cell').dataset.changed=hasDelta?'true':'false';
  document.getElementById('impact-state').textContent=hasDelta?st.stateDelta.before+' → '+st.stateDelta.after:'상태 변화 없음';
  document.getElementById('impact-result').textContent=st.branch||(st.sideEffect?'외부 연동 · '+st.sideEffect:'직접 결과 없음');
  
  // Rules row
  const rr=document.getElementById('rules-row');
  if(st.rules&&st.rules.length){
    rr.style.display='flex';
    rr.innerHTML='<span class="badge" style="flex:0 0 auto">지켜야 할 규칙</span>'+st.rules.map(r=>'<span class="pill" style="cursor:default">'+esc(r)+'</span>').join('');
  }else{
    rr.style.display='none';
  }
  
  // Cross-layer delegation edges
  const er=document.getElementById('edges-row');
  const myEdges=(currentSpec.edges||[]).filter(e=>e.stepOrdinal===st.ordinal&&EDGE_LABELS[e.kind]);
  if(myEdges.length){
    er.style.display='flex';
    er.innerHTML='<span class="badge" style="flex:0 0 auto;background:var(--ink);color:var(--paper)">이 단계에서 이어지는 곳</span>'+myEdges.map(e=>'<span class="pill" style="cursor:default"><strong>'+esc(symbolName(e.toSymbolPath))+'</strong><span style="color:var(--muted)">· '+EDGE_LABELS[e.kind]+'</span></span>').join('');
  }else{
    er.style.display='none';
  }
  
  document.getElementById('vscode-link').href='vscode://file/'+st.anchor.repoRelativePath+':'+focusStart(st);
  
  // Unknowns panel
  const up=document.getElementById('unknowns-panel');
  if(currentSpec.unknowns&&currentSpec.unknowns.length){
    up.hidden=false;
    document.getElementById('unknowns-list').innerHTML=currentSpec.unknowns.map(u=>'<li><strong>'+esc(u.subject)+'</strong><span class="why">빠진 연결: '+esc(u.reason)+'</span></li>').join('');
  }else{
    up.hidden=true;
  }
}

function symbolName(toSymbolPath){
  const h=(toSymbolPath||'').indexOf('#');
  return h>=0?toSymbolPath.slice(h+1):toSymbolPath;
}

function prevStep(){
  const c=coreIndices();
  const idx=c.indexOf(selected);
  if(idx>0)selectStep(c[idx-1]);
  else if(c.length)selectStep(c[0]);
}

function nextStep(){
  const c=coreIndices();
  const idx=c.indexOf(selected);
  if(idx>=0&&idx<c.length-1)selectStep(c[idx+1]);
  else if(idx===-1&&c.length)selectStep(c[0]);
}

function setViewMode(m){
  viewMode=m;
  ['symbol','focus'].forEach(k=>{
    document.getElementById('mode-'+k).setAttribute('aria-pressed',String(k===m));
  });
  renderCode();
}

function centerScroll(container,el){
  if(!container||!el)return;
  if(container.scrollWidth<=container.clientWidth)return;
  const r=el.getBoundingClientRect(),tr=container.getBoundingClientRect();
  container.scrollTo({left:container.scrollLeft+r.left+r.width/2-tr.left-tr.width/2,behavior:'smooth'});
}

function selectStep(i,scroll=true){
  if(!currentSpec)return;
  selected=Math.max(0,Math.min(currentSpec.steps.length-1,i));
  
  document.querySelectorAll('[data-map-step]').forEach(n=>n.setAttribute('aria-pressed',String(Number(n.dataset.mapStep)===selected)));
  document.querySelectorAll('[data-hstep]').forEach(n=>n.setAttribute('aria-current',Number(n.dataset.hstep)===selected?'step':'false'));
  
  const c=coreIndices();
  const p=c.indexOf(selected);
  const atFirstCore=p===0, atLastCore=p===c.length-1, isNonCore=p===-1;
  document.getElementById('prev').disabled=c.length? (isNonCore? false : atFirstCore) : selected===0;
  document.getElementById('next').disabled=c.length? (isNonCore? c.length===0 : atLastCore) : selected===currentSpec.steps.length-1;
  
  renderDetail();
  renderCode();
  
  if(scroll){
    const activeItem=document.querySelector('[data-hstep][aria-current="step"]');
    if(activeItem)activeItem.scrollIntoView({block:'nearest',behavior:'smooth'});
    centerScroll(document.getElementById('map-scroll'),document.querySelector('[data-map-step][aria-pressed="true"]'));
  }
}

let codeToken=0;
async function renderCode(){
  if(!currentSpec)return;
  const st=currentSpec.steps[selected];
  const token=++codeToken;
  let range=viewRange(st);
  if(viewMode==='focus'){
    range={start:focusStart(st),end:Math.max(focusEnd(st),focusStart(st)+2),known:range.known};
  }
  const note=(viewMode==='symbol')&&!range.known;
  const qs='path='+encodeURIComponent(st.anchor.repoRelativePath)+'&startLine='+range.start+'&endLine='+range.end+'&maxLines=400';
  document.getElementById('code-path').innerHTML='<b>'+esc(st.anchor.repoRelativePath)+'</b>';
  const sym=st.anchor.enclosingSymbolPath||'';
  document.getElementById('code-range').textContent=sym+' · '+range.start+'-'+range.end;
  document.getElementById('code-note').hidden=!note;
  
  try{
    const r=await api('/api/source?token='+encodeURIComponent(token)+'&'+qs);
    const text=await r.text();
    if(token!==codeToken)return;
    const startLine=Number(new URLSearchParams(qs).get('startLine'))||1;
    const lines=text.replace(/\n$/,'').split('\n');
    const peers=sameSymbolSteps(st);
    const html=lines.map((ln,i)=>{
      const num=startLine+i;
      let gutter='';let cls='';
      for(const p of peers){
        const ps=focusStart(p.st);
        if(num===ps&&p.i!==selected){
          gutter='<span class="gut"><button class="marker" data-marker="'+p.i+'" aria-pressed="false" title="'+esc(p.st.name)+'">'+String(p.i+1).padStart(2,'0')+'</button></span>';
          cls='peer';
          break;
        }
      }
      if(num>=focusStart(st)&&num<=focusEnd(st)){
        cls='hit';
        if(!gutter)gutter='<span class="gut"><button class="marker" data-marker="'+selected+'" aria-pressed="true" title="'+esc(st.name)+'">'+String(selected+1).padStart(2,'0')+'</button></span>';
      }
      return '<div class="line '+cls+'"><span class="num">'+num+'</span>'+(gutter||'<span class="gut"></span>')+'<span class="src">'+esc(ln)+'</span></div>';
    }).join('');
    document.getElementById('code').innerHTML=html;
    document.querySelectorAll('[data-marker]').forEach(n=>n.addEventListener('click',()=>selectStep(Number(n.dataset.marker))));
  }catch(e){
    if(token===codeToken)document.getElementById('code').textContent='코드 로드 실패: '+e.message;
  }
}

function sameSymbolSteps(st){
  return currentSpec.steps.map((x,i)=>({st:x,i})).filter(x=>x.st.anchor.repoRelativePath===st.anchor.repoRelativePath&&x.st.anchor.enclosingSymbolPath===st.anchor.enclosingSymbolPath&&lens(x.st).startLine);
}

function scrollToFirstStale(){
  const i=currentSpec.steps.findIndex(x=>x.freshness==='stale'||x.freshness==='orphaned');
  if(i>=0)selectStep(i);
  document.querySelector('[data-region="detail"]').scrollIntoView({block:'start',behavior:'smooth'});
}

function openSwitcher(){
  document.getElementById('switcher-modal').style.display='flex';
  document.getElementById('switcher-input').focus();
  filterFlows();
}

function closeSwitcher(){
  document.getElementById('switcher-modal').style.display='none';
}

function filterFlows(){
  const q=document.getElementById('switcher-input').value.toLowerCase();
  const el=document.getElementById('switcher-list');
  const f=cachedFlows.filter(x=>x.title.toLowerCase().includes(q)||x.entrySymbolPath.toLowerCase().includes(q));
  el.innerHTML=f.map(x=>'<div style="padding:10px 12px;border:1px solid var(--line);border-radius:7px;cursor:pointer" onclick="loadFlow(\''+x.flowId+'\');closeSwitcher()">'+
    '<div style="font-weight:800;font-size:13px">'+esc(x.title)+'</div>'+
    (x.description?'<div style="font-size:11px;color:var(--muted);margin-top:2px">'+esc(x.description)+'</div>':'')+
    '<div style="font-size:10px;color:var(--muted);margin-top:4px">'+esc(x.entrySymbolPath)+' · '+x.stepCount+'단계</div>'+
  '</div>').join('')||'<div style="padding:12px;color:var(--muted);text-align:center">검색 결과 없음</div>';
}

function toggleEdit(){
  const e=document.getElementById('edit-form');
  e.style.display=e.style.display==='grid'?'none':'grid';
  if(e.style.display==='grid'){
    document.getElementById('edit-name').value=currentSpec.steps[selected].name;
    document.getElementById('edit-rules').value=(currentSpec.steps[selected].rules||[]).join(', ');
  }
}

async function submitApproval(){
  const st=currentSpec.steps[selected];
  const name=document.getElementById('edit-name').value,rules=document.getElementById('edit-rules').value.split(',').map(s=>s.trim()).filter(Boolean);
  const r=await api('/api/approve',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({flowId:currentSpec.flowId,symbolPath:st.anchor.enclosingSymbolPath,name,rules})});
  if(!r.ok)throw new Error('승인 실패');
  await loadFlow(currentFlowId);
}

document.addEventListener('keydown',e=>{
  if((e.metaKey||e.ctrlKey)&&e.key==='k'){
    e.preventDefault();
    openSwitcher();
  }
  if(e.key==='Escape')closeSwitcher();
  if(e.key==='ArrowLeft'&&document.activeElement.tagName!=='INPUT'){
    e.preventDefault();
    prevStep();
  }
  if(e.key==='ArrowRight'&&document.activeElement.tagName!=='INPUT'){
    e.preventDefault();
    nextStep();
  }
});

init();
</script>
</body>
</html>
`
