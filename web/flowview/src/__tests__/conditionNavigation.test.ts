import { describe, it, expect } from 'vitest';
import { conditionReachability, retainReachableConditions, conditionOutcomeLabel, conditionChoices } from '../stores/conditionNavigation';
import { conditionalData } from './conditionFixture';

describe('conditional source navigation', () => {
  it('follows nested choices and retains one common successor', () => {
    const result = conditionReachability(conditionalData(), [{stepId:'root',outcome:'truthy'},{stepId:'nested',outcome:'truthy'}]);
    expect([...result.steps].sort()).toEqual(['joined','nested','root','yes']);
    expect(result.limited).toBe(false);
  });
  it('removes an unreachable child after changing the parent outcome', () => {
    expect(retainReachableConditions(conditionalData(),[{stepId:'root',outcome:'falsy'},{stepId:'nested',outcome:'truthy'}])).toEqual([{stepId:'root',outcome:'falsy'}]);
  });
  it('stops at unknown and legacy branch relationships without hanging on a cycle', () => {
    const data = conditionalData();
    data.semanticMap.edges.push({fromStepId:'joined',toStepId:'root',kind:'cycle',resolutionStatus:'resolved'});
    data.semanticMap.edges.push({fromStepId:'yes',toStepId:'missing',kind:'call',resolutionStatus:'unresolved'});
    delete data.semanticMap.edges[2].conditions;
    const result = conditionReachability(data,[{stepId:'root',outcome:'truthy'}]);
    expect(result.steps.has('yes')).toBe(false);
    expect(result.steps.has('missing')).toBe(false);
    expect(result.limited).toBe(true);
  });
});

it('distinguishes nullish fallback from boolean false without changing graph selection', () => {
  const data = conditionalData();
  data.semanticMap.edges[0].conditions![0].outcome = 'nullish';
  data.semanticMap.edges[1].conditions![0].outcome = 'non_nullish';
  const result = conditionReachability(data,[{stepId:'root',outcome:'non_nullish'}]);
  expect([...result.steps].sort()).toEqual(['joined','no','root']);
  expect(conditionOutcomeLabel('nullish')).toBe('null 또는 undefined');
  expect(conditionOutcomeLabel('non_nullish')).toBe('null·undefined가 아님');
});

it.each(['loop_back', 'await_loop_back', 'loop_exit_back', 'loop_reentry'])('stops highlighting at %s without fixing later loop outcomes', (kind) => {
  const data = conditionalData();
  data.semanticMap.edges = [
    {fromStepId:'root',toStepId:'nested',kind:'control_flow',resolutionStatus:'resolved',conditions:[{stepId:'root',outcome:'truthy'}]},
    {fromStepId:'root',toStepId:'joined',kind:'control_flow',resolutionStatus:'resolved',conditions:[{stepId:'root',outcome:'falsy'}]},
    {fromStepId:'nested',toStepId:'root',kind,resolutionStatus:'resolved'}
  ];
  // The body need not be another decision for this execution.
  data.semanticMap.steps.find(step=>step.stepId==='nested')!.kind=kind==='loop_exit_back' ? 'break' : kind==='await_loop_back' ? 'await' : kind==='loop_reentry' ? 'branch' : 'mutation';
  const result=conditionReachability(data,[{stepId:'root',outcome:'truthy'}]);
  expect([...result.steps].sort()).toEqual(['nested','root']);
  expect([...conditionReachability(data,[{stepId:'root',outcome:'falsy'}]).steps].sort()).toEqual(['joined','root']);
  expect(result.repeated).toBe(true);
  expect(result.limited).toBe(true);
  expect(data.semanticMap.edges.some(edge=>edge.toStepId==='joined')).toBe(true);
});

it('evaluates secondary condition in compound multi-condition edges', () => {
  const data = conditionalData();
  data.semanticMap.edges[0].conditions = [
    { stepId: 'root', outcome: 'truthy' },
    { stepId: 'compound', outcome: 'falsy' },
  ];
  const choices = conditionChoices(data);
  expect(choices.get('compound')).toEqual(['falsy']);

  const result = conditionReachability(data, [
    { stepId: 'root', outcome: 'truthy' },
    { stepId: 'compound', outcome: 'truthy' },
  ]);
  expect(result.steps.has('nested')).toBe(false);
});

