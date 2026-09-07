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

  <!-- Task View Feature Query Bar & Candidate Answer Strip (VS-04) -->
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

  <!-- Candidate Answer Strip. Current/confirmed authority is owned by VS-03. -->
  <section id="current-answer-strip" data-region="current-answer" style="display:none;margin-top:14px;padding:14px 16px;border:2px solid var(--ink);border-radius:8px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;flex-wrap:wrap;gap:8px">
      <span style="font-size:11px;font-weight:800;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)">Candidate Flow Result</span>
      <!-- Independent Status Axes (VS04-A8, D21) -->
      <div style="display:flex;gap:6px;align-items:center;flex-wrap:wrap">
        <span id="badge-freshness" class="badge">unknown</span>
        <span id="current-answer-stage" class="badge">awaiting proof</span>
        <span id="badge-quality" class="badge" style="display:none">unknown</span>
        <span id="badge-activity" class="badge" style="background:#f4f4f2">idle</span>
        <span id="badge-settlement" class="badge" style="background:#f4f4f2">Settlement: pending</span>
        <span id="badge-enrichment" class="badge" style="background:#f4f4f2">Enrichment: unavailable</span>
        <span id="badge-connection" class="badge" style="background:#f4f4f2">SSE: connected</span>
        <span id="current-answer-basis" style="font-size:11px;color:var(--muted)"></span>
      </div>
    </div>
    <div style="margin-bottom:4px;font-size:12px;color:var(--muted)"><b>Raw User Request:</b> <span id="current-answer-requested">—</span></div>
    <div style="margin-bottom:4px;font-size:12px;color:var(--muted)"><b>Normalized Intent Revision:</b> <span id="current-answer-intent">—</span></div>
    <div style="margin-bottom:4px;font-size:12px;color:var(--muted)"><b>Agent-reported Status:</b> <span id="current-answer-agent-status">—</span></div>
    <div style="font-size:15px;font-weight:700;line-height:1.4;color:var(--ink)" id="current-answer-statement">Evidence-backed Implementation Fact: —</div>
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
        <span id="intent-status-tag" class="badge" style="background:#f4f4f2;color:#495057">Intent: not loaded</span>
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
      <div style="display:flex;gap:6px;flex-wrap:wrap;justify-content:flex-end">
        <input id="failure-error-input" type="text" placeholder="Error" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px" />
        <input id="failure-symptom-input" type="text" placeholder="Symptom" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px" />
        <input id="failure-evidence-input" type="text" placeholder="Failure Evidence ID" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px;width:120px" />
        <button id="btn-trigger-debug" class="btn" style="font-size:11px;padding:4px 8px" onclick="triggerFailureInvestigation('debug')">오류 역추적 (Debug)</button>
        <input id="failure-trace-input" type="text" placeholder="Trace ID" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px;width:90px" />
        <input id="failure-observation-input" type="text" placeholder="Runtime Observation ID" style="font-size:11px;padding:3px 6px;border:1px solid var(--line);border-radius:4px;width:145px" />
        <button id="btn-trigger-incident" class="btn" style="font-size:11px;padding:4px 8px" onclick="triggerFailureInvestigation('incident')">인시던트 (Incident)</button>
      </div>
    </div>
    <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:6px;margin:6px 0">
      <span style="font-size:10px;color:var(--muted)">basis: <strong id="failure-basis-id">unknown</strong></span>
      <span style="font-size:10px;color:var(--muted)">generation: <strong id="failure-generation-id">unknown</strong></span>
      <span style="font-size:10px;color:var(--muted)">validated snapshot: <strong id="failure-snapshot-id">unknown</strong></span>
      <span style="font-size:10px;color:var(--muted)">freshness: <strong id="failure-freshness">unknown</strong></span>
    </div>
    <div style="display:grid;grid-template-columns:repeat(5,1fr);gap:6px;margin:6px 0">
      <input id="failure-scenario-input" type="text" placeholder="Scenario" style="font-size:10px;padding:3px 5px;border:1px solid var(--line);border-radius:4px" />
      <input id="failure-environment-input" type="text" placeholder="Environment" style="font-size:10px;padding:3px 5px;border:1px solid var(--line);border-radius:4px" />
      <input id="failure-dependency-input" type="text" placeholder="Dependency fingerprint" style="font-size:10px;padding:3px 5px;border:1px solid var(--line);border-radius:4px" />
      <input id="failure-window-from-input" type="text" placeholder="Window from (RFC3339)" style="font-size:10px;padding:3px 5px;border:1px solid var(--line);border-radius:4px" />
      <input id="failure-window-to-input" type="text" placeholder="Window to (RFC3339)" style="font-size:10px;padding:3px 5px;border:1px solid var(--line);border-radius:4px" />
    </div>
    <div id="failure-runtime-disclosure" style="font-size:10px;color:var(--muted);display:flex;gap:12px;flex-wrap:wrap;margin:6px 0">
      <span>command: <strong id="failure-command-state">not supplied</strong></span>
      <span>access: <strong id="failure-access-state">not supplied</strong></span>
      <span>isolation: <strong id="failure-isolation-state">not supplied</strong></span>
      <span>promotion: <strong id="failure-promotion-state">not evaluated</strong></span>
      <span>integrity: <strong id="failure-integrity-state">not evaluated</strong></span>
    </div>
    <div id="failure-summary-box" style="font-size:12px;color:var(--muted);margin-bottom:8px">
      <span id="failure-summary-desc">장애 발생 원인 및 타임라인을 조회할 수 있습니다.</span>
      <span id="failure-last-state" style="margin-left:8px;font-weight:bold;color:var(--text)"></span>
      <span id="failure-unknown-state" style="margin-left:8px;color:#e67700"></span>
      <span id="failure-conflict-state" style="margin-left:8px;color:#c92a2a"></span>
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
        <button id="btn-semantic-approve" name="approve" aria-label="Approve semantic proposal" class="btn" style="font-size:11px;padding:4px 10px;background:#1e602b;color:#fff" onclick="submitProposalApproval('approve')">의미 승인 (Approve)</button>
        <button id="btn-semantic-edit-then-approve" name="edit_then_approve" aria-label="Edit and approve semantic proposal" class="btn" style="font-size:11px;padding:4px 10px;background:#1864ab;color:#fff" onclick="submitProposalApproval('edit_then_approve')" disabled>수정 후 승인 (Edit then approve)</button>
        <button id="btn-semantic-reject" name="reject" aria-label="Reject semantic proposal" class="btn" style="font-size:11px;padding:4px 10px;background:#c92a2a;color:#fff" onclick="submitProposalApproval('reject')">반려 (Reject)</button>
        <button id="btn-semantic-revoke" name="revoke" aria-label="Revoke active semantic approval" class="btn" style="font-size:11px;padding:4px 10px;background:#862e9c;color:#fff" onclick="submitProposalApproval('revoke')" disabled>승인 철회 (Revoke)</button>
        <button id="btn-semantic-supersede" name="supersede" aria-label="Supersede active semantic approval" class="btn" style="font-size:11px;padding:4px 10px;background:#495057;color:#fff" onclick="submitProposalApproval('supersede')" disabled>승인 대체 (Supersede)</button>
      </div>
    </div>
    <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px">
      <label for="approval-edited-text" style="font-size:11px;font-weight:700">Edited semantic proposal text</label>
      <input id="approval-edited-text" name="editedText" aria-label="Edited semantic proposal text" type="text" maxlength="4096" disabled placeholder="Required for edit_then_approve" style="flex:1;font-size:11px;padding:4px 6px;border:1px solid var(--line);border-radius:4px" />
    </div>
    <div id="proposal-card" data-epistemic-status="unknown" style="border:1px solid var(--line);border-radius:6px;padding:10px 12px;background:var(--soft);margin-bottom:8px">
      <div style="display:flex;justify-content:space-between;font-size:12px">
        <span><strong>제안 대상:</strong> <span id="proposal-target-symbol" style="font-family:monospace">unknown</span></span>
        <span><strong>분류:</strong> <span id="proposal-category" class="badge" style="background:#f1f3f5">unknown</span></span>
      </div>
      <div style="margin-top:6px;font-size:13px;font-weight:bold" id="proposal-title">의미 제안을 확인할 수 없음</div>
      <div style="margin-top:4px;font-size:11px;color:var(--muted)" id="proposal-rationale">현재 확인된 근거가 없는 의미 제안은 표시하지 않습니다.</div>
      <div style="margin-top:6px;display:flex;gap:6px;align-items:center;font-size:10px;color:var(--muted)">
        <span id="proposal-epistemic-status" class="badge" style="background:#fff9db">unknown</span>
        <span id="proposal-authority">authority: unknown</span>
      </div>
    </div>
    <div id="enrichment-fallback" role="status" aria-live="polite" style="font-size:11px;color:#8a5a00;margin-bottom:8px">Unknown: 선택적 의미 보강을 사용할 수 없습니다. 결정적 흐름 결과를 유지합니다.</div>
    <section id="model-activation-disclosure" aria-label="Model activation disclosure 모델 활성화 공개" hidden style="border:1px solid var(--line);border-radius:6px;padding:10px 12px;background:var(--warn);margin-bottom:8px">
      <div style="font-weight:800;font-size:12px;margin-bottom:6px">Model Activation Disclosure</div>
      <div style="display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:4px 12px;font-size:11px;color:var(--muted)">
        <span>identity: <strong id="disclosure-identity">unknown</strong></span>
        <span>revision: <strong id="disclosure-revision">unknown</strong></span>
        <span>license: <strong id="disclosure-license">unknown</strong></span>
        <span>checksum: <strong id="disclosure-checksum">unknown</strong></span>
        <span>runtime: <strong id="disclosure-runtime">unknown</strong></span>
        <span>data boundary: <strong id="disclosure-data-boundary">unknown</strong></span>
      </div>
      <div style="margin-top:6px;font-size:11px;color:var(--muted)">capability change: <strong id="disclosure-capability-change">unknown</strong></div>
      <div style="margin-top:6px;display:flex;align-items:center;gap:8px;flex-wrap:wrap">
        <span id="disclosure-choice-status" role="status" aria-live="polite" style="font-size:11px;font-weight:700">Explicit choice required</span>
        <button id="btn-model-activate" class="btn" style="font-size:11px;padding:4px 10px" onclick="chooseModelActivation('activate')">Activate</button>
        <button id="btn-model-decline" class="btn" style="font-size:11px;padding:4px 10px" onclick="chooseModelActivation('decline')">Decline</button>
      </div>
    </section>
    <div id="evidence-grounding-summary" style="font-size:11px;color:var(--muted);display:flex;justify-content:space-between">
      <span>근거 팩 (Evidence Pack): <span id="evidence-pack-id" style="font-family:monospace">unavailable</span> (<span id="evidence-redaction-tag">Clean / Redacted</span>)</span>
      <span id="approval-result-msg" role="status" aria-live="polite" style="font-weight:bold;color:#1e602b"></span>
    </div>
    <section id="approval-history-panel" aria-label="Durable approval history" style="margin-top:10px;border-top:1px solid var(--line);padding-top:10px">
      <div style="display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap">
        <span id="approval-history-summary" style="font-weight:800;font-size:12px">Durable approval history</span>
        <span id="approval-history-status" role="status" aria-live="polite" class="badge" style="background:#f4f4f2">Not loaded</span>
      </div>
      <div id="approval-history-facts" style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;margin-top:6px;font-size:11px;color:var(--muted)">
        <span>state: <strong id="approval-history-state">none</strong></span>
        <span>version: <strong id="approval-history-version">0</strong></span>
        <span>freshness: <strong id="approval-history-freshness">unknown</strong></span>
      </div>
      <ol id="approval-history-events" aria-label="Ordered approval lifecycle events" style="margin:8px 0 0;padding-left:22px;font-size:11px;color:var(--muted)"></ol>
      <div id="approval-history-error" role="alert" aria-live="assertive" hidden style="margin-top:6px;font-size:11px;color:#c92a2a">Approval history could not be loaded.</div>
    </section>
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
        <div style="font-size:11px;color:var(--muted);margin-top:4px">먼저 근거가 있는 task view를 조회한 뒤 프로젝트 도메인 구조를 조회하세요.</div>
      </div>
    </div>
    <div id="onboarding-catalog-container" style="margin-top:10px;display:none;border-top:1px solid var(--line);padding-top:10px">
      <div style="font-weight:bold;font-size:12px;margin-bottom:6px">대표 흐름 카탈로그 (Level 2: Representative Flows)</div>
      <ul id="representative-flows-list" style="list-style:none;padding:0;margin:0;font-size:12px;display:flex;flex-direction:column;gap:4px"></ul>
    </div>
    <div id="onboarding-evidence-status" role="status" aria-live="polite" style="margin-top:8px;font-size:11px;color:var(--muted)">basis와 커버리지는 근거가 있는 조회 후 표시됩니다.</div>
    <div id="onboarding-error" role="alert" aria-live="assertive" style="display:none;margin-top:6px;color:#c92a2a;font-size:11px"></div>
    <div id="onboarding-summary-bar" style="margin-top:8px;font-size:11px;color:var(--muted)">
      <span>전체 도메인: <span id="onboarding-total-domains" style="font-weight:bold">0</span>개</span> |
      <span>대표 흐름: <span id="onboarding-total-flows" style="font-weight:bold">0</span>개</span> |
      <span>커버리지: <span id="onboarding-coverage-ratio" style="font-weight:bold">100%</span></span>
    </div>
  </section>

  <!-- Evidence-derived Release Capability Matrix (VS-10) -->
  <section id="release-capability-section" class="flow-tabs-section" aria-label="Release Capability 릴리즈 검증 및 역량 매트릭스" style="margin-top:14px;border:1px solid var(--line);border-radius:8px;padding:12px 16px;background:var(--paper)">
    <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:8px">
      <div style="display:flex;align-items:center;gap:8px">
        <span style="font-weight:800;font-size:13px;letter-spacing:.05em;text-transform:uppercase">Evidence-derived Release Capability Matrix</span>
        <span id="release-ready-badge" class="badge" style="background:#f4f4f2;color:#495057">Release Ready: NOT MEASURED</span>
      </div>
      <button id="btn-eval-release" class="btn" style="font-size:11px;padding:4px 10px" onclick="evaluateReleaseCapability()">릴리즈 역량 재평가 (Evaluate)</button>
    </div>
    <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-bottom:10px">
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px;background:var(--soft)">
        <div style="font-size:11px;color:var(--muted)">Activity p95</div>
        <div id="metric-latency-p95" style="font-size:14px;font-weight:bold;margin-top:2px">not measured</div>
      </div>
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px;background:var(--soft)">
        <div style="font-size:11px;color:var(--muted)">Precision / Recall</div>
        <div id="metric-precision" style="font-size:14px;font-weight:bold;margin-top:2px">not measured</div>
      </div>
      <div style="border:1px solid var(--line);border-radius:6px;padding:8px;background:var(--soft)">
        <div style="font-size:11px;color:var(--muted)">Current-or-gap p95</div>
        <div id="metric-regressions" style="font-size:14px;font-weight:bold;margin-top:2px">not measured</div>
      </div>
    </div>
    <div style="font-size:12px;margin-bottom:4px"><strong>근거 기반 역량 상태:</strong></div>
    <div id="slm-capabilities-list" style="display:flex;gap:6px;flex-wrap:wrap;font-size:11px">
      <span class="badge" style="background:#f4f4f2;color:#495057">not measured</span>
    </div>
    <div style="margin-top:8px;font-size:11px;color:var(--muted)">
      <span>평가 범위: <span id="release-fallback-tier" style="font-family:monospace;font-weight:bold">not measured</span></span>
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
          <span class="timeline-note" id="projection-summary">Projection: unknown · folds: 0 · unknown boundaries: 0</span>
          <span class="timeline-note" id="view-state-status">Selection: not measured</span>
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
// View state is measured from the rendered DOM and keyed by semantic structural
// identity. A generated step id is not a preservation key because it may change
// when a compatible generation is rebuilt.
let viewState={
  schemaId:'https://codeflow.local/schemas/rflsc.flowview-view-state.v2.schema.json',
  schemaVersion:2,
  viewId:'flow-view',
  streamId:'live-comprehension-stream',
  computedBasisId:'',
  generationId:'unpublished',
	validatedAgainstSnapshotId:'',
	proposalId:'',
	evidencePackId:'',
	intentRevision:0,
	approvalCommandId:'',
	approvalIdempotencyKey:'',
	approvalDecision:'',
	approvalPendingRequest:null,
	approvalExpectedVersion:null,
	approvalExpectedState:'',
	approvalPredecessorApprovalId:'',
	approvalVersion:0,
	approvalState:'none',
	activeApprovalId:'',
	dependencyFingerprint:'',
  repositoryId:'',
  worktreeId:'',
  displayBasis:'candidate',
  activityStatus:'idle',
  qualityStage:'Q1',
  settlement:'pending',
  enrichmentStatus:'unavailable',
  connectionStatus:'disconnected',
  selectedStepId:null,
  selectedStructuralIdentity:null,
  logicalScrollAnchor:null,
  visibleStepRefs:[],
  preservedStepRefs:[],
  corpusVersion:'rflsc-vs03-view-corpus-v1',
  currentProofVerified:false,
  lastEventSequence:0,
  identityLoss:false,
  preserved:false
};
let semanticRequestGeneration=0;
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
  return esc(s).replace(/\\/g,"\\\\").replace(/'/g,"\\'").replace(/\r/g,"\\r").replace(/\n/g,"\\n").replace(/\u2028/g,"\\u2028").replace(/\u2029/g,"\\u2029");
}

const LAYER_ORDER=['presentation','controller','usecase','domain','data','infra','external','unknown'];
const LAYER_LABELS={presentation:'프레젠테이션',controller:'컨트롤러',usecase:'유스케이스',domain:'도메인',data:'데이터',infra:'인프라',external:'외부 연동',unknown:'미분류',page:'Page (Flutter)',state:'상태(State)',repository:'Repository',ui:'Page (Flutter)',application:'UseCase'};
const KIND_LABELS={user_action:'진입',guard:'조건 확인',mutation:'상태 변경',call:'기능 실행',branch:'흐름 분기',decision:'결정',failure:'실패',security:'보안',external_effect:'외부 효과'};
const EDGE_LABELS={resolved_cross_file:'내부 위임',boundary_call:'외부 연동'};
const FRESH_LABEL={fresh:'확인됨',historical:'후보 basis',unknown:'미확인',stale:'재확인 필요',orphaned:'찾을 수 없음'};
const ENRICHMENT_STATUSES=['not_requested','pending','available','timed_out','unavailable'];

