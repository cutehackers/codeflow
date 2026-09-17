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
  });

  it('populates steps and derived properties upon receiving data', () => {
    const data = samplePayload(1);
    flowStore.receive(data);

    expect(flowStore.steps.length).toBe(5);
    expect(flowStore.flowTitle).toBe('고객 결제 요청에서 PG사 승인까지 5개 관문 엔드투엔드 시퀀스');
    expect(flowStore.selectedStep?.stepId).toBe('checkout_click');
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

  it('triggers background automatic reanalysis when past session source context is missing', async () => {
    const historical = samplePayload(1);
    // Simulate missing source context from disk/past session
    historical.flowContexts = {};
    historical.sourceFiles = {};
    historical.sourceContextMissing = true;
    historical.sourceNotice = '과거 분석에 보존된 소스 문맥이 없어 재분석이 필요합니다. 현재 워킹 트리 기반으로 자동 재분석을 진행합니다.';
    historical.request = { request: '주문 결제', entrySymbol: 'checkout.go#Checkout', flowId: 'flow-checkout-1' };

    const reanalyzed = samplePayload(2);
    reanalyzed.flowContexts = {
      'checkout': {
        stepId: 'checkout',
        canonicalPath: 'checkout.go',
        displayedLines: [{ lineNumber: 1, text: 'func Checkout() {}', isHit: true }]
      }
    };
    reanalyzed.sourceFiles = {
      'checkout.go': [{ lineNumber: 1, text: 'func Checkout() {}', isHit: true }]
    };

    const originalFetch = globalThis.fetch;
    const requestedUrls: string[] = [];
    globalThis.fetch = async (url: any) => {
      requestedUrls.push(String(url));
      return {
        ok: true,
        json: async () => reanalyzed
      } as any;
    };

    try {
      flowStore.adopt(historical);
      expect(flowStore.isAutoReanalyzing).toBe(true);
      expect(flowStore.notice).toContain('자동 재분석');

      const reanalyzedSuccess = await flowStore.triggerAutoReanalysis();
      expect(reanalyzedSuccess).toBe(true);
      const reanalysisUrl = requestedUrls.find(u => u.includes('/api/task/view'));
      expect(reanalysisUrl).toBeDefined();
      expect(reanalysisUrl).toContain('entrySymbol=checkout.go%23Checkout');
      expect(flowStore.notice).toBe('현재 워킹 트리를 기반으로 최신 분석으로 갱신되었습니다.');
      expect(Object.keys(flowStore.data?.flowContexts || {}).length).toBeGreaterThan(0);
      expect(flowStore.isAutoReanalyzing).toBe(false);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('safely handles background auto-reanalysis failure without crashing', async () => {
    const historical = samplePayload(1);
    historical.flowContexts = {};
    historical.sourceFiles = {};
    historical.sourceContextMissing = true;
    historical.request = { request: '주문 결제', entrySymbol: 'checkout.go#Checkout' };

    const originalFetch = globalThis.fetch;
    globalThis.fetch = async () => ({
      ok: false,
      status: 500,
      json: async () => ({ message: 'adapter unavailable' })
    }) as any;

    try {
      flowStore.adopt(historical);
      const result = await flowStore.triggerAutoReanalysis();
      expect(result).toBe(false);
      expect(flowStore.notice).toContain('자동 재분석을 완료하지 못했습니다');
      expect(flowStore.flowSequence).not.toBeNull(); // Historical structure remains intact
      expect(flowStore.isAutoReanalyzing).toBe(false);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('discards stale auto-reanalysis response when aborted', async () => {
    const historical = samplePayload(1);
    historical.flowContexts = {};
    historical.sourceFiles = {};
    historical.sourceContextMissing = true;
    historical.request = { request: '주문 결제', entrySymbol: 'checkout.go#Checkout' };

    const fresh = samplePayload(2);

    const originalFetch = globalThis.fetch;
    globalThis.fetch = async () => {
      await new Promise(resolve => setTimeout(resolve, 40));
      return {
        ok: true,
        json: async () => fresh
      } as any;
    };

    try {
      flowStore.adopt(historical);
      const p = flowStore.triggerAutoReanalysis();
      flowStore.abortReanalysis();
      const result = await p;
      expect(result).toBe(false);
      expect(flowStore.isAutoReanalyzing).toBe(false);
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('terminates reanalysis without infinite loops when reanalyzed result still has missing context', async () => {
    const historical = samplePayload(1);
    historical.flowContexts = {};
    historical.sourceFiles = {};
    historical.sourceContextMissing = true;
    historical.request = { request: 'deleted/file.go#DeletedMethod' };

    // Fresh response also has no source files (e.g. deleted file on disk)
    const freshMissing = samplePayload(2);
    freshMissing.flowContexts = {};
    freshMissing.sourceFiles = {};

    let fetchCount = 0;
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async () => {
      fetchCount++;
      return {
        ok: true,
        json: async () => freshMissing
      } as any;
    };

    try {
      flowStore.adopt(historical);
      // Wait for any asynchronous reanalysis tasks
      await new Promise(resolve => setTimeout(resolve, 50));
      // Fetch should be invoked exactly once, not looping indefinitely
      expect(fetchCount).toBe(1);
      expect(flowStore.isAutoReanalyzing).toBe(false);
      expect(flowStore.notice).toContain('현재 워킹 트리에서도 해당 소스 문맥을 찾을 수 없습니다');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('normalizes query containing # into entrySymbol for reanalysis', async () => {
    const historical = samplePayload(1);
    historical.flowContexts = {};
    historical.sourceFiles = {};
    historical.sourceContextMissing = true;
    historical.request = { request: 'service/handler.go#HandleOrder' };

    const requestedUrls: string[] = [];
    const originalFetch = globalThis.fetch;
    globalThis.fetch = async (url: any) => {
      requestedUrls.push(String(url));
      return {
        ok: true,
        json: async () => samplePayload(2)
      } as any;
    };

    try {
      flowStore.adopt(historical);
      await flowStore.triggerAutoReanalysis();
      const taskViewUrl = requestedUrls.find(u => u.includes('/api/task/view'));
      expect(taskViewUrl).toBeDefined();
      expect(taskViewUrl).toContain('entrySymbol=service%2Fhandler.go%23HandleOrder');
      expect(taskViewUrl).not.toContain('query=');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});

