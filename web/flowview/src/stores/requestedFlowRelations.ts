import type {FlowTaskViewData, RequestedFlowOverview} from '../types/flow';
export type RequestedFlowRelation = RequestedFlowOverview['relations'][number];
export interface RequestedFlowJoin {stepId: string; predecessors: string[]; parallel: boolean}

// Disclosure groups existing graph facts. It does not create execution order.
export function partitionRequestedFlowRelations(data: FlowTaskViewData, relations: RequestedFlowRelation[]): {primary: RequestedFlowRelation[]; routine: RequestedFlowRelation[]} {
  const sources = new Map(data.semanticMap.steps.map(step => [step.stepId, step.anchor?.repoRelativePath]));
  const joins = new Set(requestedFlowJoins(data, data.semanticMap.steps.map(step=>step.stepId)).map(join=>join.stepId));
  const primary: RequestedFlowRelation[] = [];
  const routine: RequestedFlowRelation[] = [];
  for (const relation of relations) {
    const important = !relation.derived || relation.status !== 'verified' || relation.fromSegmentId !== relation.toSegmentId ||
      relation.edges.some(edge => !['control_flow','successor'].includes(edge.kind) || !!edge.conditions?.length ||
        joins.has(edge.toStepId) ||
        (!!sources.get(edge.fromStepId) && !!sources.get(edge.toStepId) && sources.get(edge.fromStepId) !== sources.get(edge.toStepId)));
    (important ? primary : routine).push(relation);
  }
  return {primary,routine};
}

export function requestedFlowJoins(data: FlowTaskViewData, stepIds: string[]): RequestedFlowJoin[] {
  const members = new Set(stepIds);
  const incoming = new Map<string, typeof data.semanticMap.edges>();
  const provenEdges = data.requestedFlowOverview?.relations.filter(relation=>relation.derived && relation.status==='verified').flatMap(relation=>relation.edges) || [];
  for (const edge of provenEdges) {
    if (!members.has(edge.toStepId) || edge.resolutionStatus !== 'resolved' || ['callback_argument','loop_back','loop_reentry','loop_exit_back','await_loop_back'].includes(edge.kind)) continue;
    const edges = incoming.get(edge.toStepId) || [];
    edges.push(edge);
    incoming.set(edge.toStepId, edges);
  }
  return [...incoming].flatMap(([stepId, edges]) => {
    const predecessors = [...new Set(edges.map(edge => edge.fromStepId))];
    return predecessors.length > 1 ? [{stepId, predecessors, parallel:edges.every(edge=>edge.kind==='parallel_wait')}] : [];
  });
}
