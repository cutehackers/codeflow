import type { FlowTaskViewData, Step, FlowContext } from '../types/flow';
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

class FlowStore {
  data = $state<FlowTaskViewData | null>(null);
  baseline = $state<FlowTaskViewData | null>(null);
  pending = $state<FlowTaskViewData | null>(null);
  selectedStepId = $state<string | null>(null);
  viewMode = $state<'code' | 'process'>('code');
  conditionFilter = $state<string | null>(null);
  paused = $state<boolean>(false);
  compare = $state<boolean>(false);
  expanded = $state<Set<string>>(new Set());
  notice = $state<string>('요청한 흐름을 입력하세요.');
  impactCache = $state<Map<string, ChangeImpactGraph>>(new Map());

  // Derived getters
  get steps(): Step[] {
    return this.data?.semanticMap?.steps || [];
  }

  get selectedStep(): Step | null {
    if (!this.steps.length) return null;
    return this.steps.find(s => s.stepId === this.selectedStepId) || this.steps[0] || null;
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

  select(stepId: string) {
    this.selectedStepId = stepId;
  }

  setViewMode(mode: 'code' | 'process') {
    this.viewMode = mode;
  }

  togglePause() {
    this.paused = !this.paused;
    if (!this.paused && this.pending) {
      this.adopt(this.pending);
    } else {
      this.notice = this.paused ? '읽기 고정 · 새 코드는 도착 알림으로 표시합니다.' : '화면 갱신을 재개했습니다.';
    }
  }

  toggleCompare() {
    this.compare = !this.compare;
    this.paused = true;
    this.notice = '최초 표시 코드와 비교 · 읽기 고정';
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

  adopt(newData: FlowTaskViewData, force = false): boolean {
    const current = this.selectedStep;
    const match = current && current.structuralIdentity
      ? (newData.semanticMap?.steps || []).find(s => s.structuralIdentity === current.structuralIdentity)
      : null;

    if (current && !match && !force) {
      this.pending = newData;
      this.notice = '선택한 단계가 새 흐름에서 제거되었습니다. 이전 흐름을 유지합니다.';
      return false;
    }

    if (!this.baseline) {
      this.baseline = newData;
    }
    this.data = newData;
    this.pending = null;
    this.expanded = new Set();
    const surgeryTargetId = (newData.semanticDelta?.changes || []).find(c => c.kind === 'added_behavior')?.targetStepId
      || (newData.semanticDelta?.changes || []).find(c => c.kind === 'changed_rule')?.targetStepId;
    const defaultStepId = surgeryTargetId || newData.semanticMap?.steps?.[0]?.stepId || null;
    this.selectedStepId = current ? (match?.stepId || null) : defaultStepId;

    const changes = newData.semanticDelta?.changes || [];
    this.notice = changes.length
      ? `검증된 의미 변경 ${changes.length}건을 반영했습니다.`
      : this.paused
        ? '읽기 고정 · 도착한 변경을 적용했습니다.'
        : '검증된 흐름을 표시합니다.';
    return true;
  }

  receive(newData: FlowTaskViewData) {
    if (!newData?.semanticMap?.steps) {
      throw new Error('요청한 흐름의 분석 결과가 없습니다.');
    }
    if (this.paused && this.data) {
      this.pending = newData;
      this.notice = '검증된 변경이 도착했습니다. 읽기 고정 중이라 이전 흐름을 유지합니다.';
    } else {
      this.adopt(newData);
    }
  }
}

export const flowStore = new FlowStore();
