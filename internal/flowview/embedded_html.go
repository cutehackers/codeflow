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
    
    /* ---- Architecture Map v2: modes, chips, arcs, excerpts ---- */
    .map-modes{display:flex;gap:4px}
    .map-modes button{padding:4px 10px;border:1px solid var(--line);border-radius:6px;background:var(--paper);cursor:pointer;font-size:11px;font-weight:700}
    .map-modes button[aria-pressed="true"]{border-color:var(--ink);background:var(--ink);color:var(--paper)}
    .legend .status[data-status="uncertain"]::before{content:"?"}
    #map-lanes{position:relative}
    #map-arcs{position:absolute;inset:0;width:100%;height:100%;pointer-events:none;z-index:1}
    .arc{fill:none;stroke:var(--ink);stroke-width:1.4;opacity:.55}
    .arc-label{pointer-events:all;cursor:pointer;font-size:9px;font-weight:800;paint-order:stroke;stroke:#fff;stroke-width:3px;fill:var(--ink)}
    .arc-hit{fill:none;stroke:transparent;stroke-width:10;pointer-events:all;cursor:pointer}
    .node[data-uncertain]{border-style:dashed;border-color:var(--muted)}
    .node[data-uncertain] strong::after{content:" ?";color:var(--warn);font-weight:900}
    .conf{font-size:9px;color:var(--muted);font-weight:700}
    .node[aria-pressed="true"] .conf{color:#ccc}
    /* project-mode chips */
    .proj-track{display:flex;flex-wrap:wrap;gap:8px;padding:12px;min-height:56px}
    .chip{display:inline-flex;flex-direction:column;gap:2px;min-width:150px;max-width:280px;min-height:52px;padding:7px 10px;border:1px solid var(--ink);border-radius:7px;background:var(--paper);text-align:left;cursor:pointer;font-size:11px}
    .chip:hover{transform:translateY(-1px);box-shadow:2px 2px 0 var(--line)}
    .chip strong{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:11px}
    .chip .sig{font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:9px;color:var(--muted);overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:260px}
    .chip .meta{display:flex;gap:6px;align-items:center;font-size:9px;color:var(--muted)}
    .chip.on-path{box-shadow:3px 3px 0 var(--ink);background:#fafafa}
    .chip.dim,.lane.dim .lane-label,.lane.dim .proj-track{opacity:.35}
    .proj-empty{padding:18px;color:var(--muted);font-size:12px}
    /* excerpt slide-over */
    #excerpt-panel{position:fixed;top:0;right:0;height:100vh;width:min(520px,92vw);background:var(--paper);border-left:1px solid var(--ink);box-shadow:-6px 0 24px rgba(0,0,0,.12);z-index:90;display:none;flex-direction:column}
    #excerpt-panel.open{display:flex}
    .ex-head{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:14px 16px;border-bottom:1px solid var(--ink);background:var(--soft)}
    .ex-head h3{margin:0;font-size:14px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
    .ex-sub{padding:8px 16px;border-bottom:1px solid var(--line);font-size:11px;color:var(--muted);display:grid;gap:6px}
    .ex-flows{display:flex;flex-wrap:wrap;gap:6px}
    .ex-lanes{display:flex;flex-wrap:wrap;gap:6px;align-items:center}
    .ex-lanes button{padding:3px 8px;border:1px solid var(--line);border-radius:5px;background:var(--paper);cursor:pointer;font-size:10px;font-weight:700}
    .ex-lanes button.current{background:var(--ink);color:var(--paper);border-color:var(--ink)}
    .ex-code{flex:1;overflow:auto;background:#fafafa;padding:8px 0}
    .ex-note{padding:6px 16px;border-top:1px solid var(--line);font-size:11px;color:var(--muted)}

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
      <span class="badge" id="workspace-activity-badge" style="background:var(--soft);color:var(--ink)" title="현재 작업공간 상태">idle</span>
      <span id="workspace-epoch-tag" style="font-size:11px;color:var(--muted)"></span>
      <span id="workspace-pending-count" class="badge" style="font-size:11px" title="대기 중인 변경">0 pending</span>
      <span id="workspace-analysis-lag" style="font-size:11px;color:var(--muted)" title="분석 지연">0ms lag</span>
      <span id="workspace-scope-tag" style="font-size:11px;color:var(--muted)" title="영향 가능 범위">전체</span>
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

  <!-- Task View Feature Query Bar & Current Answer Strip (VS-02) -->
  <section class="semantic-query-section" data-region="semantic-query" aria-label="기능 흐름 자연어 질의">
    <form id="query-form" onsubmit="handleSemanticQuery(event)" style="display:flex;gap:8px;margin-top:14px">
      <input id="query-input" type="text" placeholder="자연어로 기능 흐름을 질문하세요 (예: 결제 처리, 회원가입, 장바구니)..." style="flex:1;padding:8px 12px;border:1px solid var(--ink);border-radius:6px;font-size:13px" />
      <button id="query-submit" type="submit" class="btn" style="padding:8px 16px;background:var(--ink);color:var(--paper);border:1px solid var(--ink);border-radius:6px;font-weight:700;cursor:pointer">질의</button>
    </form>
    <div id="disambiguation-dialog" style="display:none;margin-top:10px;padding:12px;border:1px solid var(--ink);border-radius:6px;background:var(--soft)">
      <strong id="disambiguation-title" style="display:block;margin-bottom:8px">일치하는 후보 흐름이 여러 개 있습니다:</strong>
      <div id="disambiguation-list" style="display:flex;flex-direction:column;gap:6px"></div>
    </div>
  </section>

  <!-- Current Answer Strip (VS-02 & VS-04) -->
  <section id="current-answer-strip" data-region="current-answer" style="display:none;margin-top:14px;padding:14px 16px;border:2px solid var(--ink);border-radius:8px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;flex-wrap:wrap;gap:8px">
      <span style="font-size:11px;font-weight:800;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)">Current Verified Answer</span>
      <!-- Independent Status Axes (VS04-A8, D21) -->
      <div style="display:flex;gap:6px;align-items:center;flex-wrap:wrap">
        <span id="badge-freshness" class="badge">Current</span>
        <span id="current-answer-stage" class="badge">Q2 Verified</span>
        <span id="badge-quality" class="badge" style="display:none">Q2</span>
        <span id="badge-activity" class="badge" style="background:#f4f4f2">idle</span>
        <span id="badge-settlement" class="badge" style="background:#f4f4f2">Settlement: pending</span>
        <span id="badge-enrichment" class="badge" style="background:#f4f4f2">Enrichment: none</span>
        <span id="badge-connection" class="badge" style="background:#f4f4f2">SSE: connected</span>
        <span id="current-answer-basis" style="font-size:11px;color:var(--muted)"></span>
      </div>
    </div>
    <div style="margin-bottom:4px;font-size:12px;color:var(--muted)"><b>요청 의도:</b> <span id="current-answer-requested">—</span></div>
    <div style="font-size:15px;font-weight:700;line-height:1.4;color:var(--ink)" id="current-answer-statement">—</div>
    <!-- Verified Gap Banner (VS04-A3, VS04-A11) -->
    <div id="verified-gap-banner" class="queue-banner" style="display:none;margin-top:10px;background:#fff4e6;border-left:4px solid #f08c00">
      <div>
        <div style="font-weight:700;color:#d9480f" id="verified-gap-title">⚠️ 최신 작업공간 편집 반영 대기 중 (Last Verified)</div>
        <div style="font-size:12px;color:var(--muted);margin-top:2px">영향 범위: <span id="verified-gap-scope" style="font-family:monospace;font-weight:bold">—</span> | 지연: <span id="verified-gap-lag">0</span>ms | 대기 리비전: <span id="verified-gap-pending">0</span>개</div>
      </div>
    </div>
  </section>

  <!-- Change Pulse Section (VS-05, Raw §9.9) -->
  <section id="change-pulse-section" class="flow-tabs-section" aria-label="Change Pulse 변경 감지" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Change Pulse</span>
        <span id="change-pulse-count" class="badge" style="background:#f4f4f2">0 changes</span>
      </div>
      <button id="btn-toggle-review" class="btn" style="font-size:11px;padding:4px 8px" onclick="triggerReviewMode()">비교 검토 (Review Mode)</button>
    </div>
    <ul id="change-pulse-list" style="list-style:none;padding:0;margin:0;display:flex;flex-direction:column;gap:6px">
      <li style="font-size:12px;color:var(--muted)">표시할 변경 내역이 없습니다 (active generation 기준).</li>
    </ul>
  </section>

  <!-- Requirement Alignment Board (VS-05, Raw §9.10) -->
  <section id="requirement-alignment-section" class="flow-tabs-section" aria-label="Requirement Alignment 요구사항 정렬" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Requirement Alignment</span>
        <span id="intent-status-tag" class="badge" style="background:#e7f5ff;color:#1971c2">Intent: parsed</span>
      </div>
    </div>
    <div style="overflow-x:auto">
      <table id="requirement-alignment-table" style="width:100%;border-collapse:collapse;font-size:12px">
        <thead>
          <tr style="border-bottom:1px solid var(--line);text-align:left;color:var(--muted)">
            <th style="padding:6px 8px">요구사항 (Criterion)</th>
            <th style="padding:6px 8px">구현 정렬 상태 (Status)</th>
            <th style="padding:6px 8px">연결 단계 (Steps)</th>
            <th style="padding:6px 8px">근거 (Evidence)</th>
            <th style="padding:6px 8px">비고 / 누락 (Gap)</th>
          </tr>
        </thead>
        <tbody id="requirement-alignment-tbody">
          <tr><td colspan="5" style="padding:10px 8px;color:var(--muted)">정렬된 요구사항이 없습니다.</td></tr>
        </tbody>
      </table>
    </div>
  </section>

  <!-- Change Impact Trace Section (VS-06, Raw §8.6, §10) -->
  <section id="change-impact-section" class="flow-tabs-section" aria-label="Change Impact Trace 변경 영향 추적" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Change Impact Trace</span>
        <span id="impact-status-tag" class="badge" style="background:#f4f4f2">Bounded</span>
      </div>
      <div style="display:flex;gap:6px">
        <input id="impact-symbol-input" type="text" placeholder="Symbol path" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px" />
        <button id="btn-trigger-impact" class="btn" style="font-size:11px;padding:4px 8px" onclick="triggerImpactMode()">영향 추적 (Trace Impact)</button>
      </div>
    </div>
    <div style="display:grid;grid-template-columns:1fr 1fr 1fr;gap:12px;margin-top:8px">
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px 10px;background:var(--soft)">
        <div style="font-weight:800;font-size:11px;text-transform:uppercase;color:var(--muted);margin-bottom:6px">직접 영향 (Direct Impact)</div>
        <ul id="direct-impact-list" style="list-style:none;padding:0;margin:0;font-size:12px;display:flex;flex-direction:column;gap:4px">
          <li style="color:var(--muted)">추적된 직접 영향이 없습니다.</li>
        </ul>
      </div>
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px 10px;background:var(--soft)">
        <div style="font-weight:800;font-size:11px;text-transform:uppercase;color:var(--muted);margin-bottom:6px">간접 영향 (Bounded Indirect)</div>
        <ul id="indirect-impact-list" style="list-style:none;padding:0;margin:0;font-size:12px;display:flex;flex-direction:column;gap:4px">
          <li style="color:var(--muted)">추적된 간접 영향이 없습니다.</li>
        </ul>
      </div>
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px 10px;background:var(--soft)">
        <div style="font-weight:800;font-size:11px;text-transform:uppercase;color:var(--muted);margin-bottom:6px">미확인 경계 (Unresolved Boundaries)</div>
        <ul id="unresolved-boundaries-list" style="list-style:none;padding:0;margin:0;font-size:12px;display:flex;flex-direction:column;gap:4px">
          <li style="color:var(--muted)">미확인 경계 없음 (All Grounded).</li>
        </ul>
      </div>
    </div>
  </section>

  <!-- Failure & Incident Investigation Section (VS-07, Raw §8.7, §8.8) -->
  <section id="failure-investigation-section" class="flow-tabs-section" aria-label="Failure & Incident Investigation 장애 조사" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Failure & Incident Trace</span>
        <span id="failure-mode-tag" class="badge" style="background:#fff5f5;color:#c92a2a">Debug / Incident</span>
      </div>
      <div style="display:flex;gap:6px">
        <input id="failure-error-input" type="text" placeholder="Error (e.g. CardDeclined)" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px" />
        <button id="btn-trigger-debug" class="btn" style="font-size:11px;padding:4px 8px" onclick="triggerFailureInvestigation('debug')">오류 역추적 (Debug)</button>
        <input id="failure-trace-input" type="text" placeholder="Trace ID" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px;width:90px" />
        <button id="btn-trigger-incident" class="btn" style="font-size:11px;padding:4px 8px" onclick="triggerFailureInvestigation('incident')">인시던트 (Incident)</button>
      </div>
    </div>
    <div id="failure-summary-box" style="font-size:12px;color:var(--muted);margin-bottom:8px">
      <span id="failure-summary-desc">장애 발생 원인 및 타임라인을 조회할 수 있습니다.</span>
      <span id="failure-last-state" style="margin-left:8px;font-weight:bold;color:var(--text)"></span>
    </div>
    <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px 10px;background:var(--soft)">
        <div style="font-weight:800;font-size:11px;text-transform:uppercase;color:var(--muted);margin-bottom:6px">원인 역추적 노드 (Cause Chain Nodes)</div>
        <ul id="failure-nodes-list" style="list-style:none;padding:0;margin:0;font-size:12px;display:flex;flex-direction:column;gap:4px">
          <li style="color:var(--muted)">조회된 원인 노드가 없습니다.</li>
        </ul>
      </div>
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px 10px;background:var(--soft)">
        <div style="font-weight:800;font-size:11px;text-transform:uppercase;color:var(--muted);margin-bottom:6px">인시던트 타임라인 (Timeline Events)</div>
        <ul id="failure-timeline-list" style="list-style:none;padding:0;margin:0;font-size:12px;display:flex;flex-direction:column;gap:4px">
          <li style="color:var(--muted)">인시던트 이벤트가 없습니다.</li>
        </ul>
      </div>
    </div>
  </section>

  <!-- Semantic Approval & Grounding Section (VS-08, Raw §9.4..§9.6) -->
  <section id="semantic-approval-section" class="flow-tabs-section" aria-label="Semantic Approval & Grounding 의미 승인 및 근거 접지" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Semantic Approval & Grounding</span>
        <span id="approval-status-badge" class="badge" style="background:#e7f5ff;color:#1864ab">Awaiting Human Approval</span>
      </div>
      <div style="display:flex;gap:6px">
        <button id="btn-semantic-approve" class="btn" style="font-size:11px;padding:4px 10px;background:#1e602b;color:#fff" onclick="submitProposalApproval('approved')">의미 승인 (Approve)</button>
        <button id="btn-semantic-reject" class="btn" style="font-size:11px;padding:4px 10px;background:#c92a2a;color:#fff" onclick="submitProposalApproval('rejected')">반려 (Reject)</button>
      </div>
    </div>
    <div id="proposal-card" style="border:1px solid var(--line);border-radius:6px;padding:10px 12px;background:var(--soft);margin-bottom:8px">
      <div style="display:flex;justify-content:space-between;font-size:12px">
        <span><strong>제안 대상:</strong> <span id="proposal-target-symbol" style="font-family:monospace">HomePage.handleQuickCheckout</span></span>
        <span><strong>분류:</strong> <span id="proposal-category" class="badge" style="background:#f1f3f5">business_rule</span></span>
      </div>
      <div style="margin-top:6px;font-size:13px;font-weight:bold" id="proposal-title">빠른 결제 진행 및 주문 생성</div>
      <div style="margin-top:4px;font-size:11px;color:var(--muted)" id="proposal-rationale">AST 호출 패턴 및 도메인 모델 검증을 바탕으로 제안된 비즈니스 단계입니다.</div>
    </div>
    <div id="evidence-grounding-summary" style="font-size:11px;color:var(--muted);display:flex;justify-content:space-between">
      <span>근거 팩 (Evidence Pack): <span id="evidence-pack-id" style="font-family:monospace">pack-default</span> (<span id="evidence-redaction-tag">Clean / Redacted</span>)</span>
      <span id="approval-result-msg" style="font-weight:bold;color:#2b8a3e"></span>
    </div>
  </section>

  <!-- Domain Architecture & Progressive Onboarding Section (VS-09, Raw §8.9) -->
  <section id="onboarding-domains-section" class="flow-tabs-section" aria-label="Domain Architecture 도메인 아키텍처 탐색" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Domain Architecture & Onboarding</span>
        <span id="onboarding-coverage-badge" class="badge" style="background:#e7f5ff;color:#1864ab">Level 1: System Map</span>
      </div>
      <button id="btn-explore-domains" class="btn" style="font-size:11px;padding:4px 10px" onclick="exploreDomains()">도메인 구조 탐색 (Explore)</button>
    </div>
    <div id="domain-cards-grid" style="display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:10px;margin-top:8px">
      <div style="border:1px solid var(--line);border-radius:6px;padding:10px;background:var(--soft)">
        <div style="font-weight:bold;font-size:12px">도메인 정보가 로드되지 않았습니다.</div>
        <div style="font-size:11px;color:var(--muted);margin-top:4px">탐색 버튼을 눌러 프로젝트 도메인 구조를 조회하세요.</div>
      </div>
    </div>
    <div id="onboarding-catalog-container" style="margin-top:10px;display:none;border-top:1px solid var(--line);padding-top:10px">
      <div style="font-weight:bold;font-size:12px;margin-bottom:6px">대표 흐름 카탈로그 (Level 2: Representative Flows)</div>
      <ul id="representative-flows-list" style="list-style:none;padding:0;margin:0;font-size:12px;display:flex;flex-direction:column;gap:4px"></ul>
    </div>
    <div id="onboarding-summary-bar" style="margin-top:8px;font-size:11px;color:var(--muted)">
      <span>전체 도메인: <span id="onboarding-total-domains" style="font-weight:bold">0</span>개</span> |
      <span>대표 흐름: <span id="onboarding-total-flows" style="font-weight:bold">0</span>개</span> |
      <span>커버리지: <span id="onboarding-coverage-ratio" style="font-weight:bold">100%</span></span>
    </div>
  </section>

  <!-- Release Capability & SLM Matrix Section (VS-10, Raw §16–§18) -->
  <section id="release-capability-section" class="flow-tabs-section" aria-label="Release Capability 릴리즈 검증 및 역량 매트릭스" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Release Capability & SLM Matrix</span>
        <span id="release-ready-badge" class="badge" style="background:#ebfbee;color:#1e602b">Release Ready: PASSED</span>
      </div>
      <button id="btn-eval-release" class="btn" style="font-size:11px;padding:4px 10px" onclick="evaluateReleaseCapability()">릴리즈 역량 재평가 (Evaluate)</button>
    </div>
    <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-bottom:10px">
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px;background:var(--soft)">
        <div style="font-size:11px;color:var(--muted)">Latency (p95)</div>
        <div id="metric-latency-p95" style="font-size:14px;font-weight:bold;margin-top:2px">315.0 ms</div>
      </div>
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px;background:var(--soft)">
        <div style="font-size:11px;color:var(--muted)">Precision / Recall</div>
        <div id="metric-precision" style="font-size:14px;font-weight:bold;margin-top:2px">0.93 / 0.90</div>
      </div>
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px;background:var(--soft)">
        <div style="font-size:11px;color:var(--muted)">Regressions / Violations</div>
        <div id="metric-regressions" style="font-size:14px;font-weight:bold;margin-top:2px">0 / 0</div>
      </div>
    </div>
    <div style="font-size:12px;margin-bottom:4px"><strong>SLM 세맨틱 과업 역량 상태:</strong></div>
    <div id="slm-capabilities-list" style="display:flex;gap:6px;flex-wrap:wrap;font-size:11px">
      <span class="badge" style="background:#ebfbee;color:#1e602b">진입점 해석: Full</span>
      <span class="badge" style="background:#ebfbee;color:#1e602b">슬라이스 합성: Full</span>
      <span class="badge" style="background:#ebfbee;color:#1e602b">상태 델타 추론: Full</span>
      <span class="badge" style="background:#ebfbee;color:#1e602b">비즈니스 규칙 추출: Full</span>
      <span class="badge" style="background:#ebfbee;color:#1e602b">간접 영향 추적: Full</span>
      <span class="badge" style="background:#ebfbee;color:#1e602b">장애 역추적: Full</span>
    </div>
    <div style="margin-top:8px;font-size:11px;color:var(--muted)">
      <span>폴백 티어 (Fallback Tier): <span id="release-fallback-tier" style="font-family:monospace;font-weight:bold">local_slm</span></span>
    </div>
  </section>

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

  <!-- 2. Architecture Map (flow view / project view) -->
  <section class="map-panel" data-region="map" aria-label="아키텍처 맵">
    <div class="map-head">
      <div>
        <h2 id="map-title">Architecture Map</h2>
        <p id="map-sub">비즈니스 흐름이 화면에서 외부 연동까지 전달되는 계층 구조입니다.</p>
      </div>
      <div class="map-modes" role="group" aria-label="맵 모드">
        <button id="mode-flow-map" aria-pressed="true" onclick="setMapMode('flow')">흐름 맵</button>
        <button id="mode-project-map" aria-pressed="false" onclick="setMapMode('project')">전체 보기</button>
      </div>
      <div class="legend" aria-label="신뢰 범례">
        <span class="status" data-status="fresh">확인됨</span>
        <span class="status" data-status="stale">재확인 필요</span>
        <span class="status" data-status="orphaned">찾을 수 없음</span>
        <span class="status" data-status="uncertain" title="계층 판단 근거가 약한 심볼 — 추측이 아닌 불확실 표시">판단 보류</span>
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

        <!-- Evidence Dock (VS-05, Raw §9.11) -->
        <section id="evidence-dock-section" class="evidence-dock-panel" style="margin-top:16px;border-top:1px solid var(--line);padding-top:12px">
          <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
            <span style="font-weight:800;font-size:12px;text-transform:uppercase;letter-spacing:.05em">Evidence Dock</span>
            <div class="modes" role="tablist" aria-label="Evidence Dock 탭" style="display:flex;gap:4px">
              <button id="dock-tab-why" class="btn-sm active" role="tab" aria-selected="true" onclick="switchEvidenceDockTab('why')">Why</button>
              <button id="dock-tab-code" class="btn-sm" role="tab" aria-selected="false" onclick="switchEvidenceDockTab('code')">Code</button>
              <button id="dock-tab-test" class="btn-sm" role="tab" aria-selected="false" onclick="switchEvidenceDockTab('test')">Test</button>
              <button id="dock-tab-history" class="btn-sm" role="tab" aria-selected="false" onclick="switchEvidenceDockTab('history')">History</button>
            </div>
          </div>
          <div id="dock-pane-why" class="dock-pane" role="tabpanel" aria-labelledby="dock-tab-why" style="font-size:13px;line-height:1.5;color:var(--ink)">
            <div id="dock-why-text">—</div>
          </div>
          <div id="dock-pane-code" class="dock-pane" role="tabpanel" aria-labelledby="dock-tab-code" style="display:none;font-size:12px">
            <div id="dock-code-anchor" class="mono" style="color:var(--muted)">—</div>
          </div>
          <div id="dock-pane-test" class="dock-pane" role="tabpanel" aria-labelledby="dock-tab-test" style="display:none;font-size:12px">
            <ul id="dock-test-list" style="margin:0;padding-left:16px;color:var(--ink)"><li>연결된 테스트 근거가 없습니다.</li></ul>
          </div>
          <div id="dock-pane-history" class="dock-pane" role="tabpanel" aria-labelledby="dock-tab-history" style="display:none;font-size:12px">
            <div id="dock-history-text" style="color:var(--muted)">이전 세대 대비 변경 사항 없음 (baseline 일치)</div>
          </div>
        </section>
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

<!-- Component excerpt slide-over (project map) -->
<aside id="excerpt-panel" aria-label="심볼 코드 근거">
  <div class="ex-head">
    <h3 id="ex-title">—</h3>
    <button class="btn-sm" onclick="closeExcerpt()">닫기</button>
  </div>
  <div class="ex-sub">
    <span class="mono" id="ex-sig">—</span>
    <div class="ex-flows" id="ex-flows"></div>
    <div class="ex-lanes" id="ex-lanes"></div>
  </div>
  <div class="ex-code"><div class="code" id="ex-code">—</div></div>
  <div class="ex-note">계층 재분류는 codeflow.flows.yaml 의 laneOverrides 에 저장되고 다음 렌더부터 확정 적용됩니다.</div>
</aside>

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
let mapMode='flow',cachedMap=null,excerptSymbol=null;

async function api(path,opts={}){
  const u=new URL(path,location.origin);
  if(token)u.searchParams.set('token',token);
  const h=Object.assign({},opts.headers);
  if(token)h['X-CodeFlow-Token']=token;
  const r=await fetch(u.toString(),Object.assign({},opts,{headers:h}));
  if(!r.ok && !opts.allowErrors)throw new Error(r.status+' '+r.statusText);
  return r;
}

function esc(s){
  return (s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

/* escJs: for values interpolated into single-quoted JS strings inside HTML
   attributes — HTML-escape first, then neutralize the JS string delimiter. */
function escJs(s){
  return esc(s).replace(/'/g,"\\'");
}

const LAYER_ORDER=['presentation','controller','usecase','domain','data','infra','external','unknown'];
const LAYER_LABELS={presentation:'프레젠테이션',controller:'컨트롤러',usecase:'유스케이스',domain:'도메인',data:'데이터',infra:'인프라',external:'외부 연동',unknown:'미분류',page:'Page (Flutter)',state:'상태(State)',repository:'Repository',ui:'Page (Flutter)',application:'UseCase'};
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

async function loadWorkspaceActivity(){
  try{
    const r=await api('/api/workspace/activity');
    if(r.ok){
      const d=await r.json();
      const badge=document.getElementById('workspace-activity-badge');
      const epoch=document.getElementById('workspace-epoch-tag');
      const pending=document.getElementById('workspace-pending-count');
      const lag=document.getElementById('workspace-analysis-lag');
      const scope=document.getElementById('workspace-scope-tag');
      if(badge){
        badge.textContent=d.activity||'idle';
        if(d.activity==='editing'||d.activity==='reconciling'){
          badge.className='badge warn-badge';
        }else{
          badge.className='badge';
        }
      }
      if(epoch&&d.workspaceEpoch){
        epoch.textContent='['+d.workspaceEpoch+']';
      }
      if(pending){
        pending.textContent=(d.pendingRevisions||0)+' pending';
      }
      if(lag){
        lag.textContent=(d.analysisLagMs||0)+'ms lag';
      }
      if(scope){
        scope.textContent=(d.scope&&d.scope.length)?d.scope.join(', '):'전체';
      }
    }
  }catch(e){}
}

async function init(){
  loadWorkspaceActivity();
  initLiveStream();
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
    if(mapMode==='project')highlightFlowPath();
  }catch(e){
    document.getElementById('flow-title').textContent='흐름 로드 오류: '+e.message;
  }
}

/* ---- Semantic Task View (VS-02) ---- */

let liveEventSource=null;
let lastSeenEventId=null;

function initLiveStream(){
  if(typeof EventSource==='undefined')return;
  if(liveEventSource){
    liveEventSource.close();
  }
  const token=(new URLSearchParams(window.location.search)).get('token')||'';
  let streamUrl='/api/workspace/stream?token='+encodeURIComponent(token);
  if(lastSeenEventId){
    streamUrl+='&lastEventId='+encodeURIComponent(lastSeenEventId);
  }

  const setConn=(st)=>{
    const el=document.getElementById('badge-connection');
    if(el){
      el.textContent='SSE: '+st;
      el.style.background=st==='connected'?'#d3f9d8':'#ffe3e3';
    }
  };

  try{
    liveEventSource=new EventSource(streamUrl);
    liveEventSource.onopen=()=>{setConn('connected');};
    liveEventSource.onerror=()=>{setConn('reconnecting');};

    liveEventSource.addEventListener('activity.updated',e=>{
      if(e.lastEventId)lastSeenEventId=e.lastEventId;
      try{
        const env=JSON.parse(e.data);
        updateWorkspaceActivityUI(env.data||env);
      }catch(err){console.error(err);}
    });

    liveEventSource.addEventListener('generation.published',e=>{
      if(e.lastEventId)lastSeenEventId=e.lastEventId;
      try{
        const input=document.getElementById('query-input');
        const query=(input?input.value:'').trim();
        if(query){
          handleSemanticQuery(null,true);
        }
        hideVerifiedGap();
      }catch(err){console.error(err);}
    });

    liveEventSource.addEventListener('generation.gap',e=>{
      if(e.lastEventId)lastSeenEventId=e.lastEventId;
      try{
        const env=JSON.parse(e.data);
        showVerifiedGap(env.data||env);
      }catch(err){console.error(err);}
    });

    liveEventSource.addEventListener('snapshot_sync',e=>{
      if(e.lastEventId)lastSeenEventId=e.lastEventId;
      try{
        const env=JSON.parse(e.data);
        if(env.data&&env.data.activity){
          updateWorkspaceActivityUI(env.data.activity);
        }
      }catch(err){console.error(err);}
    });
  }catch(e){
    console.error('EventSource init error:',e);
    setConn('disconnected');
  }
}

function updateWorkspaceActivityUI(act){
  if(!act)return;
  const badge=document.getElementById('workspace-activity-badge');
  const activityBadge=document.getElementById('badge-activity');
  if(badge)badge.textContent=act.activity||'idle';
  if(activityBadge)activityBadge.textContent=act.activity||'idle';

  const pending=document.getElementById('workspace-pending-count');
  if(pending)pending.textContent='pending: '+(act.pendingRevisions||0);

  const lag=document.getElementById('workspace-analysis-lag');
  if(lag)lag.textContent='lag: '+(act.analysisLagMs||0)+'ms';

  const scope=document.getElementById('workspace-scope-tag');
  if(scope)scope.textContent=(act.activeScope&&act.activeScope.length)?act.activeScope.join(', '):'none';
}

function showVerifiedGap(gap){
  const banner=document.getElementById('verified-gap-banner');
  const freshness=document.getElementById('badge-freshness');
  if(freshness){
    freshness.textContent='Last Verified';
    freshness.className='badge warn-badge';
  }
  if(!banner||!gap)return;
  banner.style.display='flex';
  const scopeEl=document.getElementById('verified-gap-scope');
  const lagEl=document.getElementById('verified-gap-lag');
  const pendingEl=document.getElementById('verified-gap-pending');
  if(scopeEl)scopeEl.textContent=(gap.affectedScope&&gap.affectedScope.length)?gap.affectedScope.join(', '):'none';
  if(lagEl)lagEl.textContent=String(gap.analysisLagMs||0);
  if(pendingEl)pendingEl.textContent=String(gap.pendingRevisions||0);
}

function hideVerifiedGap(){
  const banner=document.getElementById('verified-gap-banner');
  if(banner)banner.style.display='none';
  const freshness=document.getElementById('badge-freshness');
  if(freshness){
    freshness.textContent='Current';
    freshness.className='badge';
  }
}

async function handleSemanticQuery(event,preserveSelection=false){
  if(event)event.preventDefault();
  const input=document.getElementById('query-input');
  const query=(input?input.value:'').trim();
  if(!query)return;

  const dialog=document.getElementById('disambiguation-dialog');
  if(dialog)dialog.style.display='none';

  try{
    const r=await api('/api/task/view?query='+encodeURIComponent(query)+'&mode=feature',{allowErrors:true});
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      if(err.code==='ambiguous_target'&&err.candidateTargets){
        showDisambiguation(err.candidateTargets);
        return;
      }
      alert(err.message||'질의 처리 실패');
      return;
    }
    const d=await r.json();
    renderSemanticTaskView(d,preserveSelection);
  }catch(e){
    alert('질의 요청 오류: '+e.message);
  }
}

function showDisambiguation(candidates){
  const dialog=document.getElementById('disambiguation-dialog');
  const list=document.getElementById('disambiguation-list');
  if(!dialog||!list)return;
  list.innerHTML='';
  candidates.forEach(c=>{
    const btn=document.createElement('button');
    btn.className='btn';
    btn.style.textAlign='left';
    btn.style.padding='6px 10px';
    btn.style.background='#fff';
    btn.style.border='1px solid var(--ink)';
    btn.style.borderRadius='4px';
    btn.style.cursor='pointer';
    btn.textContent=c;
    btn.onclick=()=>{
      document.getElementById('query-input').value=c;
      selectSpecificEntry(c);
    };
    list.appendChild(btn);
  });
  dialog.style.display='block';
}

async function selectSpecificEntry(entrySymbol){
  const dialog=document.getElementById('disambiguation-dialog');
  if(dialog)dialog.style.display='none';
  try{
    const r=await api('/api/task/view?entrySymbol='+encodeURIComponent(entrySymbol)+'&mode=feature',{allowErrors:true});
    if(r.ok){
      const d=await r.json();
      renderSemanticTaskView(d,false);
    }
  }catch(e){
    console.error(e);
  }
}

function renderSemanticTaskView(data,preserveSelection=false){
  const strip=document.getElementById('current-answer-strip');
  if(strip&&data.currentAnswer){
    strip.style.display='block';
    const reqEl=document.getElementById('current-answer-requested');
    const stmtEl=document.getElementById('current-answer-statement');
    const stageEl=document.getElementById('current-answer-stage');
    const basisEl=document.getElementById('current-answer-basis');
    if(reqEl)reqEl.textContent=data.currentAnswer.requested||'—';
    if(stmtEl)stmtEl.textContent=data.currentAnswer.current||'—';
    if(stageEl&&data.semanticMap&&data.semanticMap.quality)stageEl.textContent=data.semanticMap.quality.stage+' Verified';
    if(basisEl&&data.semanticMap)basisEl.textContent='basis: '+(data.semanticMap.computedBasisId||'active');
  }

  // Update Independent Status Axes (VS04-A8, D21)
  const freshnessEl=document.getElementById('badge-freshness');
  if(freshnessEl){
    if(data.verifiedGap||(data.semanticMap&&data.semanticMap.freshness==='last_verified')){
      freshnessEl.textContent='Last Verified';
      freshnessEl.className='badge warn-badge';
    }else{
      freshnessEl.textContent='Current';
      freshnessEl.className='badge';
    }
  }
  const qualityEl=document.getElementById('badge-quality');
  if(qualityEl&&data.semanticMap&&data.semanticMap.quality){
    qualityEl.textContent=data.semanticMap.quality.stage;
  }
  const settlementEl=document.getElementById('badge-settlement');
  if(settlementEl&&data.semanticMap){
    settlementEl.textContent='Settlement: '+(data.semanticMap.settlement||'pending');
  }
  const enrichmentEl=document.getElementById('badge-enrichment');
  if(enrichmentEl){
    enrichmentEl.textContent='Enrichment: not_requested';
  }

  if(data.verifiedGap){
    showVerifiedGap(data.verifiedGap);
  }else{
    hideVerifiedGap();
  }

  if(data.semanticMap){
    const prevSelectedStepId=(currentSpec&&currentSpec.steps&&currentSpec.steps[selected])
      ?(currentSpec.steps[selected].stepId||currentSpec.steps[selected].code)
      :null;

    const projVisible=new Set(data.projection?(data.projection.visibleStepRefs||[]):[]);
    const projPreserved=new Set(data.projection?(data.projection.preservedStepRefs||[]):[]);

    currentSpec={
      title:data.semanticMap.summary.requested||(data.taskIntent?data.taskIntent.request.rawRequest:''),
      description:data.semanticMap.summary.current,
      flowId:data.semanticMap.mapId,
      basisSha:data.semanticMap.computedBasisId||'active',
      entrySymbolPath:(data.semanticMap.steps[0]||{}).technicalName||'',
      steps:data.semanticMap.steps.map(s=>({
        stepId:s.stepId,
        ordinal:s.ordinal,
        name:s.name,
        layer:s.layer,
        kind:s.kind,
        code:s.technicalName,
        anchor:s.anchor,
        codeLens:s.codeLens,
        stateDelta:s.stateDelta,
        sideEffect:s.sideEffect,
        branch:s.branch,
        rules:s.rules,
        freshness:'fresh',
        isVisible:projVisible.size===0||projVisible.has(s.stepId),
        isPreserved:projPreserved.has(s.stepId),
      })),
      unknowns:data.unknowns||[]
    };
    currentFlowId=currentSpec.flowId;

    // Stable selection (VS04-A10): preserve step identity across generation publications
    if(preserveSelection&&prevSelectedStepId){
      const matchIdx=currentSpec.steps.findIndex(st=>st.stepId===prevSelectedStepId||st.code===prevSelectedStepId);
      if(matchIdx>=0){
        selected=matchIdx;
      }else{
        selected=Math.min(selected,currentSpec.steps.length-1);
      }
    }else{
      selected=0;
    }

    renderAll();
    renderRequirementAlignment(data.taskIntent, data.semanticMap);
    if(data.changePulse){
      renderChangePulse(data.changePulse);
    }
  }
}

function renderAll(){
  renderHeader();
  renderStale();
  renderFlowTabs();
  if(mapMode==='project'){renderProjectMap();}else{renderMap();}
  renderTimeline();
  selectStep(nearestCoreIndex(selected),false);
}

/* ---- Map modes: flow view vs whole-project view ---- */

async function setMapMode(m){
  mapMode=m;
  document.getElementById('mode-flow-map').setAttribute('aria-pressed',String(m==='flow'));
  document.getElementById('mode-project-map').setAttribute('aria-pressed',String(m==='project'));
  if(m==='project'){
    await loadProjectMap();
    highlightFlowPath();
  }else{
    renderMap();
  }
}

async function loadProjectMap(force=false){
  if(cachedMap&&!force){renderProjectMap();return;}
  try{
    const r=await api('/api/map');
    cachedMap=await r.json();
  }catch(e){
    cachedMap=null;
  }
  renderProjectMap();
}

const LANE_FALLBACK_LABELS={page:'Page (Flutter)',controller:'Controller',usecase:'UseCase',state:'상태(State)',repository:'Repository',external:'API (External)',ui:'Page (Flutter)',application:'UseCase',data:'Repository'};

function renderProjectMap(){
  const el=document.getElementById('map-lanes');
  document.getElementById('map-title').textContent='Architecture Map — 전체 프로젝트';
  document.getElementById('map-sub').textContent='발행된 모든 흐름을 합쳐 이 프로젝트의 계층 구조를 요약합니다. 레인은 프로젝트 구조에서 유도됩니다.';
  if(!cachedMap||!cachedMap.lanes||!cachedMap.components){
    el.innerHTML='<div class="proj-empty">프로젝트 맵을 불러올 수 없습니다.</div>';
    return;
  }
  const lanes=cachedMap.lanes, comps=cachedMap.components||[];
  const unknown=comps.filter(c=>!lanes.some(l=>l.id===c.layer));
  let html='';
  for(const l of lanes){
    const items=comps.filter(c=>c.layer===l.id);
    html+='<div class="lane"><div class="lane-label">'+esc(l.label)+'</div><div class="proj-track">'+
      (items.length?items.map(chipHTML).join(''):'<span class="conf">구성요소 없음</span>')+
    '</div></div>';
  }
  if(unknown.length){
    html+='<div class="lane"><div class="lane-label">판단 보류</div><div class="proj-track">'+unknown.map(chipHTML).join('')+'</div></div>';
  }
  if(!lanes.length&&!comps.length){
    html='<div class="proj-empty">발행된 흐름이 없어 프로젝트 맵이 비어 있습니다.</div>';
  }
  el.innerHTML=html;
  document.querySelectorAll('[data-chip]').forEach(n=>n.addEventListener('click',()=>openExcerpt(n.dataset.chip)));
}

function chipHTML(c){
  const name=c.symbolPath.includes('#')?c.symbolPath.split('#').pop():c.symbolPath;
  const confPct=Math.round((c.confidence||0)*100);
  return '<button class="chip" data-chip="'+esc(c.symbolPath)+'" '+(c.uncertain?'data-uncertain="1"':'')+'>'+
    '<strong>'+esc(name)+'</strong>'+
    (c.signature?'<span class="sig">'+esc(c.signature)+'</span>':'<span class="sig">'+esc(c.path||'')+'</span>')+
    '<span class="meta"><span>'+c.flows.length+'개 흐름</span><span>·</span><span class="conf">'+confPct+'%</span>'+
    (c.uncertain?'<span title="계층 판단 근거 부족 — 클릭 후 재분류 가능">· 판단 보류</span>':'')+'</span>'+
  '</button>';
}

/* Selected business flow path emphasis on the project map */
function highlightFlowPath(){
  const inFlow=new Set((cachedMap&&cachedMap.components||[]).filter(c=>(c.flows||[]).includes(currentFlowId)).map(c=>c.symbolPath));
  document.querySelectorAll('[data-chip]').forEach(n=>{
    n.classList.toggle('on-path',inFlow.has(n.dataset.chip));
    n.classList.toggle('dim',currentFlowId&&inFlow.size>0&&!inFlow.has(n.dataset.chip));
  });
  document.querySelectorAll('.lane').forEach(lane=>{
    const anyOn=lane.querySelector('.chip.on-path');
    lane.classList.toggle('dim',!!anyOn?false:(currentFlowId&&inFlow.size>0));
  });
}

/* ---- Component excerpt slide-over with manual lane override ---- */

function findComponent(sym){
  return ((cachedMap&&cachedMap.components)||[]).find(c=>c.symbolPath===sym)||null;
}

async function openExcerpt(sym){
  excerptSymbol=sym;
  const c=findComponent(sym);
  const panel=document.getElementById('excerpt-panel');
  const name=sym.includes('#')?sym.split('#').pop():sym;
  document.getElementById('ex-title').textContent=name;
  document.getElementById('ex-sig').textContent=c&&c.signature?c.signature:sym;
  const pubFlows=((c&&c.flows)||[]).filter(fid=>!fid.startsWith('coverage:'));
  document.getElementById('ex-flows').innerHTML=pubFlows.map(fid=>{
    const f=cachedFlows.find(x=>x.flowId===fid);
    return '<button class="pill" onclick="loadFlow(\''+escJs(fid)+'\');closeExcerpt()">'+esc(f?f.title:fid)+'</button>';
  }).join('')||'<span class="conf">발행된 흐름 연결 없음'+(((c&&c.flows)||[]).length?' · 구조 조사 근거만 존재':'')+'</span>';
  const lanes=(currentSpec&&Array.isArray(currentSpec.lanes)&&currentSpec.lanes.length)?currentSpec.lanes:Object.entries(LANE_FALLBACK_LABELS).map(([id,label])=>({id,label}));
  document.getElementById('ex-lanes').innerHTML='<span class="conf">계층:</span>'+
    lanes.map(l=>'<button data-lane="'+esc(l.id)+'" class="'+(c&&c.layer===l.id?'current':'')+'" onclick="applyLaneOverride(\''+escJs(sym)+'\',\''+escJs(l.id)+'\')">'+esc(l.label)+'</button>').join('');
  panel.classList.add('open');

  const codeEl=document.getElementById('ex-code');
  codeEl.textContent='불러오는 중…';
  if(!c||!c.path){codeEl.textContent='파일 정보가 없어 코드를 표시할 수 없습니다.';return;}
  const start=Math.max(1,(c.line||1)-4), end=start+40;
  try{
    const r=await api('/api/source?path='+encodeURIComponent(c.path)+'&startLine='+start+'&endLine='+end+'&maxLines=60');
    const text=await r.text();
    if(excerptSymbol!==sym)return;
    const lines=text.replace(/\n$/,'').split('\n');
    codeEl.innerHTML=lines.map((ln,i)=>'<div class="line"><span class="num">'+(start+i)+'</span><span class="gut"></span><span class="src">'+esc(ln)+'</span></div>').join('');
  }catch(e){
    if(excerptSymbol===sym)codeEl.textContent='코드 로드 실패: '+e.message;
  }
}

function closeExcerpt(){
  excerptSymbol=null;
  document.getElementById('excerpt-panel').classList.remove('open');
}

async function applyLaneOverride(sym,lane){
  try{
    const r=await api('/api/map/override',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({symbol:sym,lane})});
    if(!r.ok)throw new Error(await r.text());
    cachedMap=null;
    closeExcerpt();
    await loadProjectMap(true);
    highlightFlowPath();
  }catch(e){
    alert('계층 재분류 실패: '+e.message);
  }
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
  document.getElementById('bc-flow').textContent=(s.flowId||'').slice(0,16)+'@'+((s.basisSha||'').slice(0,8)||'active');
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
    return '<button class="flow-tab'+(active?' active':'')+'" onclick="loadFlow(\''+escJs(f.flowId)+'\')" aria-current="'+(active?'true':'false')+'">'+
      '<div class="tab-title">'+esc(f.title)+'</div>'+
      (desc?'<div class="tab-desc">'+desc+'</div>':'<div class="tab-desc" style="color:var(--muted)">'+entryShort+'</div>')+
      '<div class="tab-meta"><span>'+f.stepCount+'단계</span><span style="margin-left:auto" class="badge">'+esc(f.flowId.slice(0,8))+'</span></div>'+
    '</button>';
  }).join('');
}

function renderMap(){
  const el=document.getElementById('map-lanes');
  if(!currentSpec){
    el.innerHTML='<div class="proj-empty">발행된 흐름이 없어 흐름 맵이 비어 있습니다.</div>';
    return;
  }
  const lanes=(Array.isArray(currentSpec.lanes)&&currentSpec.lanes.length?currentSpec.lanes:LAYER_ORDER.filter(l=>currentSpec.steps.some(st=>normLayer(st.layer||'usecase')===l)).map(l=>({id:l,label:LAYER_LABELS[l]})));
  document.getElementById('map-title').textContent='Architecture Map — '+((cachedFlows.find(x=>x.flowId===currentFlowId)||{}).title||'현재 흐름');
  document.getElementById('map-sub').textContent='레인은 Page → Controller → UseCase → Repository → API 순서로 코드가 외부로 전달되는 경로입니다.';
  el.innerHTML='<svg id="map-arcs"></svg>'+lanes.map(l=>{
    const inLayer=currentSpec.steps.map((st,i)=>({st,i})).filter(x=>{
      const lid=normLayer(x.st.layer||'usecase');
      return lid===l.id;
    });
    return '<div class="lane"><div class="lane-label">'+esc(l.label)+'</div><div class="lane-track" style="--cols:'+currentSpec.steps.length+'">'+
      inLayer.map(x=>{
        const conf=(x.st.layerConfidence!=null)?Math.round(x.st.layerConfidence*100)+'%':'';
        const sym=symbolName(x.st.anchor.enclosingSymbolPath)||x.st.name;
        const domain=x.st.name;
        return '<button class="node" style="grid-column:'+(x.i+1)+'" data-map-step="'+x.i+'" data-symbol="'+esc(x.st.anchor.enclosingSymbolPath||'')+'" '+(x.st.layerUncertain?'data-uncertain="1" title="계층 판단 근거가 약합니다"':'')+' data-status="'+esc(x.st.freshness)+'" aria-pressed="false"><strong>'+esc(sym)+'</strong><small>'+esc(domain)+'</small>'+(conf?'<span class="conf">'+conf+'</span>':'')+'</button>';
      }).join('')+
    '</div></div>';
  }).join('');
  document.querySelectorAll('[data-map-step]').forEach(n=>n.addEventListener('click',()=>selectStep(Number(n.dataset.mapStep))));
  drawArcs();
}

/* Cross-layer delegation arcs between map nodes (flow mode). */
function drawArcs(){
  const svg=document.getElementById('map-arcs'),host=document.getElementById('map-lanes');
  if(!svg||!host)return;
  svg.innerHTML='';
  if(mapMode!=='flow'||!currentSpec)return;
  host.style.position='relative';
  requestAnimationFrame(()=>{
    const hb=host.getBoundingClientRect();
    svg.setAttribute('width',hb.width);svg.setAttribute('height',hb.height);
    svg.setAttribute('viewBox','0 0 '+hb.width+' '+hb.height);
    const bySymbol={};
    document.querySelectorAll('[data-map-step]').forEach(n=>{
      const s=normSym(n.dataset.symbol);
      if(s&&!(s in bySymbol))bySymbol[s]=n;
    });
    let drawn=0;
    for(const e of (currentSpec.edges||[])){
      if(e.kind==='unknown_edge'||e.stepOrdinal==null)continue;
      const fromN=document.querySelector('[data-map-step="'+(e.stepOrdinal-1)+'"]');
      const toN=bySymbol[normSym(e.toSymbolPath)];
      if(!fromN||!toN||fromN===toN)continue;
      const fb=fromN.getBoundingClientRect(),tb=toN.getBoundingClientRect();
      const x1=fb.left-hb.left+fb.width/2, y1=fb.bottom-hb.top;
      const x2=tb.left-hb.left+tb.width/2, y2=tb.top-hb.top;
      if(y2<=y1+8)continue; // same lane or backwards — the pills in the detail card cover it
      const midY=(y1+y2)/2;
      const g=document.createElementNS('http://www.w3.org/2000/svg','g');
      const path=document.createElementNS('http://www.w3.org/2000/svg','path');
      path.setAttribute('d','M'+x1+' '+y1+' C'+x1+' '+midY+','+x2+' '+midY+','+x2+' '+(y2-3));
      path.setAttribute('class','arc');
      const hit=document.createElementNS('http://www.w3.org/2000/svg','path');
      hit.setAttribute('d',path.getAttribute('d'));
      hit.setAttribute('class','arc-hit');
      hit.addEventListener('click',()=>selectStep(e.stepOrdinal-1));
      const label=document.createElementNS('http://www.w3.org/2000/svg','text');
      label.setAttribute('x',(x1+x2)/2);label.setAttribute('y',midY);
      label.setAttribute('text-anchor','middle');label.setAttribute('class','arc-label');
      label.textContent=symbolName(e.toSymbolPath);
      label.addEventListener('click',()=>selectStep(e.stepOrdinal-1));
      g.appendChild(hit);g.appendChild(path);g.appendChild(label);
      svg.appendChild(g);
      drawn++;
    }
    svg.style.display=drawn?'block':'none';
  });
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
  updateEvidenceDock(st);
}

function renderRequirementAlignment(intent, semanticMap){
  const tag=document.getElementById('intent-status-tag');
  if(tag){
    tag.textContent='Intent: '+(intent&&intent.intentStatus?intent.intentStatus:'parsed');
  }
  const tbody=document.getElementById('requirement-alignment-tbody');
  if(!tbody)return;

  const criteria=(intent&&intent.acceptanceCriteria&&intent.acceptanceCriteria.length)?intent.acceptanceCriteria:[
    {id:'AC-1',text:'기본 동작 및 핵심 흐름 검증'}
  ];

  const alignments=(semanticMap&&semanticMap.requirementAlignment)?semanticMap.requirementAlignment:[];
  const alignMap=new Map();
  alignments.forEach(a=>alignMap.set(a.criterionId,a));

  tbody.innerHTML=criteria.map(c=>{
    const a=alignMap.get(c.id)||{};
    const status=a.status||'unknown';
    let statusBadge='<span class="badge" style="background:#f1f3f5;color:#495057">unknown</span>';
    if(status==='confirmed'){
      statusBadge='<span class="badge" style="background:#d3f9d8;color:#2b8a3e;font-weight:bold">confirmed</span>';
    }else if(status==='partial'){
      statusBadge='<span class="badge" style="background:#fff3bf;color:#e67700">partial</span>';
    }else if(status==='not_observed'){
      statusBadge='<span class="badge" style="background:#e9ecef;color:#868e96">not_observed</span>';
    }else if(status==='conflicting'){
      statusBadge='<span class="badge" style="background:#ffe3e3;color:#c92a2a;font-weight:bold">conflicting</span>';
    }

    const steps=(a.coveredStepRefs&&a.coveredStepRefs.length)?a.coveredStepRefs.join(', '):'—';
    const ev=(a.evidenceRefs&&a.evidenceRefs.length)?a.evidenceRefs.join(', '):'—';
    const gap=(a.missingEvidence&&a.missingEvidence.length)?a.missingEvidence.join('; '):(a.notes||'—');

    return '<tr style="border-bottom:1px solid var(--line)">'+
      '<td style="padding:6px 8px"><b>'+esc(c.id)+'</b>: '+esc(c.text)+'</td>'+
      '<td style="padding:6px 8px">'+statusBadge+'</td>'+
      '<td style="padding:6px 8px">'+esc(steps)+'</td>'+
      '<td style="padding:6px 8px;font-family:monospace">'+esc(ev)+'</td>'+
      '<td style="padding:6px 8px;color:var(--muted)">'+esc(gap)+'</td>'+
    '</tr>';
  }).join('');
}

function renderChangePulse(pulseList){
  const cnt=document.getElementById('change-pulse-count');
  const list=document.getElementById('change-pulse-list');
  if(!list)return;
  if(cnt)cnt.textContent=(pulseList?pulseList.length:0)+' changes';
  if(!pulseList||!pulseList.length){
    list.innerHTML='<li style="font-size:12px;color:var(--muted)">표시할 변경 내역이 없습니다 (active generation 기준).</li>';
    return;
  }
  list.innerHTML=pulseList.map(p=>{
    let badgeColor='#e9ecef';
    if(p.kind==='added_behavior')badgeColor='#d3f9d8';
    else if(p.kind==='changed_rule')badgeColor='#fff3bf';
    else if(p.kind==='removed_behavior')badgeColor='#ffe3e3';
    else if(p.kind==='evidence_updated')badgeColor='#e7f5ff';
    return '<li style="display:flex;align-items:center;justify-content:space-between;padding:4px 6px;border-radius:4px;background:#fff;border:1px solid var(--line)">'+
      '<div style="display:flex;align-items:center;gap:8px">'+
        '<span style="font-family:monospace;font-size:11px;color:var(--muted)">'+esc(p.time||'12:00:00')+'</span>'+
        '<span style="font-size:13px;font-weight:600">'+esc(p.summary)+'</span>'+
      '</div>'+
      '<span class="badge" style="background:'+badgeColor+'">'+esc(p.kind)+'</span>'+
    '</li>';
  }).join('');
}

async function triggerReviewMode(){
  try{
    const r=await api('/api/task/review?baseline=active&current=active');
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      alert('Review Query 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    if(d.changePulse)renderChangePulse(d.changePulse);
    if(d.requirementAlignment&&currentSpec)renderRequirementAlignment({acceptanceCriteria:[]},{requirementAlignment:d.requirementAlignment});
  }catch(e){
    console.error('triggerReviewMode error:',e);
  }
}

async function triggerImpactMode(sym){
  const symbol = sym || (document.getElementById('impact-symbol-input') ? document.getElementById('impact-symbol-input').value.trim() : '') || (selectedStep ? selectedStep.technicalName || selectedStep.name : 'HomePage.handleQuickCheckout');
  try{
    const r=await api('/api/task/impact?symbolId='+encodeURIComponent(symbol));
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      alert('Impact Query 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    renderChangeImpact(d);
  }catch(e){
    console.error('triggerImpactMode error:',e);
  }
}

function renderChangeImpact(graph){
  if(!graph)return;
  const directList=document.getElementById('direct-impact-list');
  const indirectList=document.getElementById('indirect-impact-list');
  const unresList=document.getElementById('unresolved-boundaries-list');

  if(directList){
    const items=[];
    (graph.directImpact.callers||[]).forEach(c=>items.push('<li><strong>Caller:</strong> '+esc(c.name||c.symbolPath)+'</li>'));
    (graph.directImpact.stateMutations||[]).forEach(s=>items.push('<li><strong>State:</strong> '+esc(s.targetState)+'</li>'));
    (graph.directImpact.externalEffects||[]).forEach(e=>items.push('<li><strong>Effect:</strong> '+esc(e.target)+' ('+esc(e.effectKind)+')</li>'));
    (graph.directImpact.tests||[]).forEach(t=>items.push('<li><strong>Test:</strong> '+esc(t.testSymbolPath)+'</li>'));
    directList.innerHTML=items.length?items.join(''):'<li style="color:var(--muted)">직접 영향 없음</li>';
  }

  if(indirectList){
    const items=[];
    (graph.indirectImpact.callers||[]).forEach(c=>items.push('<li><strong>Caller (Depth '+(c.depth||2)+'):</strong> '+esc(c.name||c.symbolPath)+'</li>'));
    (graph.indirectImpact.stateMutations||[]).forEach(s=>items.push('<li><strong>State:</strong> '+esc(s.targetState)+'</li>'));
    (graph.indirectImpact.externalEffects||[]).forEach(e=>items.push('<li><strong>Effect:</strong> '+esc(e.target)+'</li>'));
    indirectList.innerHTML=items.length?items.join(''):'<li style="color:var(--muted)">간접 영향 없음</li>';
  }

  if(unresList){
    const items=[];
    (graph.unresolvedBoundaries||[]).forEach(u=>items.push('<li style="color:#e03131"><strong>'+esc(u.boundaryType)+':</strong> '+esc(u.target)+' - '+esc(u.description)+'</li>'));
    unresList.innerHTML=items.length?items.join(''):'<li style="color:var(--muted)">미확인 경계 없음 (All Grounded).</li>';
  }
}

async function triggerFailureInvestigation(mode){
  const errInput = document.getElementById('failure-error-input');
  const trInput = document.getElementById('failure-trace-input');
  let url = '';
  if(mode === 'incident'){
    const traceId = (trInput ? trInput.value.trim() : '') || 'trace-inc-default';
    url = '/api/task/incident?traceId='+encodeURIComponent(traceId);
  }else{
    const errVal = (errInput ? errInput.value.trim() : '') || 'CardDeclinedException';
    url = '/api/task/debug?error='+encodeURIComponent(errVal);
  }

  try{
    const r=await api(url);
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      alert('조회 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    renderFailureInvestigation(d);
  }catch(e){
    console.error('triggerFailureInvestigation error:',e);
  }
}

function renderFailureInvestigation(trace){
  if(!trace)return;
  const tag=document.getElementById('failure-mode-tag');
  if(tag)tag.textContent=trace.mode.toUpperCase();

  const desc=document.getElementById('failure-summary-desc');
  if(desc&&trace.summary)desc.textContent=trace.summary.description;

  const st=document.getElementById('failure-last-state');
  if(st&&trace.summary)st.textContent='[최종 확인 상태: '+trace.summary.lastConfirmedState+']';

  const nodesList=document.getElementById('failure-nodes-list');
  if(nodesList){
    const items=[];
    (trace.nodes||[]).forEach(n=>{
      const statusColor = n.status==='conflicting'?'#e03131':(n.status==='runtime_observed'?'#2f9e44':'#1971c2');
      items.push('<li><strong>['+esc(n.role)+']</strong> '+esc(n.symbolPath)+' <span class="badge" style="background:'+statusColor+';color:#fff">'+esc(n.status)+'</span></li>');
    });
    nodesList.innerHTML=items.length?items.join(''):'<li style="color:var(--muted)">원인 노드 없음</li>';
  }

  const timeList=document.getElementById('failure-timeline-list');
  if(timeList){
    const items=[];
    (trace.timeline||[]).forEach(t=>{
      items.push('<li><span style="font-family:monospace;color:var(--muted)">'+esc(t.timestamp.slice(11,19))+'</span> <strong>'+esc(t.kind)+'</strong>: '+esc(t.target)+' ('+esc(t.status)+')</li>');
    });
    timeList.innerHTML=items.length?items.join(''):'<li style="color:var(--muted)">인시던트 이벤트 없음 (Debug 모드)</li>';
  }
}

async function submitProposalApproval(decision){
  const targetSym = (document.getElementById('proposal-target-symbol') ? document.getElementById('proposal-target-symbol').textContent.trim() : '') || 'HomePage.handleQuickCheckout';
  const badge = document.getElementById('approval-status-badge');
  const msg = document.getElementById('approval-result-msg');
  try{
    const r=await api('/api/semantic/approve',{
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        proposalId: 'prop-'+targetSym,
        decision: decision,
        approver: 'developer@workspace.local'
      })
    });
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      alert('승인 처리 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    if(badge){
      if(decision==='approved'){
        badge.textContent='Approved';
        badge.style.background='#ebfbee';
        badge.style.color='#2b8a3e';
      }else{
        badge.textContent='Rejected';
        badge.style.background='#fff5f5';
        badge.style.color='#c92a2a';
      }
    }
    if(msg)msg.textContent='✓ 승인 기록 생성됨: '+d.approvalId+' ('+d.decision+')';
  }catch(e){
    console.error('submitProposalApproval error:',e);
  }
}

async function exploreDomains(){
  try{
    const r=await api('/api/task/onboarding');
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      alert('도메인 탐색 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    renderDomainOverview(d);
  }catch(e){
    console.error('exploreDomains error:',e);
  }
}

function renderDomainOverview(ov){
  if(!ov)return;
  const grid=document.getElementById('domain-cards-grid');
  const totalDom=document.getElementById('onboarding-total-domains');
  const totalFlows=document.getElementById('onboarding-total-flows');
  const covRatio=document.getElementById('onboarding-coverage-ratio');

  if(totalDom&&ov.summary)totalDom.textContent=String(ov.summary.totalDomains);
  if(totalFlows&&ov.summary)totalFlows.textContent=String(ov.summary.totalFlows);
  if(covRatio&&ov.summary)covRatio.textContent=Math.round(ov.summary.coverageRatio*100)+'%';

  if(grid){
    const cards=[];
    (ov.domains||[]).forEach(d=>{
      cards.push(
        '<div style="border:1px solid var(--line);border-radius:6px;padding:10px;background:var(--paper);cursor:pointer" onclick="loadDomainCatalog(\''+esc(d.name)+'\')">'+
          '<div style="display:flex;justify-content:space-between;align-items:center">'+
            '<strong style="font-size:13px">'+esc(d.name)+'</strong>'+
            '<span class="badge" style="background:#e7f5ff;color:#1864ab">'+d.representativeFlowCount+' flows</span>'+
          '</div>'+
          '<div style="font-size:11px;color:var(--muted);margin-top:4px">'+esc(d.description)+'</div>'+
          '<div style="font-size:10px;font-family:monospace;color:var(--muted);margin-top:6px">진입점: '+esc((d.entryPoints||[]).join(', '))+'</div>'+
        '</div>'
      );
    });
    grid.innerHTML=cards.length?cards.join(''):'<div style="color:var(--muted)">도메인이 없습니다.</div>';
  }
}

function loadDomainCatalog(domainName){
  const catContainer=document.getElementById('onboarding-catalog-container');
  const list=document.getElementById('representative-flows-list');
  if(catContainer)catContainer.style.display='block';
  if(list){
    list.innerHTML=
      '<li><strong>'+esc(domainName)+' 기본 흐름:</strong> 진입점 실행 → 비즈니스 규칙 검증 → 상태 전이</li>'+
      '<li><strong>'+esc(domainName)+' 예외/대체 흐름:</strong> 오류 처리 및 트랜잭션 롤백</li>';
  }
}

async function evaluateReleaseCapability(){
  try{
    const r=await api('/api/release/capability?targetVersion=v0.9.0-rc1');
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      alert('릴리즈 역량 평가 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    renderReleaseCapability(d);
  }catch(e){
    console.error('evaluateReleaseCapability error:',e);
  }
}

function renderReleaseCapability(data){
  if(!data)return;
  const rep=data.benchmarkReport;
  const slm=data.slmCapability;

  const badge=document.getElementById('release-ready-badge');
  const lat=document.getElementById('metric-latency-p95');
  const prec=document.getElementById('metric-precision');
  const reg=document.getElementById('metric-regressions');
  const tier=document.getElementById('release-fallback-tier');

  if(badge&&rep){
    if(rep.releaseReady){
      badge.textContent='Release Ready: PASSED';
      badge.style.background='#ebfbee';
      badge.style.color='#1e602b';
    }else{
      badge.textContent='Release Ready: FAILED';
      badge.style.background='#fff5f5';
      badge.style.color='#c92a2a';
    }
  }

  if(lat&&rep&&rep.metrics)lat.textContent=rep.metrics.latencyP95Ms.toFixed(1)+' ms';
  if(prec&&rep&&rep.metrics)prec.textContent=rep.metrics.precision.toFixed(2)+' / '+rep.metrics.recall.toFixed(2);
  if(reg&&rep&&rep.metrics)reg.textContent=rep.metrics.regressionFailures+' / '+rep.metrics.contractViolations;
  if(tier&&slm)tier.textContent=slm.fallbackTier;
}
function switchEvidenceDockTab(tab){
  ['why','code','test','history'].forEach(t=>{
    const btn=document.getElementById('dock-tab-'+t);
    const pane=document.getElementById('dock-pane-'+t);
    if(btn){
      btn.className='btn-sm '+(t===tab?'active':'');
      btn.setAttribute('aria-selected',String(t===tab));
    }
    if(pane){
      pane.style.display=t===tab?'block':'none';
    }
  });
}

function updateEvidenceDock(st){
  if(!st)return;
  const whyText=document.getElementById('dock-why-text');
  if(whyText){
    const whyDesc='단계 목적: '+st.name+
      (st.stateDelta?'\n상태 변화: '+st.stateDelta.before+' → '+st.stateDelta.after:'')+
      (st.branch?'\n분기 조건: '+st.branch:'')+
      (st.sideEffect?'\n외부 효과: '+st.sideEffect:'')+
      (st.rules&&st.rules.length?'\n규칙: '+st.rules.join(', '):'');
    whyText.innerText=whyDesc;
  }
  const codeAnchor=document.getElementById('dock-code-anchor');
  if(codeAnchor&&st.anchor){
    codeAnchor.textContent=(st.anchor.repoRelativePath||'—')+' (bytes: '+(st.anchor.byteRange?st.anchor.byteRange.join('..'):'—')+')';
  }
  const testList=document.getElementById('dock-test-list');
  if(testList){
    if(st.evidenceRefs&&st.evidenceRefs.length){
      testList.innerHTML=st.evidenceRefs.map(ev=>'<li>'+esc(ev)+'</li>').join('');
    }else{
      testList.innerHTML='<li>연결된 테스트 근거가 없습니다.</li>';
    }
  }
}

function symbolName(toSymbolPath){
  return normSym(toSymbolPath);
}

/* normSym mirrors the server's normSymbol: edge targets may be
   file-qualified ("lib/a.dart#Class.method") while node keys are bare. */
function normSym(s){
  s=s||'';
  const h=s.lastIndexOf('#');
  return h>=0?s.slice(h+1):s;
}
function normLayer(id){
  if(id==='ui'||id==='page'||id==='widget')return'presentation';
  if(id==='application')return'usecase';
  if(id==='data'||id==='repository')return'data';
  if(id==='api')return'external';
  // state stays state for legacy flows; unknown stays unknown
  return id||'usecase';
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
  el.innerHTML=f.map(x=>'<div style="padding:10px 12px;border:1px solid var(--line);border-radius:7px;cursor:pointer" onclick="loadFlow(\''+escJs(x.flowId)+'\');closeSwitcher()">'+
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
  if(e.key==='Escape'){closeSwitcher();closeExcerpt();}
  if(e.key==='ArrowLeft'&&document.activeElement.tagName!=='INPUT'){
    e.preventDefault();
    prevStep();
  }
  if(e.key==='ArrowRight'&&document.activeElement.tagName!=='INPUT'){
    e.preventDefault();
    nextStep();
  }
});

window.addEventListener('resize',()=>{
  if(mapMode==='flow')drawArcs();
});

init();
</script>
</body>
</html>
`
