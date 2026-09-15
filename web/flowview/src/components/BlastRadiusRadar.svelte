<script lang="ts">
  import { flowStore } from '../stores/flowStore.svelte';
  import type { Step } from '../types/flow';
  import type { ChangeImpactGraph } from '../types/impact';

  const selectedStep = $derived(flowStore.selectedStep);
  const data = $derived(flowStore.data);

  let impactGraph = $state<ChangeImpactGraph | null>(null);

  // Fallback impact generator when API is not available
  function getFallbackImpact(step: Step): ChangeImpactGraph {
    const incoming = (data?.semanticMap?.edges || []).filter(
      e => e.toStepId === step.stepId && ['call', 'calls'].includes(e.kind)
    );
    const callers = incoming.map(e => {
      const from = flowStore.steps.find(s => s.stepId === e.fromStepId);
      return {
        name: from?.technicalName || from?.name || 'Caller',
        symbolPath: from?.technicalName || ''
      };
    });
    return {
      directImpact: { callers, tests: [] },
      indirectImpact: { callers: [] }
    };
  }

  // Effect to load impact data when selected step changes
  $effect(() => {
    if (!selectedStep) {
      impactGraph = null;
      return;
    }

    if (flowStore.impactCache.has(selectedStep.stepId)) {
      impactGraph = flowStore.impactCache.get(selectedStep.stepId)!;
      return;
    }

    // Check sample mock impact data
    const sampleImpact = data?.sampleImpacts?.[selectedStep.stepId];
    if (sampleImpact) {
      impactGraph = sampleImpact;
      flowStore.impactCache.set(selectedStep.stepId, sampleImpact);
      return;
    }

    // Fallback based on incoming edges
    const fallback = getFallbackImpact(selectedStep);
    impactGraph = fallback;

    // Asynchronously try live /api/task/impact if running in server mode
    if (window.location.protocol.startsWith('http')) {
      const token = new URLSearchParams(window.location.search).get('token') || '';
      const symbol = selectedStep.technicalName || selectedStep.name;
      fetch(`/api/task/impact?target=${encodeURIComponent(symbol)}${token ? `&token=${encodeURIComponent(token)}` : ''}`)
        .then(r => r.ok ? r.json() : null)
        .then(res => {
          if (res?.directImpact) {
            impactGraph = res;
            flowStore.impactCache.set(selectedStep.stepId, res);
          }
        })
        .catch(() => {});
    }
  });

  const ring1Nodes = $derived.by(() => {
    if (!impactGraph) return [];
    const callers = impactGraph.directImpact?.callers || [];
    const mutations = impactGraph.directImpact?.stateMutations || [];
    return [
      ...callers.map(c => ({ name: c.name || c.symbolPath, symbol: c.symbolPath, kind: 'caller' })),
      ...mutations.map(m => ({ name: m.targetState || 'State', symbol: m.targetState, kind: 'state' }))
    ];
  });

  const ring2Nodes = $derived.by(() => {
    if (!impactGraph) return [];
    return (impactGraph.indirectImpact?.callers || []).map(c => ({
      name: c.name || c.symbolPath,
      symbol: c.symbolPath,
      kind: 'indirect'
    }));
  });

  const ring3Nodes = $derived.by(() => {
    if (!impactGraph) return [];
    const tests = [
      ...(impactGraph.directImpact?.tests || []),
      ...(impactGraph.indirectImpact?.tests || [])
    ];
    return tests.map(t => ({
      name: t.testSymbolPath || t.testFile || 'test',
      symbol: t.testSymbolPath,
      broken: !!t.broken,
      kind: 'test'
    }));
  });

  const activeRingsCount = $derived(
    (ring1Nodes.length ? 1 : 0) + (ring2Nodes.length ? 1 : 0) + (ring3Nodes.length ? 1 : 0)
  );

  const hasBrokenTest = $derived(ring3Nodes.some(t => t.broken));

  const statusPillText = $derived.by(() => {
    if (!selectedStep) return 'Impact: None';
    if (hasBrokenTest) return 'Impact: High (Broken Test)';
    if (activeRingsCount > 0) return `Impact: ${activeRingsCount} Ring${activeRingsCount > 1 ? 's' : ''}`;
    return 'Impact: Direct Only';
  });

  const targetLabel = $derived.by(() => {
    if (!selectedStep) return '대기 중';
    const sym = selectedStep.technicalName || selectedStep.name;
    return sym.split('.').pop()?.slice(0, 14) || sym;
  });

  function getNodeCoordinates(total: number, index: number, radius: number, angleOffset: number) {
    const theta = (2 * Math.PI * index / total) + angleOffset;
    return {
      x: Math.round(radius * Math.cos(theta)),
      y: Math.round(radius * Math.sin(theta))
    };
  }

  function handleNodeClick(symbol: string) {
    const matched = flowStore.steps.find(s => s.technicalName === symbol || s.stepId === symbol);
    if (matched) {
      flowStore.select(matched.stepId);
    }
  }
</script>

