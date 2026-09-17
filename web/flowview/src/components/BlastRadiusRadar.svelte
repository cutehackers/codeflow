<script lang="ts">
  import { flowStore } from '../stores/flowStore.svelte';
  import type { Step } from '../types/flow';
  import type { ChangeImpactGraph } from '../types/impact';

  const selectedStep = $derived(flowStore.selectedStep);
  const data = $derived(flowStore.data);

  const impactGraph = $derived.by((): ChangeImpactGraph | null => {
    if (!selectedStep) return null;
    const relations = (data?.semanticMap.edges || []).filter(e => e.resolutionStatus === 'resolved' && (e.toStepId === selectedStep.stepId || e.fromStepId === selectedStep.stepId));
    const connected = relations.map(e => flowStore.steps.find(s => s.stepId === (e.toStepId === selectedStep.stepId ? e.fromStepId : e.toStepId))).filter((s): s is Step => !!s);
    const unique = [...new Map(connected.map(s => [s.stepId, s])).values()];
    return { directImpact: { callers: unique.map(s => ({name:s.name, symbolPath:s.stepId})), tests:[] }, indirectImpact: {callers:[]} };
  });

  function cleanLabel(raw: string, maxLen = 13): string {
    if (!raw) return '';
    const withoutExt = raw.replace(/\.[a-zA-Z0-9]+$/, '');
    const lastSeg = withoutExt.split(/[./\\]/).pop() || withoutExt;
    if (lastSeg.length <= maxLen) return lastSeg;
    return lastSeg.slice(0, maxLen - 1) + '…';
  }

  const ring1Callers = $derived.by(() => {
    if (!impactGraph) return [];
    return (impactGraph.directImpact?.callers || []).map(c => ({
      name: c.name || c.symbolPath,
      symbol: c.symbolPath,
      label: cleanLabel(c.name || c.symbolPath, 12),
      kind: 'caller' as const
    }));
  });

  const ring1Mutations = $derived.by(() => {
    if (!impactGraph) return [];
    return (impactGraph.directImpact?.stateMutations || []).map(m => {
      const full = m.targetState || 'State';
      const label = cleanLabel(full.split('.').pop() || full, 10);
      return {
        name: full,
        symbol: full,
        label,
        kind: 'state' as const
      };
    });
  });

  const ring1Nodes = $derived([...ring1Callers, ...ring1Mutations]);

  const ring2Nodes = $derived.by(() => {
    if (!impactGraph) return [];
    return (impactGraph.indirectImpact?.callers || []).map(c => ({
      name: c.name || c.symbolPath,
      symbol: c.symbolPath,
      label: cleanLabel(c.name || c.symbolPath, 12),
      kind: 'indirect' as const
    }));
  });

  const ring3Nodes = $derived.by(() => {
    if (!impactGraph) return [];
    const tests = [
      ...(impactGraph.directImpact?.tests || []),
      ...(impactGraph.indirectImpact?.tests || [])
    ];
    return tests.map(t => {
      const raw = t.testSymbolPath || t.testFile || 'test';
      return {
        name: raw,
        symbol: t.testSymbolPath,
        label: cleanLabel(raw, 14),
        broken: !!t.broken,
        kind: 'test' as const
      };
    });
  });

  const activeRingsCount = $derived(
    (ring1Nodes.length ? 1 : 0) + (ring2Nodes.length ? 1 : 0) + (ring3Nodes.length ? 1 : 0)
  );

  const hasBrokenTest = $derived(ring3Nodes.some(t => t.broken));

  const statusPillText = $derived.by(() => {
    if (!selectedStep) return 'Impact: None';
    if (hasBrokenTest) return 'Broken Test';
    if (activeRingsCount > 0) return '검증된 직접 관계';
    return '직접 관계 미확인';
  });

  const targetLabel = $derived.by(() => {
    if (!selectedStep) return '대기 중';
    const sym = selectedStep.technicalName || selectedStep.name;
    return cleanLabel(sym, 14);
  });

  function getQuadrantCoords(radius: number, total: number, index: number, baseAngle: number) {
    if (total <= 1) {
      return {
        x: Math.round(radius * Math.cos(baseAngle)),
        y: Math.round(radius * Math.sin(baseAngle))
      };
    }
    const spread = Math.min(Math.PI * 0.8, (total - 1) * 0.55);
    const start = baseAngle - spread / 2;
    const step = spread / (total - 1);
    const theta = start + step * index;
    return {
      x: Math.round(radius * Math.cos(theta)),
      y: Math.round(radius * Math.sin(theta))
    };
  }

  function getNodeBoxWidth(label: string): number {
    return Math.min(68, Math.max(38, Math.round(label.length * 5.6 + 12)));
  }

  function onNodeClick(stepId: string) {
    if (!flowStore.steps.some(s => s.stepId === stepId)) return;
    flowStore.saveNavigationState();
    flowStore.select(stepId);
  }
