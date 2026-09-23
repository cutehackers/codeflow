import { describe, it, expect, beforeEach } from 'vitest';
import { flowStore } from '../stores/flowStore.svelte';
import { samplePayload } from '../stores/sampleData';

describe('FlowStore (Svelte 5 Runes)', () => {
  beforeEach(() => {
    flowStore.data = null;
    flowStore.selectedFrameId = null;
    flowStore.savedNavigationState = null;
    flowStore.expanded = new Set();
    flowStore.baseline = null;
    flowStore.pending = null;
    flowStore.paused = false;
    flowStore.compare = false;
    flowStore.selectedStepId = null;
    flowStore.conditionFilter = null;
    flowStore.viewMode = 'code';
    flowStore.overviewOpen = true;
    flowStore.expandedFrames = new Set();
    flowStore.selectedRelationId = null;
  });

  it('populates steps and derived properties upon receiving data', () => {
    const data = samplePayload(1);
    flowStore.receive(data);

    expect(flowStore.steps.length).toBe(5);
    expect(flowStore.flowTitle).toBe('고객 결제 요청에서 PG사 승인까지 5개 관문 엔드투엔드 시퀀스');
    expect(flowStore.selectedStep?.stepId).toBe('checkout_click');
  });

  it('preserves and exposes flowResolution with raw request title', () => {
    const data = samplePayload(1);
    data.flowResolution = {
      schemaVersion: 1,
      status: 'resolved',
      rawRequest: '원문 사용자 요청 흐름',
      candidateId: 'cand-01',
      entrySymbolPath: 'app/checkout.ts#processCheckout',
      flowId: 'flow-test-1234',
      evidence: [{
        candidateId: 'cand-01',
        entrySymbolPath: 'app/checkout.ts#processCheckout',
        description: '장바구니 결제 처리',
        snapshotId: 'snap-01',
        computedBasisId: 'basis-01'
      }]
    };
    flowStore.receive(data);

    expect(flowStore.flowResolution).toEqual(data.flowResolution);
    expect(flowStore.flowTitle).toBe('원문 사용자 요청 흐름');
  });

  it('selects steps by ID', () => {
    flowStore.receive(samplePayload(1));
    flowStore.select('validate_cart');

    expect(flowStore.selectedStepId).toBe('validate_cart');
    expect(flowStore.selectedStep?.technicalName).toBe('ValidateCartUseCase');
  });

  it('correctly detects rule modifications in semanticDelta', () => {
    flowStore.receive(samplePayload(2));
    const deltas = flowStore.activeDeltaChanges;

    expect(deltas.length).toBe(2);
    expect(deltas.some(d => d.kind === 'changed_rule')).toBe(true);
    expect(deltas.some(d => d.kind === 'added_behavior')).toBe(true);
  });

  it('computes matchingStepIds when condition filter is active', () => {
    flowStore.receive(samplePayload(2));
    flowStore.setConditionFilter('validate_cart');

    const matches = flowStore.matchingStepIds;
    expect(matches).not.toBeNull();
    expect(matches?.has('validate_cart')).toBe(true);
    expect(matches?.has('check_stock')).toBe(true); // connected edge
  });

  it('toggles view mode between code and process', () => {
    expect(flowStore.viewMode).toBe('code');
    flowStore.setViewMode('process');
    expect(flowStore.viewMode).toBe('process');
    flowStore.setViewMode('code');
    expect(flowStore.viewMode).toBe('code');
  });

  it('requires explicit baseline selection before comparison', () => {
    flowStore.receive(samplePayload(1));
    expect(flowStore.compare).toBe(false);

    flowStore.toggleCompare();
    expect(flowStore.compare).toBe(false);
    expect(flowStore.baseline).toBeNull();
    flowStore.setBaseline(samplePayload(2));
    flowStore.toggleCompare();
    expect(flowStore.compare).toBe(true);
  });

  it('populates flowSequence frames and synchronizes frame selection with steps', () => {
    flowStore.receive(samplePayload(1));
    expect(flowStore.frames.length).toBe(5);
    expect(flowStore.frames[0].frameID).toBe('frame-01');
    expect(flowStore.frames[0].role).toBe('entry');

    // Selecting frame-02 synchronizes selectedFrameId and selectedStepId
    flowStore.select('frame-02');
    expect(flowStore.selectedFrameId).toBe('frame-02');
    expect(flowStore.selectedStepId).toBe('validate_cart');
    expect(flowStore.selectedFrame?.title).toBe('장바구니 정합성 검증');

    // Selecting by stepId synchronizes frameId
    flowStore.select('check_stock');
    expect(flowStore.selectedFrameId).toBe('frame-03');
    expect(flowStore.selectedStepId).toBe('check_stock');
  });

  it('restores frame selection via frameMatchKey on re-analysis', () => {
    flowStore.receive(samplePayload(1));
    flowStore.select('frame-02'); // validate_cart (gateway / CartService)

    // Simulate re-analysis with modified payload where step positions might shift
    const reanalysis = samplePayload(2);
    // Keep frameMatchKey for validate_cart but shift frame order or internal step
    const success = flowStore.adopt(reanalysis);
    expect(success).toBe(true);
    expect(flowStore.selectedStepId).toBe('validate_cart');
    expect(flowStore.selectedFrameId).toBe('frame-02');
  });

  it('preserves screen when frameMatchKey has no match in re-analysis', () => {
    flowStore.receive(samplePayload(1));
    flowStore.select('frame-02');

    // Create payload where frame-02 symbol is completely removed
    const altered = samplePayload(2);
    altered.flowSequence!.frames = altered.flowSequence!.frames.filter(f => f.primaryStepRef !== 'validate_cart');
    altered.semanticMap.steps = altered.semanticMap.steps.filter(s => s.stepId !== 'validate_cart');
    altered.semanticMap.edges = altered.semanticMap.edges.filter(e => e.fromStepId !== 'validate_cart' && e.toStepId !== 'validate_cart');

    const success = flowStore.adopt(altered);
    expect(success).toBe(false);
    expect(flowStore.pending).toBe(altered);
    expect(flowStore.notice).toContain('선택한 장면이 새 분석에서 대응되지 않아');
  });

  it('saves and restores navigation state on relation exploration roundtrip', () => {
    flowStore.receive(samplePayload(1));
    flowStore.select('frame-02');
    expect(flowStore.selectedFrameId).toBe('frame-02');

    // User explores a relation -> saves state and jumps to relation frame
    flowStore.saveNavigationState();
    expect(flowStore.savedNavigationState).not.toBeNull();
    expect(flowStore.savedNavigationState?.frameId).toBe('frame-02');

    flowStore.select('frame-05');
    expect(flowStore.selectedFrameId).toBe('frame-05');

    // User closes relation / clicks return to original scene
    flowStore.restoreNavigationState();
    expect(flowStore.selectedFrameId).toBe('frame-02');
    expect(flowStore.savedNavigationState).toBeNull();
  });

  it('keeps an internal step selected and restores it after relation navigation', () => {
  const data = samplePayload(1);
  const detail = {...data.semanticMap.steps[0], stepId:'detail', structuralIdentity:'detail', name:'내부 처리'};
  data.semanticMap.steps.push(detail);
  data.flowSequence!.frames[0].stepRefs.push('detail');
  flowStore.adopt(data,true);
  flowStore.select('detail');
  expect(flowStore.selectedStep?.stepId).toBe('detail');
  expect(flowStore.selectedFrameId).toBe(data.flowSequence!.frames[0].frameID);
  flowStore.saveNavigationState();
  flowStore.select('validate_cart');
  flowStore.restoreNavigationState();
  expect(flowStore.selectedStep?.stepId).toBe('detail');
});

  it('does not invent scenes from raw steps or replace selection on ambiguous matching', () => {
    const data = samplePayload(1);
    flowStore.adopt(data, true);
    const missing = { ...data, flowSequence: undefined };
    expect(() => flowStore.adopt(missing)).toThrow('FlowSequence');
    const ambiguous = samplePayload(2);
    ambiguous.flowSequence!.frames.push({ ...ambiguous.flowSequence!.frames[0], frameID: 'duplicate' });
    expect(flowStore.adopt(ambiguous)).toBe(false);
    expect(flowStore.data?.semanticMap.generationId).toBe('sample-v1');
  });

  it('labels FlowSequence frames with micro-semantic texts', async () => {
    const data = samplePayload(1);
    flowStore.adopt(data, true);

    const originalFetch = globalThis.fetch;
    globalThis.fetch = async () => ({
      ok: true,
      json: async () => ({
        labels: [
          { frameID: data.flowSequence!.frames[0].frameID, text: '고객 주문 요청 접수 및 검증', status: 'proposed' }
        ]
      })
    }) as any;

    try {
      const proposed = await flowStore.labelFlowSequence();
      expect(proposed).toBe(true);
      expect(flowStore.flowSequence?.frames[0].text).toBe('고객 주문 요청 접수 및 검증');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('discards stale SLM response when scene selection aborts in-flight request', async () => {
    const data = samplePayload(1);
    flowStore.adopt(data, true);

    const originalFetch = globalThis.fetch;
    // Simulate slow network request
    globalThis.fetch = async () => {
      await new Promise(resolve => setTimeout(resolve, 50));
      return {
        ok: true,
        json: async () => ({
          labels: [
            { frameID: data.flowSequence!.frames[0].frameID, text: '뒤늦게 도착한 과거 응답', status: 'proposed' }
          ]
        })
      } as any;
    };

    try {
      const p = flowStore.labelFlowSequence();
      // User switches selection, triggering abortLabeling
      flowStore.select('validate_cart');
      const result = await p;
      // Stale response must be discarded (false) and not overwrite text
      expect(result).toBe(false);
      expect(flowStore.flowSequence?.frames[0].text).not.toBe('뒤늦게 도착한 과거 응답');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('keeps historical data without requesting automatic analysis when source is absent', async () => {
    const data = samplePayload(1);
    data.flowContexts = {};
    data.sourceFiles = {};
    data.sourceContextMissing = true;
    data.sourceNotice = '입력한 분석의 소스 근거를 검증하지 않았습니다. 다시 분석을 실행하세요.';
    const original = globalThis.fetch;
    const requests: string[] = [];
    globalThis.fetch = async (url: any) => { requests.push(String(url)); throw new Error('unexpected request'); };
    try {
      flowStore.adopt(data, true);
      await Promise.resolve();
      expect(requests).toEqual([]);
      expect(flowStore.data?.semanticMap.generationId).toBe(data.semanticMap.generationId);
      expect(flowStore.notice).toBe(data.sourceNotice);
    } finally { globalThis.fetch = original; }
  });
  it('expands an unselected frame and restores expansion and filtering after relation navigation', () => {
    flowStore.adopt(samplePayload(1), true);
    flowStore.toggleFrame('frame-02');
    expect(flowStore.selectedFrameId).toBe('frame-01');
    flowStore.setConditionFilter('validate_cart');
    flowStore.saveNavigationState();
    flowStore.toggleFrame('frame-02');
    flowStore.select('frame-03');
    flowStore.setConditionFilter(null);
    flowStore.restoreNavigationState();
    expect(flowStore.expandedFrames.has('frame-02')).toBe(true);
    expect(flowStore.selectedFrameId).toBe('frame-01');
    expect(flowStore.conditionFilter).toBe('validate_cart');
  });
});

it('returns to the first origin after multiple relation hops and restores the view mode', () => {
  flowStore.adopt(samplePayload(1), true);
  flowStore.setViewMode('process');
  flowStore.overviewOpen = false;
  flowStore.saveNavigationState();
  flowStore.select('validate_cart');
  flowStore.saveNavigationState();
  flowStore.select('check_stock');
  flowStore.setViewMode('code');
  flowStore.overviewOpen = true;
  flowStore.restoreNavigationState();
  expect(flowStore.selectedStepId).toBe('checkout_click');
  expect(flowStore.viewMode).toBe('process');
  expect(flowStore.overviewOpen).toBe(false);
});
