import type { CodeReadingPath, Edge, FlowTaskViewData, Step } from '../types/flow';
import type { FlowSequence, FlowSequenceFrame } from '../types/flow_sequence';

// The curator identifies source-backed steps whose invocation is not proven.
// Keep those frames accessible without presenting them as the requested
// execution path. This changes navigation only, never frame membership.
export function partitionExecutionNavigation(sequence: FlowSequence | null | undefined): {
  execution: FlowSequenceFrame[];
  unproven: FlowSequenceFrame[];
} {
  if (!sequence) return {execution: [], unproven: []};
  const unprovenIDs = new Set(sequence.summaryLimitations
    ?.filter(limitation => limitation.code === 'invocation_unproven')
    .flatMap(limitation => limitation.frameRefs) || []);
  return {
    execution: sequence.frames.filter(frame => !unprovenIDs.has(frame.frameID)),
    unproven: sequence.frames.filter(frame => unprovenIDs.has(frame.frameID)),
  };
}

export function partitionCodeReadingNavigation(sequence: FlowSequence | null | undefined, path: CodeReadingPath | null | undefined): {
  reading: FlowSequenceFrame[];
  other: FlowSequenceFrame[];
  unproven: FlowSequenceFrame[];
} {
  const execution = partitionExecutionNavigation(sequence);
  if (!path?.nodes.length) return {reading: execution.execution, other: [], unproven: execution.unproven};
  const frames = new Map(execution.execution.map(frame => [frame.frameID, frame]));
  const selected = new Set<string>();
  const reading = path.nodes.flatMap(node => {
    const frame = frames.get(node.frameId);
    if (!frame || selected.has(node.frameId)) return [];
    selected.add(node.frameId);
    return [frame];
  });
  return {reading, other: execution.execution.filter(frame => !selected.has(frame.frameID)), unproven: execution.unproven};
}

// A short reading relation retains its confirmed intermediate source steps.
// Keep loop revisits and other already selected points in the main path.
export function codeReadingRelationDetailSteps(relation: CodeReadingPath['relations'][number], path: CodeReadingPath, edges: Edge[], steps: Step[]): Step[] {
  const selected = new Set(path.nodes.map(node => node.stepId));
  const byID = new Map(steps.map(step => [step.stepId, step]));
  const seen = new Set<string>();
  return relation.edgeIndexes.slice(0, -1).flatMap(index => {
    const stepID = edges[index]?.toStepId;
    if (!stepID || selected.has(stepID) || seen.has(stepID)) return [];
    seen.add(stepID);
    const step = byID.get(stepID);
    return step ? [step] : [];
  });
}

export function codeReadingRelationDetailOwners(path: CodeReadingPath, edges: Edge[], steps: Step[]): Map<string, string> {
  const owners = new Map<string, string>();
  for (const relation of path.relations) {
    const key = `${relation.fromNodeId}\u0000${relation.toNodeId}`;
    for (const step of codeReadingRelationDetailSteps(relation, path, edges, steps)) {
      if (!owners.has(step.stepId)) owners.set(step.stepId, key);
    }
  }
  return owners;
}

export interface OmittedCodeReadingBranch {
  edgeIndex: number;
  outcome: NonNullable<Edge['conditions']>[number]['outcome'];
  toStepId: string;
}

export function omittedCodeReadingBranches(path: CodeReadingPath | null | undefined, edges: Edge[]): Map<string, OmittedCodeReadingBranch[]> {
  const omitted = new Map<string, OmittedCodeReadingBranch[]>();
  if (!path) return omitted;
  const represented = new Set(path.nodes.map(node => node.stepId));
  const used = new Set<number>();
  for (const relation of path.relations) {
    for (const index of relation.edgeIndexes) {
      const edge = edges[index];
      if (!edge) continue;
      used.add(index);
      represented.add(edge.fromStepId);
      represented.add(edge.toStepId);
    }
  }
  for (const [edgeIndex, edge] of edges.entries()) {
    if (used.has(edgeIndex) || !represented.has(edge.fromStepId) || edge.resolutionStatus !== 'resolved') continue;
    for (const condition of edge.conditions || []) {
      if (condition.stepId !== edge.fromStepId) continue;
      const branches = omitted.get(edge.fromStepId) || [];
      branches.push({edgeIndex, outcome: condition.outcome, toStepId: edge.toStepId});
      omitted.set(edge.fromStepId, branches);
    }
  }
  return omitted;
}

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

