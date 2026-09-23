import { tick } from 'svelte';
import { conditionChoices, conditionReachability, retainReachableConditions, type ConditionSelection } from './conditionNavigation';
import { buildFlowRelations, validateFlowReferences } from './flowNavigation';
import type { FlowTaskViewData, Step, FlowContext, FlowResolution } from '../types/flow';
import type { FlowSequence, FlowSequenceFrame } from '../types/flow_sequence';
import type { ChangeImpactGraph } from '../types/impact';

export const GATEWAY_ROLES: Record<string, string> = {
  entry: '시작',
  decision: '판단',
  process: '처리',
  effect: '외부 효과',
  result: '결과',
  boundary: '분석 경계'
};

export const EDGE_LABELS: Record<string, string> = {
  resolved_cross_file: '호출',
  call: '호출',
  calls: '호출',
  successor: '다음 처리',
  control_flow: '다음 처리',
  return: '복귀',
  loop_exit_back: '내부 반복 종료 후 외부 조건 재평가',
  loop_exit: '반복 종료 후 진행',
  switch_exit: '분기 종료 후 진행',
  parallel_wait: '병렬 완료 대기',
  loop_back: '다음 반복의 조건 재평가',
  loop_reentry: '다음 반복 본문 진입',
  await_loop_back: '대기 성공 후 다음 반복의 조건 재평가',
  await_resume: '대기 성공 후 재개',
  branch: '분기',
  error: '오류',
  failure: '실패',
  finally: '정리 실행',
  async: '비동기 인계',
  cycle: '반복 연결',
  external: '외부 연결'
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
  viewMode: 'code' | 'process';
  overviewOpen: boolean;
  scrollY: number;
  expandedFrames: string[];
  expandedCode: string[];
  conditionFilter: string | null;
  conditionSelections: ConditionSelection[];
  relationId: string | null;
  panelPositions: Array<{id: string; top: number; left: number}>;
  focusElement: HTMLElement | null;
  focusKey: string | null;
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
  conditionSelections = $state<ConditionSelection[]>([]);
  get conditionChoices() { return conditionChoices(this.data); }
  get conditionReachability() { return conditionReachability(this.data, this.conditionSelections); }
  expandedFrames = $state<Set<string>>(new Set());
  selectedRelationId = $state<string | null>(null);
  overviewOpen = $state(true);
  get relations() { return buildFlowRelations(this.data); }
  get selectedRelation() { return this.relations.find(r => r.id === this.selectedRelationId) || null; }
  get incomingRelations() { return this.relations.filter(r => r.edge.toStepId === this.selectedStepId); }
  get outgoingRelations() { return this.relations.filter(r => r.edge.fromStepId === this.selectedStepId); }
  stepsForFrame(frame: FlowSequenceFrame): Step[] {
    const steps = new Map(this.steps.map(s => [s.stepId, s]));
    return frame.stepRefs.map(id => steps.get(id)).filter((s): s is Step => !!s);
  }
  toggleFrame(frameID: string) {
    const next = new Set(this.expandedFrames);
    if (next.has(frameID)) next.delete(frameID); else next.add(frameID);
    this.expandedFrames = next;
  }
  selectRelation(id: string | null) {
    this.selectedRelationId = id;
    this.conditionFilter = null;
    this.conditionSelections = [];
  }
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

  get flowResolution(): FlowResolution | null {
    return this.data?.flowResolution || null;
  }

  get flowTitle(): string {
    return this.flowResolution?.rawRequest || this.data?.semanticMap?.summary?.requested || '요청한 코드 흐름';
  }

  get activeDeltaChanges() {
    return (this.data?.semanticDelta?.changes || []).filter(c => DELTA_LABELS[c.kind]);
  }

  get matchingStepIds(): Set<string> | null {
    if (this.selectedRelation) {
      const relation = this.selectedRelation;
      return new Set([relation.edge.fromStepId, ...(relation.resolved ? [relation.edge.toStepId] : [])]);
    }
    if (this.conditionSelections.length) return this.conditionReachability.steps;
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
    this.conditionSelections = [];
    this.conditionFilter = stepId;
    this.selectedRelationId = null;
  }

  chooseCondition(stepId: string, outcome: ConditionSelection['outcome']) {
    const candidates = this.conditionSelections.map(selection => ({ ...selection }));
    const index = candidates.findIndex(selection => selection.stepId === stepId);
    if (index < 0) candidates.push({stepId, outcome}); else candidates[index] = {stepId, outcome};
    const retained = retainReachableConditions(this.data, candidates);
    this.conditionSelections = retained;
    this.conditionFilter = null;
    this.selectedRelationId = null;
    this.notice = retained.length < candidates.length ? '선택한 경로에서 도달할 수 없는 하위 조건을 해제했습니다.' : '';
  }

  removeCondition(stepId: string) {
    if (this.conditionSelections[0]?.stepId === stepId) {
      const hadChildren = this.conditionSelections.length > 1;
      this.conditionSelections = [];
      this.notice = hadChildren ? '시작 조건과 그 아래 선택한 조건을 해제했습니다.' : '';
      return;
    }
    this.conditionSelections = retainReachableConditions(this.data, this.conditionSelections.filter(selection => selection.stepId !== stepId));
  }

  select(stepOrFrameId: string) {
    this.abortLabeling();
    const frame = this.frames.find(f => f.frameID === stepOrFrameId || f.primaryStepRef === stepOrFrameId || f.stepRefs.includes(stepOrFrameId));
    if (frame) {
      this.selectedFrameId = frame.frameID;
      this.selectedStepId = stepOrFrameId === frame.frameID ? frame.primaryStepRef : stepOrFrameId;
      const selected = this.selectedStepId;
      void tick().then(() => {
        if (selected !== this.selectedStepId || typeof document === 'undefined') return;
        const code = document.querySelector<HTMLElement>('[data-code-focus] .compare-grid > div:last-child .line.hit, [data-code-focus] > article > pre .line.hit') || document.querySelector<HTMLElement>('[data-code-focus] .line.hit, [data-code-focus] .source-empty');
        if (code) {
          const rect = code.getBoundingClientRect();
          if (rect.top < 0 || rect.bottom > window.innerHeight) code.scrollIntoView({block:'center'});
        }
      });
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

  captureNavigationState(): SavedNavigationState {
    return {
      frameId: this.selectedFrameId,
      stepId: this.selectedStepId,
      compare: this.compare,
      viewMode: this.viewMode,
      overviewOpen: this.overviewOpen,
      scrollY: typeof window === 'undefined' ? 0 : window.scrollY,
      expandedFrames: [...this.expandedFrames],
      expandedCode: [...this.expanded],
      conditionFilter: this.conditionFilter,
      conditionSelections: this.conditionSelections.map(selection => ({ ...selection })),
      relationId: this.selectedRelationId,
      panelPositions: typeof document === 'undefined' ? [] : Array.from(document.querySelectorAll<HTMLElement>('[data-navigation-panel]')).map(el => ({id: el.id, top: el.scrollTop, left: el.scrollLeft})),
      focusElement: typeof document === 'undefined' ? null : document.activeElement as HTMLElement,
      focusKey: typeof document === 'undefined' ? null : (document.activeElement as HTMLElement)?.dataset.navigationFocus || null,
    };
  }

  saveNavigationState() {
    if (!this.savedNavigationState) this.savedNavigationState = this.captureNavigationState();
  }

  applyNavigationState(saved: SavedNavigationState) {
    if (saved.stepId) this.select(saved.stepId);
    this.compare = saved.compare;
    this.viewMode = saved.viewMode;
    this.overviewOpen = saved.overviewOpen;
    this.expandedFrames = new Set(saved.expandedFrames);
    this.expanded = new Set(saved.expandedCode);
    this.conditionFilter = saved.conditionFilter;
    this.conditionSelections = retainReachableConditions(this.data, saved.conditionSelections || []);
    this.selectedRelationId = saved.relationId;
    void tick().then(() => {
      if (typeof document === 'undefined') return;
      for (const position of saved.panelPositions) document.getElementById(position.id)?.scrollTo({top: position.top, left: position.left});
      const focusControl = Array.from(document.querySelectorAll<HTMLElement>('[data-navigation-focus]')).find(el => saved.focusKey && el.dataset.navigationFocus === saved.focusKey);
      const navControls = Array.from(document.querySelectorAll<HTMLElement>('[data-flow-step], [data-frame]'));
      const fallback = navControls.find(el => el.dataset.flowStep === saved.stepId) || navControls.find(el => el.dataset.frame === saved.frameId);
      const focus = saved.focusElement?.isConnected ? saved.focusElement : focusControl || fallback;
      focus?.focus({preventScroll:true});
      window.scrollTo({top:saved.scrollY});
    });
  }

  restoreNavigationState() {
    if (!this.savedNavigationState) return;
    this.applyNavigationState(this.savedNavigationState);
    this.savedNavigationState = null;
    this.notice = '원래 읽던 장면 위치로 복귀했습니다.';
  }

  adopt(newData: FlowTaskViewData, force = false, isAutoReanalysisResult = false): boolean {
    if (!newData.flowSequence) throw new Error('FlowSequence가 없습니다. 다시 분석해 주세요.');
    const referenceError = validateFlowReferences(newData);
    if (referenceError) {
      this.notice = referenceError;
      return false;
    }
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
    const frameKeys = new Set(this.frames.filter(f => this.expandedFrames.has(f.frameID)).map(f => f.frameMatchKey));
    const retainedFrames = newData.flowSequence.frames.filter(f => frameKeys.has(f.frameMatchKey) && newData.flowSequence!.frames.filter(other => other.frameMatchKey === f.frameMatchKey).length === 1).map(f => f.frameID);
    const oldSteps = this.steps;
    const remapStep = (id: string) => {
      const old = oldSteps.find(s => s.stepId === id);
      const candidates = newData.semanticMap.steps.filter(s => s.stepId === id || (old?.structuralIdentity && s.structuralIdentity === old.structuralIdentity));
      return candidates.length === 1 ? candidates[0].stepId : null;
    };
    const retainedExpanded = new Set([...this.expanded].map(remapStep).filter((id): id is string => !!id));
    const retainedFilter = this.conditionFilter ? remapStep(this.conditionFilter) : null;
    const retainedConditions = this.conditionSelections.length && remapStep(this.conditionSelections[0].stepId)
      ? retainReachableConditions(newData, this.conditionSelections.flatMap(selection => { const stepId = remapStep(selection.stepId); return stepId ? [{ ...selection, stepId }] : []; })) : [];
    this.abortLabeling();
    this.data = newData;
    this.pending = null;
    this.impactCache = new Map();
    if (force) { this.compare = false; this.baseline = null; }
    this.selectedRelationId = null;
    if (force || !current) {
      this.expanded = new Set();
      this.expandedFrames = new Set();
      this.conditionFilter = null;
      this.conditionSelections = [];
      this.selectedFrameId = newData.flowSequence.frames[0]?.frameID || null;
      this.selectedStepId = newData.flowSequence.frames[0]?.primaryStepRef || null;
      this.savedNavigationState = null;
    } else {
      this.selectedFrameId = matches[0].frameID;
      this.selectedStepId = retainedStep!.stepId;
      this.expanded = retainedExpanded;
      this.expandedFrames = new Set(retainedFrames);
      this.conditionFilter = retainedFilter;
      this.conditionSelections = retainedConditions;
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
      this.notice = newData.sourceNotice || '저장된 분석의 소스 문맥이 없습니다. 필요하면 다시 분석을 눌러 주세요.';
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
