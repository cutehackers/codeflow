import type { FlowTaskViewData, Step, FlowContext } from '../types/flow';
import type { Storyboard, StoryboardFrame } from '../types/storyboard';
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
  candidates = $state<string[]>([]);
  views = $state<Array<{viewId: string; title: string; savedAt?: string}>>([]);
  legacyFlows = $state<Array<{flowId: string; title: string; savedAt?: string}>>([]);
  impactCache = $state<Map<string, ChangeImpactGraph>>(new Map());
  savedNavigationState = $state<SavedNavigationState | null>(null);

  // Derived getters
  get storyboard(): Storyboard | null {
    return this.data?.storyboard || null;
  }

  get frames(): StoryboardFrame[] {
    return this.data?.storyboard?.frames || [];
  }

  get selectedFrame(): StoryboardFrame | null {
    if (!this.frames.length) return null;
    if (this.selectedFrameId) {
      const match = this.frames.find(f => f.frameId === this.selectedFrameId);
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
    const frame = this.frames.find(f => f.frameId === stepOrFrameId || f.primaryStepRef === stepOrFrameId || f.stepRefs.includes(stepOrFrameId));
    if (frame) {
      this.selectedFrameId = frame.frameId;
      this.selectedStepId = stepOrFrameId === frame.frameId ? frame.primaryStepRef : stepOrFrameId;
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

  adopt(newData: FlowTaskViewData, force = false): boolean {
    if (!newData.storyboard) throw new Error('스토리보드가 없습니다. 다시 분석해 주세요.');
    const current = this.selectedFrame;
    const matches = newData.storyboard.frames.filter(f => f.frameMatchKey === current?.frameMatchKey);
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
      this.selectedFrameId = newData.storyboard.frames[0]?.frameId || null;
      this.selectedStepId = newData.storyboard.frames[0]?.primaryStepRef || null;
      this.savedNavigationState = null;
    } else {
      this.selectedFrameId = matches[0].frameId;
      this.selectedStepId = retainedStep!.stepId;
      this.expanded = retainedExpanded;
      this.conditionFilter = retainedFilter;
    }
    this.notice = newData.sourceNotice || '저장된 분석의 스토리보드입니다.';
    return true;
  }

  receive(newData: FlowTaskViewData) { return this.adopt(newData); }
}

export const flowStore = new FlowStore();