function verifiedReturnBypassPairs(steps: Step[], edges: Edge[]): Set<string> {
  const byID = new Map(steps.map(step => [step.stepId, step]));
  const byOrdinal = new Map(steps.map(step => [step.ordinal, step]));
  const pairs = new Set<string>();
  for (const edge of edges) {
    if (edge.kind !== 'return' || edge.resolutionStatus !== 'resolved') continue;
    const returned = byID.get(edge.fromStepId);
    const caller = byOrdinal.get(returned?.callerStepOrdinal);
    if (returned?.kind === 'return' && caller?.kind === 'call' && returned.invocationId &&
      caller.invocationId && returned.invocationId !== caller.invocationId)
      pairs.add(`${caller.stepId}\u0000${edge.toStepId}`);
  }
  return pairs;
}

export function buildFlowRelations(data: FlowTaskViewData | null): FlowRelation[] {
  if (!data) return [];
  const steps = new Map(data.semanticMap.steps.map(s => [s.stepId, s]));
  const parents = new Map<string, FlowSequenceFrame>();
  for (const frame of data.flowSequence?.frames || []) {
    for (const ref of frame.stepRefs) parents.set(ref, frame);
  }
  const bypassPairs = verifiedReturnBypassPairs(data.semanticMap.steps, data.semanticMap.edges);
  return data.semanticMap.edges.map((edge, index) => ({
    id: `${index}:${edge.fromStepId}:${edge.toStepId}:${edge.kind}`,
    edge,
    fromStep: steps.get(edge.fromStepId) || null,
    toStep: steps.get(edge.toStepId) || null,
    fromFrame: parents.get(edge.fromStepId) || null,
    toFrame: parents.get(edge.toStepId) || null,
    resolved: edge.resolutionStatus === 'resolved' && steps.has(edge.fromStepId) && steps.has(edge.toStepId),
  })).filter(relation => relation.edge.kind !== 'control_flow' ||
    !bypassPairs.has(`${relation.edge.fromStepId}\u0000${relation.edge.toStepId}`));
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
  const focus = data.requestedFocus;
  if (focus && (!focus.symbolPath || !focus.reason || !focus.evidenceRefs?.length ||
    !steps.has(focus.callStepId) || !steps.has(focus.entryStepId) ||
    focus.snapshotId !== data.flowSequence.snapshotID ||
    !parents.has(focus.callStepId) || !parents.has(focus.entryStepId))) return '요청한 구현의 코드 근거를 확인할 수 없습니다.';
  for (const limitation of data.flowSequence.summaryLimitations || []) {
    if (!limitation.message || !Array.isArray(limitation.frameRefs) || !limitation.frameRefs.length || limitation.frameRefs.some(ref => !frames.has(ref))) return '요약 한계의 대상 관문을 찾을 수 없습니다.';
  }
  const overview = data.requestedFlowOverview;
  if (overview) {
    if (overview.schemaId !== 'https://codeflow.local/schemas/requested-flow-overview.schema.json' || ![1,2].includes(overview.schemaVersion) ||
      overview.snapshotId !== data.flowSequence.snapshotID || overview.computedBasisId !== data.flowSequence.computedBasisId ||
      overview.request !== data.flowResolution?.rawRequest || !Array.isArray(overview.segments) || !overview.segments.length ||
      !Array.isArray(overview.relations) || !Array.isArray(overview.boundaries)) return '처리 구간 개요의 분석 기준이 맞지 않습니다.';
    const segments = new Set<string>();
    const blocks = new Set<string>();
    for (const segment of overview.segments) {
      if (!segment.segmentId || segments.has(segment.segmentId) || !segment.purpose || !segment.inclusionReason ||
        !Array.isArray(segment.stepIds) || !segment.stepIds.length || segment.stepIds.some(id => !steps.has(id)) || !segment.stepIds.includes(segment.entryStepId) ||
        !Array.isArray(segment.blocks) || !segment.blocks.length) return '처리 구간의 코드 근거를 확인할 수 없습니다.';
      segments.add(segment.segmentId);
      const represented = new Set<string>();
      for (const block of segment.blocks) {
        const lines = data.sourceFiles?.[block.repoRelativePath];
        if (!block.blockId || blocks.has(block.blockId) || !Array.isArray(block.stepIds) || !block.stepIds.length || !Array.isArray(block.highlights) || !block.highlights.length ||
          !Array.isArray(block.lineRange) || block.lineRange.length !== 2 || !block.lineRange.every(Number.isInteger) || block.lineRange[0] < 1 || block.lineRange[0] > block.lineRange[1] || !lines || block.lineRange[1] - block.lineRange[0] + 1 > lines.length ||
          block.stepIds.some(id => !segment.stepIds.includes(id) || !data.semanticMap.steps.some(step => step.stepId === id && step.anchor?.repoRelativePath === block.repoRelativePath))) return '코드 블록의 소스 근거를 확인할 수 없습니다.';
        blocks.add(block.blockId);
        for (let line = block.lineRange[0]; line <= block.lineRange[1]; line++) if (!lines.some(item => item.lineNumber === line)) return '이 분석 시점의 코드 블록 소스가 없습니다.';
        for (const highlight of block.highlights) {
          if (!block.stepIds.includes(highlight.stepId) || highlight.lineRange[0] < block.lineRange[0] || highlight.lineRange[1] > block.lineRange[1] ||
            !Array.isArray(highlight.lineSelections) || !highlight.lineSelections.length || highlight.lineSelections.some(selection => {
              const line = lines.find(item => item.lineNumber === selection.lineNumber);
              return !line || ![selection.lineNumber, selection.startColumn, selection.endColumn].every(Number.isInteger) || selection.lineNumber < highlight.lineRange[0] || selection.lineNumber > highlight.lineRange[1] || selection.startColumn < 0 || selection.endColumn < selection.startColumn || (selection.endColumn === selection.startColumn && line.text.length !== 0) || selection.endColumn > line.text.length;
            })) return '코드 블록의 문장 강조 근거를 확인할 수 없습니다.';
          represented.add(highlight.stepId);
        }
        if (block.stepIds.some(id => !represented.has(id))) return '코드 블록에 포함된 문장의 강조 근거가 없습니다.';
      }
      if (segment.stepIds.some(id => !represented.has(id))) return '처리 구간에 포함된 문장의 코드 블록이 없습니다.';
    }
    if (overview.schemaVersion === 2) {
      if (!Array.isArray(overview.categories) || !overview.categories.length) return '상위 범주의 처리 구간 소속을 확인할 수 없습니다.';
      const categoryIds = new Set<string>();
      const categoryTitles = new Set<string>();
      const membership = new Set<string>();
      for (const category of overview.categories) {
        if (!category.categoryId?.trim() || categoryIds.has(category.categoryId.trim()) || !category.title?.trim() || categoryTitles.has(category.title.trim()) || !Array.isArray(category.segmentIds) || !category.segmentIds.length) return '상위 범주의 처리 구간 소속을 확인할 수 없습니다.';
        categoryIds.add(category.categoryId.trim());
        categoryTitles.add(category.title.trim());
        for (const id of category.segmentIds) {
          if (!segments.has(id) || membership.has(id)) return '처리 구간이 없거나 여러 상위 범주에 중복되어 있습니다.';
          membership.add(id);
        }
      }
      if (membership.size !== segments.size) return '상위 범주에 포함되지 않은 처리 구간이 있습니다.';
    } else if (overview.categories !== undefined) return '처리 구간 개요 버전과 상위 범주가 맞지 않습니다.';
    for (const relation of overview.relations) {
      if ((relation.fromSegmentId && !segments.has(relation.fromSegmentId)) || (relation.toSegmentId && !segments.has(relation.toSegmentId)) ||
        !steps.has(relation.fromStepId) || (relation.status === 'verified' && !steps.has(relation.toStepId)) || !relation.edgeIndexes?.length || relation.edgeIndexes.length !== relation.edges?.length) return '처리 구간의 연결 근거를 확인할 수 없습니다.';
      let cursor = relation.fromStepId;
      for (const [position, index] of relation.edgeIndexes.entries()) {
        const edge = data.semanticMap.edges[index];
        if (!edge || edge.fromStepId !== cursor || edge.toStepId !== relation.edges[position]?.toStepId || edge.kind !== relation.edges[position]?.kind || edge.resolutionStatus !== relation.edges[position]?.resolutionStatus || JSON.stringify(edge.conditions || []) !== JSON.stringify(relation.edges[position]?.conditions || [])) return '처리 구간의 연결 근거를 확인할 수 없습니다.';
        cursor = edge.toStepId;
      }
      if (cursor !== relation.toStepId) return '처리 구간의 연결 근거를 확인할 수 없습니다.';
    }
    if (overview.boundaries.some(boundary => !steps.has(boundary.fromStepId) || !boundary.reason)) return '처리 구간의 분석 경계를 확인할 수 없습니다.';
  }
  const path = data.codeReadingPath;
  if (path) {
    if (path.schemaId !== 'https://codeflow.local/schemas/code-reading-path.schema.json' || path.schemaVersion !== 2 ||
      path.snapshotId !== data.flowSequence.snapshotID || path.computedBasisId !== data.flowSequence.computedBasisId ||
      path.request !== data.flowResolution?.rawRequest || !path.nodes.length || !Array.isArray(path.relations) ||
      path.nodes[0].stepId !== data.semanticMap.steps[0]?.stepId) return '코드 읽기 경로의 분석 기준이 맞지 않습니다.';
    const unproven = new Set(data.flowSequence.summaryLimitations?.filter(item => item.code === 'invocation_unproven').flatMap(item => item.frameRefs) || []);
    const selected = new Set<string>();
    const nodeIndex = new Map(path.nodes.map((node, index) => [node.stepId, index]));
    const bypassPairs = verifiedReturnBypassPairs(data.semanticMap.steps, data.semanticMap.edges);
    for (const [index, node] of path.nodes.entries()) {
      const step = data.semanticMap.steps.find(item => item.stepId === node.stepId);
      const context = data.flowContexts?.[node.stepId];
      if (!step || selected.has(node.stepId) || unproven.has(node.frameId) ||
        !data.flowSequence.frames.some(frame => frame.frameID === node.frameId && frame.stepRefs.includes(node.stepId)) ||
        !node.codeViewAvailable || !node.inclusionReason || !node.requestPart || !path.request.includes(node.requestPart) || !node.evidenceRefs?.length ||
        node.symbol !== `${node.sourceAnchor.repoRelativePath}#${node.sourceAnchor.enclosingSymbolPath}` ||
        node.sourceAnchor.repoRelativePath !== step.anchor?.repoRelativePath ||
        node.sourceAnchor.enclosingSymbolPath !== step.anchor?.enclosingSymbolPath ||
        context?.snapshotId !== path.snapshotId || context?.precision !== 'exact' || context?.sourceValidationStatus !== 'verified' ||
        context?.statement?.byteRange[0] !== node.sourceAnchor.byteRange[0] || context.statement.byteRange[1] !== node.sourceAnchor.byteRange[1] ||
        !Array.isArray(node.previousNodeIds) || !Array.isArray(node.nextNodeIds) ||
        (index > 0 && !node.previousNodeIds.length) ||
        (index === 0 && node.previousNodeIds.length > 0) ||
        (!node.nextNodeIds.length && node.role !== 'outcome' && node.role !== 'boundary'))
        return '코드 읽기 경로의 소스 또는 연결 근거를 확인할 수 없습니다.';
      selected.add(node.stepId);
    }
    const previous = new Map(path.nodes.map(node => [node.stepId, [] as string[]]));
    const next = new Map(path.nodes.map(node => [node.stepId, [] as string[]]));
    const pairs = new Set<string>();
    for (const relation of path.relations) {
      const fromIndex = nodeIndex.get(relation.fromNodeId);
      const toIndex = nodeIndex.get(relation.toNodeId);
      const pair = `${relation.fromNodeId}\u0000${relation.toNodeId}`;
      if (fromIndex === undefined || toIndex === undefined || fromIndex >= toIndex || pairs.has(pair) ||
        relation.status !== 'verified' || !relation.edgeIndexes?.length)
        return '코드 읽기 경로의 연결 근거를 확인할 수 없습니다.';
      pairs.add(pair);
      let cursor = relation.fromNodeId;
      for (const [index, edgeIndex] of relation.edgeIndexes.entries()) {
        const edge = data.semanticMap.edges[edgeIndex];
        const selectedPosition = edge ? nodeIndex.get(edge.toStepId) : undefined;
        const loopRevisit = edge && ['loop_back', 'loop_reentry', 'loop_exit_back', 'await_loop_back'].includes(edge.kind) &&
          selectedPosition !== undefined && selectedPosition < fromIndex;
        if (!edge || edge.fromStepId !== cursor || edge.resolutionStatus !== 'resolved' || edge.kind === 'callback_argument' ||
          (edge.kind === 'control_flow' && bypassPairs.has(`${edge.fromStepId}\u0000${edge.toStepId}`)) ||
          (index < relation.edgeIndexes.length - 1 && selectedPosition !== undefined && !loopRevisit))
          return '코드 읽기 경로의 연결 근거를 확인할 수 없습니다.';
        cursor = edge.toStepId;
      }
      if (cursor !== relation.toNodeId) return '코드 읽기 경로의 연결 근거를 확인할 수 없습니다.';
      next.get(relation.fromNodeId)!.push(relation.toNodeId);
      previous.get(relation.toNodeId)!.push(relation.fromNodeId);
    }
    for (const node of path.nodes) {
      if (JSON.stringify(node.previousNodeIds) !== JSON.stringify(previous.get(node.stepId)) ||
        JSON.stringify(node.nextNodeIds) !== JSON.stringify(next.get(node.stepId)))
        return '코드 읽기 경로의 연결 근거를 확인할 수 없습니다.';
    }
    if (focus && !pairs.has(`${focus.callStepId}\u0000${focus.entryStepId}`))
      return '요청한 구현의 코드 근거를 확인할 수 없습니다.';
  }
  return null;
}

