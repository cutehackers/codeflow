import {afterAll, beforeAll, beforeEach, describe, expect, it, vi} from 'vitest';
import {flowStore} from '../stores/flowStore.svelte';
import {samplePayload} from '../stores/sampleData';
import type {FlowTaskViewData} from '../types/flow';

vi.mock('../App.svelte', () => ({default: {}}));
vi.mock('svelte', async original => ({...await original<typeof import('svelte')>(), mount: vi.fn()}));
const response = (data: unknown, ok = true) => ({ok, status:ok?200:500, json:async()=>data});
let incoming: FlowTaskViewData;
let compareResponse: () => Promise<ReturnType<typeof response>>;
const fetchMock = vi.fn(async (input: URL) => {
  if (input.pathname === '/api/task/view') return response(incoming);
  if (input.pathname === '/api/view') return response({...incoming,viewId:input.searchParams.get('viewId') || incoming.viewId});
  if (input.pathname === '/api/view/compare') return compareResponse();
  return response({views:[]});
});

beforeAll(async () => {
  const location = {href:'http://localhost/?token=test',origin:'http://localhost',search:'?token=test'};
  vi.stubGlobal('location',location);
  vi.stubGlobal('history',{pushState:(_state: unknown,_title: string,url: string)=>{location.href=url;location.search=new URL(url).search;}});
  vi.stubGlobal('document',{getElementById:()=>({}),querySelectorAll:()=>[],querySelector:()=>null,activeElement:null});
  vi.stubGlobal('window',{location,history:globalThis.history,addEventListener:vi.fn(),scrollY:0,scrollTo:vi.fn()});
  vi.stubGlobal('fetch',fetchMock);
  await import('../main');
  await Promise.resolve();
});
afterAll(()=>vi.unstubAllGlobals());
beforeEach(() => {
  window.cancelFlowRequest();
  const current = samplePayload(1);
  current.viewId = 'current-view';
  current.request = {request:'checkout',entrySymbol:'checkout.ts#processCheckout'};
  flowStore.adopt(current,true);
  flowStore.home = false;
  const baseline = samplePayload(2);
  baseline.viewId = 'baseline-view';
  flowStore.baseline = baseline;
  flowStore.compare = true;
  incoming = structuredClone(current);
  incoming.viewId = 'next-view';
  compareResponse = async()=>response({changes:[]});
  fetchMock.mockClear();
});

describe('explicit detail opening after browser analysis', () => {
  it('retains the original view until opened and preserves its fixed baseline with a matching delta', async () => {
    const baseline = flowStore.baseline;
    const current = flowStore.data;
    expect(await window.fetchTaskView('', '', '', true)).toBe(false);
    expect(flowStore.data).toBe(current);
    expect(await window.openFlowDetail()).toBe(true);
    expect(flowStore.data?.viewId).toBe('next-view');
    expect(flowStore.baseline).toBe(baseline);
    expect(flowStore.compare).toBe(true);
    expect(flowStore.data?.semanticDelta).toEqual({changes:[]});
    const compared = fetchMock.mock.calls.map(call=>call[0]).find(url=>url.pathname==='/api/view/compare');
    expect(compared?.searchParams.get('viewId')).toBe('next-view');
    expect(compared?.searchParams.get('baselineId')).toBe('baseline-view');
  });

  it('opens the pending analysis after changing the comparison baseline', async () => {
    await window.fetchTaskView('', '', '', true);
    const pending = flowStore.overviewDetailPending;
    await window.compareFlowViews('other-baseline');
    const baseline = flowStore.baseline;
    expect(flowStore.overviewDetailPending).toBe(pending);
    fetchMock.mockClear();
    expect(await window.openFlowDetail()).toBe(true);
    expect(flowStore.data?.viewId).toBe('next-view');
    expect(flowStore.baseline).toBe(baseline);
    expect(flowStore.compare).toBe(true);
    const compared = fetchMock.mock.calls.map(call=>call[0]).find(url=>url.pathname==='/api/view/compare');
    expect(compared?.searchParams.get('viewId')).toBe('next-view');
    expect(compared?.searchParams.get('baselineId')).toBe('other-baseline');
  });

  it('keeps a pending analysis openable after a failed comparison', async () => {
    await window.fetchTaskView('', '', '', true);
    compareResponse = async()=>response({message:'comparison failed'},false);
    await window.compareFlowViews('other-baseline');
    compareResponse = async()=>response({changes:[]});
    expect(await window.openFlowDetail()).toBe(true);
    expect(flowStore.data?.viewId).toBe('next-view');
    expect(flowStore.baseline?.viewId).toBe('baseline-view');
  });

  it('keeps the old view and comparison when obtaining its matching delta fails', async () => {
    const current = flowStore.data;
    const baseline = flowStore.baseline;
    await window.fetchTaskView('', '', '', true);
    compareResponse = async()=>response({message:'comparison failed'},false);
    expect(await window.openFlowDetail()).toBe(false);
    expect(flowStore.data).toBe(current);
    expect(flowStore.baseline).toBe(baseline);
    expect(flowStore.compare).toBe(true);
    expect(flowStore.notice).toContain('comparison failed');
  });

  it('does not adopt a delayed detail result after another saved View opens', async () => {
    await window.fetchTaskView('', '', '', true);
    let finish!: (value: ReturnType<typeof response>) => void;
    compareResponse = ()=>new Promise(resolve=>{finish=resolve;});
    const opening = window.openFlowDetail();
    incoming = {...incoming,viewId:'other-view'};
    expect(await window.openFlowView('other-view')).toBe(true);
    const other = flowStore.data;
    finish(response({changes:[]}));
    expect(await opening).toBe(false);
    expect(flowStore.data).toBe(other);
    expect(flowStore.data?.viewId).toBe('other-view');
    expect(flowStore.compare).toBe(false);
  });

  it('does not adopt a delayed comparison after cancellation', async () => {
    const current = flowStore.data;
    await window.fetchTaskView('', '', '', true);
    let finish!: (value: ReturnType<typeof response>) => void;
    compareResponse = ()=>new Promise(resolve=>{finish=resolve;});
    const opening = window.openFlowDetail();
    window.cancelFlowRequest();
    finish(response({changes:[]}));
    expect(await opening).toBe(false);
    expect(flowStore.data).toBe(current);
    expect(flowStore.overviewDetailPending).toBeNull();
    expect(flowStore.compare).toBe(true);
  });
});
