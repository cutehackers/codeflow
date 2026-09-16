import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { flowStore } from './stores/flowStore.svelte';
import type { FlowTaskViewData } from './types/flow';

const target = document.getElementById('app');
if (!target) throw new Error('Target #app element not found');
const app = mount(App, { target });

declare global {
  interface Window {
    flowStore: typeof flowStore;
    fetchTaskView: (query?: string, entry?: string, flow?: string, reanalyze?: boolean) => Promise<boolean>;
    openFlowView: (id: string, legacy?: boolean) => Promise<boolean>;
    showFlowHome: () => Promise<void>;
    compareFlowViews: (baselineId: string) => Promise<void>;
    cancelFlowRequest: () => void;
  }
}
window.flowStore = flowStore;
const params = new URLSearchParams(location.search);
const token = params.get('token') || '';
let sequence = 0;
let controller: AbortController | null = null;

async function api(path: string, signal?: AbortSignal) {
  const url = new URL(path, location.origin);
  const response = await fetch(url, { signal, headers: token ? { 'X-CodeFlow-Token': token } : {} });
  const data = await response.json().catch(() => null);
  if (!response.ok) throw Object.assign(new Error(data?.message || `요청 실패 (${response.status})`), { candidates:data?.candidateTargets || [] });
  return data;
}

function setURL(viewId?: string, flowId?: string) {
  const url = new URL(location.href);
  url.search = '';
  if (token) url.searchParams.set('token', token);
  if (viewId) url.searchParams.set('viewId', viewId);
  if (flowId) url.searchParams.set('flow', flowId);
  if (url.href !== location.href) history.pushState({}, '', url);
}

async function load(path: string, preserve: boolean, legacyId?: string): Promise<boolean> {
  controller?.abort();
  controller = new AbortController();
  const ticket = ++sequence;
  flowStore.busy = true;
  flowStore.candidates = [];
  flowStore.notice = preserve ? '다시 분석 중…' : '흐름을 여는 중…';
  try {
    const data: FlowTaskViewData = await api(path, controller.signal);
    if (ticket !== sequence) return false;
    const adopted = flowStore.adopt(data, !preserve);
    if (adopted) {
      flowStore.home = false;
      setURL(data.viewId, data.viewId ? undefined : legacyId);
      try { const saved = await api('/api/views'); if (ticket === sequence) flowStore.views = saved.views || []; } catch { /* Current result remains readable if listing fails. */ }
    }
    return adopted;
  } catch (error) {
    if (ticket === sequence) { flowStore.notice = `흐름을 열지 못했습니다: ${(error as Error).message}`; flowStore.candidates = (error as Error & {candidates?:string[]}).candidates || []; }
    return false;
  } finally {
    if (ticket === sequence) flowStore.busy = false;
  }
}

window.cancelFlowRequest = () => {
  ++sequence;
  controller?.abort();
  flowStore.busy = false;
  flowStore.notice = '요청을 취소했습니다. 기존 화면을 유지합니다.';
};

window.openFlowView = (id, legacy = false) => load(legacy ? `/api/view/legacy?flowId=${encodeURIComponent(id)}` : `/api/view?viewId=${encodeURIComponent(id)}`, false, legacy ? id : undefined);
window.fetchTaskView = (query = '', entry = '', flow = '', reanalyze = false) => {
  const saved = reanalyze ? flowStore.data?.request : undefined;
  const search = new URLSearchParams({ mode: 'feature' });
  const isSymbol = query.includes('#');
  const request = saved?.request || (isSymbol ? '' : query);
  const symbol = saved?.entrySymbol || entry || (isSymbol ? query : '');
  if (request) search.set('query', request);
  if (symbol) search.set('entrySymbol', symbol);
  if (saved?.flowId || flow) search.set('flowId', saved?.flowId || flow);
  return load(`/api/task/view?${search}`, reanalyze);
};

window.showFlowHome = async () => {
  window.cancelFlowRequest();
  const ticket = sequence;
  flowStore.home = true;
  flowStore.notice = '';
  flowStore.listError = '';
  flowStore.candidates = [];
  setURL();
  try {
    const [saved, legacy] = await Promise.all([api('/api/views'), api('/api/flows')]);
    if (ticket !== sequence) return;
    flowStore.views = saved.views || [];
    flowStore.legacyFlows = legacy.flows || [];
  } catch (error) {
    if (ticket === sequence) flowStore.listError = `목록을 불러오지 못했습니다: ${(error as Error).message}`;
  }
};

window.compareFlowViews = async (baselineId) => {
  const currentId = flowStore.data?.viewId;
  if (!currentId || !baselineId) return;
  const ticket = ++sequence;
  controller?.abort();
  controller = new AbortController();
  flowStore.busy = true;
  try {
    const [baseline, delta] = await Promise.all([
      api(`/api/view?viewId=${encodeURIComponent(baselineId)}`, controller.signal),
      api(`/api/view/compare?viewId=${currentId}&baselineId=${encodeURIComponent(baselineId)}`, controller.signal)
    ]);
    if (ticket !== sequence || currentId !== flowStore.data?.viewId) return;
    flowStore.setBaseline(baseline);
    flowStore.data = { ...flowStore.data!, semanticDelta: delta };
    flowStore.compare = true;
  } catch (error) {
    if (ticket === sequence) flowStore.notice = `비교하지 못했습니다: ${(error as Error).message}`;
  } finally { if (ticket === sequence) flowStore.busy = false; }
};

window.addEventListener('codeflow:query', (event) => {
  const query = (event as CustomEvent<{ query: string }>).detail.query;
  if (query?.trim()) void window.fetchTaskView(query.trim());
});
async function bootstrap() {
  const query = new URLSearchParams(location.search);
  if (query.get('viewId')) await window.openFlowView(query.get('viewId')!);
  else if (query.get('flow') || query.get('flowId')) await window.openFlowView(query.get('flow') || query.get('flowId')!, true);
  else if (query.get('entrySymbol') || query.get('entry') || query.get('request') || query.get('query')) await window.fetchTaskView(query.get('request') || query.get('query') || '', query.get('entrySymbol') || query.get('entry') || '');
  else await window.showFlowHome();
}
window.addEventListener('popstate', () => { void bootstrap(); });
void bootstrap();
export default app;
