import type { Edge, FlowTaskViewData } from '../types/flow';

export interface ConditionSelection { stepId: string; outcome: 'truthy' | 'falsy' | 'nullish' | 'non_nullish' }

export function conditionChoices(data: FlowTaskViewData | null): Map<string, ConditionSelection['outcome'][]> {
  const choices = new Map<string, ConditionSelection['outcome'][]>();
  for (const edge of data?.semanticMap.edges || []) {
    if (edge.resolutionStatus !== 'resolved') continue;
    for (const condition of edge.conditions || []) {
      const outcomes = choices.get(condition.stepId) || [];
      if (!outcomes.includes(condition.outcome)) outcomes.push(condition.outcome);
      choices.set(condition.stepId, outcomes);
    }
  }
  return choices;
}

// This is reachability in the supplied source graph, not proof that a combination
// of predicates can occur at runtime. Each unselected branch retains alternatives.
export function conditionReachability(data: FlowTaskViewData | null, selections: ConditionSelection[]): { steps: Set<string>; limited: boolean; repeated: boolean } {
  const steps = new Set<string>();
  if (!data || !selections.length) return { steps, limited: false, repeated: false };
  const sourceSteps = new Map(data.semanticMap.steps.map(step => [step.stepId, step]));
  const decisions = new Map(selections.map(selection => [selection.stepId, selection.outcome]));
  const outgoing = new Map<string, Edge[]>();
  for (const edge of data.semanticMap.edges) outgoing.set(edge.fromStepId, [...(outgoing.get(edge.fromStepId) || []), edge]);
  const pending = [selections[0].stepId];
  let limited = false;
  let repeated = false;
  while (pending.length) {
    const id = pending.pop()!;
    if (steps.has(id) || !sourceSteps.has(id)) continue;
    steps.add(id);
    const source = sourceSteps.get(id)!;
    for (const edge of outgoing.get(id) || []) {
      let conditionMismatch = false;
      for (const condition of edge.conditions || []) {
        const outcome = decisions.get(condition.stepId);
        if (outcome && outcome !== condition.outcome) {
          conditionMismatch = true;
          break;
        }
      }
      if (conditionMismatch) continue;
      if (edge.resolutionStatus !== 'resolved' || !sourceSteps.has(edge.toStepId)) { limited = true; continue; }
      if (edge.kind === 'loop_back' || edge.kind === 'await_loop_back' || edge.kind === 'loop_exit_back' || edge.kind === 'loop_reentry') { limited = true; repeated = true; continue; }
      if ((source.kind === 'branch' || source.kind === 'guard') && (!edge.conditions || edge.conditions.length === 0)) { limited = true; continue; }
      pending.push(edge.toStepId);
    }
  }
  return { steps, limited, repeated };
}

export function retainReachableConditions(data: FlowTaskViewData | null, candidates: ConditionSelection[]): ConditionSelection[] {
  const choices = conditionChoices(data);
  const retained: ConditionSelection[] = [];
  for (const candidate of candidates) {
    if (!choices.get(candidate.stepId)?.includes(candidate.outcome)) { if (!retained.length) return []; continue; }
    if (retained.some(selection => selection.stepId === candidate.stepId)) continue;
    if (retained.length && !conditionReachability(data, retained).steps.has(candidate.stepId)) continue;
    retained.push({ ...candidate });
  }
  return retained;
}

export function conditionOutcomeLabel(outcome: ConditionSelection['outcome']): string {
  return {truthy:'조건 참', falsy:'조건 거짓', nullish:'null 또는 undefined', non_nullish:'null·undefined가 아님'}[outcome];
}
