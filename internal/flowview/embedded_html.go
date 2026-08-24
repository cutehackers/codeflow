package flowview

// IndexHTML is the embedded single-page FlowView — monochrome, vertical reading (legacy style preserved).
const IndexHTML = `<!doctype html>
<html lang="ko">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>CodeFlow — FlowView</title>
  <style>
    :root{color-scheme:light;--ink:#111;--muted:#6a6a6a;--line:#bdbdbd;--soft:#f3f3f1;--paper:#fff;--warn:#fff8dd}
    *{box-sizing:border-box}html{scroll-behavior:smooth}body{margin:0;background:var(--paper);color:var(--ink);font:14px/1.5 ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}
    button,a{font:inherit}button{color:inherit}button:focus-visible,a:focus-visible{outline:3px solid #777;outline-offset:2px}code,pre,.mono{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
    .shell{width:min(1420px,100%);margin:auto;padding:22px 26px 56px}
    .top{display:flex;justify-content:space-between;gap:24px;border-bottom:3px solid var(--ink);padding-bottom:18px}
    .top h1{margin:4px 0 4px;font-size:clamp(25px,4vw,40px);line-height:1.08;letter-spacing:-.04em}
    .top p{max-width:760px;margin:0;color:var(--muted)}
    .top-state{display:grid;justify-items:end;align-content:start;gap:8px}
    .badge{display:inline-flex;align-items:center;min-height:24px;padding:2px 9px;border:1px solid var(--ink);border-radius:999px;font-size:11px;font-weight:800}
    .journey{display:grid;gap:11px;padding:16px 0 14px;border-bottom:1px solid var(--line)}
    .journey-head{display:flex;justify-content:space-between;gap:16px;align-items:baseline}
    .crumbs{display:flex;align-items:center;gap:7px;flex-wrap:wrap}
    .crumb{padding:8px 11px;border:1px solid var(--line);border-radius:7px;background:var(--paper);font-weight:700}
    .crumb.active{background:var(--ink);color:var(--paper);border-color:var(--ink)}
    .snapshot{display:flex;align-items:center;flex-wrap:wrap;gap:8px 12px;padding:12px 0;border-bottom:1px solid var(--line);color:var(--muted);font-size:12px}
    .workbench{display:grid;grid-template-columns:290px minmax(0,1fr);gap:26px;padding-top:22px}
    .timeline{min-width:0;border-right:1px solid var(--line);padding-right:19px}
    .timeline-title{display:flex;justify-content:space-between;align-items:baseline;margin-bottom:13px}
    .steps{display:grid;list-style:none;margin:0;padding:0}
    .steps li{position:relative;padding:0 0 7px 26px}
    .steps li:before{content:"";position:absolute;left:9px;top:0;bottom:0;width:1px;background:var(--line)}
    .step{position:relative;width:100%;display:grid;grid-template-columns:32px minmax(0,1fr);gap:8px;padding:8px 7px;border:0;border-radius:7px;background:transparent;text-align:left;cursor:pointer}
    .step:hover,.step.active{background:var(--soft)}
    .marker{display:grid;place-items:center;width:27px;height:27px;border:1px solid var(--ink);border-radius:50%;background:var(--paper);font:700 10px ui-monospace,monospace}
    .step.active .marker{background:var(--ink);color:var(--paper)}
    .step-copy{border-left:3px solid transparent;padding-left:7px}
    .step.active .step-copy{border-left-color:var(--ink)}
    .step-copy small{color:var(--muted);font-size:10px}
    .hero{border:1px solid var(--ink);border-radius:10px;box-shadow:5px 5px 0 var(--soft);overflow:hidden}
    .hero-head{display:flex;justify-content:space-between;gap:12px;padding:12px 15px;border-bottom:1px solid var(--ink);background:var(--soft)}
    .causal{display:grid;grid-template-columns:1fr 24px 1fr 24px 1fr;border-top:1px solid var(--ink)}
    .causal-cell{padding:10px 13px}
    .causal-cell.state{background:var(--ink);color:var(--paper)}
    .causal-arrow{display:grid;place-items:center;border-inline:1px solid var(--ink)}
    .code-wrap{overflow:auto;background:#fafafa}
    .code{padding:10px 0;font:13px/1.65 ui-monospace,monospace}
    .line{display:grid;grid-template-columns:5ch max-content}
    .num{color:#888;text-align:right;border-right:1px solid #ddd;padding-right:10px}
    .src{padding:0 16px;white-space:pre}
    .line.hit{background:#e6e6e2;box-shadow:inset 4px 0 var(--ink)}
    .queue-banner{margin:0 0 16px;padding:10px 11px;border-left:4px solid var(--ink);background:var(--warn);display:flex;justify-content:space-between}
    .modal{position:fixed;inset:0;background:rgba(0,0,0,0.35);display:none;align-items:center;justify-content:center;z-index:100}
    .modal-box{background:var(--paper);border:1px solid var(--ink);border-radius:8px;width:500px;max-width:90vw;padding:16px}
    .input-text{width:100%;border:1px solid var(--ink);padding:8px 10px;border-radius:6px}
    @media(max-width:900px){.workbench{grid-template-columns:1fr}.timeline{border-right:0;border-bottom:1px solid var(--line)}}
  </style>
</head>
<body>
<main class="shell" id="flowview">
  <header class="top">
    <div><span style="font-size:11px;font-weight:800;letter-spacing:.12em;color:var(--muted)">CODEFLOW · FLOWVIEW</span><h1 id="flow-title">흐름을 불러오는 중…</h1><p id="flow-desc">비즈니스 흐름을 코드 근거와 함께 세로로 읽습니다. 토큰은 URL에 포함됩니다.</p></div>
    <div class="top-state"><span class="badge" id="flow-badge">—</span><span id="flow-basis" style="font-size:12px;color:var(--muted)"></span></div>
  </header>
  <div id="queue-banner" class="queue-banner" style="display:none"><span><b>승인 큐:</b> <span id="queue-count">0</span>개 단계 재승인 필요</span><button onclick="scrollToFirstStale()" style="border:1px solid var(--ink);background:var(--paper);padding:6px 10px;border-radius:6px;cursor:pointer">검토</button></div>
  <section class="journey" id="journey" style="display:none"><div class="journey-head"><h2>비즈니스 여정</h2><span id="journey-meta"></span></div><div class="crumbs" id="crumbs"></div></section>
  <section class="snapshot" id="snapshot"><span class="badge">basis</span><code id="snapshot-basis">—</code><span id="snapshot-status"></span></section>
  <section class="workbench">
    <aside class="timeline"><div class="timeline-title"><h2>순차 타임라인</h2><span id="count">—</span></div><ol class="steps" id="steps"></ol></aside>
    <section class="detail">
      <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px"><div><small id="position">—</small><div id="title" style="font-size:17px;font-weight:700"></div></div><div style="display:flex;gap:7px"><button onclick="prevStep()" style="border:1px solid var(--ink);background:var(--paper);padding:6px 10px;border-radius:6px;cursor:pointer">← 이전</button><button onclick="nextStep()" style="border:1px solid var(--ink);background:var(--paper);padding:6px 10px;border-radius:6px;cursor:pointer">다음 →</button><button onclick="openSwitcher()" style="border:1px solid var(--ink);background:var(--paper);padding:6px 10px;border-radius:6px;cursor:pointer">⌘K</button></div></div>
      <article class="hero"><div class="hero-head"><strong id="kind">—</strong><span id="trust"></span></div><div style="display:flex;justify-content:space-between;padding:8px 13px;border-bottom:1px solid var(--line);font-size:12px"><span class="mono" id="path">—</span><a id="vscode-link" href="#" style="border:1px solid var(--ink);background:var(--ink);color:var(--paper);padding:5px 8px;border-radius:5px;text-decoration:none;font-weight:800">VS Code ↗</a></div><div class="code-wrap"><div class="code" id="code"></div></div><div class="causal"><div class="causal-cell"><small>사용자 행동</small><span id="user">—</span></div><div class="causal-arrow">→</div><div class="causal-cell state"><small>상태 변화</small><span id="state">—</span></div><div class="causal-arrow">→</div><div class="causal-cell"><small>화면 결과</small><span id="result">—</span></div></div></article>
      <div style="margin-top:12px;display:flex;gap:8px"><button onclick="toggleEdit()" style="border:1px solid var(--ink);background:var(--paper);padding:6px 10px;border-radius:6px;cursor:pointer">인라인 승인</button></div>
      <div id="edit-form" style="display:none;margin-top:10px;border:1px solid var(--ink);padding:10px;border-radius:6px"><input id="edit-name" class="input-text" placeholder="단계 이름"><input id="edit-rules" class="input-text" placeholder="규칙 (쉼표 구분)" style="margin-top:6px"><button onclick="submitApproval()" style="margin-top:8px;border:1px solid var(--ink);background:var(--ink);color:var(--paper);padding:6px 10px;border-radius:6px;cursor:pointer">승인 완료</button></div>
    </section>
  </section>
</main>
<div id="switcher-modal" class="modal" onclick="if(event.target===this)closeSwitcher()"><div class="modal-box"><input id="switcher-input" class="input-text" placeholder="흐름 검색 (제목, 심볼)..." oninput="filterFlows()"><div id="switcher-list" style="max-height:300px;overflow:auto;margin-top:8px"></div></div></div>
<script>
const params=new URLSearchParams(location.search),token=params.get('token')||'';let currentFlowId=params.get('flow')||'',cachedFlows=[],currentSpec=null,selected=0;
async function api(path,opts={}){const u=new URL(path,location.origin);if(token)u.searchParams.set('token',token);const h=Object.assign({},opts.headers);if(token)h['X-CodeFlow-Token']=token;const r=await fetch(u.toString(),Object.assign({},opts,{headers:h}));if(!r.ok)throw new Error(r.status+' '+r.statusText);return r;}
async function init(){try{const r=await api('/api/flows');const d=await r.json();cachedFlows=d.flows||[];if(!cachedFlows.length){document.getElementById('flow-title').textContent='발행된 흐름 없음';document.getElementById('flow-desc').textContent='codeflow publish를 실행하세요.';return;}if(!currentFlowId)currentFlowId=cachedFlows[0].flowId;await loadFlow(currentFlowId);}catch(e){document.getElementById('flow-title').textContent='오류: '+e.message;}}
async function loadFlow(id){currentFlowId=id;const r=await api('/api/flow?id='+encodeURIComponent(id));currentSpec=await r.json();selected=0;render();}
function render(){const s=currentSpec;document.getElementById('flow-title').textContent=s.title;document.getElementById('flow-badge').textContent=s.flowId.slice(0,16);document.getElementById('flow-basis').textContent='basis '+s.basisSha.slice(0,8)+' · '+s.steps.length+' steps';document.getElementById('snapshot-basis').textContent=s.basisSha.slice(0,8);document.getElementById('snapshot-status').textContent=s.steps.some(x=>x.freshness==='stale')?'stale 있음':'fresh';let stale=0;s.steps.forEach(x=>{if(x.freshness==='stale')stale++;});document.getElementById('queue-banner').style.display=stale?'flex':'none';document.getElementById('queue-count').textContent=stale;document.getElementById('count').textContent=String(selected+1).padStart(2,'0')+' / '+String(s.steps.length).padStart(2,'0');document.getElementById('steps').innerHTML=s.steps.map((st,i)=>'<li><button class="step '+(i===selected?'active':'')+'" onclick="selected='+i+';renderDetail()"><span class="marker">'+String(i+1).padStart(2,'0')+'</span><span class="step-copy"><small>'+st.provenance+' · '+st.freshness+'</small><strong>'+esc(st.name)+'</strong></span></button></li>').join('');renderDetail();}
function renderDetail(){if(!currentSpec)return;const st=currentSpec.steps[selected];document.getElementById('position').textContent=String(selected+1).padStart(2,'0')+' / '+String(currentSpec.steps.length).padStart(2,'0')+' · '+st.provenance;document.getElementById('title').textContent=st.name;document.getElementById('kind').textContent=st.provenance;document.getElementById('trust').textContent=(st.freshness==='stale'?'● stale · 재승인 필요':'● '+st.freshness+' · '+(st.confidence||''));document.getElementById('path').textContent=st.anchor.repoRelativePath+' · '+st.anchor.enclosingSymbolPath;document.getElementById('vscode-link').href='vscode://file/'+st.anchor.repoRelativePath+':'+(st.codeLens?st.codeLens.startLine:'1');document.getElementById('user').textContent=st.name;document.getElementById('state').textContent=st.stateDelta?st.stateDelta.before+' → '+st.stateDelta.after:(st.sideEffect||'—');document.getElementById('result').textContent=st.branch||(st.sideEffect?'외부 호출 '+st.sideEffect:'—');loadCode(st);}
async function loadCode(st){const el=document.getElementById('code');el.innerHTML='불러오는 중…';try{const r=await api('/api/source?path='+encodeURIComponent(st.anchor.repoRelativePath)+'&startLine='+(st.codeLens?st.codeLens.startLine:1)+'&endLine='+(st.codeLens?st.codeLens.endLine:20));const t=await r.text();const lines=t.split('\n');el.innerHTML=lines.map((ln,i)=>'<div class="line '+( (st.codeLens && i+st.codeLens.startLine>=st.codeLens.startLine && i+st.codeLens.startLine<=st.codeLens.endLine)?'hit':'')+'"><span class="num">'+( (st.codeLens?st.codeLens.startLine:1)+i )+'</span><span class="src">'+esc(ln)+'</span></div>').join('');}catch(e){el.textContent='로드 실패: '+e.message;}}
function prevStep(){selected=Math.max(0,selected-1);renderDetail();}function nextStep(){selected=Math.min(currentSpec.steps.length-1,selected+1);renderDetail();}
function openSwitcher(){document.getElementById('switcher-modal').style.display='flex';document.getElementById('switcher-input').focus();filterFlows();}
function closeSwitcher(){document.getElementById('switcher-modal').style.display='none';}
function filterFlows(){const q=document.getElementById('switcher-input').value.toLowerCase();const el=document.getElementById('switcher-list');const f=cachedFlows.filter(x=>x.title.toLowerCase().includes(q)||x.entrySymbolPath.toLowerCase().includes(q));el.innerHTML=f.map(x=>'<div style="padding:8px;border-bottom:1px solid var(--line);cursor:pointer" onclick="loadFlow(\''+x.flowId+'\');closeSwitcher()"><div style="font-weight:700">'+esc(x.title)+'</div><div style="font-size:11px;color:var(--muted)">'+esc(x.entrySymbolPath)+'</div></div>').join('')||'<div style="padding:8px;color:var(--muted)">검색 결과 없음</div>';}
function toggleEdit(){const e=document.getElementById('edit-form');e.style.display=e.style.display==='block'?'none':'block';if(e.style.display==='block'){document.getElementById('edit-name').value=currentSpec.steps[selected].name;document.getElementById('edit-rules').value=(currentSpec.steps[selected].rules||[]).join(', ');}}
async function submitApproval(){const st=currentSpec.steps[selected];const name=document.getElementById('edit-name').value, rules=document.getElementById('edit-rules').value.split(',').map(s=>s.trim()).filter(Boolean);const r=await api('/api/approve',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({flowId:currentSpec.flowId,symbolPath:st.anchor.enclosingSymbolPath,name,rules})});if(!r.ok)throw new Error('승인 실패');alert('승인되었습니다');await loadFlow(currentFlowId);}
function esc(s){return (s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');}
document.addEventListener('keydown',e=>{if((e.metaKey||e.ctrlKey)&&e.key==='k'){e.preventDefault();openSwitcher();}if(e.key==='Escape')closeSwitcher();});
init();
</script>
</body>
</html>
`
