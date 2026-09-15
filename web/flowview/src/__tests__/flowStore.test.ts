import { describe, it, expect, beforeEach } from 'vitest';
import { flowStore } from '../stores/flowStore.svelte';
import { samplePayload } from '../stores/sampleData';

describe('FlowStore (Svelte 5 Runes)', () => {
  beforeEach(() => {
    flowStore.data = null;
    flowStore.baseline = null;
    flowStore.pending = null;
    flowStore.paused = false;
    flowStore.compare = false;
    flowStore.selectedStepId = null;
    flowStore.conditionFilter = null;
  });

  it('populates steps and derived properties upon receiving data', () => {
    const data = samplePayload(1);
    flowStore.receive(data);

    expect(flowStore.steps.length).toBe(5);
    expect(flowStore.flowTitle).toBe('고객 결제 요청에서 PG사 승인까지 5개 관문 엔드투엔드 시퀀스');
    expect(flowStore.selectedStep?.stepId).toBe('check_stock');
  });

  it('selects steps by ID', () => {
    flowStore.receive(samplePayload(1));
    flowStore.select('validate_cart');

    expect(flowStore.selectedStepId).toBe('validate_cart');
    expect(flowStore.selectedStep?.technicalName).toBe('ValidateCartUseCase');
  });

  it('gates updates when paused', () => {
    flowStore.receive(samplePayload(1));
    flowStore.togglePause();
    expect(flowStore.paused).toBe(true);

    const v2 = samplePayload(2);
    flowStore.receive(v2);

    // Should not adopt directly while paused
    expect(flowStore.pending).toBe(v2);
    expect(flowStore.data?.semanticMap.generationId).toBe('sample-v1');

    // Toggle pause off should adopt pending
    flowStore.togglePause();
    expect(flowStore.paused).toBe(false);
    expect(flowStore.pending).toBeNull();
    expect(flowStore.data?.semanticMap.generationId).toBe('sample-v2');
  });

  it('correctly detects rule modifications in semanticDelta', () => {
    flowStore.receive(samplePayload(2));
    const deltas = flowStore.activeDeltaChanges;

    expect(deltas.length).toBe(2);
    expect(deltas.some(d => d.kind === 'changed_rule')).toBe(true);
    expect(deltas.some(d => d.kind === 'added_behavior')).toBe(true);
  });

  it('computes matchingStepIds when condition filter is active', () => {
    flowStore.receive(samplePayload(2));
    flowStore.setConditionFilter('validate_cart');

    const matches = flowStore.matchingStepIds;
    expect(matches).not.toBeNull();
    expect(matches?.has('validate_cart')).toBe(true);
    expect(matches?.has('check_stock')).toBe(true); // connected edge
  });

  it('maps Storyboard Gateway roles correctly for each layer', () => {
    const data = samplePayload(1);
    flowStore.receive(data);

    const steps = flowStore.steps;
    expect(steps[0].layer).toBe('ui_event');
    expect(steps[1].layer).toBe('gateway');
    expect(steps[2].layer).toBe('domain_core');
    expect(steps[3].layer).toBe('application');
    expect(steps[4].layer).toBe('external_pg');
  });

  it('toggles view mode between code and process', () => {
    expect(flowStore.viewMode).toBe('code');
    flowStore.setViewMode('process');
    expect(flowStore.viewMode).toBe('process');
    flowStore.setViewMode('code');
    expect(flowStore.viewMode).toBe('code');
  });

  it('toggles compare mode and locks reading baseline', () => {
    flowStore.receive(samplePayload(1));
    expect(flowStore.compare).toBe(false);

    flowStore.toggleCompare();
    expect(flowStore.compare).toBe(true);
    expect(flowStore.paused).toBe(true);
    expect(flowStore.baseline).not.toBeNull();
  });
});
