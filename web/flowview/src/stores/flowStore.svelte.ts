import type { FlowTaskViewData, Step, FlowContext } from '../types/flow';
import type { FlowSequence, FlowSequenceFrame } from '../types/flow_sequence';
import type { ChangeImpactGraph } from '../types/impact';

export const LAYER_LABELS: Record<string, string> = {
  presentation: 'UI',
  page: 'UI',
  ui: 'UI',
  controller: 'Controller',
  usecase: 'UseCase',
  application: 'UseCase',
  domain: 'Domain',
  data: 'Repository',
  repository: 'Repository',
  infra: 'Infrastructure',
  external: 'External'
};

export const GATEWAY_ROLES: Record<string, string> = {
  entry: '시작',
  decision: '판단',
  process: '처리',
  effect: '외부 효과',
  result: '결과',
  boundary: 'ANALYSIS BOUNDARY',
  presentation: 'UI EVENT',
  page: 'UI EVENT',
  ui: 'UI EVENT',
  ui_event: 'UI EVENT',
  gateway: 'GATEWAY',
  controller: 'GATEWAY',
  validator: 'GATEWAY',
  domain: 'DOMAIN CORE',
  domain_core: 'DOMAIN CORE',
  usecase: 'APPLICATION',
  application: 'APPLICATION',
  service: 'APPLICATION',
  external: 'EXTERNAL PG',
  external_pg: 'EXTERNAL PG',
  data: 'STATE PERSISTENCE',
  repository: 'STATE PERSISTENCE',
  infra: 'INFRASTRUCTURE'
};

export const EDGE_LABELS: Record<string, string> = {
  resolved_cross_file: '호출',
  call: '호출',
  calls: '호출',
  successor: '다음 처리',
  return: '반환',
  branch: '분기',
  error: '오류',
  failure: '실패',
  async: '비동기 호출'
};

export const DELTA_LABELS: Record<string, string> = {
  added_behavior: '행동 추가',
  changed_rule: '행동·조건 변경',
  removed_behavior: '행동 제거',
  evidence_updated: '근거 갱신',
  structural_only: '호출·위치 변경',
  ambiguous_move: '이동 판단 보류'
};

export interface SavedNavigationState {
  frameId: string | null;
  stepId: string | null;
  compare: boolean;
  scrollY: number;
}

export function isSourceContextMissing(data: FlowTaskViewData | null | undefined): boolean {
  if (!data) return false;
  if (data.sourceContextMissing || data.needsReanalysis) return true;
  const hasContexts = data.flowContexts && Object.keys(data.flowContexts).length > 0;
  const hasSources = data.sourceFiles && Object.keys(data.sourceFiles).length > 0;
  if (!hasContexts && !hasSources && (data.semanticMap?.steps?.length || 0) > 0) {
    return true;
  }
  if (data.sourceNotice && (
    data.sourceNotice.includes('재분석이 필요') ||
    data.sourceNotice.includes('보존된 소스 문맥이 없어') ||
    data.sourceNotice.includes('자동 재분석')
  )) {
    return true;
  }
  return false;
}

class FlowStore {
  data = $state<FlowTaskViewData | null>(null);
  baseline = $state<FlowTaskViewData | null>(null);
  pending = $state<FlowTaskViewData | null>(null);
  selectedStepId = $state<string | null>(null);
  selectedFrameId = $state<string | null>(null);
  viewMode = $state<'code' | 'process'>('code');
  conditionFilter = $state<string | null>(null);
  paused = $state<boolean>(false);
  compare = $state<boolean>(false);
  expanded = $state<Set<string>>(new Set());
  notice = $state<string>('');
  home = $state(true);
  busy = $state(false);
  listError = $state('');
  errorCode = $state('');
  candidates = $state<string[]>([]);
  views = $state<Array<{viewId: string; title: string; savedAt?: string}>>([]);
  impactCache = $state<Map<string, ChangeImpactGraph>>(new Map());
  savedNavigationState = $state<SavedNavigationState | null>(null);
  generationSequence = $state(0);
  isAutoReanalyzing = $state(false);
  activeLabelAbortController: AbortController | null = null;
  activeReanalyzeAbortController: AbortController | null = null;