function enrichmentEnvelope(data){
  const root=data&&data.enrichment&&typeof data.enrichment==='object'?data.enrichment:{};
  const state=root.state&&typeof root.state==='object'?root.state:(data&&data.enrichmentState&&typeof data.enrichmentState==='object'?data.enrichmentState:(root.status?root:{}));
  const rawStatus=(state&&state.status)||(data&&data.enrichmentStatus)||'unavailable';
  const status=ENRICHMENT_STATUSES.includes(rawStatus)?rawStatus:'unavailable';
	return {
		status:status,
		state:state||{},
		proposal:root.proposal||((data&&data.semanticProposal)||((data&&data.proposal)||null)),
		pack:root.pack||((data&&data.pack)||null),
		fallback:root.fallback||((data&&data.fallback)||null),
    disclosure:root.disclosure||((state&&state.disclosure)||((data&&data.modelActivationDisclosure)||((data&&data.disclosure)||null)))
  };
}

function enrichmentDisplayValue(value,fallback){
  if(Array.isArray(value))return value.length?value.join(', '):fallback;
  if(value===null||value===undefined)return fallback;
  const text=String(value).trim();
  return text?text:fallback;
}

function isDisplayOnlyInferredProposal(proposal){
  if(!proposal||proposal.epistemicStatus!=='inferred')return false;
  if(proposal.authority!=='model'&&proposal.authority!=='inferred')return false;
  if(proposal.claimScope!=='display_only'&&proposal.claimScope!=='expression_only')return false;
  return !!(enrichmentDisplayValue(proposal.targetSymbolPath,'')&&enrichmentDisplayValue(proposal.proposedTitle,'')&&enrichmentDisplayValue(proposal.proposedCategory,''));
}

function setSemanticEvidencePackIdentity(packID){
  const value=typeof packID==='string'?packID.trim():'';
  viewState={...viewState,evidencePackId:value};
  const display=document.getElementById('evidence-pack-id');
  if(display)display.textContent=value||'unavailable';
}

function clearSemanticEnrichmentIdentity(){
  setSemanticEvidencePackIdentity('');
	viewState={...viewState,proposalId:'',approvalCommandId:'',approvalIdempotencyKey:'',approvalDecision:'',approvalExpectedVersion:null,approvalExpectedState:'',approvalPredecessorApprovalId:'',approvalVersion:0,approvalState:'none',activeApprovalId:''};
	viewState={...viewState,approvalPendingRequest:null};
	if(typeof clearApprovalHistoryUI==='function')clearApprovalHistoryUI();
	if(typeof syncApprovalControls==='function')syncApprovalControls();
}

function beginSemanticRequest(){
	semanticRequestGeneration+=1;
	clearSemanticEnrichmentIdentity();
	return semanticRequestGeneration;
}

function isLatestSemanticRequest(generation){
	return generation===semanticRequestGeneration;
}

const APPROVAL_HISTORY_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-history.v1.schema.json';
const APPROVAL_HISTORY_SCHEMA_VERSION=1;
const APPROVAL_EVENT_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json';
const APPROVAL_AGGREGATE_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json';
const APPROVAL_HISTORY_STATES=['none','active','rejected','revoked','superseded'];
const APPROVAL_HISTORY_DECISIONS=['approve','edit_then_approve','reject','revoke','supersede'];
const APPROVAL_HISTORY_RELATIONS=['initial','edit','reject','revoke','supersede'];
const APPROVAL_HISTORY_MAX_RECORDS=256;
let approvalHistoryModel=null;
let approvalHistoryLoadSequence=0;
let approvalHistoryMutationSequence=0;
let approvalHistoryRefreshPending=null;
const approvalHistoryNotificationLoads=new Map();

function approvalHistoryExactKeys(value,required,optional){
  if(!value||typeof value!=='object'||Array.isArray(value))return false;
  const allowed=new Set(required.concat(optional||[]));
  const keys=Object.keys(value);
  if(keys.length<required.length)return false;
  for(const key of required)if(!Object.prototype.hasOwnProperty.call(value,key))return false;
  for(const key of keys)if(!allowed.has(key))return false;
  return true;
}

function approvalHistoryValidID(value,allowEmpty=false,max=256){
  if(typeof value!=='string')return false;
  if(!allowEmpty&&value.trim()==='')return false;
  if(value.trim()!==value||Array.from(value).length>max)return false;
  for(const ch of value){
    const code=ch.codePointAt(0);
    if(code===0||code===0x7f||code<0x20||code>=0x80&&code<=0x9f||ch==='/'||ch==='\\'||value.includes('..'))return false;
  }
  return allowEmpty||value.length>0;
}

function approvalHistoryValidInteger(value,min,max){
  return Number.isSafeInteger(value)&&value>=min&&value<=max;
}

function approvalHistoryIdentity(){
  const spec=currentSpec&&typeof currentSpec==='object'?currentSpec:null;
  return {
    loadSequence:approvalHistoryLoadSequence,
    mutationSequence:approvalHistoryMutationSequence,
    requestGeneration:typeof semanticRequestGeneration==='number'?semanticRequestGeneration:0,
    proposalId:typeof viewState.proposalId==='string'?viewState.proposalId.trim():'',
    evidencePackId:typeof viewState.evidencePackId==='string'?viewState.evidencePackId.trim():'',
    computedBasisId:typeof viewState.computedBasisId==='string'?viewState.computedBasisId.trim():'',
    generationId:typeof viewState.generationId==='string'?viewState.generationId.trim():'',
    intentRevision:Number.isInteger(viewState.intentRevision)?viewState.intentRevision:0,
    validatedSnapshotId:typeof viewState.validatedAgainstSnapshotId==='string'?viewState.validatedAgainstSnapshotId.trim():'',
    mapId:spec&&typeof spec.flowId==='string'?spec.flowId.trim():'',
    workspaceId:spec&&typeof spec.workspaceId==='string'?spec.workspaceId.trim():'',
    taskId:spec&&typeof spec.taskId==='string'?spec.taskId.trim():'',
    pendingRequest:viewState.approvalPendingRequest||null
  };
}

function approvalHistoryIdentityIsCurrent(identity){
  if(!identity||identity.loadSequence!==approvalHistoryLoadSequence||identity.mutationSequence!==approvalHistoryMutationSequence)return false;
  const current=approvalHistoryIdentity();
  return identity.requestGeneration===current.requestGeneration&&identity.proposalId===current.proposalId&&identity.evidencePackId===current.evidencePackId&&identity.computedBasisId===current.computedBasisId&&identity.generationId===current.generationId&&identity.intentRevision===current.intentRevision&&identity.validatedSnapshotId===current.validatedSnapshotId&&identity.mapId===current.mapId&&identity.workspaceId===current.workspaceId&&identity.taskId===current.taskId&&identity.pendingRequest===current.pendingRequest;
}

function approvalHistoryValidText(value,max){
  return typeof value==='string'&&Array.from(value).length<=max&&value.indexOf('\u0000')<0;
}

function approvalHistoryValidTimestamp(value){
  if(!approvalHistoryValidText(value,128))return false;
  const match=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/.exec(value);
  if(!match)return false;
  const year=Number(match[1]),month=Number(match[2]),day=Number(match[3]),hour=Number(match[4]),minute=Number(match[5]),second=Number(match[6]),fraction=match[7]||'';
  if(fraction&&fraction.endsWith('0'))return false;
  const milliseconds=Number((fraction+'000').slice(0,3));
  const date=new Date(0);
  date.setUTCFullYear(year,month-1,day);
  date.setUTCHours(hour,minute,second,milliseconds);
  return date.getUTCFullYear()===year&&date.getUTCMonth()===month-1&&date.getUTCDate()===day&&date.getUTCHours()===hour&&date.getUTCMinutes()===minute&&date.getUTCSeconds()===second&&date.getUTCMilliseconds()===milliseconds;
}

