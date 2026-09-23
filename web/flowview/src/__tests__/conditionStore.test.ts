import { beforeEach, expect, it } from 'vitest';
import { flowStore } from '../stores/flowStore.svelte';
import { validateFlowReferences } from '../stores/flowNavigation';
import { conditionalData } from './conditionFixture';

beforeEach(() => {
  flowStore.savedNavigationState = null;
  flowStore.adopt(conditionalData(), true);
});

it('keeps code selection while changing parent conditions and reports child removal', () => {
  flowStore.select('yes');
  flowStore.chooseCondition('root', 'truthy');
  flowStore.chooseCondition('nested', 'truthy');
  expect(flowStore.matchingStepIds?.has('yes')).toBe(true);
  flowStore.chooseCondition('root', 'falsy');
  expect(flowStore.conditionSelections).toEqual([{stepId:'root',outcome:'falsy'}]);
  expect(flowStore.selectedStepId).toBe('yes');
  expect(flowStore.matchingStepIds?.has('yes')).toBe(false);
  expect(flowStore.notice).toContain('하위 조건을 해제');
});

it('restores ordered condition choices with the original reading context', () => {
  flowStore.chooseCondition('root','truthy');
  flowStore.chooseCondition('nested','falsy');
  flowStore.select('no');
  flowStore.saveNavigationState();
  flowStore.setConditionFilter(null);
  flowStore.select('yes');
  flowStore.restoreNavigationState();
  expect(flowStore.conditionSelections).toEqual([{stepId:'root',outcome:'truthy'},{stepId:'nested',outcome:'falsy'}]);
  expect(flowStore.selectedStepId).toBe('no');
});

it('removes child restrictions and retains valid choices when adopting an update', () => {
  flowStore.chooseCondition('root','truthy');
  flowStore.chooseCondition('nested','truthy');
  flowStore.removeCondition('nested');
  expect(flowStore.matchingStepIds?.has('no')).toBe(true);
  expect(flowStore.adopt(conditionalData())).toBe(true);
  expect(flowStore.conditionSelections).toEqual([{stepId:'root',outcome:'truthy'}]);
  flowStore.setConditionFilter(null);
  expect(flowStore.matchingStepIds).toBeNull();
});

it('rejects an invalid condition instead of applying it as an unconditional relationship', () => {
  const data = conditionalData();
  data.semanticMap.edges[0].conditions![0].stepId = 'missing';
  expect(validateFlowReferences(data)).toContain('조건');
  expect(flowStore.adopt(data,true)).toBe(false);
});
