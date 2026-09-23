import type { Edge, FlowTaskViewData, Step } from '../types/flow';
import type { FlowSequenceFrame } from '../types/flow_sequence';

export interface FlowRelation {
  id: string;
  edge: Edge;
  fromStep: Step | null;
  toStep: Step | null;
  fromFrame: FlowSequenceFrame | null;
  toFrame: FlowSequenceFrame | null;
  resolved: boolean;
}

export function incomingRelationSummary(relations: FlowRelation[]): string | null {
  if (relations.length < 2) return null;
  if (relations.every(relation => relation.edge.kind === 'parallel_wait')) return `${relations.length}개 병렬 작업 완료 대기`;
  return `${relations.length}개 선행 연결 · 동시 완료를 뜻하지 않음`;
}

export function buildFlowRelations(data: FlowTaskViewData | null): FlowRelation[] {
  if (!data) return [];
  const steps = new Map(data.semanticMap.steps.map(s => [s.stepId, s]));
  const parents = new Map<string, FlowSequenceFrame>();
  for (const frame of data.flowSequence?.frames || []) {
    for (const ref of frame.stepRefs) parents.set(ref, frame);
  }
  return data.semanticMap.edges.map((edge, index) => ({
    id: `${index}:${edge.fromStepId}:${edge.toStepId}:${edge.kind}`,
    edge,
    fromStep: steps.get(edge.fromStepId) || null,
    toStep: steps.get(edge.toStepId) || null,
    fromFrame: parents.get(edge.fromStepId) || null,
    toFrame: parents.get(edge.toStepId) || null,
    resolved: edge.resolutionStatus === 'resolved' && steps.has(edge.fromStepId) && steps.has(edge.toStepId),
  }));
}

export function validateFlowReferences(data: FlowTaskViewData): string | null {
  if (!data.flowSequence) return 'FlowSequence가 없습니다. 다시 분석해 주세요.';
  const steps = new Set(data.semanticMap.steps.map(s => s.stepId));
  if (steps.size !== data.semanticMap.steps.length) return '중복된 실행 단계가 있습니다.';
  for (const edge of data.semanticMap.edges) {
    if (!edge.conditions?.length) continue;
    const condition = edge.conditions[0];
    const source = data.semanticMap.steps.find(step => step.stepId === edge.fromStepId);
    const target = data.semanticMap.steps.find(step => step.stepId === edge.toStepId);
    if (edge.conditions.length !== 1 || !['control_flow', 'loop_back', 'loop_reentry'].includes(edge.kind) || edge.resolutionStatus !== 'resolved' || !source || !target || condition.stepId !== source.stepId || !['truthy', 'falsy', 'nullish', 'non_nullish'].includes(condition.outcome) || !['branch', 'guard'].includes(source.kind || '') || !source.invocationId || source.invocationId !== target.invocationId || source.anchor?.repoRelativePath !== target.anchor?.repoRelativePath || source.anchor?.enclosingSymbolPath !== target.anchor?.enclosingSymbolPath) return '조건 연결의 근거를 확인할 수 없습니다.';
  }
  const frames = new Set<string>();
  const parents = new Set<string>();
  for (const frame of data.flowSequence.frames) {
    if (frames.has(frame.frameID)) return '중복된 관문이 있습니다.';
    frames.add(frame.frameID);
    if (!frame.stepRefs.includes(frame.primaryStepRef)) return '대표 코드가 관문에 포함되지 않았습니다.';
    for (const ref of frame.stepRefs) {
      if (!steps.has(ref)) return '관문에 연결된 실행 단계를 찾을 수 없습니다.';
      if (parents.has(ref)) return '한 실행 단계가 여러 관문에 중복되어 있습니다.';
      parents.add(ref);
    }
  }
  if (parents.size !== steps.size) return '관문에 포함되지 않은 실행 단계가 있습니다.';
  for (const limitation of data.flowSequence.summaryLimitations || []) {
    if (!limitation.message || !Array.isArray(limitation.frameRefs) || !limitation.frameRefs.length || limitation.frameRefs.some(ref => !frames.has(ref))) return '요약 한계의 대상 관문을 찾을 수 없습니다.';
  }
  return null;
}

// Source bundles may contain ranges rather than a complete file starting at line 1.
export function sourceWindow<T extends {lineNumber: number}>(lines: T[], focusLine: number): T[] {
  const focusIndex = lines.findIndex(line => line.lineNumber >= focusLine);
  const start = Math.max(0, (focusIndex < 0 ? lines.length - 1 : focusIndex) - 4);
  return lines.slice(start, start + 16);
}

export function implementationSymbol(step: Step | undefined): string {
  return step?.anchor?.enclosingSymbolPath || step?.technicalName || '';
}