  // Derived getters
  get flowSequence(): FlowSequence | null {
    return this.data?.flowSequence || null;
  }

  get frames(): FlowSequenceFrame[] {
    return this.data?.flowSequence?.frames || [];
  }

  get selectedFrame(): FlowSequenceFrame | null {
    if (!this.frames.length) return null;
    if (this.selectedFrameId) {
      const match = this.frames.find(f => f.frameID === this.selectedFrameId);
      if (match) return match;
    }
    if (this.selectedStepId) {
      const match = this.frames.find(f => f.primaryStepRef === this.selectedStepId || f.stepRefs.includes(this.selectedStepId!));
      if (match) return match;
    }
    return this.frames[0] || null;
  }

  get steps(): Step[] {
    return this.data?.semanticMap?.steps || [];
  }

  get selectedStep(): Step | null {
    return this.steps.find(s => s.stepId === this.selectedStepId) || null;
  }

  get sceneSteps(): Step[] {
    const refs = this.selectedFrame?.stepRefs || [];
    return refs.map(id => this.steps.find(s => s.stepId === id)).filter((s): s is Step => !!s);
  }

  get flowTitle(): string {
    return this.data?.semanticMap?.summary?.requested || '요청한 코드 흐름';
  }

  get layersList(): string[] {
    return [...new Set(this.steps.map(s => LAYER_LABELS[s.layer] || s.layer || '계층 미확인'))];
  }

  get activeDeltaChanges() {
    return (this.data?.semanticDelta?.changes || []).filter(c => DELTA_LABELS[c.kind]);
  }

  get matchingStepIds(): Set<string> | null {
    if (!this.conditionFilter) return null;
    const branch = this.steps.find(s => s.stepId === this.conditionFilter);
    if (!branch) return null;
    const ids = new Set<string>([branch.stepId]);
    for (const edge of this.data?.semanticMap?.edges || []) {
      if (edge.resolutionStatus === 'resolved' && (edge.fromStepId === branch.stepId || edge.toStepId === branch.stepId)) {
        ids.add(edge.fromStepId);
        ids.add(edge.toStepId);
      }
    }
    return ids;
  }

  setConditionFilter(stepId: string | null) {
    this.conditionFilter = stepId;
  }

  select(stepOrFrameId: string) {
    this.abortLabeling();
    const frame = this.frames.find(f => f.frameID === stepOrFrameId || f.primaryStepRef === stepOrFrameId || f.stepRefs.includes(stepOrFrameId));
    if (frame) {
      this.selectedFrameId = frame.frameID;
      this.selectedStepId = stepOrFrameId === frame.frameID ? frame.primaryStepRef : stepOrFrameId;
    }
  }

  setViewMode(mode: 'code' | 'process') {
    this.viewMode = mode;
  }

  toggleCompare() {
    if (!this.baseline) return;
    this.compare = !this.compare;
  }

  setBaseline(base: FlowTaskViewData) {
    this.baseline = base;
    this.notice = '새 비교 기준을 고정했습니다.';
  }

  toggleExpand(stepId: string) {
    const next = new Set(this.expanded);
    if (next.has(stepId)) {
      next.delete(stepId);
    } else {
      next.add(stepId);
    }
    this.expanded = next;
  }

  saveNavigationState() {
    const scrollY = typeof window !== 'undefined' ? window.scrollY : 0;
    this.savedNavigationState = {
      frameId: this.selectedFrameId,
      stepId: this.selectedStepId,
      compare: this.compare,
      scrollY
    };
  }

  restoreNavigationState() {
    if (!this.savedNavigationState) return;
    const saved = this.savedNavigationState;
    if (saved.stepId) {
      this.select(saved.stepId);
    }
    this.compare = saved.compare;
    this.savedNavigationState = null;
    if (typeof window !== 'undefined') {
      window.scrollTo({ top: saved.scrollY, behavior: 'smooth' });
    }
    this.notice = '원래 읽던 장면 위치로 복귀했습니다.';
  }

