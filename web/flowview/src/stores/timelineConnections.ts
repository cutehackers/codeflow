import type { Edge } from '../types/flow';
import { conditionOutcomeLabel } from './conditionNavigation';

// Describe immediate incoming source edges only. A local condition is not proof
// that it dominates every path to this step, or that alternatives run together.
export function timelineConnectionLabels(stepRefs: string[], edges: Edge[]): Map<string, string> {
  const positions = new Map(stepRefs.map((id, index) => [id, index + 1]));
  const incoming = new Map<string, Edge[]>();
  for (const edge of edges) {
    if (!positions.has(edge.toStepId)) continue;
    incoming.set(edge.toStepId, [...(incoming.get(edge.toStepId) || []), edge]);
  }
  const labels = new Map<string, string>();
  for (const id of stepRefs) {
    const connections = incoming.get(id) || [];
    if (connections.some(edge => edge.resolutionStatus !== 'resolved')) {
      labels.set(id, '진입 연결 일부 미확인');
      continue;
    }
    const local = connections.filter(edge => positions.has(edge.fromStepId) && edge.kind === 'control_flow');
    const routes = [...new Set(local.map(edge => {
      const condition = edge.conditions?.[0];
      return condition && condition.stepId === edge.fromStepId
        ? `단계 ${positions.get(edge.fromStepId)} · ${conditionOutcomeLabel(condition.outcome)}`
        : `단계 ${positions.get(edge.fromStepId)}`;
    }))];
    if (routes.length > 1) labels.set(id, '여러 선행 연결 · 실행 방식은 각 관계에서 확인');
    else if (routes.length === 1 && local.some(edge => edge.conditions?.length)) labels.set(id, `${routes[0]}에서 연결`);
  }
  return labels;
}