export function navigationTitle(title: string, symbol: string): string {
  // When analysis only supplied a source statement, use its known owner rather than
  // inventing a purpose or repeating source in the navigation rail.
  const sourceStatement = /^(?:const|let|var|return|throw|await|break|continue|if|else|for|while|switch|function|delete|new)\b/.test(title.trim())
    || /^(?:\+\+|--)?[\w$.[\]\'"]+(?:\+\+|--)$|^(?:\+\+|--)[\w$.]+$|^[\w$.]+\s*(?:[+*\/%&|^?-]|<<|>>|>>>|&&|\|\||\?\?)?=(?!=|>)|^\[[^\]]+\]\s*=/.test(title.trim())
    || /[{};]|(?:[\w.$]+)\s*\([^)]*\)/.test(title);
  return sourceStatement ? symbol || '처리 이름 미확인' : title;
}

export function navigationRole(step: Step, edges: Edge[]): string {
  if (step.kind === 'break') {
    return edges.some(edge => edge.fromStepId === step.stepId && edge.kind === 'switch_exit' && edge.resolutionStatus === 'resolved') ? '분기 종료' : '반복 종료';
  }
  return {continue: '다음 반복', await: '완료 대기'}[step.kind || ''] || '처리';
}

// Titles describe existing graph facts only. They never change membership,
// infer a callee from source text, or promote an unresolved relation.
export function buildNavigationTitles(steps: Step[], edges: Edge[]): Map<string, string> {
  const byId = new Map(steps.map(step => [step.stepId, step]));
  const byOrdinal = new Map(steps.filter(step => step.ordinal !== undefined).map(step => [step.ordinal!, step]));
  const callees = new Map<string, Set<string>>();
  for (const edge of edges) {
    if (edge.resolutionStatus !== 'resolved' || !['call', 'calls', 'resolved_cross_file'].includes(edge.kind)) continue;
    if (byId.get(edge.fromStepId)?.kind !== 'call') continue;
    const symbol = implementationSymbol(byId.get(edge.toStepId));
    if (!symbol) continue;
    const targets = callees.get(edge.fromStepId) || new Set<string>();
    targets.add(symbol);
    callees.set(edge.fromStepId, targets);
  }
  const titles = new Map<string, string>();
  for (const step of steps) {
    const owner = implementationSymbol(step);
    let title = navigationTitle(step.name, owner);
    if (navigationTitle(step.name, '') === '처리 이름 미확인') {
      const targets = callees.get(step.stepId);
      if (targets?.size) title = `${[...targets].sort().join(', ')} 호출`;
      else if (['break', 'continue', 'await'].includes(step.kind || '')) title = navigationRole(step, edges);
      else if (['guard', 'branch', 'decision'].includes(step.kind || '') && step.branch) title = `조건: ${step.branch}`;
      else if (step.kind === 'mutation') {
        title = '상태 변경';
        const source = step.assignmentSourceOrdinal === undefined ? undefined : byOrdinal.get(step.assignmentSourceOrdinal);
        const sourceTargets = source && callees.get(source.stepId);
        if (sourceTargets?.size) title = `${[...sourceTargets].sort().join(', ')} 결과 반영`;
      }
    }
    titles.set(step.stepId, title);
  }
  return titles;
}

// The execution rail is an index into source evidence, not a second code view.
// For a generic mutation label, the known enclosing implementation is more useful
// than repeating "상태 변경" for every item. The role remains visible separately.
export function executionNavigationLabel(step: Step, title: string): { title: string; detail: string } {
  const owner = implementationSymbol(step);
  if (title === '상태 변경' && owner) return { title: owner, detail: title };
  return { title, detail: owner && owner !== title ? owner : '' };
}

// Only follow explicit step/boundary references. Global analysis bookkeeping
// and another step's unknowns do not explain the selected source.
export function selectedCodeLimitations(data: FlowTaskViewData | null, step: Step | null): string[] {
  if (!data || !step) return [];
  const subjects = new Set([step.stepId]);
  for (const rule of step.rules || []) {
    if (rule.startsWith('boundary:')) subjects.add(rule.slice('boundary:'.length));
  }
  for (const edge of data.semanticMap.edges) {
    if (edge.fromStepId === step.stepId && edge.resolutionStatus !== 'resolved' && edge.toSymbolPath) subjects.add(edge.toSymbolPath);
  }
  const labels: Record<string, string> = {
    unresolved_dynamic_call: '실행 시 결정되는 호출 대상을 확인하지 못했습니다.',
    unresolved_type: '호출 대상이나 이후 처리의 연결을 확인하지 못했습니다.',
    truncated_traversal: '분석 범위 밖의 후속 처리는 확인하지 못했습니다.',
    no_evidence: '이 연결을 뒷받침할 코드 근거가 없습니다.',
    stale_anchor: '분석 당시의 코드 위치가 현재 소스와 일치하지 않습니다.',
    adapter_error: '코드 분석 중 오류가 발생해 연결을 확인하지 못했습니다.'
  };
  return [...new Set((data.semanticMap.unknowns || []).flatMap(item => {
    if (typeof item === 'string' || !item.subject || !subjects.has(item.subject) || !item.reason?.trim()) return [];
    return [labels[item.reason] || item.reason];
  }))];
}