export function requestedFocusFrame(sequence: FlowSequence, entryStepId: string | undefined): FlowSequenceFrame | undefined {
  return entryStepId ? sequence.frames.find(frame => frame.stepRefs.includes(entryStepId)) : undefined;
}

// Source bundles may contain ranges rather than a complete file starting at line 1.
export function sourceWindow<T extends {lineNumber: number; isHit?: boolean}>(lines: T[], focusLine: number): T[] {
  const focusIndex = lines.findIndex(line => line.lineNumber >= focusLine);
  const firstHit = lines.findIndex(line => line.isHit);
  let lastHit = firstHit;
  for (let index = firstHit + 1; index < lines.length; index++) {
    if (lines[index].isHit) lastHit = index;
  }
  const start = Math.max(0, (firstHit < 0 ? (focusIndex < 0 ? lines.length - 1 : focusIndex) : firstHit) - 4);
  return lines.slice(start, Math.max(start + 16, lastHit + 5));
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

export function summarizeUnknownBoundaries(
  unknowns: Array<{reason?: string; subject?: string} | string>,
): Array<{description: string; count: number}> {
  const counts = new Map<string, number>();
  for (const unknown of unknowns) {
    const reason = typeof unknown === 'string' ? unknown : unknown.reason || '';
    const subject = typeof unknown === 'string' ? '' : unknown.subject || '';
    const separator = subject.lastIndexOf('#');
    const path = separator >= 0 ? subject.slice(0, separator) : '';
    const symbol = separator >= 0 ? subject.slice(separator + 1) : subject;
    const target = /^unresolved_dynamic\.callback_\d+$/.test(symbol)
      ? '전달된 콜백'
      : symbol.startsWith('unresolved_dynamic.')
        ? `${symbol.slice('unresolved_dynamic.'.length)} 호출`
        : symbol === 'causalObservationClosure' ? '' : symbol;
    let explanation = '코드에서 다음 처리와의 연결을 확인하지 못했습니다.';
    if (reason.includes('콜백을 전달한 코드는 확인했지만')) {
      explanation = '실제 호출 시점과 실행 여부를 확인하지 못했습니다.';
    } else if (reason.includes('재귀 호출이')) {
      explanation = '재귀 호출의 다음 실행 문장은 분석 범위 때문에 펼치지 못했습니다.';
    } else if (reason.includes('outside the selected')) {
      explanation = '선택한 코드 범위 밖의 호출입니다.';
    } else if (reason.includes('multiple canonical')) {
      explanation = '호출 대상을 하나로 확인하지 못했습니다.';
    } else if (reason.includes('closure')) {
      explanation = '요청한 흐름의 끝까지 연결을 확인하지 못했습니다.';
    }
    const description = [path, target, explanation].filter(Boolean).join(' · ');
    counts.set(description, (counts.get(description) || 0) + 1);
  }
  return [...counts].map(([description, count]) => ({description, count}));
}

// Preserve curated points, including multiple statements in one frame. Relations
// and their surrounding boundaries come only from the stored source graph.
export function requestedFlowOverview(data: FlowTaskViewData | null) {
  const path = data?.codeReadingPath;
  if (!data || !path) return [];
  const relations = buildFlowRelations(data);
  return path.nodes.map(node => {
    const outgoing = path.relations.filter(relation => relation.fromNodeId === node.nodeId);
    const stepIds = new Set([node.stepId]);
    const edgeIndexes = new Set<number>();
    for (const relation of outgoing) {
      for (const index of relation.edgeIndexes) {
        edgeIndexes.add(index);
        const edge = data.semanticMap.edges[index];
        if (edge) stepIds.add(edge.fromStepId);
      }
    }
    return {
      node,
      outgoing: outgoing.map(relation => ({
        relation,
        target: path.nodes.find(item => item.nodeId === relation.toNodeId),
        details: codeReadingRelationDetailSteps(relation, path, data.semanticMap.edges, data.semanticMap.steps),
        edges: relation.edgeIndexes.map(index => data.semanticMap.edges[index]),
        contextEdges: relation.edgeIndexes.map(index => data.semanticMap.edges[index]).filter(edge =>
          edge.conditions?.length || !['control_flow', 'successor', 'call', 'calls', 'resolved_cross_file', 'return'].includes(edge.kind)),
      })),
      joins: [...stepIds].flatMap(stepId => {
        const incoming = relations.filter(relation => relation.resolved && relation.edge.toStepId === stepId);
        const step = data.semanticMap.steps.find(item => item.stepId === stepId);
        return step && incoming.length > 1 ? [{step, incoming}] : [];
      }),
      boundaries: relations.filter(relation => stepIds.has(relation.edge.fromStepId) &&
        !edgeIndexes.has(Number(relation.id.split(':')[0]))),
      limitations: [...new Set([...stepIds].flatMap(id => selectedCodeLimitations(data, data.semanticMap.steps.find(step => step.stepId === id) || null)))],
    };
  });
}