  adopt(newData: FlowTaskViewData, force = false, isAutoReanalysisResult = false): boolean {
    if (!newData.flowSequence) throw new Error('FlowSequence가 없습니다. 다시 분석해 주세요.');
    const current = this.selectedFrame;
    const matches = newData.flowSequence.frames.filter(f => f.frameMatchKey === current?.frameMatchKey);
    const previousStep = this.selectedStep;
    const stepMatches = newData.semanticMap.steps.filter(s => s.structuralIdentity && s.structuralIdentity === previousStep?.structuralIdentity);
    const retainedStep = newData.semanticMap.steps.find(s => s.stepId === this.selectedStepId) || (stepMatches.length === 1 ? stepMatches[0] : null);
    if (!force && current && (matches.length !== 1 || !retainedStep || !matches[0].stepRefs.includes(retainedStep.stepId))) {
      this.pending = newData;
      this.notice = '선택한 장면이 새 분석에서 대응되지 않아 이전 화면을 유지합니다.';
      return false;
    }
    const oldSteps = this.steps;
    const remapStep = (id: string) => {
      const old = oldSteps.find(s => s.stepId === id);
      const candidates = newData.semanticMap.steps.filter(s => s.stepId === id || (old?.structuralIdentity && s.structuralIdentity === old.structuralIdentity));
      return candidates.length === 1 ? candidates[0].stepId : null;
    };
    const retainedExpanded = new Set([...this.expanded].map(remapStep).filter((id): id is string => !!id));
    const retainedFilter = this.conditionFilter ? remapStep(this.conditionFilter) : null;
    this.data = newData;
    this.pending = null;
    this.impactCache = new Map();
    this.compare = false;
    if (force || !current) {
      this.expanded = new Set();
      this.conditionFilter = null;
      this.selectedFrameId = newData.flowSequence.frames[0]?.frameID || null;
      this.selectedStepId = newData.flowSequence.frames[0]?.primaryStepRef || null;
      this.savedNavigationState = null;
    } else {
      this.selectedFrameId = matches[0].frameID;
      this.selectedStepId = retainedStep!.stepId;
      this.expanded = retainedExpanded;
      this.conditionFilter = retainedFilter;
    }
    const missingSource = isSourceContextMissing(newData);
    if (isAutoReanalysisResult) {
      if (missingSource) {
        this.notice = '현재 워킹 트리에서도 해당 소스 문맥을 찾을 수 없습니다.';
      } else {
        this.notice = '현재 워킹 트리를 기반으로 최신 분석으로 갱신되었습니다.';
        this.labelFlowSequence().catch(() => {});
      }
    } else if (missingSource) {
      this.notice = newData.sourceNotice || '과거 분석에 보존된 소스 문맥이 없어 현재 워킹 트리 기반으로 자동 재분석 중입니다…';
      this.triggerAutoReanalysis().catch(() => {});
    } else {
      this.notice = newData.sourceNotice || '저장된 분석의 FlowSequence입니다.';
      this.labelFlowSequence().catch(() => {});
    }
    return true;
  }

  abortReanalysis() {
    if (this.activeReanalyzeAbortController) {
      this.activeReanalyzeAbortController.abort();
      this.activeReanalyzeAbortController = null;
      this.isAutoReanalyzing = false;
      this.generationSequence++;
    }
  }