<section class="radar-card" id="radar-card" aria-label="파급 영향 레이더">
  <div class="radar-head">
    <span class="radar-title">Blast Radius Radar</span>
    <span class="pill invert" id="radar-pill">{statusPillText}</span>
  </div>

  <div class="radar-viewport">
    <svg id="radar-svg" viewBox="-125 -125 250 250" preserveAspectRatio="xMidYMid meet" role="img" aria-label="파급 영향 동심원 그래프">
      <!-- 3 Concentric Rings -->
      <circle cx="0" cy="0" r="28" fill="none" stroke="#dcdcd8" stroke-dasharray="3 3"></circle>
      <circle cx="0" cy="0" r="58" fill="none" stroke="#dcdcd8" stroke-dasharray="3 3"></circle>
      <circle cx="0" cy="0" r="88" fill="none" stroke="#dcdcd8" stroke-dasharray="3 3"></circle>

      <!-- Center Target Node -->
      <circle cx="0" cy="0" r="11" fill="#171717"></circle>
      <text x="0" y="3" text-anchor="middle" fill="#ffffff" font-size="7" font-weight="900">TARGET</text>
      <text id="radar-target-label" x="0" y="19" text-anchor="middle" font-size="7.5" font-weight="700">{targetLabel}</text>

      <!-- Ring 1 Nodes (Direct Callers / State Mutations) -->
      {#each ring1Nodes as node, i}
        {@const coords = getNodeCoordinates(ring1Nodes.length, i, 28, -Math.PI / 2)}
        {@const shortName = node.name.split('.').pop()?.slice(0, 8) || node.name}
        <g
          class="radar-node"
          transform="translate({coords.x}, {coords.y})"
          data-radar-symbol={node.symbol}
          role="button"
          tabindex="0"
          onclick={() => handleNodeClick(node.symbol)}
          onkeydown={(e) => e.key === 'Enter' && handleNodeClick(node.symbol)}
        >
          <rect x="-20" y="-6" width="40" height="12" rx="3" fill="#ffffff" stroke="#171717" stroke-width="1"></rect>
          <text x="0" y="3" text-anchor="middle" font-size="6.5" font-weight="750" fill="#171717">{shortName}</text>
        </g>
      {/each}

      <!-- Ring 2 Nodes (Indirect Callers) -->
      {#each ring2Nodes as node, i}
        {@const coords = getNodeCoordinates(ring2Nodes.length, i, 58, -Math.PI / 4)}
        {@const shortName = node.name.split('.').pop()?.slice(0, 9) || node.name}
        <g
          class="radar-node"
          transform="translate({coords.x}, {coords.y})"
          data-radar-symbol={node.symbol}
          role="button"
          tabindex="0"
          onclick={() => handleNodeClick(node.symbol)}
          onkeydown={(e) => e.key === 'Enter' && handleNodeClick(node.symbol)}
        >
          <rect x="-22" y="-6" width="44" height="12" rx="3" fill="#ffffff" stroke="#666666" stroke-dasharray="2 2" stroke-width="1"></rect>
          <text x="0" y="3" text-anchor="middle" font-size="6.5" font-weight="700" fill="#555555">{shortName}</text>
        </g>
      {/each}

      <!-- Ring 3 Nodes (Tests) -->
      {#each ring3Nodes as testNode, i}
        {@const coords = getNodeCoordinates(ring3Nodes.length, i, 88, -Math.PI / 4)}
        {@const shortName = testNode.name.split('/').pop()?.slice(0, 10) || testNode.name}
        <g
          class="radar-node"
          transform="translate({coords.x}, {coords.y})"
          data-radar-symbol={testNode.symbol}
          role="button"
          tabindex="0"
          onclick={() => handleNodeClick(testNode.symbol)}
          onkeydown={(e) => e.key === 'Enter' && handleNodeClick(testNode.symbol)}
        >
          <rect x="-26" y="-7" width="52" height="14" rx="3" fill={testNode.broken ? '#c92a2a' : '#171717'}></rect>
          <text x="0" y="3" text-anchor="middle" font-size="6.5" font-weight="750" fill="#ffffff">{shortName}</text>
        </g>
      {/each}
    </svg>
  </div>

  <div class="radar-caption" id="radar-caption">
    {#if selectedStep}
      심볼 {targetLabel}의 상위 호출자 {ring1Nodes.length + ring2Nodes.length}개 및 테스트 {ring3Nodes.length}개 추적
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
    padding: 12px;
    margin-bottom: 16px;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.04);
  }
  .radar-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }
  .radar-title {
    font-size: 11px;
    font-weight: 800;
    letter-spacing: 0.5px;
  }
  .pill {
    font-size: 10px;
    font-weight: 700;
    padding: 2px 8px;
    border-radius: 999px;
  }
  .pill.invert {
    background: var(--ink, #171717);
    color: #fff;
    border: 1px solid var(--ink, #171717);
  }
  .radar-viewport {
    width: 100%;
    aspect-ratio: 1 / 1;
    max-width: 230px;
    margin: 0 auto;
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
    text-align: center;
  }
  .radar-node {
    cursor: pointer;
    transition: opacity .15s, transform .15s;
  }
  .radar-node:hover {
    filter: drop-shadow(0 1px 3px rgba(0, 0, 0, 0.25));
  }
</style>
