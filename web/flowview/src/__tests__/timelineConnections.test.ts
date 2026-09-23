import { describe, expect, it } from 'vitest';
import { timelineConnectionLabels } from '../stores/timelineConnections';
import type { Edge } from '../types/flow';

const edge = (from: string, to: string, outcome: 'truthy' | 'falsy'): Edge => ({fromStepId: from, toStepId: to, kind: 'control_flow', resolutionStatus: 'resolved', conditions: [{stepId: from, outcome}]});
describe('gateway timeline incoming connections', () => {
  it('distinguishes conditional entry from a shared return without duplicating steps', () => {
    const refs = ['first', 'second', 'return'];
    const labels = timelineConnectionLabels(refs, [edge('first','second','truthy'), edge('first','return','falsy'), edge('second','return','truthy'), edge('second','return','falsy')]);
    expect([...labels]).toEqual([['second','단계 1 · 조건 참에서 연결'], ['return','여러 선행 연결 · 실행 방식은 각 관계에서 확인']]);
    expect(refs).toEqual(['first', 'second', 'return']);
  });
  it('does not infer mutually exclusive or parallel execution from multiple predecessors', () => {
    const edges: Edge[] = ['first', 'second'].map(fromStepId => ({fromStepId, toStepId: 'next', kind: 'control_flow', resolutionStatus: 'resolved'}));
    expect(timelineConnectionLabels(['first','second','next'], edges).get('next')).toBe('여러 선행 연결 · 실행 방식은 각 관계에서 확인');
  });
  it('does not describe external decisions as local step numbers', () => {
    expect(timelineConnectionLabels(['return'], [edge('outside','return','truthy')]).size).toBe(0);
  });
  it('retains uncertainty instead of claiming a verified route', () => {
    const unknown = {...edge('first','second','truthy'), resolutionStatus: 'unresolved'};
    expect(timelineConnectionLabels(['first','second'], [unknown]).get('second')).toBe('진입 연결 일부 미확인');
  });
});