  async triggerAutoReanalysis(): Promise<boolean> {
    if (!this.data) return false;
    const req = this.data.request;
    const firstStep = this.data.semanticMap?.steps?.[0];
    const anchorSymbol = firstStep?.anchor?.repoRelativePath && firstStep?.anchor?.enclosingSymbolPath
      ? `${firstStep.anchor.repoRelativePath}#${firstStep.anchor.enclosingSymbolPath}`
      : '';
    let entrySymbol = req?.entrySymbol || '';
    let query = req?.request || this.data.semanticMap?.summary?.requested || '';
    const flowId = this.data.flowId || req?.flowId || '';

    if (!entrySymbol && query.includes('#')) {
      entrySymbol = query;
      query = '';
    }
    if (!entrySymbol) {
      entrySymbol = anchorSymbol;
    }

    if (!entrySymbol && !query && !flowId) return false;

    this.abortReanalysis();
    const currentSequence = ++this.generationSequence;
    const controller = new AbortController();
    this.activeReanalyzeAbortController = controller;
    this.isAutoReanalyzing = true;

    try {
      const search = new URLSearchParams({ mode: 'feature' });
      if (query && !query.includes('#')) search.set('query', query);
      if (entrySymbol) search.set('entrySymbol', entrySymbol);
      if (flowId) search.set('flowId', flowId);
      if (req?.domain) search.set('domain', req.domain);

      const headers: Record<string, string> = {};
      if (typeof window !== 'undefined') {
        const token = new URLSearchParams(window.location.search).get('token') || (window as any).__codeflowToken;
        if (token) headers['X-CodeFlow-Token'] = token;
      }

      const resp = await fetch(`/api/task/view?${search.toString()}`, {
        signal: controller.signal,
        headers
      });

      if (controller.signal.aborted || currentSequence !== this.generationSequence) {
        return false;
      }

      if (!resp.ok) {
        if (currentSequence === this.generationSequence) {
          const errData = await resp.json().catch(() => null);
          const detail = errData?.message || `상태 코드 ${resp.status}`;
          this.notice = `과거 분석에 보존된 소스 문맥이 없으며, 자동 재분석을 완료하지 못했습니다 (${detail}).`;
        }
        return false;
      }

      const latestData: FlowTaskViewData = await resp.json();
      if (controller.signal.aborted || currentSequence !== this.generationSequence) return false;

      this.adopt(latestData, true, true);
      if (typeof window !== 'undefined' && typeof window.history !== 'undefined' && latestData.viewId) {
        const url = new URL(window.location.href);
        url.searchParams.set('viewId', latestData.viewId);
        url.searchParams.delete('flow');
        url.searchParams.delete('flowId');
        window.history.replaceState({}, '', url.toString());

        fetch('/api/views', { headers }).then(r => r.json()).then(v => {
          if (v?.views && currentSequence === this.generationSequence) {
            this.views = v.views;
          }
        }).catch(() => {});
      }
      return true;
    } catch (e: any) {
      if (e?.name === 'AbortError') return false;
      if (currentSequence === this.generationSequence) {
        this.notice = `과거 분석에 보존된 소스 문맥이 없으며, 자동 재분석 중 오류가 발생했습니다: ${e?.message || e}`;
      }
      return false;
    } finally {
      if (this.activeReanalyzeAbortController === controller) {
        this.activeReanalyzeAbortController = null;
        this.isAutoReanalyzing = false;
      }
    }
  }

  abortLabeling() {
    if (this.activeLabelAbortController) {
      this.activeLabelAbortController.abort();
      this.activeLabelAbortController = null;
      this.generationSequence++;
    }
  }

  async labelFlowSequence(): Promise<boolean> {
    if (!this.data?.flowSequence?.frames?.length) return false;

    if (this.activeLabelAbortController) {
      this.activeLabelAbortController.abort();
      this.activeLabelAbortController = null;
    }

    const currentSequence = ++this.generationSequence;
    const controller = new AbortController();
    this.activeLabelAbortController = controller;

    try {
      const resp = await fetch('/api/semantic/labels', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowID: this.data.flowId || 'current-flow',
          snapshotID: this.data.flowSequence.snapshotID,
          frames: this.data.flowSequence.frames
        }),
        signal: controller.signal
      });

      if (!resp.ok) return false;
      const res = await resp.json();

      // Discard stale response if generationSequence changed or request was aborted
      if (currentSequence !== this.generationSequence || !res.labels?.length) {
        return false;
      }

      const textMap = new Map<string, string>();
      for (const item of res.labels) {
        if (item.frameID && item.text && item.status === 'proposed') {
          textMap.set(item.frameID, item.text);
        }
      }

      if (this.data?.flowSequence?.frames) {
        for (const frame of this.data.flowSequence.frames) {
          if (textMap.has(frame.frameID)) {
            frame.text = textMap.get(frame.frameID)!;
          }
        }
      }
      return true;
    } catch {
      // Silent fallback
      return false;
    } finally {
      if (this.activeLabelAbortController === controller) {
        this.activeLabelAbortController = null;
      }
    }
  }

  receive(newData: FlowTaskViewData) { return this.adopt(newData); }
}

export const flowStore = new FlowStore();