function approvalHistoryTransition(state,active,event){
  if(event.decision==='approve'){
    if(state!=='none'||event.lifecycleRelation!=='initial'||Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId')||event.approvedText.trim()==='')return null;
    return {state:'active',active:event.approvalId};
  }
  if(event.decision==='edit_then_approve'){
    if(state!=='active'||event.lifecycleRelation!=='edit'||event.predecessorApprovalId!==active||event.approvedText.trim()==='')return null;
    return {state:'active',active:event.approvalId};
  }
  if(event.decision==='reject'){
    if((state!=='none'&&state!=='active')||event.lifecycleRelation!=='reject'||event.approvedText!=='')return null;
    if(state==='none'&&Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId'))return null;
    if(state==='active'&&Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId')&&event.predecessorApprovalId!==active)return null;
    return {state:'rejected',active:''};
  }
  if(event.decision==='revoke'||event.decision==='supersede'){
    if(state!=='active'||event.lifecycleRelation!==event.decision||event.predecessorApprovalId!==active||event.approvedText!=='')return null;
    return {state:event.decision==='revoke'?'revoked':'superseded',active:''};
  }
  return null;
}

function validateApprovalHistoryPayload(payload,identity){
  const topRequired=['schemaId','schemaVersion','target','events','aggregate','freshness'];
  if(!approvalHistoryExactKeys(payload,topRequired,[])||payload.schemaId!==APPROVAL_HISTORY_SCHEMA_ID||payload.schemaVersion!==APPROVAL_HISTORY_SCHEMA_VERSION||!['current','historical'].includes(payload.freshness))return null;
  const targetKeys=['workspaceId','proposalId','evidencePackId','computedBasisId','generationId','intentRevision','validatedSnapshotId','workspaceEpoch','mapId','taskId'];
  const target=payload.target;
  if(!approvalHistoryExactKeys(target,targetKeys,[]))return null;
  for(const key of ['workspaceId','proposalId','evidencePackId','computedBasisId','generationId','validatedSnapshotId','mapId','taskId'])if(!approvalHistoryValidID(target[key]))return null;
  if(!approvalHistoryValidInteger(target.intentRevision,1,1000000)||!approvalHistoryValidInteger(target.workspaceEpoch,0,Number.MAX_SAFE_INTEGER))return null;
  if(!identity||!approvalHistoryValidID(identity.workspaceId)||target.workspaceId!==identity.workspaceId||target.proposalId!==identity.proposalId||target.evidencePackId!==identity.evidencePackId)return null;
  if(identity.computedBasisId&&target.computedBasisId!==identity.computedBasisId)return null;
  if(identity.generationId&&identity.generationId!=='unpublished'&&target.generationId!==identity.generationId)return null;
  if(identity.intentRevision>0&&target.intentRevision!==identity.intentRevision)return null;
  if(identity.validatedSnapshotId&&target.validatedSnapshotId!==identity.validatedSnapshotId)return null;
  if(identity.mapId&&target.mapId!==identity.mapId)return null;
  if(identity.workspaceId&&target.workspaceId!==identity.workspaceId)return null;
  if(identity.taskId&&target.taskId!==identity.taskId)return null;

  if(!Array.isArray(payload.events)||payload.events.length<1||payload.events.length>APPROVAL_HISTORY_MAX_RECORDS-1)return null;
  const eventKeys=['schemaId','schemaVersion','eventId','approvalId','aggregateId','aggregateVersion','actorId','sessionId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','intentRevision','decision','approvedText','timestamp','lifecycleRelation'];
  const eventOptional=['predecessorApprovalId'];
  const eventIDs=new Set();
  const approvalIDs=new Set();
  for(const event of payload.events){
    if(!approvalHistoryExactKeys(event,eventKeys,eventOptional)||event.schemaId!==APPROVAL_EVENT_SCHEMA_ID||event.schemaVersion!==2)return null;
    for(const key of ['eventId','approvalId','aggregateId'])if(!approvalHistoryValidID(event[key]))return null;
    for(const key of ['actorId','sessionId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId'])if(!approvalHistoryValidText(event[key],256))return null;
    if(!approvalHistoryValidInteger(event.aggregateVersion,1,1000000000)||!approvalHistoryValidInteger(event.intentRevision,1,1000000)||!APPROVAL_HISTORY_DECISIONS.includes(event.decision)||!APPROVAL_HISTORY_RELATIONS.includes(event.lifecycleRelation)||!approvalHistoryValidText(event.approvedText,4096)||!approvalHistoryValidTimestamp(event.timestamp))return null;
    if(Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId')&&!approvalHistoryValidID(event.predecessorApprovalId))return null;
    if(event.workspaceId!==target.workspaceId||event.proposalId!==target.proposalId||event.evidencePackId!==target.evidencePackId||event.computedBasisId!==target.computedBasisId||event.generationId!==target.generationId||event.intentRevision!==target.intentRevision)return null;
    if(eventIDs.has(event.eventId)||approvalIDs.has(event.approvalId))return null;
    eventIDs.add(event.eventId);approvalIDs.add(event.approvalId);
  }

  const aggregate=payload.aggregate;
  const aggregateRequired=['schemaId','schemaVersion','aggregateId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','intentRevision','version','state','lastEventId','lastDecision','history'];
  if(!approvalHistoryExactKeys(aggregate,aggregateRequired,['activeApprovalId'])||aggregate.schemaId!==APPROVAL_AGGREGATE_SCHEMA_ID||aggregate.schemaVersion!==2)return null;
  for(const key of ['aggregateId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','lastEventId'])if(!approvalHistoryValidID(aggregate[key]))return null;
  if(!approvalHistoryValidInteger(aggregate.intentRevision,1,1000000)||!approvalHistoryValidInteger(aggregate.version,0,1000000000)||!APPROVAL_HISTORY_STATES.includes(aggregate.state)||!['none'].concat(APPROVAL_HISTORY_DECISIONS).includes(aggregate.lastDecision))return null;
  if(Object.prototype.hasOwnProperty.call(aggregate,'activeApprovalId')&&!approvalHistoryValidID(aggregate.activeApprovalId))return null;
  if(aggregate.workspaceId!==target.workspaceId||aggregate.proposalId!==target.proposalId||aggregate.evidencePackId!==target.evidencePackId||aggregate.computedBasisId!==target.computedBasisId||aggregate.generationId!==target.generationId||aggregate.intentRevision!==target.intentRevision)return null;
  if(aggregate.aggregateId!==payload.events[0].aggregateId||aggregate.version!==payload.events.length)return null;
  for(const event of payload.events)if(event.aggregateId!==aggregate.aggregateId)return null;
  if(!Array.isArray(aggregate.history)||aggregate.history.length!==payload.events.length+1||aggregate.history.length>APPROVAL_HISTORY_MAX_RECORDS)return null;
  const genesis=aggregate.history[0];
  if(!approvalHistoryExactKeys(genesis,['version','state','eventId','decision'],['approvalId'])||genesis.version!==0||genesis.state!=='none'||genesis.decision!=='none'||Object.prototype.hasOwnProperty.call(genesis,'approvalId')||!approvalHistoryValidID(genesis.eventId)||eventIDs.has(genesis.eventId))return null;
  let state='none';
  let active='';
  for(let index=0;index<payload.events.length;index+=1){
    const event=payload.events[index];
    if(event.aggregateVersion!==index+1)return null;
    const transition=approvalHistoryTransition(state,active,event);
    if(!transition)return null;
    const entry=aggregate.history[index+1];
    if(!approvalHistoryExactKeys(entry,['version','state','eventId','decision'],['approvalId'])||entry.version!==index+1||entry.eventId!==event.eventId||entry.decision!==event.decision)return null;
    if(event.decision==='approve'||event.decision==='edit_then_approve'||Object.prototype.hasOwnProperty.call(event,'approvalId')){
      if(entry.approvalId!==event.approvalId)return null;
    }else if(Object.prototype.hasOwnProperty.call(entry,'approvalId'))return null;
    if(entry.state!==transition.state)return null;
    state=transition.state;active=transition.active;
  }
  if(aggregate.state!==state||aggregate.lastEventId!==payload.events[payload.events.length-1].eventId||aggregate.lastDecision!==payload.events[payload.events.length-1].decision)return null;
  if(state==='active'){
    if(aggregate.activeApprovalId!==active)return null;
  }else if(Object.prototype.hasOwnProperty.call(aggregate,'activeApprovalId')&&aggregate.activeApprovalId!=='')return null;
  return JSON.parse(JSON.stringify(payload));
}

function approvalHistoryStateLabel(state){
  return {none:'Awaiting approval',active:'Active approval',rejected:'Rejected',revoked:'Revoked',superseded:'Superseded'}[state]||'Approval history';
}

function approvalHistoryDecisionLabel(decision){
  return {approve:'Approved',edit_then_approve:'Edited and approved',reject:'Rejected',revoke:'Revoked',supersede:'Superseded'}[decision]||'Lifecycle event';
}

function renderApprovalHistoryDisplay(events,aggregate,freshness){
  const stateEl=document.getElementById('approval-history-state');
  const versionEl=document.getElementById('approval-history-version');
  const freshnessEl=document.getElementById('approval-history-freshness');
  const statusEl=document.getElementById('approval-history-status');
  const summaryEl=document.getElementById('approval-history-summary');
  const list=document.getElementById('approval-history-events');
  const errorEl=document.getElementById('approval-history-error');
  if(stateEl)stateEl.textContent=aggregate&&APPROVAL_HISTORY_STATES.includes(aggregate.state)?aggregate.state:'none';
  if(versionEl)versionEl.textContent=aggregate&&Number.isSafeInteger(aggregate.version)?String(aggregate.version):'0';
  if(freshnessEl)freshnessEl.textContent=['current','historical'].includes(freshness)?freshness:'unknown';
  if(statusEl){statusEl.textContent=aggregate&&aggregate.version>0?'Loaded from durable history':'No durable approval history';statusEl.style.background=aggregate&&aggregate.version>0?'#ebfbee':'#f4f4f2';}
  if(summaryEl)summaryEl.textContent=aggregate&&aggregate.version>0?'Durable approval history':'No durable approval history';
  if(errorEl){errorEl.hidden=true;errorEl.textContent='';}
  if(list){
    list.textContent='';
    (Array.isArray(events)?events:[]).forEach((event,index)=>{
      const item=document.createElement('li');
      item.textContent='v'+String(Number.isSafeInteger(event.aggregateVersion)?event.aggregateVersion:index+1)+' · '+approvalHistoryDecisionLabel(event.decision);
      list.appendChild(item);
    });
  }
}

function clearApprovalHistoryUI(freshness){
  approvalHistoryModel=null;
  const value=freshness==='current'?'current':'unknown';
  renderApprovalHistoryDisplay([], {state:'none',version:0}, value);
  viewState={...viewState,approvalVersion:0,approvalState:'none',approvalExpectedVersion:0,approvalExpectedState:'none',activeApprovalId:'',approvalPredecessorApprovalId:''};
  if(typeof syncApprovalControls==='function')syncApprovalControls();
}

function setApprovalHistoryFailure(){
  const statusEl=document.getElementById('approval-history-status');
  const summaryEl=document.getElementById('approval-history-summary');
  const errorEl=document.getElementById('approval-history-error');
  if(statusEl)statusEl.textContent='Unavailable';
  if(summaryEl)summaryEl.textContent='Durable approval history';
  if(errorEl){errorEl.hidden=false;errorEl.textContent='Approval history could not be loaded.';}
}

function applyApprovalHistoryPayload(payload){
  approvalHistoryModel=JSON.parse(JSON.stringify(payload));
  const aggregate=approvalHistoryModel.aggregate;
  renderApprovalHistoryDisplay(approvalHistoryModel.events,aggregate,approvalHistoryModel.freshness);
  viewState={...viewState,approvalVersion:aggregate.version,approvalState:aggregate.state,approvalExpectedVersion:aggregate.version,approvalExpectedState:aggregate.state,activeApprovalId:aggregate.activeApprovalId||'',approvalPredecessorApprovalId:aggregate.activeApprovalId||''};
  const badge=document.getElementById('approval-status-badge');
  if(badge){badge.textContent=approvalHistoryStateLabel(aggregate.state);badge.style.background=aggregate.state==='active'?'#ebfbee':(aggregate.state==='none'?'#e7f5ff':'#fff5f5');badge.style.color=aggregate.state==='active'?'#2b8a3e':(aggregate.state==='none'?'#1864ab':'#c92a2a');}
  if(typeof syncApprovalControls==='function')syncApprovalControls();
}

async function loadApprovalHistory(expectedAggregateId=''){
  const proposalId=typeof viewState.proposalId==='string'?viewState.proposalId.trim():'';
  const evidencePackId=typeof viewState.evidencePackId==='string'?viewState.evidencePackId.trim():'';
  if(!proposalId||!evidencePackId){clearApprovalHistoryUI();return;}
  // An aggregate notification is not yet known to concern the selected
  // proposal. Supersede only that aggregate's other probes until the exact
  // durable history establishes relevance.
  const notificationToken=expectedAggregateId?{}:null;
  if(notificationToken)approvalHistoryNotificationLoads.set(expectedAggregateId,notificationToken);
  else approvalHistoryLoadSequence+=1;
  const identity=approvalHistoryIdentity();
  const loadIsCurrent=()=>approvalHistoryIdentityIsCurrent(identity)&&(!notificationToken||approvalHistoryNotificationLoads.get(expectedAggregateId)===notificationToken);
  const statusEl=document.getElementById('approval-history-status');
  const errorEl=document.getElementById('approval-history-error');
  if(!expectedAggregateId){
    if(statusEl)statusEl.textContent='Loading durable history';
    if(errorEl)errorEl.hidden=true;
  }
  try{
    const r=await api('/api/semantic/approval-history?proposalId='+encodeURIComponent(proposalId)+'&evidencePackId='+encodeURIComponent(evidencePackId),{allowErrors:true});
    if(!loadIsCurrent())return;
    let payload=null;
    try{payload=await r.json();}catch(_){payload=null;}
    if(!loadIsCurrent())return;
    if(!r.ok){
      if(r.status===404&&payload&&payload.code==='approval_unavailable'){if(!expectedAggregateId)clearApprovalHistoryUI();}
      else setApprovalHistoryFailure();
      return;
    }
    const validated=validateApprovalHistoryPayload(payload,identity);
    if(!validated||!loadIsCurrent()){if(loadIsCurrent())setApprovalHistoryFailure();return;}
    if(expectedAggregateId&&validated.aggregate.aggregateId!==expectedAggregateId)return;
    if(notificationToken)approvalHistoryLoadSequence+=1;
    applyApprovalHistoryPayload(validated);
  }catch(_){
    if(loadIsCurrent())setApprovalHistoryFailure();
  }finally{
    if(notificationToken&&approvalHistoryNotificationLoads.get(expectedAggregateId)===notificationToken)approvalHistoryNotificationLoads.delete(expectedAggregateId);
  }
}

function requestApprovalHistoryRefresh(expectedAggregateId=''){
  const identity=approvalHistoryIdentity();
  if(!identity.proposalId||!identity.evidencePackId)return;
  if(expectedAggregateId&&approvalHistoryModel&&approvalHistoryModel.aggregate.aggregateId!==expectedAggregateId)return;
  if(viewState.approvalPendingRequest){
    approvalHistoryRefreshPending={proposalId:identity.proposalId,evidencePackId:identity.evidencePackId,requestGeneration:identity.requestGeneration};
    return;
  }
  approvalHistoryRefreshPending=null;
  loadApprovalHistory(expectedAggregateId);
}

function appendApprovalHistoryProjection(payload){
  if(!payload||!Array.isArray(payload.events)||!payload.aggregate)return false;
  const identity=approvalHistoryIdentity();
  const validated=validateApprovalHistoryPayload(payload,identity);
  if(!validated||!approvalHistoryIdentityIsCurrent(identity))return false;
  approvalHistoryMutationSequence+=1;
  applyApprovalHistoryPayload(validated);
  return true;
}

function appendApprovalHistoryLocalReceipt(result){
  if(!result||!result.receipt||!result.receipt.event||!result.receipt.aggregate)return false;
  const event=result.receipt.event;
  const aggregate=result.receipt.aggregate;
  if(aggregate.version!==1||event.aggregateVersion!==1||event.decision!=='approve'||event.lifecycleRelation!=='initial'||Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId')||event.approvedText.trim()===''||aggregate.state!=='active'||aggregate.lastEventId!==event.eventId||aggregate.lastDecision!=='approve'||aggregate.activeApprovalId!==event.approvalId)return false;
  if(!Array.isArray(aggregate.history)||aggregate.history.length!==2)return false;
  const genesis=aggregate.history[0];
  const tail=aggregate.history[1];
  if(!approvalExecutionExactKeys(genesis,['version','state','eventId','decision'],[])||genesis.version!==0||genesis.state!=='none'||genesis.decision!=='none'||!approvalExecutionValidID(genesis.eventId))return false;
  if(!approvalExecutionExactKeys(tail,['version','state','eventId','decision'],['approvalId'])||tail.version!==1||tail.state!=='active'||tail.eventId!==event.eventId||tail.decision!=='approve'||!Object.prototype.hasOwnProperty.call(tail,'approvalId')||tail.approvalId!==event.approvalId)return false;
  const spec=typeof currentSpec!=='undefined'&&currentSpec&&typeof currentSpec==='object'?currentSpec:null;
  const mapId=spec&&typeof spec.flowId==='string'?spec.flowId.trim():'';
  const taskId=spec&&typeof spec.taskId==='string'?spec.taskId.trim():'';
  if(!approvalHistoryValidID(mapId)||!approvalHistoryValidID(taskId))return false;
  const hasWorkspaceEpoch=spec&&Object.prototype.hasOwnProperty.call(spec,'workspaceEpoch');
  const workspaceEpoch=hasWorkspaceEpoch?spec.workspaceEpoch:0;
  if(!approvalHistoryValidInteger(workspaceEpoch,0,Number.MAX_SAFE_INTEGER))return false;
  const payload={
    schemaId:APPROVAL_HISTORY_SCHEMA_ID,
    schemaVersion:APPROVAL_HISTORY_SCHEMA_VERSION,
    target:{workspaceId:event.workspaceId,proposalId:event.proposalId,evidencePackId:event.evidencePackId,computedBasisId:event.computedBasisId,generationId:event.generationId,intentRevision:event.intentRevision,validatedSnapshotId:result.validatedSnapshotId,workspaceEpoch:workspaceEpoch,mapId:mapId,taskId:taskId},
    events:[event],
    aggregate:aggregate,
    freshness:result.freshness==='historical'?'historical':'current'
  };
  const identity=approvalHistoryIdentity();
  const validated=validateApprovalHistoryPayload(payload,identity);
  if(!validated||!approvalHistoryIdentityIsCurrent(identity))return false;
  approvalHistoryMutationSequence+=1;
  applyApprovalHistoryPayload(validated);
  return true;
}

function appendApprovalHistoryReceipt(result){
  if(!result||arguments.length!==1||typeof approvalExecutionExactKeys!=='function'||typeof APPROVAL_VALIDATED_EXECUTION_RESULTS==='undefined'||!APPROVAL_VALIDATED_EXECUTION_RESULTS||typeof APPROVAL_VALIDATED_EXECUTION_RESULTS.has!=='function'||!APPROVAL_VALIDATED_EXECUTION_RESULTS.has(result)||!approvalExecutionExactKeys(result,['receipt','generationId','computedBasisId','validatedSnapshotId','intentRevision','freshness'],[])||!result.receipt||!approvalExecutionExactKeys(result.receipt,['idempotencyResult','event','aggregate','outbox','replayed'],[])||!result.receipt.event||!result.receipt.aggregate)return false;
  const event=result.receipt.event;
  const aggregate=result.receipt.aggregate;
  if(!event||!aggregate)return false;
  if(!approvalHistoryModel||!Array.isArray(approvalHistoryModel.events))return appendApprovalHistoryLocalReceipt(result);
  const identity=approvalHistoryIdentity();
  if(!approvalHistoryIdentityIsCurrent(identity))return false;
  const existingAggregate=approvalHistoryModel.aggregate&&typeof approvalHistoryModel.aggregate==='object'?approvalHistoryModel.aggregate:null;
  const existingEvent=approvalHistoryModel.events[approvalHistoryModel.events.length-1];
  const sameCommittedEvent=existingAggregate&&existingEvent&&existingAggregate.version===aggregate.version&&existingAggregate.aggregateId===aggregate.aggregateId&&existingAggregate.state===aggregate.state&&existingAggregate.lastEventId===event.eventId&&existingAggregate.lastDecision===event.decision&&existingEvent.eventId===event.eventId&&existingEvent.approvalId===event.approvalId&&existingEvent.aggregateVersion===event.aggregateVersion&&existingEvent.decision===event.decision;
  if(sameCommittedEvent){
    if(approvalHistoryModel.freshness===result.freshness)return true;
    const freshnessCandidate={...approvalHistoryModel,freshness:result.freshness};
    const freshnessValidated=validateApprovalHistoryPayload(freshnessCandidate,identity);
    if(!freshnessValidated||!approvalHistoryIdentityIsCurrent(identity))return false;
    approvalHistoryMutationSequence+=1;
    applyApprovalHistoryPayload(freshnessValidated);
    return true;
  }
  const candidate={...approvalHistoryModel,events:approvalHistoryModel.events.concat([event]),aggregate:aggregate,freshness:result.freshness};
  const validated=validateApprovalHistoryPayload(candidate,identity);
  if(!validated||!approvalHistoryIdentityIsCurrent(identity))return false;
  approvalHistoryMutationSequence+=1;
  applyApprovalHistoryPayload(validated);
  return true;
}

function renderSemanticEnrichment(data){
  const enrichment=enrichmentEnvelope(data);
  const status=enrichment.status;
	const durablePackID=status==='available'&&enrichment.pack&&typeof enrichment.pack.evidencePackId==='string'?enrichment.pack.evidencePackId:'';
	setSemanticEvidencePackIdentity(durablePackID);
	viewState={...viewState,enrichmentStatus:status,
		proposalId:enrichment.proposal&&typeof enrichment.proposal.proposalId==='string'?enrichment.proposal.proposalId:'',
		evidencePackId:viewState.evidencePackId};

  const badge=document.getElementById('badge-enrichment');
  if(badge){
    badge.textContent='Enrichment: '+status;
    badge.style.background=status==='available'?'#d3f9d8':(status==='pending'?'#fff9db':'#f4f4f2');
  }

  const card=document.getElementById('proposal-card');
  const proposal=enrichment.proposal;
  const inferred=isDisplayOnlyInferredProposal(proposal);
  const fallback=enrichment.fallback;
  const fallbackReason=enrichmentDisplayValue(fallback&&fallback.reason,enrichmentDisplayValue(enrichment.state&&enrichment.state.reason,'optional semantic enrichment is unavailable'));
  const target=document.getElementById('proposal-target-symbol');
  const category=document.getElementById('proposal-category');
  const title=document.getElementById('proposal-title');
  const rationale=document.getElementById('proposal-rationale');
  const epistemic=document.getElementById('proposal-epistemic-status');
  const authority=document.getElementById('proposal-authority');

  if(card){
    card.dataset.epistemicStatus=inferred?'inferred':'unknown';
    card.setAttribute('aria-label',inferred?'Inferred semantic proposal':'Unknown semantic proposal state');
  }
  if(inferred){
    if(target)target.textContent=enrichmentDisplayValue(proposal.targetSymbolPath,'unknown');
    if(category)category.textContent=enrichmentDisplayValue(proposal.proposedCategory,'unknown');
    if(title)title.textContent=enrichmentDisplayValue(proposal.proposedTitle,'semantic proposal');
    if(rationale)rationale.textContent=enrichmentDisplayValue(proposal.rationale||proposal.proposedRationale,'Grounded in current verified evidence.');
    if(epistemic)epistemic.textContent='inferred';
    if(authority)authority.textContent='authority: '+enrichmentDisplayValue(proposal.authority,'model');
  }else{
    if(target)target.textContent='unknown';
    if(category)category.textContent='unknown';
    if(title)title.textContent=fallback&&fallback.title?enrichmentDisplayValue(fallback.title,'semantic proposal unavailable'):'의미 제안을 확인할 수 없음';
    if(rationale)rationale.textContent=fallbackReason;
    if(epistemic)epistemic.textContent='unknown';
    if(authority)authority.textContent='authority: unknown';
  }

  const fallbackEl=document.getElementById('enrichment-fallback');
  if(fallbackEl){
    if(inferred){
      fallbackEl.style.display='none';
    }else{
      const prefix=status==='timed_out'?'Timed out: ':((status==='pending')?'Pending: ':'Unknown: ');
      fallbackEl.textContent=prefix+fallbackReason+'. 결정적 흐름 결과를 유지합니다.';
      fallbackEl.style.display='block';
    }
  }

  const disclosure=enrichment.disclosure;
  const disclosureEl=document.getElementById('model-activation-disclosure');
  const hasDisclosure=!!(disclosure&&disclosure.modelId&&disclosure.revision&&disclosure.license&&disclosure.checksum&&disclosure.runtime&&disclosure.dataBoundary);
  if(disclosureEl){
    disclosureEl.hidden=!hasDisclosure;
    disclosureEl.style.display=hasDisclosure?'block':'none';
  }
  if(hasDisclosure){
    const disclosureFields=[
      ['disclosure-identity',disclosure.modelId],
      ['disclosure-revision',disclosure.revision],
      ['disclosure-license',disclosure.license],
      ['disclosure-checksum',disclosure.checksum],
      ['disclosure-runtime',disclosure.runtime],
      ['disclosure-data-boundary',disclosure.dataBoundary]
    ];
    disclosureFields.forEach(([id,value])=>{const el=document.getElementById(id);if(el)el.textContent=enrichmentDisplayValue(value,'unknown');});
    const change=document.getElementById('disclosure-capability-change');
    if(change)change.textContent=enrichmentDisplayValue(disclosure.capabilityChange,'none declared');
    const choice=document.getElementById('disclosure-choice-status');
    const activate=document.getElementById('btn-model-activate');
    const decline=document.getElementById('btn-model-decline');
    const choiceValue=enrichmentDisplayValue(disclosure.choice,'');
    if(choice)choice.textContent=choiceValue==='activate'?'Activation choice recorded':(choiceValue==='decline'?'Activation declined':'Explicit choice required');
    if(activate)activate.disabled=choiceValue==='decline';
    if(decline)decline.disabled=choiceValue==='activate';
  }
}

function isCoreStep(st){
  if(!st)return false;
  if(st.isPreserved)return true;
  if(st.kind==='user_action'||st.kind==='guard'||st.kind==='branch'||st.kind==='decision'||st.kind==='failure'||st.kind==='security'||st.kind==='external_effect')return true;
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
      if(epoch&&Number.isInteger(d.workspaceEpoch)){
        epoch.textContent='['+d.workspaceEpoch+']';
      }
      if(pending){
        pending.textContent=(Number.isInteger(d.pendingRevisions)?d.pendingRevisions:'unknown')+' pending';
      }
      if(lag){
        lag.textContent=(Number.isFinite(d.analysisLagMs)?d.analysisLagMs:'unknown')+'ms lag';
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

function approvalRecoveryDataIsValid(data){
  if(!approvalHistoryExactKeys(data,['activity','activePointer','activeManifest'],['verifiedGap','proofError']))return false;
  if(Object.prototype.hasOwnProperty.call(data,'proofError')&&typeof data.proofError!=='string')return false;
  if(Object.prototype.hasOwnProperty.call(data,'verifiedGap')&&(!data.verifiedGap||typeof data.verifiedGap!=='object'||Array.isArray(data.verifiedGap)))return false;
  const activity=data.activity;
  if(!approvalHistoryExactKeys(activity,['schemaId','schemaVersion','activity','analysisLagMs','pendingRevisions','currentSnapshotId','workspaceEpoch','timestamp'],['traceId','scope','ackLatencyMs','revisionIds','capturedAt','acknowledgedAt','analysisStartedAt','currentOrGapAt','activityLatencyMs','currentOrGapLatencyMs','metricsMeasured']))return false;
  if(activity.schemaId!=='https://codeflow.local/schemas/rflsc.activity-state.v2.schema.json'||activity.schemaVersion!==2||!['idle','editing','analyzing','publishing','reconciling'].includes(activity.activity))return false;
  for(const key of ['analysisLagMs','pendingRevisions','workspaceEpoch'])if(!approvalHistoryValidInteger(activity[key],0,Number.MAX_SAFE_INTEGER))return false;
  if(!approvalHistoryValidID(activity.currentSnapshotId,true)||!approvalHistoryValidTimestamp(activity.timestamp))return false;
  if(Object.prototype.hasOwnProperty.call(activity,'traceId')&&!approvalHistoryValidID(activity.traceId,true))return false;
  if((activity.activity!=='idle'||activity.pendingRevisions>0)&&(!activity.traceId||!activity.currentSnapshotId))return false;
  for(const key of ['scope','revisionIds'])if(Object.prototype.hasOwnProperty.call(activity,key)&&(!Array.isArray(activity[key])||activity[key].some(value=>typeof value!=='string'||!value)||new Set(activity[key]).size!==activity[key].length))return false;
  for(const key of ['capturedAt','acknowledgedAt','analysisStartedAt','currentOrGapAt'])if(Object.prototype.hasOwnProperty.call(activity,key)&&!approvalHistoryValidTimestamp(activity[key]))return false;
  for(const key of ['activityLatencyMs','currentOrGapLatencyMs'])if(Object.prototype.hasOwnProperty.call(activity,key)&&!approvalHistoryValidInteger(activity[key],-1,Number.MAX_SAFE_INTEGER))return false;
  if(Object.prototype.hasOwnProperty.call(activity,'ackLatencyMs')&&(!Number.isFinite(activity.ackLatencyMs)||activity.ackLatencyMs<0))return false;
  if(Object.prototype.hasOwnProperty.call(activity,'metricsMeasured')&&typeof activity.metricsMeasured!=='boolean')return false;
  // Recovery proof artifacts are not adopted as approval authority. Check
  // their identity relationship, then query the selected durable history.
  const pointer=data.activePointer,manifest=data.activeManifest;
  if(pointer===null||manifest===null)return pointer===null&&manifest===null;
  if(!pointer||typeof pointer!=='object'||Array.isArray(pointer)||!manifest||typeof manifest!=='object'||Array.isArray(manifest))return false;
  if(pointer.schemaId!=='https://codeflow.local/schemas/rflsc.active-pointer.v2.schema.json'||pointer.schemaVersion!==2||manifest.schemaId!=='https://codeflow.local/schemas/rflsc.generation-proof-manifest.v2.schema.json'||manifest.schemaVersion!==2)return false;
  return approvalHistoryValidID(pointer.generationId)&&/^[a-f0-9]{64}$/.test(pointer.computedBasisId)&&approvalHistoryValidID(pointer.validatedAgainstSnapshotId)&&pointer.generationId===manifest.generationId&&pointer.computedBasisId===manifest.computedBasisId&&pointer.validatedAgainstSnapshotId===manifest.validatedAgainstSnapshotId;
}

function validatedApprovalStreamEnvelope(event,type){
  let env;
  try{env=JSON.parse(event.data);}catch(_){return null;}
  if(!approvalHistoryExactKeys(env,['schemaId','schemaVersion','streamId','sequence','eventId','eventType','occurredAt','data'],['computedBasisId','validatedAgainstSnapshotId','generationId','payloadRef']))return null;
  if(env.schemaId!=='https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json'||env.schemaVersion!==2||env.eventType!==type||!approvalHistoryValidID(env.streamId)||!approvalHistoryValidID(env.eventId)||event.lastEventId!==env.eventId||!approvalHistoryValidInteger(env.sequence,1,Number.MAX_SAFE_INTEGER)||!approvalHistoryValidTimestamp(env.occurredAt))return null;
  if(!env.data||typeof env.data!=='object'||Array.isArray(env.data))return null;
  if(Object.prototype.hasOwnProperty.call(env,'payloadRef')&&env.payloadRef!==null&&(typeof env.payloadRef!=='string'||!env.payloadRef))return null;
  if(Object.prototype.hasOwnProperty.call(env,'computedBasisId')&&env.computedBasisId!==null&&(typeof env.computedBasisId!=='string'||!/^[a-f0-9]{64}$/.test(env.computedBasisId)))return null;
  if(Object.prototype.hasOwnProperty.call(env,'validatedAgainstSnapshotId')&&env.validatedAgainstSnapshotId!==null&&!approvalHistoryValidID(env.validatedAgainstSnapshotId))return null;
  if(Object.prototype.hasOwnProperty.call(env,'generationId')&&env.generationId!==null&&typeof env.generationId!=='string')return null;
  const previous=Number.isSafeInteger(viewState.lastEventSequence)?viewState.lastEventSequence:0;
  if(env.sequence<previous||type==='approval.updated'&&env.sequence===previous&&lastSeenEventId!=='snapshot-sync-empty')return null;
  if(type==='snapshot_sync')return approvalRecoveryDataIsValid(env.data)?env:null;
  const data=env.data;
  if(!approvalHistoryExactKeys(data,['approvalEvent','computedBasisId','validatedAgainstSnapshotId','generationId','committedAt']))return null;
  if(!/^[a-f0-9]{64}$/.test(data.computedBasisId)||!approvalHistoryValidID(data.validatedAgainstSnapshotId)||!approvalHistoryValidID(data.generationId)||!approvalHistoryValidTimestamp(data.committedAt))return null;
  const orderedTimestamp=value=>value.slice(0,19)+'.'+(value.slice(19,-1).replace(/^\./,'')).padEnd(9,'0');
  if(orderedTimestamp(env.occurredAt)<orderedTimestamp(data.committedAt))return null;
  if(env.computedBasisId!==data.computedBasisId||env.validatedAgainstSnapshotId!==data.validatedAgainstSnapshotId||env.generationId!==data.generationId)return null;
  const committed=data.approvalEvent;
  if(!approvalHistoryExactKeys(committed,['eventId','approvalId','aggregateId','aggregateVersion','workspaceId','decision','payloadDigest']))return null;
  for(const key of ['eventId','approvalId','aggregateId','workspaceId'])if(!approvalHistoryValidID(committed[key]))return null;
  if(!approvalHistoryValidInteger(committed.aggregateVersion,1,1000000000)||!APPROVAL_HISTORY_DECISIONS.includes(committed.decision)||typeof committed.payloadDigest!=='string'||!/^sha256:[a-f0-9]{64}$/.test(committed.payloadDigest))return null;
  return env;
}

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
    viewState={...viewState,connectionStatus:st};
    setViewStateStatus(viewState);
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
        if(Number.isInteger(env.sequence))viewState={...viewState,lastEventSequence:env.sequence};
        updateWorkspaceActivityUI(env.data||env);
      }catch(err){console.error(err);}
    });

    liveEventSource.addEventListener('generation.published',e=>{
      if(e.lastEventId)lastSeenEventId=e.lastEventId;
      try{
        const env=JSON.parse(e.data);
        if(Number.isInteger(env.sequence))viewState={...viewState,lastEventSequence:env.sequence};
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
        if(Number.isInteger(env.sequence))viewState={...viewState,lastEventSequence:env.sequence,displayBasis:'last_verified',currentProofVerified:false};
        showVerifiedGap(env.data||env);
      }catch(err){console.error(err);}
    });

    liveEventSource.addEventListener('approval.updated',e=>{
      const env=validatedApprovalStreamEnvelope(e,'approval.updated');
      if(!env)return;
      const identity=approvalHistoryIdentity();
      if(env.data.approvalEvent.workspaceId!==identity.workspaceId)return;
      lastSeenEventId=env.eventId;
      viewState={...viewState,lastEventSequence:env.sequence};
      if(env.computedBasisId!==identity.computedBasisId||env.generationId!==identity.generationId||env.validatedAgainstSnapshotId!==identity.validatedSnapshotId)return;
      requestApprovalHistoryRefresh(env.data.approvalEvent.aggregateId);
    });

    liveEventSource.addEventListener('snapshot_sync',e=>{
      const env=validatedApprovalStreamEnvelope(e,'snapshot_sync');
      if(!env)return;
      lastSeenEventId=env.eventId;
      viewState={...viewState,lastEventSequence:env.sequence};
      if(env.data.activity)updateWorkspaceActivityUI(env.data.activity);
      requestApprovalHistoryRefresh();
    });
  }catch(e){
    console.error('EventSource init error:',e);
    setConn('disconnected');
  }
}

function updateWorkspaceActivityUI(act){
  if(!act)return;
  if(typeof act.activity==='string')viewState={...viewState,activityStatus:act.activity};
  const badge=document.getElementById('workspace-activity-badge');
  const activityBadge=document.getElementById('badge-activity');
  if(badge)badge.textContent=act.activity||'idle';
  if(activityBadge)activityBadge.textContent=act.activity||'idle';

  const pending=document.getElementById('workspace-pending-count');
  if(pending)pending.textContent='pending: '+(Number.isInteger(act.pendingRevisions)?act.pendingRevisions:'unknown');

  const lag=document.getElementById('workspace-analysis-lag');
  if(lag)lag.textContent='lag: '+(Number.isFinite(act.analysisLagMs)?act.analysisLagMs:'unknown')+'ms';

  const scope=document.getElementById('workspace-scope-tag');
  if(scope)scope.textContent=(Array.isArray(act.scope)&&act.scope.length)?act.scope.join(', '):'none';
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
  if(lagEl)lagEl.textContent=String(Number.isFinite(gap.analysisLagMs)?gap.analysisLagMs:'unknown');
  if(pendingEl)pendingEl.textContent=String(Number.isInteger(gap.pendingRevisions)?gap.pendingRevisions:'unknown');
}

function hideVerifiedGap(){
  const banner=document.getElementById('verified-gap-banner');
  if(banner)banner.style.display='none';
}

function chooseModelActivation(choice){
  if(choice!=='activate'&&choice!=='decline')return;
  const status=document.getElementById('disclosure-choice-status');
  const activate=document.getElementById('btn-model-activate');
  const decline=document.getElementById('btn-model-decline');
  if(status)status.textContent=choice==='activate'?'Activation choice recorded':'Activation declined';
  if(activate)activate.disabled=choice==='decline';
  if(decline)decline.disabled=choice==='activate';
  window.__codeflowModelActivationChoice=choice;
}

async function handleSemanticQuery(event,preserveSelection=false){
  if(event)event.preventDefault();
  const input=document.getElementById('query-input');
  const query=(input?input.value:'').trim();
	if(!query){
		beginSemanticRequest();
		return;
	}
	const requestGeneration=beginSemanticRequest();

  const dialog=document.getElementById('disambiguation-dialog');
  if(dialog)dialog.style.display='none';

  try{
    const r=await api('/api/task/view?query='+encodeURIComponent(query)+'&mode=feature',{allowErrors:true});
	if(!isLatestSemanticRequest(requestGeneration))return;
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      if(!isLatestSemanticRequest(requestGeneration))return;
      clearSemanticEnrichmentIdentity();
      if(err.code==='ambiguous_target'&&err.candidateTargets){
        showDisambiguation(err.candidateTargets);
        return;
      }
      alert(err.message||'질의 처리 실패');
      return;
    }
    const d=await r.json();
	if(!isLatestSemanticRequest(requestGeneration))return;
    renderSemanticTaskView(d,preserveSelection);
  }catch(e){
	if(!isLatestSemanticRequest(requestGeneration))return;
    clearSemanticEnrichmentIdentity();
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
	const requestGeneration=beginSemanticRequest();
  try{
    const r=await api('/api/task/view?entrySymbol='+encodeURIComponent(entrySymbol)+'&mode=feature',{allowErrors:true});
	if(!isLatestSemanticRequest(requestGeneration))return;
    if(r.ok){
      const d=await r.json();
	  if(!isLatestSemanticRequest(requestGeneration))return;
      renderSemanticTaskView(d,false);
    }else{
      clearSemanticEnrichmentIdentity();
    }
  }catch(e){
	if(!isLatestSemanticRequest(requestGeneration))return;
    clearSemanticEnrichmentIdentity();
    console.error(e);
  }
}

function structuralIdentityOf(st){
  const value=st&&st.structuralIdentity;
  return typeof value==='string'&&value.trim()?value:null;
}

function captureLogicalViewState(){
  if(!currentSpec||!Array.isArray(currentSpec.steps)||!currentSpec.steps.length){
    return {...viewState,selectedStepId:null,selectedStructuralIdentity:null,logicalScrollAnchor:null,identityLoss:false,preserved:false};
  }
  const st=currentSpec.steps[selected]||null;
  const identity=structuralIdentityOf(st);
  const list=document.getElementById('timeline-list');
  const item=list&&list.querySelector('[data-hstep="'+selected+'"]');
  const offsetPx=item&&list?Math.max(0,item.offsetTop-list.scrollTop):0;
  return {
    ...viewState,
    selectedStepId:st&&st.stepId?st.stepId:null,
    selectedStructuralIdentity:identity,
    logicalScrollAnchor:identity&&st&&st.stepId?{stepId:st.stepId,structuralIdentity:identity,offsetPx}:null,
    identityLoss:false,
    preserved:false
  };
}

function setViewStateStatus(state){
  if(state)window.__codeflowViewState=JSON.parse(JSON.stringify(state));
  const el=document.getElementById('view-state-status');
  if(!el)return;
  if(state&&state.identityLoss){
    el.textContent='Selection: identity lost · fallback excluded';
  }else if(state&&state.preserved){
    el.textContent='Selection: preserved · scroll anchor restored';
  }else{
    el.textContent='Selection: not measured';
  }
}

function restoreLogicalViewState(previous){
  if(!previous||!currentSpec||!Array.isArray(currentSpec.steps))return;
  const identity=previous.selectedStructuralIdentity;
  if(!identity)return;
  const matches=currentSpec.steps.map((st,index)=>({st,index})).filter(item=>structuralIdentityOf(item.st)===identity);
  if(matches.length!==1){
    viewState={...viewState,selectedStepId:null,selectedStructuralIdentity:identity,logicalScrollAnchor:null,identityLoss:true,preserved:false};
    setViewStateStatus(viewState);
    return;
  }
  const matchIdx=matches[0].index;
  selected=matchIdx;
  selectStep(matchIdx,false);
  viewState={...viewState,selectedStepId:currentSpec.steps[matchIdx].stepId||null,selectedStructuralIdentity:identity,logicalScrollAnchor:null,preserved:false,identityLoss:false};
  const anchor=previous.logicalScrollAnchor;
  const list=document.getElementById('timeline-list');
  const item=list&&list.querySelector('[data-hstep="'+matchIdx+'"]');
  if(list&&item&&anchor&&anchor.structuralIdentity===identity&&Number.isFinite(anchor.offsetPx)&&anchor.offsetPx>=0){
    list.scrollTop=Math.max(0,item.offsetTop-anchor.offsetPx);
    viewState={...viewState,logicalScrollAnchor:{stepId:currentSpec.steps[matchIdx].stepId,structuralIdentity:identity,offsetPx:anchor.offsetPx},preserved:true};
  }
  setViewStateStatus(viewState);
}

function updateViewStateGeneration(data){
  const map=data&&data.semanticMap?data.semanticMap:null;
  const projection=data&&data.projection?data.projection:null;
  const enrichment=enrichmentEnvelope(data);
  const quality=map&&map.quality&&map.quality.stage;
  const proofVerified=data&&data.currentProofVerified===true;
  viewState={
    ...viewState,
    computedBasisId:map&&map.computedBasisId?map.computedBasisId:'',
    generationId:map&&map.generationId?map.generationId:'unpublished',
    validatedAgainstSnapshotId:map&&map.validatedAgainstSnapshotId?map.validatedAgainstSnapshotId:'',
    intentRevision:map&&map.task&&Number.isInteger(map.task.intentRevision)?map.task.intentRevision:(data&&data.taskIntent&&Number.isInteger(data.taskIntent.revision)?data.taskIntent.revision:0),
    dependencyFingerprint:map&&map.basis&&map.basis.dependencyFingerprint?map.basis.dependencyFingerprint:'',
    repositoryId:map&&map.basis&&map.basis.repositoryId?map.basis.repositoryId:'',
    worktreeId:map&&map.basis&&map.basis.worktreeId?map.basis.worktreeId:'',
    displayBasis:data&&data.verifiedGap?'last_verified':(proofVerified?'current':'candidate'),
    qualityStage:['Q1','Q2','Q3','Q4'].includes(quality)?quality:'Q1',
    settlement:map&&['pending','passed','failed'].includes(map.settlement)?map.settlement:'pending',
    enrichmentStatus:enrichment.status,
    currentProofVerified:proofVerified,
    visibleStepRefs:projection&&Array.isArray(projection.visibleStepRefs)?[...new Set(projection.visibleStepRefs)]:[],
    preservedStepRefs:projection&&Array.isArray(projection.preservedStepRefs)?[...new Set(projection.preservedStepRefs)]:[],
    identityLoss:false,
    preserved:false
  };
}

function renderSemanticTaskView(data,preserveSelection=false){
  const previousViewState=preserveSelection?captureLogicalViewState():null;
  renderSemanticEnrichment(data||{});
  const strip=document.getElementById('current-answer-strip');
  if(strip&&data.candidateAnswer){
    strip.style.display='block';
    const reqEl=document.getElementById('current-answer-requested');
    const stmtEl=document.getElementById('current-answer-statement');
    const stageEl=document.getElementById('current-answer-stage');
    const basisEl=document.getElementById('current-answer-basis');
    const intentEl=document.getElementById('current-answer-intent');
    const agentStatusEl=document.getElementById('current-answer-agent-status');
    if(reqEl)reqEl.textContent=data.taskIntent&&data.taskIntent.request?data.taskIntent.request.rawRequest:(data.candidateAnswer.requested||'—');
    if(stmtEl)stmtEl.textContent='Evidence-backed Implementation Fact: '+(data.candidateAnswer.candidate||'—');
    if(intentEl)intentEl.textContent=data.taskIntent?('revision '+String(data.taskIntent.revision||'—')):'not loaded';
    if(agentStatusEl)agentStatusEl.textContent=(data.agentReportedStatus&&data.agentReportedStatus!=='not_reported')?data.agentReportedStatus:'not reported';
    if(stageEl&&data.semanticMap&&data.semanticMap.quality)stageEl.textContent=data.semanticMap.quality.stage||'unknown';
    if(basisEl&&data.semanticMap)basisEl.textContent=data.semanticMap.computedBasisId?('basis: '+data.semanticMap.computedBasisId):'basis: unknown';
  }

  // Update Independent Status Axes (VS04-A8, D21)
  const freshnessEl=document.getElementById('badge-freshness');
  if(freshnessEl){
    const freshness=(data.semanticMap&&data.semanticMap.freshness)||(data.candidateAnswer&&data.candidateAnswer.freshness)||'unknown';
    if(data.verifiedGap||freshness==='last_verified'){
      freshnessEl.textContent='last_verified';
      freshnessEl.className='badge warn-badge';
    }else{
      freshnessEl.textContent=freshness;
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
  if(data.verifiedGap){
    showVerifiedGap(data.verifiedGap);
  }else{
    hideVerifiedGap();
  }

  if(data.semanticMap){
    const projVisible=new Set(data.projection?(data.projection.visibleStepRefs||[]):[]);
    const projPreserved=new Set(data.projection?(data.projection.preservedStepRefs||[]):[]);

    currentSpec={
      title:data.semanticMap.summary.requested||(data.taskIntent?data.taskIntent.request.rawRequest:''),
      description:data.semanticMap.summary.current,
      flowId:data.semanticMap.mapId,
      workspaceId:data.workspaceId||data.semanticMap.workspaceId||'',
      taskId:data.semanticMap.taskId||(data.semanticMap.task&&data.semanticMap.task.taskId)||data.taskId||'',
      workspaceEpoch:Number.isSafeInteger(data.semanticMap.workspaceEpoch)?data.semanticMap.workspaceEpoch:(data.semanticMap.basis&&Number.isSafeInteger(data.semanticMap.basis.workspaceEpoch)?data.semanticMap.basis.workspaceEpoch:0),
      basisSha:data.semanticMap.computedBasisId||'unknown',
      generationId:data.semanticMap.generationId||'',
      validatedAgainstSnapshotId:data.semanticMap.validatedAgainstSnapshotId||'',
      freshness:data.semanticMap.freshness||'unknown',
      dependencyFingerprint:(data.semanticMap.basis&&data.semanticMap.basis.dependencyFingerprint)||'',
      entrySymbolPath:(data.semanticMap.steps[0]||{}).technicalName||'',
      steps:data.semanticMap.steps.map(s=>({
        stepId:s.stepId,
        ordinal:s.ordinal,
        name:s.name,
        structuralIdentity:s.structuralIdentity||null,
        layer:s.layer,
        kind:s.kind,
        code:s.technicalName,
        anchor:s.anchor,
        codeLens:s.codeLens,
        stateDelta:s.stateDelta,
        sideEffect:s.sideEffect,
        branch:s.branch,
        rules:s.rules,
        freshness:s.freshness||data.semanticMap.freshness||'unknown',
        isVisible:projVisible.size===0||projVisible.has(s.stepId),
        isPreserved:projPreserved.has(s.stepId),
      })),
      unknowns:(data.semanticMap.unknowns||[]).concat((data.projection&&data.projection.unknownBoundaryRefs||[]).map(ref=>({subject:ref,reason:'unknown boundary preserved by projection'}))),
      projection:data.projection||null
    };
    const projectionSummary=document.getElementById('projection-summary');
    if(projectionSummary){
      const folds=(data.projection&&data.projection.foldedSubflows)||[];
      const boundaries=(data.projection&&data.projection.unknownBoundaryRefs)||[];
      projectionSummary.textContent='Projection: '+(data.projection?'candidate':'unknown')+' · folds: '+folds.length+' · unknown boundaries: '+boundaries.length;
    }
    currentFlowId=currentSpec.flowId;
    updateViewStateGeneration(data);

    // Stable selection (VS03-A14): only a canonical structural identity can
    // preserve selection across generations. Step-id or symbol fallbacks are
    // intentionally excluded from the measured eligible denominator.
    selected=0;

    renderAll();
    if(previousViewState){
      restoreLogicalViewState(previousViewState);
    }else{
      viewState={...viewState,selectedStepId:currentSpec.steps[0]&&currentSpec.steps[0].stepId||null,selectedStructuralIdentity:currentSpec.steps[0]&&structuralIdentityOf(currentSpec.steps[0]),logicalScrollAnchor:null,identityLoss:false,preserved:false};
      setViewStateStatus(viewState);
    }
    renderRequirementAlignment(data.taskIntent, data.semanticMap);
    if(data.changePulse){
      renderChangePulse(data.changePulse);
    }
    if(typeof syncApprovalControls==='function')syncApprovalControls();
    if(typeof loadApprovalHistory==='function')loadApprovalHistory();
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
  document.getElementById('bc-flow').textContent=(s.flowId||'').slice(0,16)+'@'+((s.basisSha||'').slice(0,8)||'unknown');
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
    tag.textContent='Intent: '+(intent&&intent.intentStatus?intent.intentStatus:'not loaded');
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

const IMPACT_MAX_DEPTH=3;
const IMPACT_MAX_NODES=50;
const IMPACT_RELATION_KINDS=['calls'];

function impactQueryFromLoadedView(sym){
  const explicitSymbol=typeof sym==='string'?sym.trim():'';
  const input=document.getElementById('impact-symbol-input');
  const inputSymbol=input&&typeof input.value==='string'?input.value.trim():'';
  const activeStep=currentSpec&&Array.isArray(currentSpec.steps)?currentSpec.steps[selected]:null;
  const activeAnchor=activeStep&&activeStep.anchor;
  const loadedSymbol=activeAnchor&&typeof activeAnchor.enclosingSymbolPath==='string'?activeAnchor.enclosingSymbolPath.trim():'';
  const symbol=explicitSymbol||inputSymbol||loadedSymbol;
  if(!symbol){
    throw new Error('missing_precondition: enter a symbol or select a step from the loaded view');
  }

  const basis=currentSpec&&typeof currentSpec.basisSha==='string'?currentSpec.basisSha.trim():'';
  const generation=viewState&&typeof viewState.generationId==='string'?viewState.generationId.trim():'';
  if(!basis||basis==='unknown'||!generation||generation==='unpublished'){
    throw new Error('missing_precondition: the loaded view must expose computedBasisId and generationId');
  }

  const query=new URLSearchParams();
  query.set('symbolId',symbol);
  query.set('computedBasisId',basis);
  query.set('generationId',generation);
  query.set('freshness',viewState.currentProofVerified===true?'current':'historical');
  query.set('maxDepth',String(IMPACT_MAX_DEPTH));
  query.set('maxNodes',String(IMPACT_MAX_NODES));
  IMPACT_RELATION_KINDS.forEach(kind=>query.append('relationKinds',kind));
  return query;
}

async function triggerImpactMode(sym){
  try{
    const query=impactQueryFromLoadedView(sym);
    const r=await api('/api/task/impact?'+query.toString(),{allowErrors:true});
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      alert('Impact Query 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    renderChangeImpact(d);
  }catch(e){
    if(e&&typeof e.message==='string'&&e.message.indexOf('missing_precondition:')===0){
      alert('Impact Query 실패: '+e.message);
      return;
    }
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

function failureInputValue(id){
  const el=document.getElementById(id);
  return el&&typeof el.value==='string'?el.value.trim():'';
}

function failureIdentityFromLoadedView(){
  const basis=(currentSpec&&currentSpec.basisSha)||viewState.computedBasisId||'';
  const generation=(currentSpec&&currentSpec.generationId)||viewState.generationId||'';
  const snapshot=(currentSpec&&currentSpec.validatedAgainstSnapshotId)||viewState.validatedAgainstSnapshotId||'';
  const freshness=viewState.currentProofVerified===true?'current':((currentSpec&&currentSpec.freshness)||'historical');
  if(!basis||basis==='unknown'||!generation||generation==='unpublished'||!snapshot||!freshness||freshness==='unknown'){
    throw new Error('missing_precondition: load a semantic view with exact basis, generation, validated snapshot, and freshness first');
  }
  return {basis:basis,generation:generation,snapshot:snapshot,freshness:freshness};
}

async function triggerFailureInvestigation(mode){
  let identity;
  try{
    identity=failureIdentityFromLoadedView();
  }catch(e){
    alert('조회 실패: '+(e.message||e));
    return;
  }
  const query=new URLSearchParams();
  query.set('computedBasisId',identity.basis);
  query.set('generationId',identity.generation);
  query.set('validatedAgainstSnapshotId',identity.snapshot);
  query.set('freshness',identity.freshness);
  let url='';
  if(mode === 'incident'){
    const traceId=failureInputValue('failure-trace-input');
    const observationId=failureInputValue('failure-observation-input');
    const scenario=failureInputValue('failure-scenario-input');
    const environment=failureInputValue('failure-environment-input');
    const dependency=failureInputValue('failure-dependency-input');
    const from=failureInputValue('failure-window-from-input');
    const to=failureInputValue('failure-window-to-input');
    if(traceId)query.set('traceId',traceId);
    if(observationId)query.set('runtimeObservationId',observationId);
    query.set('scenario',scenario);
    query.set('environment',environment);
    query.set('dependencyFingerprint',dependency);
    query.set('timeWindowFrom',from);
    query.set('timeWindowTo',to);
    url='/api/task/incident?'+query.toString();
  }else{
    const error=failureInputValue('failure-error-input');
    const symptom=failureInputValue('failure-symptom-input');
    const evidence=failureInputValue('failure-evidence-input');
    if(error)query.set('error',error);
    if(symptom)query.set('symptom',symptom);
    if(evidence)query.set('failureEvidenceId',evidence);
    url='/api/task/debug?'+query.toString();
  }

  try{
    const r=await api(url,{allowErrors:true});
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      renderFailureState(err);
      alert('조회 실패: '+(err.message||r.statusText));
      return;
    }
    const d=await r.json();
    renderFailureInvestigation(d.trace||d,d);
  }catch(e){
    renderFailureState({code:'unknown',message:e.message||String(e)});
    console.error('triggerFailureInvestigation error:',e);
  }
}

function renderFailureState(state){
  const tag=document.getElementById('failure-mode-tag');
  if(tag)tag.textContent=(state&&state.code?String(state.code):'unknown').toUpperCase();
  const desc=document.getElementById('failure-summary-desc');
  if(desc)desc.textContent=(state&&state.message)?String(state.message):'장애 조사 상태를 확인할 수 없습니다.';
  const unknown=document.getElementById('failure-unknown-state');
  if(unknown)unknown.textContent=(state&&state.code==='unknown')?'[unknown]':'';
  const promotion=document.getElementById('failure-promotion-state');
  if(promotion&&state&&state.code==='blocked')promotion.textContent='blocked';
}

function renderFailureInvestigation(trace,envelope){
  if(!trace)return;
  const tag=document.getElementById('failure-mode-tag');
  if(tag)tag.textContent=trace.mode?trace.mode.toUpperCase():'UNKNOWN';

  const basis=document.getElementById('failure-basis-id');
  const generation=document.getElementById('failure-generation-id');
  const snapshot=document.getElementById('failure-snapshot-id');
  const freshness=document.getElementById('failure-freshness');
  if(basis)basis.textContent=trace.computedBasisId||'unknown';
  if(generation)generation.textContent=trace.generationId||'unknown';
  if(snapshot)snapshot.textContent=trace.validatedAgainstSnapshotId||'unknown';
  if(freshness)freshness.textContent=trace.freshness||'unknown';

  const observation=(envelope&&envelope.runtimeObservation)||null;
  const isolation=(envelope&&envelope.runtimeIsolation)||null;
  const command=document.getElementById('failure-command-state');
  const access=document.getElementById('failure-access-state');
  const isolationEl=document.getElementById('failure-isolation-state');
  const promotion=document.getElementById('failure-promotion-state');
  const integrity=document.getElementById('failure-integrity-state');
  if(command)command.textContent=isolation?(isolation.command||'supplied'):'not supplied';
  if(access)access.textContent=isolation?(isolation.accessScope&&isolation.accessScope.source||'supplied'):'not supplied';
  if(isolationEl)isolationEl.textContent=isolation?(isolation.isolationScope&&isolation.isolationScope.level||'supplied'):(observation?(observation.isolationLevel||'observed'):'not supplied');
  if(promotion)promotion.textContent=isolation?(isolation.evidencePromotion||'blocked'):(observation?'not evaluated':'not evaluated');
  if(integrity)integrity.textContent=isolation?(isolation.sourceIntegrityStatus||'unknown'):(observation?'provider-validated':'not evaluated');
  const scenario=document.getElementById('failure-scenario-input');
  const environment=document.getElementById('failure-environment-input');
  const dependency=document.getElementById('failure-dependency-input');
  const from=document.getElementById('failure-window-from-input');
  const to=document.getElementById('failure-window-to-input');
  if(observation){
    if(scenario)scenario.value=observation.scenario||'';
    if(environment)environment.value=observation.environment||'';
    if(dependency)dependency.value=observation.dependencyFingerprint||'';
    if(from)from.value=observation.timeWindow&&observation.timeWindow.from||'';
    if(to)to.value=observation.timeWindow&&observation.timeWindow.to||'';
  }

  const desc=document.getElementById('failure-summary-desc');
  if(desc&&trace.summary)desc.textContent=trace.summary.description;

  const st=document.getElementById('failure-last-state');
  if(st&&trace.summary)st.textContent='[최종 확인 상태: '+trace.summary.lastConfirmedState+']';
  const unknown=document.getElementById('failure-unknown-state');
  if(unknown)unknown.textContent=trace.unknownCount?'[unknown: '+String(trace.unknownCount)+']':'';
  const conflict=document.getElementById('failure-conflict-state');
  if(conflict)conflict.textContent=trace.hasConflicts?'[conflict]':'';

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
    timeList.innerHTML=items.length?items.join(''):'<li style="color:var(--muted)">관측된 타임라인 이벤트가 없습니다.</li>';
  }
}

function secureApprovalActionID(prefix){
  const randomSource=typeof globalThis!=='undefined'&&globalThis.crypto?globalThis.crypto:null;
  if(!randomSource)return '';
  if(typeof randomSource.randomUUID==='function'){
    try{
      const value=randomSource.randomUUID();
      if(typeof value==='string'&&value.trim())return prefix+value;
    }catch(_){
      // Fall through to getRandomValues when the preferred API is unavailable.
    }
  }
  if(typeof randomSource.getRandomValues==='function'&&typeof Uint8Array==='function'){
    try{
      const bytes=new Uint8Array(16);
      randomSource.getRandomValues(bytes);
      let value='';
      for(let i=0;i<bytes.length;i++)value+=bytes[i].toString(16).padStart(2,'0');
      return prefix+value;
    }catch(_){
      // The approval action must fail closed if no secure source succeeds.
    }
  }
  return '';
}

function approvalPendingRequestMatches(pending,semantic){
  if(!pending||typeof pending!=='object'||!Object.isFrozen(pending))return false;
  if(pending.proposalId!==semantic.proposalId||pending.evidencePackId!==semantic.evidencePackId||pending.computedBasisId!==semantic.computedBasisId||pending.generationId!==semantic.generationId||pending.intentRevision!==semantic.intentRevision||pending.decision!==semantic.decision||pending.expectedApprovalVersion!==semantic.expectedApprovalVersion||pending.expectedState!==semantic.expectedState)return false;
  const pendingPredecessor=typeof pending.predecessorApprovalId==='string'?pending.predecessorApprovalId:'';
  const semanticPredecessor=typeof semantic.predecessorApprovalId==='string'?semantic.predecessorApprovalId:'';
  const hasPendingPredecessor=Object.prototype.hasOwnProperty.call(pending,'predecessorApprovalId');
  if((semanticPredecessor!==''&&!hasPendingPredecessor)||(semanticPredecessor===''&&hasPendingPredecessor)||pendingPredecessor!==semanticPredecessor)return false;
  const hasPendingEditedText=Object.prototype.hasOwnProperty.call(pending,'editedText');
  const semanticEditedText=semantic.decision==='edit_then_approve'&&typeof semantic.editedText==='string'?semantic.editedText:'';
  const pendingEditedText=typeof pending.editedText==='string'?pending.editedText:'';
  if((semanticEditedText!==''&&!hasPendingEditedText)||(semanticEditedText===''&&hasPendingEditedText))return false;
  return pendingEditedText===semanticEditedText;
}

const APPROVAL_ERROR_MESSAGES=Object.freeze({
  approval_invalid:'approval request is invalid',
  approval_conflict:'approval request conflicts with current state',
  approval_unavailable:'approval service is unavailable',
  approval_unauthenticated:'approval authentication is required',
  approval_unauthorized:'approval workspace is not authorized'
});

function approvalErrorMessage(payload){
  const code=payload&&typeof payload==='object'&&Object.prototype.hasOwnProperty.call(payload,'code')&&typeof payload.code==='string'?payload.code:'';
  if(code&&Object.prototype.hasOwnProperty.call(APPROVAL_ERROR_MESSAGES,code))return code+': '+APPROVAL_ERROR_MESSAGES[code];
  return 'approval request failed; please retry';
}

const APPROVAL_EXECUTION_SCHEMA_IDS=Object.freeze({
  event:'https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json',
  aggregate:'https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json',
  idempotency:'https://codeflow.local/schemas/rflsc.approval-idempotency-result.v1.schema.json',
  outbox:'https://codeflow.local/schemas/rflsc.approval-outbox.v1.schema.json'
});
const APPROVAL_EXECUTION_DECISIONS=Object.freeze(['approve','edit_then_approve','reject','revoke','supersede']);
const APPROVAL_EXECUTION_STATES=Object.freeze(['none','active','rejected','revoked','superseded']);
const APPROVAL_EXECUTION_RELATIONS=Object.freeze(['initial','edit','reject','revoke','supersede']);
const APPROVAL_EXECUTION_DIGEST=/^sha256:[0-9a-f]{64}$/;
const APPROVAL_EXECUTION_MAX_VERSION=1000000000;
const APPROVAL_VALIDATED_EXECUTION_RESULTS=new WeakSet();

function approvalExecutionExactKeys(value,required,optional){
  if(!value||typeof value!=='object'||Array.isArray(value))return false;
  const allowed=new Set(required.concat(optional||[]));
  for(const key of required)if(!Object.prototype.hasOwnProperty.call(value,key))return false;
  for(const key of Object.keys(value))if(!allowed.has(key))return false;
  return true;
}

function approvalExecutionValidText(value,max,required){
  if(typeof value!=='string'||Array.from(value).length>max||value.indexOf('\u0000')>=0)return false;
  return !required||value.trim()!=='';
}

function approvalExecutionValidID(value){
  if(!approvalExecutionValidText(value,256,true)||value.trim()!==value||value.includes('..'))return false;
  for(const ch of value){
    const code=ch.codePointAt(0);
    if(code===0||code===0x7f||code<0x20||code>=0x80&&code<=0x9f||ch==='/'||ch==='\\')return false;
  }
  return true;
}

function approvalExecutionValidInteger(value,min,max){
  return Number.isSafeInteger(value)&&value>=min&&value<=max;
}

function approvalExecutionValidDigest(value){
  return typeof value==='string'&&APPROVAL_EXECUTION_DIGEST.test(value);
}

function approvalExecutionValidTimestamp(value){
  if(!approvalExecutionValidText(value,128,true))return false;
  const match=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/.exec(value);
  if(!match)return false;
  const year=Number(match[1]),month=Number(match[2]),day=Number(match[3]),hour=Number(match[4]),minute=Number(match[5]),second=Number(match[6]),fraction=match[7]||'';
  if(fraction&&fraction.endsWith('0'))return false;
  const date=new Date(0);
  date.setUTCFullYear(year,month-1,day);
  date.setUTCHours(hour,minute,second,0);
  return date.getUTCFullYear()===year&&date.getUTCMonth()===month-1&&date.getUTCDate()===day&&date.getUTCHours()===hour&&date.getUTCMinutes()===minute&&date.getUTCSeconds()===second;
}

function approvalExecutionPreviousAggregateMatchesView(previous,semantic){
  const aggregate=previous&&previous.aggregate&&typeof previous.aggregate==='object'?previous.aggregate:null;
  if(!aggregate)return true;
  if(!approvalExecutionExactKeys(aggregate,['schemaId','schemaVersion','aggregateId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','intentRevision','version','state','lastEventId','lastDecision','history'],['activeApprovalId']))return false;
  if(aggregate.schemaId!==APPROVAL_EXECUTION_SCHEMA_IDS.aggregate||aggregate.schemaVersion!==2)return false;
  if(!approvalExecutionValidID(aggregate.aggregateId)||!approvalExecutionValidID(aggregate.lastEventId)||!approvalExecutionValidID(aggregate.workspaceId)||!approvalExecutionValidID(aggregate.proposalId)||!approvalExecutionValidID(aggregate.evidencePackId)||!approvalExecutionValidID(aggregate.computedBasisId)||!approvalExecutionValidID(aggregate.generationId))return false;
  if(!approvalExecutionValidInteger(aggregate.intentRevision,1,1000000)||!approvalExecutionValidInteger(aggregate.version,0,APPROVAL_EXECUTION_MAX_VERSION)||!APPROVAL_EXECUTION_STATES.includes(aggregate.state)||!['none'].concat(APPROVAL_EXECUTION_DECISIONS).includes(aggregate.lastDecision)||!Array.isArray(aggregate.history)||aggregate.history.length!==aggregate.version+1||aggregate.history.length>256)return false;
  if(aggregate.version!==previous.version||aggregate.state!==previous.state)return false;
  const currentWorkspace=approvalExecutionCurrentWorkspaceID();
  if(!approvalExecutionValidID(currentWorkspace)||aggregate.workspaceId!==currentWorkspace)return false;
  if(aggregate.proposalId!==semantic.proposalId||aggregate.evidencePackId!==semantic.evidencePackId||aggregate.computedBasisId!==semantic.computedBasisId||aggregate.generationId!==semantic.generationId||aggregate.intentRevision!==semantic.intentRevision)return false;
  if(aggregate.state==='active'){
    if(!Object.prototype.hasOwnProperty.call(aggregate,'activeApprovalId')||aggregate.activeApprovalId!==previous.activeApprovalId||!approvalExecutionValidID(aggregate.activeApprovalId))return false;
  }else if(Object.prototype.hasOwnProperty.call(aggregate,'activeApprovalId'))return false;
  const previousTail=aggregate.history[aggregate.history.length-1];
  if(!previousTail||previousTail.eventId!==aggregate.lastEventId||previousTail.state!==aggregate.state||previousTail.decision!==aggregate.lastDecision)return false;
  if(aggregate.state==='active'&&previousTail.approvalId!==aggregate.activeApprovalId)return false;
  return true;
}

function approvalExecutionCurrentWorkspaceID(){
  const viewWorkspace=typeof viewState.workspaceId==='string'?viewState.workspaceId.trim():'';
  if(viewWorkspace)return viewWorkspace;
  const specWorkspace=typeof currentSpec!=='undefined'&&currentSpec&&typeof currentSpec.workspaceId==='string'?currentSpec.workspaceId.trim():'';
  return specWorkspace;
}

function approvalExecutionValidPendingRequest(pending,semantic){
  if(!pending||typeof pending!=='object'||Array.isArray(pending)||!Object.isFrozen(pending))return false;
  const optional=[];
  if(semantic.predecessorApprovalId)optional.push('predecessorApprovalId');
  if(semantic.decision==='edit_then_approve')optional.push('editedText');
  if(!approvalExecutionExactKeys(pending,['commandId','proposalId','evidencePackId','computedBasisId','generationId','intentRevision','decision','idempotencyKey','expectedApprovalVersion','expectedState'],optional))return false;
  for(const key of ['commandId','proposalId','evidencePackId','computedBasisId','generationId','idempotencyKey'])if(!approvalExecutionValidID(pending[key]))return false;
  if(pending.generationId==='unpublished'||!approvalExecutionValidInteger(pending.intentRevision,1,1000000)||!APPROVAL_EXECUTION_DECISIONS.includes(pending.decision)||!approvalExecutionValidInteger(pending.expectedApprovalVersion,0,APPROVAL_EXECUTION_MAX_VERSION)||!APPROVAL_EXECUTION_STATES.includes(pending.expectedState))return false;
  if((pending.expectedState==='none')!== (pending.expectedApprovalVersion===0))return false;
  if(pending.proposalId!==semantic.proposalId||pending.evidencePackId!==semantic.evidencePackId||pending.computedBasisId!==semantic.computedBasisId||pending.generationId!==semantic.generationId||pending.intentRevision!==semantic.intentRevision||pending.decision!==semantic.decision||pending.expectedApprovalVersion!==semantic.expectedApprovalVersion||pending.expectedState!==semantic.expectedState)return false;
  if(semantic.predecessorApprovalId){
    if(!Object.prototype.hasOwnProperty.call(pending,'predecessorApprovalId')||pending.predecessorApprovalId!==semantic.predecessorApprovalId||!approvalExecutionValidID(pending.predecessorApprovalId))return false;
  }else if(Object.prototype.hasOwnProperty.call(pending,'predecessorApprovalId'))return false;
  if(semantic.decision==='edit_then_approve'){
    if(!Object.prototype.hasOwnProperty.call(pending,'editedText')||pending.editedText!==semantic.editedText||!approvalExecutionValidText(pending.editedText,4096,true))return false;
  }else if(Object.prototype.hasOwnProperty.call(pending,'editedText'))return false;
  return true;
}

function approvalExecutionExpectedTransition(previous,pending){
  const state=previous&&typeof previous.state==='string'?previous.state:'';
  const active=previous&&typeof previous.activeApprovalId==='string'?previous.activeApprovalId:'';
  switch(pending.decision){
    case 'approve':
      return state==='none'&&active===''&&!pending.predecessorApprovalId?{state:'active',relation:'initial',predecessor:'forbidden'}:null;
    case 'edit_then_approve':
      return state==='active'&&approvalExecutionValidID(active)&&pending.predecessorApprovalId===active?{state:'active',relation:'edit',predecessor:'required'}:null;
    case 'reject':
      if(state!=='none'&&state!=='active')return null;
      if(state==='none'&&pending.predecessorApprovalId)return null;
      if(state==='active'&&pending.predecessorApprovalId!==active)return null;
      return {state:'rejected',relation:'reject',predecessor:state==='active'?'required':'forbidden'};
    case 'revoke':
      return state==='active'&&approvalExecutionValidID(active)&&pending.predecessorApprovalId===active?{state:'revoked',relation:'revoke',predecessor:'required'}:null;
    case 'supersede':
      return state==='active'&&approvalExecutionValidID(active)&&pending.predecessorApprovalId===active?{state:'superseded',relation:'supersede',predecessor:'required'}:null;
    default:
      return null;
  }
}

function approvalExecutionHistoryEntryEqual(left,right){
  const required=['version','state','eventId','decision'];
  const optional=['approvalId'];
  if(!approvalExecutionExactKeys(left,required,optional)||!approvalExecutionExactKeys(right,required,optional))return false;
  for(const key of required)if(left[key]!==right[key])return false;
  const leftHas=Object.prototype.hasOwnProperty.call(left,'approvalId');
  const rightHas=Object.prototype.hasOwnProperty.call(right,'approvalId');
  return leftHas===rightHas&&(!leftHas||left.approvalId===right.approvalId);
}

function approvalExecutionEventIsNew(event,previous){
  const aggregate=previous&&previous.aggregate&&typeof previous.aggregate==='object'?previous.aggregate:null;
  if(!aggregate||!Array.isArray(aggregate.history))return true;
  return !aggregate.history.some(entry=>entry.eventId===event.eventId||entry.approvalId&&entry.approvalId===event.approvalId);
}

function validateApprovalExecutionResult(result,pending,semantic,previous){
  const topRequired=['receipt','generationId','computedBasisId','validatedSnapshotId','intentRevision','freshness'];
  if(!approvalExecutionExactKeys(result,topRequired,[])||!approvalExecutionValidID(result.generationId)||result.generationId==='unpublished'||!approvalExecutionValidID(result.computedBasisId)||!approvalExecutionValidID(result.validatedSnapshotId)||!approvalExecutionValidInteger(result.intentRevision,1,1000000)||!['current','historical'].includes(result.freshness))return null;
  if(!approvalExecutionValidPendingRequest(pending,semantic))return null;
  if(result.generationId!==pending.generationId||result.computedBasisId!==pending.computedBasisId||result.intentRevision!==pending.intentRevision)return null;
  const currentSnapshot=typeof viewState.validatedAgainstSnapshotId==='string'?viewState.validatedAgainstSnapshotId.trim():'';
  if(!currentSnapshot||result.validatedSnapshotId!==currentSnapshot)return null;
  const currentWorkspace=approvalExecutionCurrentWorkspaceID();

  const receipt=result.receipt;
  if(!approvalExecutionExactKeys(receipt,['idempotencyResult','event','aggregate','outbox','replayed'],[])||typeof receipt.replayed!=='boolean')return null;
  const event=receipt.event;
  const eventRequired=['schemaId','schemaVersion','eventId','approvalId','aggregateId','aggregateVersion','actorId','sessionId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','intentRevision','decision','approvedText','timestamp','lifecycleRelation'];
  if(!approvalExecutionExactKeys(event,eventRequired,['predecessorApprovalId'])||event.schemaId!==APPROVAL_EXECUTION_SCHEMA_IDS.event||event.schemaVersion!==2)return null;
  for(const key of ['eventId','approvalId','aggregateId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId'])if(!approvalExecutionValidID(event[key]))return null;
  for(const key of ['actorId','sessionId'])if(!approvalExecutionValidID(event[key]))return null;
  if(!approvalExecutionValidInteger(event.aggregateVersion,1,APPROVAL_EXECUTION_MAX_VERSION)||!approvalExecutionValidInteger(event.intentRevision,1,1000000)||!APPROVAL_EXECUTION_DECISIONS.includes(event.decision)||!APPROVAL_EXECUTION_RELATIONS.includes(event.lifecycleRelation)||!approvalExecutionValidText(event.approvedText,4096,false)||!approvalExecutionValidTimestamp(event.timestamp))return null;
  if(Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId')&&!approvalExecutionValidID(event.predecessorApprovalId))return null;
  if(!approvalExecutionValidID(currentWorkspace)||event.workspaceId!==currentWorkspace)return null;
  if(event.proposalId!==pending.proposalId||event.evidencePackId!==pending.evidencePackId||event.computedBasisId!==pending.computedBasisId||event.generationId!==pending.generationId||event.intentRevision!==pending.intentRevision||event.decision!==pending.decision)return null;

  const previousState=previous&&typeof previous.state==='string'?previous.state:'';
  const previousVersion=previous&&Number.isSafeInteger(previous.version)?previous.version:-1;
  const previousActive=previous&&typeof previous.activeApprovalId==='string'?previous.activeApprovalId:'';
  if(!APPROVAL_EXECUTION_STATES.includes(previousState)||!approvalExecutionValidInteger(previousVersion,0,APPROVAL_EXECUTION_MAX_VERSION)||((previousState==='active')!==Boolean(previousActive))||((previousState==='none')!==(previousVersion===0)))return null;
  if(pending.expectedApprovalVersion!==previousVersion||pending.expectedState!==previousState)return null;
  if(!approvalExecutionPreviousAggregateMatchesView(previous,semantic))return null;
  const transition=approvalExecutionExpectedTransition({state:previousState,activeApprovalId:previousActive},pending);
  if(!transition||event.aggregateVersion!==previousVersion+1||event.lifecycleRelation!==transition.relation||event.aggregateVersion>APPROVAL_EXECUTION_MAX_VERSION||!approvalExecutionEventIsNew(event,previous))return null;
  if(transition.predecessor==='required'){
    if(!Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId')||event.predecessorApprovalId!==previousActive)return null;
  }else if(Object.prototype.hasOwnProperty.call(event,'predecessorApprovalId'))return null;
  if(pending.decision==='approve'){
    if(event.approvedText.trim()==='')return null;
  }else if(pending.decision==='edit_then_approve'){
    if(event.approvedText!==pending.editedText||event.approvedText.trim()==='')return null;
  }else if(event.approvedText!=='')return null;

  const aggregate=receipt.aggregate;
  const aggregateRequired=['schemaId','schemaVersion','aggregateId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','intentRevision','version','state','lastEventId','lastDecision','history'];
  if(!approvalExecutionExactKeys(aggregate,aggregateRequired,['activeApprovalId'])||aggregate.schemaId!==APPROVAL_EXECUTION_SCHEMA_IDS.aggregate||aggregate.schemaVersion!==2)return null;
  for(const key of ['aggregateId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','lastEventId'])if(!approvalExecutionValidID(aggregate[key]))return null;
  if(!approvalExecutionValidInteger(aggregate.intentRevision,1,1000000)||!approvalExecutionValidInteger(aggregate.version,0,APPROVAL_EXECUTION_MAX_VERSION)||!APPROVAL_EXECUTION_STATES.includes(aggregate.state)||!['none'].concat(APPROVAL_EXECUTION_DECISIONS).includes(aggregate.lastDecision)||!Array.isArray(aggregate.history)||aggregate.history.length!==previousVersion+2||aggregate.history.length>256)return null;
  if(aggregate.workspaceId!==event.workspaceId||aggregate.proposalId!==event.proposalId||aggregate.evidencePackId!==event.evidencePackId||aggregate.computedBasisId!==event.computedBasisId||aggregate.generationId!==event.generationId||aggregate.intentRevision!==event.intentRevision||aggregate.aggregateId!==event.aggregateId||aggregate.version!==event.aggregateVersion||aggregate.state!==transition.state||aggregate.lastEventId!==event.eventId||aggregate.lastDecision!==event.decision)return null;
  if(transition.state==='active'){
    if(!Object.prototype.hasOwnProperty.call(aggregate,'activeApprovalId')||aggregate.activeApprovalId!==event.approvalId||!approvalExecutionValidID(aggregate.activeApprovalId))return null;
  }else if(Object.prototype.hasOwnProperty.call(aggregate,'activeApprovalId'))return null;
  const genesis=aggregate.history[0];
  if(!approvalExecutionExactKeys(genesis,['version','state','eventId','decision'],[])||genesis.version!==0||genesis.state!=='none'||genesis.decision!=='none'||!approvalExecutionValidID(genesis.eventId))return null;
  const seenEvents=new Set([genesis.eventId]);
  const seenApprovals=new Set();
  let historyState='none';
  for(let index=1;index<aggregate.history.length;index+=1){
    const entry=aggregate.history[index];
    if(!approvalExecutionExactKeys(entry,['version','state','eventId','decision'],['approvalId'])||entry.version!==index||!APPROVAL_EXECUTION_STATES.includes(entry.state)||!APPROVAL_EXECUTION_DECISIONS.includes(entry.decision)||!approvalExecutionValidID(entry.eventId)||!Object.prototype.hasOwnProperty.call(entry,'approvalId')||!approvalExecutionValidID(entry.approvalId)||seenEvents.has(entry.eventId)||seenApprovals.has(entry.approvalId))return null;
    let expectedHistoryState='';
    if(entry.decision==='approve'){if(historyState!=='none')return null;expectedHistoryState='active';}
    else if(entry.decision==='edit_then_approve'){if(historyState!=='active')return null;expectedHistoryState='active';}
    else if(entry.decision==='reject'){if(historyState!=='none'&&historyState!=='active')return null;expectedHistoryState='rejected';}
    else if(entry.decision==='revoke'){if(historyState!=='active')return null;expectedHistoryState='revoked';}
    else if(entry.decision==='supersede'){if(historyState!=='active')return null;expectedHistoryState='superseded';}
    if(entry.state!==expectedHistoryState)return null;
    seenEvents.add(entry.eventId);seenApprovals.add(entry.approvalId);
    historyState=entry.state;
  }
  const tail=aggregate.history[aggregate.history.length-1];
  if(tail.eventId!==event.eventId||tail.approvalId!==event.approvalId||tail.state!==transition.state||tail.decision!==event.decision)return null;
  const previousAggregate=previous&&previous.aggregate&&typeof previous.aggregate==='object'?previous.aggregate:null;
  if(previousAggregate){
    if(aggregate.aggregateId!==previousAggregate.aggregateId)return null;
    if(!Array.isArray(previousAggregate.history)||previousAggregate.history.length!==previousVersion+1)return null;
    for(let index=0;index<previousAggregate.history.length;index+=1)if(!approvalExecutionHistoryEntryEqual(aggregate.history[index],previousAggregate.history[index]))return null;
  }else if(previousVersion>0){
    const previousTail=aggregate.history[previousVersion];
    if(previousTail.version!==previousVersion||previousTail.state!==previousState||previousState==='active'&&previousTail.approvalId!==previousActive||previousState!=='active'&&previousTail.approvalId==='')return null;
  }

  const idempotency=receipt.idempotencyResult;
  const idempotencyRequired=['schemaId','schemaVersion','idempotencyKey','commandId','actorId','sessionId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','requestDigest','outcome','approvalId','eventId','aggregateId','aggregateVersion','state','committedAt','originalCommand','originalResult'];
  if(!approvalExecutionExactKeys(idempotency,idempotencyRequired,[])||idempotency.schemaId!==APPROVAL_EXECUTION_SCHEMA_IDS.idempotency||idempotency.schemaVersion!==1)return null;
  for(const key of ['idempotencyKey','commandId','actorId','sessionId','workspaceId','proposalId','evidencePackId','computedBasisId','generationId','approvalId','eventId','aggregateId'])if(!approvalExecutionValidID(idempotency[key]))return null;
  if(!approvalExecutionValidDigest(idempotency.requestDigest)||idempotency.outcome!=='committed'||!approvalExecutionValidInteger(idempotency.aggregateVersion,1,APPROVAL_EXECUTION_MAX_VERSION)||!APPROVAL_EXECUTION_STATES.includes(idempotency.state)||!approvalExecutionValidTimestamp(idempotency.committedAt))return null;
  if(idempotency.idempotencyKey!==pending.idempotencyKey||idempotency.commandId!==pending.commandId||idempotency.actorId!==event.actorId||idempotency.sessionId!==event.sessionId||idempotency.workspaceId!==event.workspaceId||idempotency.proposalId!==event.proposalId||idempotency.evidencePackId!==event.evidencePackId||idempotency.computedBasisId!==event.computedBasisId||idempotency.generationId!==event.generationId||idempotency.approvalId!==event.approvalId||idempotency.eventId!==event.eventId||idempotency.aggregateId!==aggregate.aggregateId||idempotency.aggregateVersion!==aggregate.version||idempotency.state!==aggregate.state||idempotency.committedAt!==event.timestamp)return null;
  const originalCommand=idempotency.originalCommand;
  if(!approvalExecutionExactKeys(originalCommand,['commandId','idempotencyKey','actorId','workspaceId','proposalId','decision','expectedApprovalVersion'],[])||!approvalExecutionValidID(originalCommand.commandId)||!approvalExecutionValidID(originalCommand.idempotencyKey)||!approvalExecutionValidID(originalCommand.actorId)||!approvalExecutionValidID(originalCommand.workspaceId)||!approvalExecutionValidID(originalCommand.proposalId)||!APPROVAL_EXECUTION_DECISIONS.includes(originalCommand.decision)||!approvalExecutionValidInteger(originalCommand.expectedApprovalVersion,0,APPROVAL_EXECUTION_MAX_VERSION))return null;
  if(originalCommand.commandId!==pending.commandId||originalCommand.idempotencyKey!==pending.idempotencyKey||originalCommand.actorId!==idempotency.actorId||originalCommand.workspaceId!==idempotency.workspaceId||originalCommand.proposalId!==pending.proposalId||originalCommand.decision!==pending.decision||originalCommand.expectedApprovalVersion!==previousVersion)return null;
  const originalResult=idempotency.originalResult;
  if(!approvalExecutionExactKeys(originalResult,['outcome','approvalId','eventId','aggregateId','aggregateVersion','state'],[])||originalResult.outcome!=='committed'||!approvalExecutionValidID(originalResult.approvalId)||!approvalExecutionValidID(originalResult.eventId)||!approvalExecutionValidID(originalResult.aggregateId)||!approvalExecutionValidInteger(originalResult.aggregateVersion,1,APPROVAL_EXECUTION_MAX_VERSION)||!APPROVAL_EXECUTION_STATES.includes(originalResult.state))return null;
  if(originalResult.approvalId!==event.approvalId||originalResult.eventId!==event.eventId||originalResult.aggregateId!==aggregate.aggregateId||originalResult.aggregateVersion!==aggregate.version||originalResult.state!==aggregate.state)return null;

  const outbox=receipt.outbox;
  const outboxRequired=['schemaId','schemaVersion','outboxId','eventId','aggregateId','aggregateVersion','workspaceId','payloadDigest','committedAt','deliveryState','committedEvent'];
  if(!approvalExecutionExactKeys(outbox,outboxRequired,['publishedAt','failureReason'])||outbox.schemaId!==APPROVAL_EXECUTION_SCHEMA_IDS.outbox||outbox.schemaVersion!==1)return null;
  for(const key of ['outboxId','eventId','aggregateId','workspaceId'])if(!approvalExecutionValidID(outbox[key]))return null;
  if(!approvalExecutionValidInteger(outbox.aggregateVersion,1,APPROVAL_EXECUTION_MAX_VERSION)||!approvalExecutionValidDigest(outbox.payloadDigest)||!approvalExecutionValidTimestamp(outbox.committedAt)||!['pending','published'].includes(outbox.deliveryState))return null;
  if(Object.prototype.hasOwnProperty.call(outbox,'failureReason'))return null;
  if(outbox.deliveryState==='pending'){
    if(Object.prototype.hasOwnProperty.call(outbox,'publishedAt'))return null;
  }else{
    if(!approvalExecutionValidTimestamp(outbox.publishedAt))return null;
    const orderedTimestamp=value=>value.slice(0,19)+'.'+(value.slice(19,-1).replace(/^\./,'')).padEnd(9,'0');
    if(orderedTimestamp(outbox.publishedAt)<orderedTimestamp(outbox.committedAt))return null;
  }
  if(outbox.eventId!==event.eventId||outbox.aggregateId!==aggregate.aggregateId||outbox.aggregateVersion!==aggregate.version||outbox.workspaceId!==event.workspaceId||outbox.committedAt!==event.timestamp)return null;
  const committedEvent=outbox.committedEvent;
  if(!approvalExecutionExactKeys(committedEvent,['eventId','approvalId','aggregateId','aggregateVersion','workspaceId','decision','payloadDigest'],[])||!approvalExecutionValidID(committedEvent.eventId)||!approvalExecutionValidID(committedEvent.approvalId)||!approvalExecutionValidID(committedEvent.aggregateId)||!approvalExecutionValidInteger(committedEvent.aggregateVersion,1,APPROVAL_EXECUTION_MAX_VERSION)||!approvalExecutionValidID(committedEvent.workspaceId)||!APPROVAL_EXECUTION_DECISIONS.includes(committedEvent.decision)||!approvalExecutionValidDigest(committedEvent.payloadDigest))return null;
  if(committedEvent.eventId!==event.eventId||committedEvent.approvalId!==event.approvalId||committedEvent.aggregateId!==aggregate.aggregateId||committedEvent.aggregateVersion!==aggregate.version||committedEvent.workspaceId!==event.workspaceId||committedEvent.decision!==event.decision||committedEvent.payloadDigest!==outbox.payloadDigest)return null;
  const validated=JSON.parse(JSON.stringify(result));
  APPROVAL_VALIDATED_EXECUTION_RESULTS.add(validated);
  return validated;
}

function approvalEditedTextValue(){
  const input=document.getElementById('approval-edited-text');
  return input&&typeof input.value==='string'?input.value:'';
}

function approvalEditedTextLength(value){
  return typeof value==='string'?Array.from(value).length:0;
}

function approvalLifecycleAdmission(decision,editedText){
  const state=typeof viewState.approvalState==='string'?viewState.approvalState:'none';
  const expectedState=typeof viewState.approvalExpectedState==='string'&&viewState.approvalExpectedState?viewState.approvalExpectedState:state;
  const activeApprovalId=typeof viewState.activeApprovalId==='string'?viewState.activeApprovalId.trim():'';
  const predecessorApprovalId=typeof viewState.approvalPredecessorApprovalId==='string'?viewState.approvalPredecessorApprovalId.trim():'';
  if(expectedState!==state)return false;
  if(decision==='approve')return state==='none'&&!activeApprovalId&&!predecessorApprovalId;
  if(decision==='edit_then_approve')return state==='active'&&!!activeApprovalId&&typeof editedText==='string'&&editedText.trim()!==''&&approvalEditedTextLength(editedText)<=4096;
  if(decision==='reject')return state==='none'?(!activeApprovalId&&!predecessorApprovalId):(state==='active'&&!!activeApprovalId);
  if(decision==='revoke'||decision==='supersede')return state==='active'&&!!activeApprovalId;
  return false;
}

function approvalAdmissionMessage(decision,editedText){
  if(decision==='edit_then_approve'){
    const state=typeof viewState.approvalState==='string'?viewState.approvalState:'none';
    const activeApprovalId=typeof viewState.activeApprovalId==='string'?viewState.activeApprovalId.trim():'';
    if(state!=='active'||!activeApprovalId)return 'edit_then_approve requires an active approval';
    if(typeof editedText!=='string'||editedText.trim()==='')return 'edited text is required for edit_then_approve';
    if(approvalEditedTextLength(editedText)>4096)return 'edited text must be 4096 characters or fewer';
  }
  return 'approval action is not allowed in the current lifecycle state';
}

function syncApprovalControls(){
  const state=typeof viewState.approvalState==='string'?viewState.approvalState:'none';
  const expectedState=typeof viewState.approvalExpectedState==='string'&&viewState.approvalExpectedState?viewState.approvalExpectedState:state;
  const coherentState=expectedState===state;
  const proposalId=typeof viewState.proposalId==='string'?viewState.proposalId.trim():'';
  const evidencePackId=typeof viewState.evidencePackId==='string'?viewState.evidencePackId.trim():'';
  const hasIdentity=!!proposalId&&!!evidencePackId;
  const activeApprovalId=typeof viewState.activeApprovalId==='string'?viewState.activeApprovalId.trim():'';
  const predecessorApprovalId=typeof viewState.approvalPredecessorApprovalId==='string'?viewState.approvalPredecessorApprovalId.trim():'';
  const controls={
    approve:document.getElementById('btn-semantic-approve'),
    edit_then_approve:document.getElementById('btn-semantic-edit-then-approve'),
    reject:document.getElementById('btn-semantic-reject'),
    revoke:document.getElementById('btn-semantic-revoke'),
    supersede:document.getElementById('btn-semantic-supersede')
  };
  if(controls.approve)controls.approve.disabled=!(hasIdentity&&coherentState&&state==='none'&&!activeApprovalId&&!predecessorApprovalId);
  if(controls.edit_then_approve)controls.edit_then_approve.disabled=!(hasIdentity&&coherentState&&state==='active'&&!!activeApprovalId);
  if(controls.reject)controls.reject.disabled=!(hasIdentity&&coherentState&&(state==='none'?(!activeApprovalId&&!predecessorApprovalId):(state==='active'&&!!activeApprovalId)));
  if(controls.revoke)controls.revoke.disabled=!(hasIdentity&&coherentState&&state==='active'&&!!activeApprovalId);
  if(controls.supersede)controls.supersede.disabled=!(hasIdentity&&coherentState&&state==='active'&&!!activeApprovalId);
  const input=document.getElementById('approval-edited-text');
  if(input)input.disabled=!(hasIdentity&&coherentState&&state==='active'&&!!activeApprovalId);
}

function approvalOperationIsCurrent(requestGeneration,pendingRequest,semantic){
  const currentGeneration=typeof semanticRequestGeneration==='number'?semanticRequestGeneration:0;
  if(requestGeneration!==currentGeneration||viewState.approvalPendingRequest!==pendingRequest)return false;
  if(!pendingRequest||typeof pendingRequest!=='object'||!Object.isFrozen(pendingRequest))return false;
  const currentProposalId=typeof viewState.proposalId==='string'?viewState.proposalId.trim():'';
  const currentEvidencePackId=typeof viewState.evidencePackId==='string'?viewState.evidencePackId.trim():'';
  const currentComputedBasisId=typeof viewState.computedBasisId==='string'?viewState.computedBasisId.trim():'';
  const currentGenerationId=typeof viewState.generationId==='string'?viewState.generationId.trim():'';
  const currentValidatedSnapshotId=typeof viewState.validatedAgainstSnapshotId==='string'?viewState.validatedAgainstSnapshotId.trim():'';
  const currentIntentRevision=Number.isInteger(viewState.intentRevision)?viewState.intentRevision:0;
  const currentDecision=typeof viewState.approvalDecision==='string'?viewState.approvalDecision:'';
  const currentExpectedVersion=Number.isInteger(viewState.approvalExpectedVersion)?viewState.approvalExpectedVersion:(Number.isInteger(viewState.approvalVersion)?viewState.approvalVersion:0);
  const currentExpectedState=typeof viewState.approvalExpectedState==='string'&&viewState.approvalExpectedState?viewState.approvalExpectedState:(viewState.approvalState||'none');
  const currentState=typeof viewState.approvalState==='string'?viewState.approvalState:'none';
  const currentPredecessorApprovalId=typeof viewState.activeApprovalId==='string'?viewState.activeApprovalId.trim():'';
  const currentApprovalPredecessorApprovalId=typeof viewState.approvalPredecessorApprovalId==='string'?viewState.approvalPredecessorApprovalId.trim():'';
  const editedTextInput=document.getElementById('approval-edited-text');
  const currentEditedText=semantic.decision==='edit_then_approve'&&editedTextInput&&typeof editedTextInput.value==='string'?editedTextInput.value:'';
  const pendingEditedText=typeof pendingRequest.editedText==='string'?pendingRequest.editedText:'';
  return currentProposalId===semantic.proposalId&&currentEvidencePackId===semantic.evidencePackId&&currentComputedBasisId===semantic.computedBasisId&&currentGenerationId===semantic.generationId&&currentValidatedSnapshotId===semantic.validatedSnapshotId&&currentIntentRevision===semantic.intentRevision&&currentDecision===semantic.decision&&currentExpectedVersion===semantic.expectedApprovalVersion&&currentExpectedState===semantic.expectedState&&currentState===semantic.expectedState&&currentPredecessorApprovalId===semantic.predecessorApprovalId&&currentApprovalPredecessorApprovalId===semantic.predecessorApprovalId&&currentEditedText===semantic.editedText&&pendingRequest.proposalId===semantic.proposalId&&pendingRequest.evidencePackId===semantic.evidencePackId&&pendingRequest.computedBasisId===semantic.computedBasisId&&pendingRequest.generationId===semantic.generationId&&pendingRequest.intentRevision===semantic.intentRevision&&pendingRequest.decision===semantic.decision&&pendingRequest.expectedApprovalVersion===semantic.expectedApprovalVersion&&pendingRequest.expectedState===semantic.expectedState&&pendingEditedText===semantic.editedText;
}

async function submitProposalApproval(decision){
  const proposalId=typeof viewState.proposalId==='string'?viewState.proposalId.trim():'';
  const evidencePackId=typeof viewState.evidencePackId==='string'?viewState.evidencePackId.trim():'';
  const badge=document.getElementById('approval-status-badge');
  const msg=document.getElementById('approval-result-msg');
  if(!proposalId||!evidencePackId){
    if(msg)msg.textContent='✓ 저장된 의미 제안과 근거 팩이 필요합니다.';
    return;
  }
  if(decision!=='approve'&&decision!=='reject'&&decision!=='edit_then_approve'&&decision!=='revoke'&&decision!=='supersede'){
    if(msg)msg.textContent='invalid approval decision';
    return;
  }
  const editedTextInput=document.getElementById('approval-edited-text');
  const editedText=editedTextInput&&typeof editedTextInput.value==='string'?editedTextInput.value:'';
  const lifecycleIsAdmitted=typeof approvalLifecycleAdmission==='function'?approvalLifecycleAdmission:()=>true;
  if(!lifecycleIsAdmitted(decision,editedText)){
    if(msg)msg.textContent=approvalAdmissionMessage(decision,editedText);
    return;
  }
  const computedBasisId=typeof viewState.computedBasisId==='string'?viewState.computedBasisId.trim():'';
  const generationId=typeof viewState.generationId==='string'?viewState.generationId.trim():'';
  const validatedSnapshotId=typeof viewState.validatedAgainstSnapshotId==='string'?viewState.validatedAgainstSnapshotId.trim():'';
  const intentRevision=Number.isInteger(viewState.intentRevision)?viewState.intentRevision:0;
  const expectedApprovalVersion=Number.isInteger(viewState.approvalExpectedVersion)?viewState.approvalExpectedVersion:(Number.isInteger(viewState.approvalVersion)?viewState.approvalVersion:0);
  const expectedState=typeof viewState.approvalExpectedState==='string'&&viewState.approvalExpectedState?viewState.approvalExpectedState:(viewState.approvalState||'none');
  const predecessorApprovalId=typeof viewState.activeApprovalId==='string'?viewState.activeApprovalId.trim():'';
  const semantic={proposalId:proposalId,evidencePackId:evidencePackId,computedBasisId:computedBasisId,generationId:generationId,validatedSnapshotId:validatedSnapshotId,intentRevision:intentRevision,decision:decision,editedText:decision==='edit_then_approve'?editedText:'',expectedApprovalVersion:expectedApprovalVersion,expectedState:expectedState,predecessorApprovalId:predecessorApprovalId};
  let previousAggregate=null;
  if(typeof approvalHistoryModel!=='undefined'&&approvalHistoryModel&&approvalHistoryModel.aggregate&&typeof approvalHistoryModel.aggregate==='object'){
    try{previousAggregate=JSON.parse(JSON.stringify(approvalHistoryModel.aggregate));}catch(_){previousAggregate=null;}
  }
  const previous={state:typeof viewState.approvalState==='string'?viewState.approvalState:'none',version:expectedApprovalVersion,activeApprovalId:predecessorApprovalId,aggregate:previousAggregate};
  let pendingRequest=approvalPendingRequestMatches(viewState.approvalPendingRequest,semantic)?viewState.approvalPendingRequest:null;
  if(!pendingRequest){
    const commandId=secureApprovalActionID('cmd-ui-');
    const idempotencyKey=secureApprovalActionID('idem-ui-');
    if(!commandId||!idempotencyKey){
      if(msg)msg.textContent='승인 처리에 필요한 보안 임의성을 사용할 수 없습니다.';
      return;
    }
    const request={
      commandId:commandId,
      proposalId:semantic.proposalId,
      evidencePackId:semantic.evidencePackId,
      computedBasisId:semantic.computedBasisId,
      generationId:semantic.generationId,
      intentRevision:semantic.intentRevision,
      decision:semantic.decision,
      idempotencyKey:idempotencyKey,
      expectedApprovalVersion:semantic.expectedApprovalVersion,
      expectedState:semantic.expectedState
    };
    if(semantic.predecessorApprovalId)request.predecessorApprovalId=semantic.predecessorApprovalId;
    if(semantic.decision==='edit_then_approve')request.editedText=semantic.editedText;
    pendingRequest=Object.freeze(request);
    viewState={...viewState,
      approvalPendingRequest:pendingRequest,
      approvalCommandId:pendingRequest.commandId,
      approvalIdempotencyKey:pendingRequest.idempotencyKey,
      approvalDecision:pendingRequest.decision,
      approvalExpectedVersion:pendingRequest.expectedApprovalVersion,
      approvalExpectedState:pendingRequest.expectedState,
      approvalPredecessorApprovalId:semantic.predecessorApprovalId
    };
  }
  const requestGeneration=typeof semanticRequestGeneration==='number'?semanticRequestGeneration:0;
  const operationPendingRequest=pendingRequest;
  const operationSemantic=Object.freeze({...semantic});
  const operationIsCurrent=typeof approvalOperationIsCurrent==='function'?approvalOperationIsCurrent:()=>true;
  const operationSpecID=(key)=>{const spec=typeof currentSpec!=='undefined'&&currentSpec&&typeof currentSpec==='object'?currentSpec:null;return spec&&typeof spec[key]==='string'?spec[key].trim():'';};
  const operationMapID=operationSpecID('flowId');
  const operationTaskID=operationSpecID('taskId');
  const operationWorkspaceID=operationSpecID('workspaceId');
  const replayOperationIsCurrent=(result)=>{
    if(typeof semanticRequestGeneration!=='number'||semanticRequestGeneration!==requestGeneration||viewState.approvalPendingRequest!==null)return false;
    const currentProposalId=typeof viewState.proposalId==='string'?viewState.proposalId.trim():'';
    const currentEvidencePackId=typeof viewState.evidencePackId==='string'?viewState.evidencePackId.trim():'';
    const currentComputedBasisId=typeof viewState.computedBasisId==='string'?viewState.computedBasisId.trim():'';
    const currentGenerationId=typeof viewState.generationId==='string'?viewState.generationId.trim():'';
    const currentValidatedSnapshotId=typeof viewState.validatedAgainstSnapshotId==='string'?viewState.validatedAgainstSnapshotId.trim():'';
    const currentIntentRevision=Number.isInteger(viewState.intentRevision)?viewState.intentRevision:0;
    if(currentProposalId!==operationSemantic.proposalId||currentEvidencePackId!==operationSemantic.evidencePackId||currentComputedBasisId!==operationSemantic.computedBasisId||currentGenerationId!==operationSemantic.generationId||currentValidatedSnapshotId!==operationSemantic.validatedSnapshotId||currentIntentRevision!==operationSemantic.intentRevision||operationSpecID('flowId')!==operationMapID||operationSpecID('taskId')!==operationTaskID||operationSpecID('workspaceId')!==operationWorkspaceID)return false;
    if(typeof approvalHistoryModel==='undefined'||!approvalHistoryModel||!Array.isArray(approvalHistoryModel.events)||!approvalHistoryModel.aggregate)return false;
    const displayedAggregate=approvalHistoryModel.aggregate;
    const displayedActiveApprovalId=typeof displayedAggregate.activeApprovalId==='string'?displayedAggregate.activeApprovalId:'';
    if(viewState.approvalVersion!==displayedAggregate.version||viewState.approvalState!==displayedAggregate.state||viewState.approvalExpectedVersion!==displayedAggregate.version||viewState.approvalExpectedState!==displayedAggregate.state||viewState.activeApprovalId!==displayedActiveApprovalId||viewState.approvalPredecessorApprovalId!==displayedActiveApprovalId||viewState.approvalCommandId!==''||viewState.approvalIdempotencyKey!==''||viewState.approvalDecision!=='')return false;
    if(!result)return true;
    if(!result.receipt||result.receipt.replayed!==true)return false;
    const existingEvent=approvalHistoryModel.events[approvalHistoryModel.events.length-1];
    const existingAggregate=displayedAggregate;
    const event=result.receipt.event;
    const aggregate=result.receipt.aggregate;
    if(!event||!aggregate)return false;
    const sameValue=(left,right)=>{
      if(left===right)return true;
      if(!left||!right||typeof left!==typeof right)return false;
      if(Array.isArray(left)||Array.isArray(right)){
        if(!Array.isArray(left)||!Array.isArray(right)||left.length!==right.length)return false;
        for(let index=0;index<left.length;index+=1)if(!sameValue(left[index],right[index]))return false;
        return true;
      }
      if(typeof left!=='object')return false;
      const leftKeys=Object.keys(left).sort();
      const rightKeys=Object.keys(right).sort();
      if(leftKeys.length!==rightKeys.length)return false;
      for(let index=0;index<leftKeys.length;index+=1){if(leftKeys[index]!==rightKeys[index]||!sameValue(left[leftKeys[index]],right[rightKeys[index]]))return false;}
      return true;
    };
    return sameValue(existingEvent,event)&&sameValue(existingAggregate,aggregate);
  };
  if(typeof approvalHistoryMutationSequence==='number')approvalHistoryMutationSequence+=1;
  try{
    const r=await api('/api/semantic/approve',{
      allowErrors:true,
      method:'POST',
      headers:{'Content-Type':'application/json'},
      body: JSON.stringify({
        commandId: pendingRequest.commandId,
        proposalId: pendingRequest.proposalId,
        evidencePackId: pendingRequest.evidencePackId,
        computedBasisId: pendingRequest.computedBasisId,
        generationId: pendingRequest.generationId,
        intentRevision: pendingRequest.intentRevision,
        decision: pendingRequest.decision,
        ...(pendingRequest.editedText!==undefined?{editedText: pendingRequest.editedText}: {}),
        idempotencyKey: pendingRequest.idempotencyKey,
        expectedApprovalVersion: pendingRequest.expectedApprovalVersion,
        expectedState: pendingRequest.expectedState,
        ...(pendingRequest.predecessorApprovalId?{predecessorApprovalId: pendingRequest.predecessorApprovalId}: {})
      })
    });
    const operationCurrentBeforeResponse=operationIsCurrent(requestGeneration,operationPendingRequest,operationSemantic);
    const replayWindowBeforeResponse=!operationCurrentBeforeResponse&&replayOperationIsCurrent(null);
    if(!operationCurrentBeforeResponse&&!replayWindowBeforeResponse)return;
    if(!r.ok){
      let payload=null;
      try{payload=await r.json();}catch(_){
        payload=null;
      }
      if(!operationCurrentBeforeResponse)return;
      if(!operationIsCurrent(requestGeneration,operationPendingRequest,operationSemantic))return;
      if(msg)msg.textContent=approvalErrorMessage(payload);
      return;
    }
    const d=await r.json();
    const operationCurrentAfterJSON=operationIsCurrent(requestGeneration,operationPendingRequest,operationSemantic);
    const replayWindowAfterJSON=!operationCurrentAfterJSON&&replayOperationIsCurrent(null);
    if(!operationCurrentAfterJSON&&!replayWindowAfterJSON)return;
    const validated=validateApprovalExecutionResult(d,operationPendingRequest,operationSemantic,previous);
    if(!validated){
      if(operationCurrentAfterJSON&&operationIsCurrent(requestGeneration,operationPendingRequest,operationSemantic)&&msg)msg.textContent='approval request failed; please retry';
      return;
    }
    if(replayWindowAfterJSON){
      if(!replayOperationIsCurrent(validated))return;
      if(typeof appendApprovalHistoryReceipt==='function'&&!appendApprovalHistoryReceipt(validated))return;
      return;
    }
    if(!operationIsCurrent(requestGeneration,operationPendingRequest,operationSemantic))return;
    const committedEvent=validated.receipt.event;
    const committedAggregate=validated.receipt.aggregate;
    const committedVersion=Number.isInteger(committedAggregate.version)?committedAggregate.version:viewState.approvalVersion;
    const committedState=typeof committedAggregate.state==='string'?committedAggregate.state:viewState.approvalState;
    const committedActiveApprovalId=typeof committedAggregate.activeApprovalId==='string'?committedAggregate.activeApprovalId:'';
    if(typeof appendApprovalHistoryReceipt==='function'&&!appendApprovalHistoryReceipt(validated)){
      if(msg)msg.textContent='approval request failed; please retry';
      return;
    }
    viewState={...viewState,
      approvalVersion:committedVersion,
      approvalState:committedState,
      activeApprovalId:committedActiveApprovalId,
      approvalExpectedVersion:committedVersion,
      approvalExpectedState:committedState,
      approvalPredecessorApprovalId:committedActiveApprovalId,
      approvalPendingRequest:null,
      approvalCommandId:'',
      approvalIdempotencyKey:'',
      approvalDecision:''
    };
    if(badge){
      const badgeLabels={none:'Awaiting Human Approval',active:'Active',rejected:'Rejected',revoked:'Revoked',superseded:'Superseded'};
      if(committedState==='active'){
        badge.textContent=badgeLabels.active;
        badge.style.background='#ebfbee';
        badge.style.color='#2b8a3e';
      }else if(badgeLabels[committedState]){
        badge.textContent=badgeLabels[committedState];
        badge.style.background='#fff5f5';
        badge.style.color='#c92a2a';
      }else{
        badge.textContent=badgeLabels.none;
        badge.style.background='#f4f4f2';
        badge.style.color='#495057';
      }
    }
    if(typeof syncApprovalControls==='function')syncApprovalControls();
    if(msg)msg.textContent='✓ 승인 기록 생성됨: '+(committedEvent.approvalId||'')+' ('+(committedEvent.decision||decision)+')';
    if(typeof approvalHistoryRefreshPending!=='undefined'&&approvalHistoryRefreshPending){
      const refresh=approvalHistoryRefreshPending;
      approvalHistoryRefreshPending=null;
      if(refresh.proposalId===viewState.proposalId&&refresh.evidencePackId===viewState.evidencePackId&&refresh.requestGeneration===semanticRequestGeneration)requestApprovalHistoryRefresh();
    }
  }catch(_){
    if(!operationIsCurrent(requestGeneration,operationPendingRequest,operationSemantic))return;
    if(msg)msg.textContent='approval request could not be sent; please retry';
  }
}

async function exploreDomains(){
  const errEl=document.getElementById('onboarding-error');
  if(errEl){errEl.style.display='none';errEl.textContent='';}
  const basis=viewState||{};
  const repositoryId=(basis.repositoryId||'').trim();
  const computedBasisId=(basis.computedBasisId||'').trim();
  const generationId=(basis.generationId||'').trim();
  const snapshotId=(basis.validatedAgainstSnapshotId||'').trim();
  if(!repositoryId||!computedBasisId||!generationId||!snapshotId||generationId==='unpublished'){
    const message='missing_precondition: 먼저 repositoryId, basis, generation, snapshot이 있는 task view를 조회하세요.';
    if(errEl){errEl.style.display='block';errEl.textContent=message;}
    return;
  }
  try{
    const q=new URLSearchParams({
      repositoryId:repositoryId,
      computedBasisId:computedBasisId,
      generationId:generationId,
      validatedAgainstSnapshotId:snapshotId,
      freshness:basis.currentProofVerified===true?'current':'historical',
      level:'1'
    });
    const r=await api('/api/task/onboarding?'+q.toString(),{allowErrors:true});
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      const message=err.message||r.statusText;
      if(errEl){errEl.style.display='block';errEl.textContent=message;}
      return;
    }
    const d=await r.json();
    renderDomainOverview(d);
  }catch(e){
    if(errEl){errEl.style.display='block';errEl.textContent=String(e&&e.message||e);}
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
  if(covRatio&&ov.summary)covRatio.textContent=Math.round((Number(ov.summary.coverageRatio)||0)*100)+'%';
  const badge=document.getElementById('onboarding-coverage-badge');
  if(badge){
    const current=ov.freshness==='current';
    badge.textContent=current?'Level 1: Current verified':'Level 1: Historical candidate';
    badge.className=current?'badge':'badge warn-badge';
  }
  const status=document.getElementById('onboarding-evidence-status');
  if(status){
    const c=ov.coverageBoundary||{};
    status.textContent='basis: '+(ov.computedBasisId||'unknown')+' · generation: '+(ov.generationId||'unknown')+' · freshness: '+(ov.freshness||'unknown')+' · domains '+String((ov.domains||[]).length)+' · unmapped '+String((ov.unmappedModules||[]).length)+' · excluded '+String((c.excludedReasons||[]).length);
  }

  if(grid){
    const cards=[];
    (ov.domains||[]).forEach(d=>{
      const domainRef=d.domainId||d.name||'';
      const refs=Array.isArray(d.evidenceRefs)?d.evidenceRefs.join(', '):'';
      cards.push(
        '<button type="button" style="width:100%;text-align:left;border:1px solid var(--line);border-radius:6px;padding:10px;background:var(--paper);cursor:pointer;color:inherit" aria-label="도메인 '+esc(d.name)+' 대표 흐름 보기" onclick="loadDomainCatalog(\''+escJs(domainRef)+'\')">'+
          '<div style="display:flex;justify-content:space-between;align-items:center">'+
            '<strong style="font-size:13px">'+esc(d.name)+'</strong>'+
            '<span class="badge" style="background:#e7f5ff;color:#1864ab">'+d.representativeFlowCount+' flows</span>'+
          '</div>'+
	          '<div style="font-size:11px;color:var(--muted);margin-top:4px">'+esc(d.responsibility||d.description||'unknown')+'</div>'+
	          '<div style="font-size:10px;color:var(--muted);margin-top:6px">상태: '+esc(d.epistemicState||'unknown')+' · 신뢰도: '+Math.round((Number(d.confidence)||0)*100)+'%</div>'+
	          '<div style="font-size:10px;color:var(--muted);margin-top:4px">'+esc(d.rationale||d.selectionReason||'')+'</div>'+
	          '<div style="font-size:10px;font-family:monospace;color:var(--muted);margin-top:4px">근거: '+esc(refs||'없음')+'</div>'+
	          '<div style="font-size:10px;font-family:monospace;color:var(--muted);margin-top:4px">진입점: '+esc((d.entryPoints||[]).join(', ')||'없음')+'</div>'+
        '</button>'
      );
    });
    const unmapped=(ov.unmappedModules||[]).map(u=>'<li>'+esc(typeof u==='string'?u:(u.subject||u.modulePath||u))+'</li>').join('');
    const unknowns=(ov.unknowns||[]).map(u=>'<li>'+esc(u.subject||'unknown')+' · '+esc(u.reason||'unresolved')+'</li>').join('');
    grid.innerHTML=(cards.length?cards.join(''):'<div style="color:var(--muted)">근거가 있는 도메인이 없습니다.</div>')+(unmapped?'<div style="grid-column:1/-1;border:1px dashed var(--line);padding:8px;color:var(--muted)"><strong>미분류 영역</strong><ul style="margin:4px 0 0 16px">'+unmapped+'</ul></div>':'')+(unknowns?'<div style="grid-column:1/-1;border:1px dashed var(--line);padding:8px;color:var(--muted)"><strong>확인되지 않은 근거</strong><ul style="margin:4px 0 0 16px">'+unknowns+'</ul></div>':'');
  }
}

async function loadDomainCatalog(domainName){
  const catContainer=document.getElementById('onboarding-catalog-container');
  const list=document.getElementById('representative-flows-list');
  if(catContainer)catContainer.style.display='block';
  const errEl=document.getElementById('onboarding-error');
  if(errEl){errEl.style.display='none';errEl.textContent='';}
  if(list)list.innerHTML='<li style="color:var(--muted)">대표 흐름 근거를 조회하는 중...</li>';
  const q=new URLSearchParams({
    repositoryId:(viewState.repositoryId||''),
    domain:domainName,
    freshness:viewState.currentProofVerified===true?'current':'historical',
    computedBasisId:viewState.computedBasisId||'',
    generationId:viewState.generationId||'',
    validatedAgainstSnapshotId:viewState.validatedAgainstSnapshotId||'',
    level:'2'
  });
  try{
    const r=await api('/api/task/onboarding?'+q.toString(),{allowErrors:true});
    if(!r.ok){
      const err=await r.json().catch(()=>({}));
      if(errEl){errEl.style.display='block';errEl.textContent=err.message||r.statusText;}
      if(list)list.innerHTML='';
      return;
    }
    const cat=await r.json();
    if(list){
      const flows=Array.isArray(cat.flows)?cat.flows:[];
      list.innerHTML=flows.length?flows.map(f=>{
        const canonical=(f.groundedMapId||'')+' / '+(f.entrySymbol||f.flowId||'');
        return '<li style="border:1px solid var(--line);border-radius:5px;padding:7px;background:var(--soft)">'+
	          '<div style="display:flex;justify-content:space-between;gap:8px"><strong>'+esc(f.title||f.entrySymbol||f.flowId)+'</strong><span class="badge">score '+Number(f.complexityScore||0).toFixed(1)+'</span></div>'+
	          '<div class="mono" style="font-size:10px;color:var(--muted);margin-top:3px">'+esc(f.entrySymbol||'')+'</div>'+
	          '<div style="font-size:10px;color:var(--muted);margin-top:3px">'+esc(f.selectionReason||'')+' · '+esc(f.epistemicState||'unknown')+' · '+Math.round((Number(f.confidence)||0)*100)+'%</div>'+
	          '<div style="font-size:10px;color:var(--muted);margin-top:3px">'+esc(f.rationale||'')+'</div>'+
	          '<div style="font-size:10px;color:var(--muted);margin-top:3px">근거: '+esc((f.evidenceRefs||[]).join(', ')||'없음')+' · canonical: '+esc(canonical)+'</div>'+
        '</li>';
      }).join(''):'<li style="color:var(--muted)">선택한 도메인에 근거가 있는 대표 흐름이 없습니다.</li>';
    }
  }catch(e){
    if(errEl){errEl.style.display='block';errEl.textContent=String(e&&e.message||e);}
    if(list)list.innerHTML='';
  }
}

async function evaluateReleaseCapability(){
  try{
    const r=await api('/api/release/capability',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}',allowErrors:true});
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
  const matrix=data.capabilityMatrix;

  const badge=document.getElementById('release-ready-badge');
  const lat=document.getElementById('metric-latency-p95');
  const prec=document.getElementById('metric-precision');
  const reg=document.getElementById('metric-regressions');
  const tier=document.getElementById('release-fallback-tier');

  const metrics=rep&&Array.isArray(rep.metrics)?rep.metrics:[];
  const metric=name=>metrics.find(m=>m&&m.metric===name);
  const activity=metric('activity_latency_ms');
  const currentOrGap=metric('current_or_gap_latency_ms');
  const precision=metric('precision');
  const recall=metric('recall');
  const measured=!!activity&&!!currentOrGap&&!!precision&&!!recall;

  if(badge&&rep){
    if(rep.releaseReady&&measured){
      badge.textContent='Release Ready: PASSED';
      badge.style.background='#ebfbee';
      badge.style.color='#1e602b';
    }else{
      badge.textContent=measured?'Release Ready: FAILED':'Release Ready: NOT MEASURED';
      badge.style.background=measured?'#fff5f5':'#f4f4f2';
      badge.style.color=measured?'#c92a2a':'#495057';
    }
  }

  if(lat)lat.textContent=activity?Number(activity.value).toFixed(1)+' ms':'not measured';
  if(prec)prec.textContent=precision&&recall?Number(precision.value).toFixed(2)+' / '+Number(recall.value).toFixed(2):'not measured';
  if(reg)reg.textContent=currentOrGap?Number(currentOrGap.value).toFixed(1)+' ms':'not measured';
  if(tier)tier.textContent=matrix&&matrix.profileId?(matrix.profileId+' / '+(matrix.corpusVersion||'undeclared corpus')):'not measured';
  const list=document.getElementById('slm-capabilities-list');
  const capabilities=matrix&&Array.isArray(matrix.capabilities)?matrix.capabilities:[];
  if(list)list.innerHTML=capabilities.length?capabilities.map(c=>'<span class="badge">'+esc(c.capabilityId)+': '+esc(c.state)+'</span>').join(''):'<span class="badge" style="background:#f4f4f2;color:#495057">not measured</span>';
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