</script>

<section class="radar-card" id="radar-card" aria-label="파급 영향 레이더">
  <div class="radar-head">
    <span class="radar-title">Blast Radius Radar</span>
    <span class="pill {hasBrokenTest ? 'pill-broken' : 'invert'}" id="radar-pill">{statusPillText}</span>
  </div>

  <div class="radar-viewport">
    <svg id="radar-svg" viewBox="-100 -100 200 200" preserveAspectRatio="xMidYMid meet" role="img" aria-label="파급 영향 동심원 그래프">
      <!-- Crosshairs (Ray guides) -->
      <line x1="0" y1="-95" x2="0" y2="95" stroke="#ecece6" stroke-dasharray="2 3" stroke-width="0.8" />
      <line x1="-95" y1="0" x2="95" y2="0" stroke="#ecece6" stroke-dasharray="2 3" stroke-width="0.8" />

      <!-- 3 Concentric Rings -->
      <circle cx="0" cy="0" r="30" fill="none" stroke="#dcdcd8" stroke-dasharray="3 3" stroke-width="0.9"></circle>
      <circle cx="0" cy="0" r="62" fill="none" stroke="#dcdcd8" stroke-dasharray="3 3" stroke-width="0.9"></circle>
      <circle cx="0" cy="0" r="90" fill="none" stroke="#dcdcd8" stroke-dasharray="3 3" stroke-width="0.9"></circle>

      <!-- Ring Sub-labels -->
      <text x="2" y="-32" font-size="5" font-weight="700" fill="#c0c0b8">R1</text>
      <text x="2" y="-64" font-size="5" font-weight="700" fill="#c0c0b8">R2</text>
      <text x="2" y="-92" font-size="5" font-weight="700" fill="#c0c0b8">R3</text>

      <!-- Center Target Node -->
      <circle cx="0" cy="0" r="11" fill="#171717"></circle>
      <text x="0" y="3" text-anchor="middle" fill="#ffffff" font-size="6.5" font-weight="900" letter-spacing="0.5">TARGET</text>
      <text id="radar-target-label" x="0" y="19" text-anchor="middle" font-size="7.5" font-weight="800" fill="#171717">{targetLabel}</text>

      <!-- Ring 1 Callers: Placed in North-West quadrant (~10 o'clock) -->
      {#each ring1Callers as node, i}
        {@const coords = getQuadrantCoords(30, ring1Callers.length, i, -Math.PI * 0.82)}
        {@const bw = getNodeBoxWidth(node.label)}
        <g
          class="radar-node"
          transform="translate({coords.x}, {coords.y})"
          data-radar-symbol={node.symbol}
          role="button"
          tabindex="0"
          onclick={() => onNodeClick(node.symbol)}
          onkeydown={(e) => e.key === 'Enter' && onNodeClick(node.symbol)}
        >
          <rect x={-bw / 2} y="-6.5" width={bw} height="13" rx="3" fill="#ffffff" stroke="#171717" stroke-width="1.2"></rect>
          <text x="0" y="0.5" text-anchor="middle" dominant-baseline="central" font-size="6.5" font-weight="750" fill="#171717">{node.label}</text>
        </g>
      {/each}

      <!-- Ring 1 State Mutations: Placed in South-West quadrant (~7:30 o'clock) -->
      {#each ring1Mutations as node, i}
        {@const coords = getQuadrantCoords(30, ring1Mutations.length, i, Math.PI * 0.75)}
        {@const bw = getNodeBoxWidth(node.label)}
        <g
          class="radar-node"
          transform="translate({coords.x}, {coords.y})"
          data-radar-symbol={node.symbol}
          role="button"
          tabindex="0"
          onclick={() => onNodeClick(node.symbol)}
          onkeydown={(e) => e.key === 'Enter' && onNodeClick(node.symbol)}
        >
          <rect x={-bw / 2} y="-6.5" width={bw} height="13" rx="3" fill="#fff3bf" stroke="#d9480f" stroke-width="1"></rect>
          <text x="0" y="0.5" text-anchor="middle" dominant-baseline="central" font-size="6.5" font-weight="750" fill="#d9480f">{node.label}</text>
        </g>
      {/each}

      <!-- Ring 2 Nodes (Indirect Callers): Placed in North-East quadrant (~1:30 o'clock) -->
      {#each ring2Nodes as node, i}
        {@const coords = getQuadrantCoords(62, ring2Nodes.length, i, -Math.PI * 0.25)}
        {@const bw = getNodeBoxWidth(node.label)}
        <g
          class="radar-node"
          transform="translate({coords.x}, {coords.y})"
          data-radar-symbol={node.symbol}
          role="button"
          tabindex="0"
          onclick={() => onNodeClick(node.symbol)}
          onkeydown={(e) => e.key === 'Enter' && onNodeClick(node.symbol)}
        >
          <rect x={-bw / 2} y="-6.5" width={bw} height="13" rx="3" fill="#ffffff" stroke="#555555" stroke-dasharray="2 2" stroke-width="1"></rect>
          <text x="0" y="0.5" text-anchor="middle" dominant-baseline="central" font-size="6.5" font-weight="700" fill="#444444">{node.label}</text>
        </g>
      {/each}

      <!-- Ring 3 Nodes (Tests): Placed in South-East quadrant (~4:30 o'clock) -->
      {#each ring3Nodes as testNode, i}
        {@const coords = getQuadrantCoords(90, ring3Nodes.length, i, Math.PI * 0.28)}
        {@const bw = getNodeBoxWidth(testNode.label)}
        <g
          class="radar-node"
          transform="translate({coords.x}, {coords.y})"
          data-radar-symbol={testNode.symbol}
          role="button"
          tabindex="0"
          onclick={() => onNodeClick(testNode.symbol)}
          onkeydown={(e) => e.key === 'Enter' && onNodeClick(testNode.symbol)}
        >
          <rect
            x={-bw / 2}
            y="-7"
            width={bw}
            height="14"
            rx="3"
            fill={testNode.broken ? '#c92a2a' : '#171717'}
            stroke={testNode.broken ? '#a61e1e' : 'none'}
            stroke-width="0.5"
          ></rect>
          <text x="0" y="0.5" text-anchor="middle" dominant-baseline="central" font-size="6.5" font-weight="750" fill="#ffffff">{testNode.label}</text>
        </g>
      {/each}
    </svg>
  </div>

  <div class="radar-caption" id="radar-caption">
    {#if selectedStep}
      <code>{targetLabel}</code> · {ring1Nodes.length ? `검증된 직접 연결 ${ring1Nodes.length}개` : '이 분석에서 직접 연결을 확인하지 못했습니다.'}
      <br />관련 테스트의 실행 결과는 이 분석에 없습니다.
    {:else}
      단계를 선택하면 직접 호출자와 관련 테스트가 레이더에 표시됩니다.
    {/if}
  </div>
</section>

<style>
  .radar-card {
    border: 1px solid var(--line, #ddd);
    border-radius: 8px;
    background: var(--paper, #fff);
    padding: 12px 14px;
    margin-bottom: 16px;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  }
  .radar-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    border-bottom: 1px solid var(--line, #e6e6e0);
    padding-bottom: 7px;
    margin-bottom: 9px;
  }
  .radar-title {
    font-size: 11px;
    font-weight: 800;
    letter-spacing: 0.6px;
    text-transform: uppercase;
    white-space: nowrap;
  }
  .pill {
    font-size: 9px;
    font-weight: 700;
    padding: 2px 7px;
    border-radius: 999px;
    white-space: nowrap;
  }
  .pill.invert {
    background: var(--ink, #171717);
    color: #fff;
    border: 1px solid var(--ink, #171717);
  }
  .pill.pill-broken {
    background: #c92a2a;
    color: #fff;
    border: 1px solid #a61e1e;
  }
  .radar-viewport {
    width: 100%;
    height: 185px;
    border: 1px solid var(--line, #e2e2dc);
    border-radius: 6px;
    background: #fafaf8;
    display: grid;
    place-items: center;
    position: relative;
    overflow: hidden;
  }
  .radar-viewport svg {
    width: 100%;
    height: 100%;
    display: block;
  }
  .radar-caption {
    font-size: 10px;
    color: var(--muted, #666);
    margin-top: 8px;
    line-height: 1.5;
    text-align: left;
  }
  .radar-caption code {
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    font-size: 9.5px;
    background: #f0f0ed;
    padding: 1px 4px;
    border-radius: 3px;
    color: var(--ink, #171717);
    font-weight: 600;
  }
  .radar-node {
    cursor: pointer;
    transition: filter .12s ease-out;
  }
  .radar-node:hover {
    filter: drop-shadow(0 2px 4px rgba(0, 0, 0, 0.22));
  }
</style>
