package flowview

// IndexHTML is the embedded single-page FlowView application.
const IndexHTML = `<!DOCTYPE html>
<html lang="ko">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>CodeFlow — Business Flow View</title>
  <style>
    :root {
      --bg: #0d1117;
      --card-bg: #161b22;
      --border: #30363d;
      --text: #c9d1d9;
      --text-muted: #8b949e;
      --text-bright: #f0f6fc;
      --accent: #58a6ff;
      --approved: #3fb950;
      --session: #a371f7;
      --stale: #d29922;
      --unknown: #f85149;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      background-color: var(--bg);
      color: var(--text);
      line-height: 1.6;
      display: flex;
      flex-direction: column;
      min-height: 100vh;
    }
    header {
      background-color: var(--card-bg);
      border-bottom: 1px solid var(--border);
      padding: 12px 24px;
      display: flex;
      justify-content: space-between;
      align-items: center;
      position: sticky;
      top: 0;
      z-index: 100;
    }
    .brand { font-weight: 700; font-size: 1.1rem; color: var(--text-bright); display: flex; align-items: center; gap: 8px; }
    .nav-actions { display: flex; gap: 12px; align-items: center; }
    .btn {
      background: #21262d;
      border: 1px solid var(--border);
      color: var(--text-bright);
      padding: 6px 12px;
      border-radius: 6px;
      cursor: pointer;
      font-size: 0.85rem;
      transition: all 0.2s;
    }
    .btn:hover { background: #30363d; border-color: var(--text-muted); }
    .btn-primary { background: #238636; border-color: rgba(240,246,252,0.1); }
    .btn-primary:hover { background: #2ea043; }
    .badge {
      display: inline-block;
      padding: 2px 8px;
      border-radius: 12px;
      font-size: 0.75rem;
      font-weight: 600;
      text-transform: uppercase;
    }
    .badge-approved { background: rgba(63,185,80,0.15); color: var(--approved); border: 1px solid var(--approved); }
    .badge-session { background: rgba(163,113,247,0.15); color: var(--session); border: 1px solid var(--session); }
    .badge-derived { background: rgba(139,148,158,0.15); color: var(--text-muted); border: 1px solid var(--border); }
    .badge-stale { background: rgba(210,153,34,0.15); color: var(--stale); border: 1px solid var(--stale); }
    .badge-unknown { background: rgba(248,81,73,0.15); color: var(--unknown); border: 1px solid var(--unknown); }
    
    .container { max-width: 900px; margin: 0 auto; padding: 24px 16px; width: 100%; flex: 1; }
    
    .queue-banner {
      background: rgba(210,153,34,0.1);
      border: 1px solid var(--stale);
      border-radius: 6px;
      padding: 12px 16px;
      margin-bottom: 24px;
      display: flex;
      justify-content: space-between;
      align-items: center;
    }
    
    .flow-header { margin-bottom: 32px; border-bottom: 1px solid var(--border); padding-bottom: 16px; }
    .flow-title { font-size: 1.6rem; color: var(--text-bright); margin-bottom: 8px; }
    .flow-meta { font-size: 0.85rem; color: var(--text-muted); display: flex; gap: 16px; align-items: center; }
    
    .timeline { position: relative; padding-left: 32px; }
    .timeline::before {
      content: '';
      position: absolute;
      left: 12px;
      top: 0;
      bottom: 0;
      width: 2px;
      background: var(--border);
    }
    .step-card {
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 16px;
      margin-bottom: 20px;
      position: relative;
      transition: border-color 0.2s;
    }
    .step-card:hover { border-color: var(--accent); }
    .step-bullet {
      position: absolute;
      left: -32px;
      top: 18px;
      width: 24px;
      height: 24px;
      border-radius: 50%;
      background: #21262d;
      border: 2px solid var(--border);
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: 0.75rem;
      font-weight: 700;
      color: var(--text-bright);
    }
    .step-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
    .step-name { font-size: 1.1rem; font-weight: 600; color: var(--text-bright); }
    .step-badges { display: flex; gap: 6px; }
    
    .step-content { font-size: 0.9rem; margin-top: 8px; display: flex; flex-direction: column; gap: 6px; }
    .step-row { display: flex; gap: 8px; }
    .step-label { color: var(--text-muted); min-width: 64px; font-weight: 500; }
    .step-val { color: var(--text); flex: 1; }
    
    .lens-code {
      background: #090d13;
      border: 1px solid var(--border);
      border-radius: 6px;
      padding: 12px;
      font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
      font-size: 0.8rem;
      overflow-x: auto;
      margin-top: 10px;
      display: none;
      white-space: pre;
    }
    .edit-form { margin-top: 12px; border-top: 1px solid var(--border); padding-top: 12px; display: none; }
    .input-text {
      width: 100%;
      background: #090d13;
      border: 1px solid var(--border);
      color: var(--text-bright);
      padding: 6px 10px;
      border-radius: 4px;
      margin-bottom: 8px;
      font-size: 0.85rem;
    }
    
    /* Modal quick switcher */
    .modal {
      display: none;
      position: fixed;
      inset: 0;
      background: rgba(0,0,0,0.7);
      backdrop-filter: blur(2px);
      z-index: 1000;
      align-items: center;
      justify-content: center;
    }
    .modal-box {
      background: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 8px;
      width: 500px;
      max-width: 90vw;
      padding: 16px;
      box-shadow: 0 8px 24px rgba(0,0,0,0.5);
    }
  </style>
</head>
<body>
  <header>
    <div class="brand">
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"></polyline></svg>
      CodeFlow View
    </div>
    <div class="nav-actions">
      <button class="btn" onclick="openSwitcher()">⌘K Quick Switcher</button>
    </div>
  </header>

  <div class="container">
    <div id="queue-banner" class="queue-banner" style="display: none;">
      <div><strong>승인 큐 알림:</strong> <span id="queue-count">0</span>개의 단계가 코드 변경으로 인해 재승인이 필요합니다.</div>
      <button class="btn btn-primary" onclick="scrollToFirstStale()">검토하기</button>
    </div>

    <div id="flow-container">
      <div style="text-align: center; padding: 48px; color: var(--text-muted);">
        흐름 데이터를 불러오는 중입니다...
      </div>
    </div>
  </div>

  <div id="switcher-modal" class="modal" onclick="if(event.target === this) closeSwitcher()">
    <div class="modal-box">
      <input type="text" id="switcher-input" class="input-text" placeholder="흐름 검색 (제목, 심볼 경로)..." oninput="filterFlows()">
      <div id="switcher-list" style="max-height: 300px; overflow-y: auto; margin-top: 8px;"></div>
    </div>
  </div>

  <script>
    const params = new URLSearchParams(window.location.search);
    const token = params.get('token') || '';
    let currentFlowId = params.get('flow') || '';
    let cachedFlows = [];
    let currentSpec = null;

    async function init() {
      try {
        const res = await fetch('/api/flows');
        const data = await res.json();
        cachedFlows = data.flows || [];

        if (cachedFlows.length > 0) {
          if (!currentFlowId) {
            currentFlowId = cachedFlows[0].flowId;
          }
          await loadFlow(currentFlowId);
        } else {
          document.getElementById('flow-container').innerHTML = '<div style="text-align: center; padding: 48px;">발행된 비즈니스 흐름이 없습니다. (codeflow publish 실행 필요)</div>';
        }
      } catch (e) {
        document.getElementById('flow-container').innerHTML = '<div style="color: var(--unknown);">오류: ' + e.message + '</div>';
      }
    }

    async function loadFlow(flowId) {
      currentFlowId = flowId;
      try {
        const res = await fetch('/api/flow?id=' + encodeURIComponent(flowId));
        if (!res.ok) throw new Error('흐름을 찾을 수 없습니다.');
        currentSpec = await res.json();
        renderFlow(currentSpec);
      } catch (e) {
        document.getElementById('flow-container').innerHTML = '<div style="color: var(--unknown);">오류: ' + e.message + '</div>';
      }
    }

    function renderFlow(spec) {
      let staleCount = 0;
      let html = '<div class="flow-header">';
      html += '<h1 class="flow-title">' + escapeHtml(spec.title) + '</h1>';
      html += '<div class="flow-meta">';
      html += '<span>ID: ' + escapeHtml(spec.flowId) + '</span>';
      html += '<span>Basis SHA: ' + spec.basisSha.substring(0, 8) + '</span>';
      html += '<span>단계: ' + spec.steps.length + '개</span>';
      html += '</div></div>';

      html += '<div class="timeline">';
      spec.steps.forEach(step => {
        if (step.freshness === 'stale') staleCount++;
        const provBadge = '<span class="badge badge-' + step.provenance + '">' + step.provenance + '</span>';
        const freshBadge = step.freshness === 'stale' ? '<span class="badge badge-stale">stale</span>' : '';

        html += '<div class="step-card" id="step-' + step.ordinal + '">';
        html += '<div class="step-bullet">' + step.ordinal + '</div>';
        html += '<div class="step-header">';
        html += '<div class="step-name">' + escapeHtml(step.name) + '</div>';
        html += '<div class="step-badges">' + provBadge + freshBadge + '</div>';
        html += '</div>';

        html += '<div class="step-content">';
        if (step.rules && step.rules.length > 0) {
          html += '<div class="step-row"><span class="step-label">규칙:</span><span class="step-val">' + escapeHtml(step.rules.join(', ')) + '</span></div>';
        }
        if (step.stateDelta) {
          html += '<div class="step-row"><span class="step-label">상태:</span><span class="step-val">' + escapeHtml(step.stateDelta.before) + ' → ' + escapeHtml(step.stateDelta.after) + '</span></div>';
        }
        if (step.sideEffect) {
          html += '<div class="step-row"><span class="step-label">외부:</span><span class="step-val">' + escapeHtml(step.sideEffect) + '</span></div>';
        }
        if (step.branch) {
          html += '<div class="step-row"><span class="step-label">분기:</span><span class="step-val">' + escapeHtml(step.branch) + '</span></div>';
        }
        html += '</div>';

        html += '<div style="margin-top: 12px; display: flex; gap: 8px;">';
        html += '<button class="btn" onclick="toggleLens(' + step.ordinal + ', \'' + escapeHtml(step.anchor.repoRelativePath) + '\', ' + step.anchor.byteRange[0] + ', ' + step.anchor.byteRange[1] + ')">코드 렌즈</button>';
        html += '<button class="btn" onclick="toggleEdit(' + step.ordinal + ')">인라인 승인</button>';
        html += '</div>';

        html += '<div class="lens-code" id="lens-' + step.ordinal + '">불러오는 중...</div>';

        html += '<div class="edit-form" id="edit-' + step.ordinal + '">';
        html += '<input type="text" class="input-text" id="name-' + step.ordinal + '" value="' + escapeHtml(step.name) + '">';
        html += '<input type="text" class="input-text" id="rules-' + step.ordinal + '" placeholder="비즈니스 규칙 (쉼표 구분)" value="' + (step.rules ? escapeHtml(step.rules.join(', ')) : '') + '">';
        html += '<button class="btn btn-primary" onclick="submitApproval(' + step.ordinal + ', \'' + escapeHtml(step.anchor.enclosingSymbolPath) + '\')">승인 완료</button>';
        html += '</div>';

        html += '</div>';
      });
      html += '</div>';

      document.getElementById('flow-container').innerHTML = html;

      const qBanner = document.getElementById('queue-banner');
      if (staleCount > 0) {
        document.getElementById('queue-count').innerText = staleCount;
        qBanner.style.display = 'flex';
      } else {
        qBanner.style.display = 'none';
      }
    }

    async function toggleLens(ord, path, startByte, endByte) {
      const el = document.getElementById('lens-' + ord);
      if (el.style.display === 'block') {
        el.style.display = 'none';
        return;
      }
      el.style.display = 'block';
      try {
        const res = await fetch('/api/source?path=' + encodeURIComponent(path));
        const fullSource = await res.text();
        const snippet = fullSource.substring(startByte, Math.min(endByte + 200, fullSource.length));
        el.innerText = snippet;
      } catch (e) {
        el.innerText = '소스 코드를 불러올 수 없습니다: ' + e.message;
      }
    }

    function toggleEdit(ord) {
      const el = document.getElementById('edit-' + ord);
      el.style.display = el.style.display === 'block' ? 'none' : 'block';
    }

    async function submitApproval(ord, symbolPath) {
      const name = document.getElementById('name-' + ord).value;
      const rawRules = document.getElementById('rules-' + ord).value;
      const rules = rawRules.split(',').map(s => s.trim()).filter(Boolean);

      try {
        const res = await fetch('/api/approve', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-CodeFlow-Token': token
          },
          body: JSON.stringify({
            flowId: currentFlowId,
            symbolPath: symbolPath,
            name: name,
            rules: rules
          })
        });
        if (!res.ok) throw new Error('승인 저장 실패');
        alert('승인되었습니다!');
        await loadFlow(currentFlowId);
      } catch (e) {
        alert('오류: ' + e.message);
      }
    }

    function openSwitcher() {
      document.getElementById('switcher-modal').style.display = 'flex';
      document.getElementById('switcher-input').focus();
      filterFlows();
    }

    function closeSwitcher() {
      document.getElementById('switcher-modal').style.display = 'none';
    }

    function filterFlows() {
      const q = document.getElementById('switcher-input').value.toLowerCase();
      const listEl = document.getElementById('switcher-list');
      const filtered = cachedFlows.filter(f => f.title.toLowerCase().includes(q) || f.entrySymbolPath.toLowerCase().includes(q));

      let html = '';
      filtered.forEach(f => {
        html += '<div style="padding: 8px; border-bottom: 1px solid var(--border); cursor: pointer;" onclick="loadFlow(\'' + f.flowId + '\'); closeSwitcher();">';
        html += '<div style="font-weight: 600; color: var(--text-bright);">' + escapeHtml(f.title) + '</div>';
        html += '<div style="font-size: 0.75rem; color: var(--text-muted);">' + escapeHtml(f.entrySymbolPath) + '</div>';
        html += '</div>';
      });
      listEl.innerHTML = html || '<div style="padding: 8px; color: var(--text-muted);">검색 결과가 없습니다.</div>';
    }

    document.addEventListener('keydown', e => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        openSwitcher();
      }
      if (e.key === 'Escape') {
        closeSwitcher();
      }
    });

    function escapeHtml(str) {
      if (!str) return '';
      return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
    }

    init();
  </script>
</body>
</html>
`
