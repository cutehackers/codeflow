import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { flowStore, type SavedNavigationState } from './stores/flowStore.svelte';
import type { FlowTaskViewData } from './types/flow';

const target = document.getElementById('app');
if (!target) throw new Error('Target #app element not found');
const app = mount(App, { target });

declare global {
  interface Window {
    flowStore: typeof flowStore;
    fetchTaskView: (query?: string, entry?: string, flow?: string, reanalyze?: boolean) => Promise<boolean>;
    openFlowView: (id: string) => Promise<boolean>;
    showFlowHome: () => Promise<void>;
    compareFlowViews: (baselineId: string) => Promise<void>;
    cancelFlowRequest: () => void;
  }
}
window.flowStore = flowStore;
const params = new URLSearchParams(location.search);
const token = params.get('token') || '';
(window as any).__codeflowToken = token;
let sequence = 0;
let controller: AbortController | null = null;
const viewNavigation = new Map<string, { navigation: SavedNavigationState; baseline: FlowTaskViewData | null; delta: FlowTaskViewData['semanticDelta']; returnTo: SavedNavigationState | null }>();
function rememberView() {
  if (!flowStore.home && flowStore.data?.viewId) {
    viewNavigation.set(flowStore.data.viewId, { navigation: flowStore.captureNavigationState(), baseline: flowStore.baseline, delta: flowStore.data.semanticDelta, returnTo: flowStore.savedNavigationState });
  }
}

async function api(path: string, signal?: AbortSignal) {
  const url = new URL(path, location.origin);
  const response = await fetch(url, { signal, headers: token ? { 'X-CodeFlow-Token': token } : {} });
  const data = await response.json().catch(() => null);
  if (!response.ok) throw Object.assign(new Error(data?.message || `요청 실패 (${response.status})`), { candidates: data?.candidateTargets || [], code: data?.code || data?.error || '' });
  return data;
}

function setURL(viewId?: string) {
  const url = new URL(location.href);
  url.search = '';
  if (token) url.searchParams.set('token', token);
  if (viewId) url.searchParams.set('viewId', viewId);
  if (url.href !== location.href) history.pushState({}, '', url);
}

async function load(path: string, preserve: boolean, restore = false): Promise<boolean> {
  rememberView();
  controller?.abort();
  controller = new AbortController();
  const ticket = ++sequence;
  flowStore.busy = true;
  flowStore.candidates = [];
  flowStore.errorCode = '';
  flowStore.notice = preserve ? '다시 분석 중…' : '흐름을 여는 중…';
  try {
    let data: FlowTaskViewData = await api(path, controller.signal);
    if (ticket !== sequence) return false;
    // Do not replace the comparison until its fixed baseline has a matching delta.
    if (preserve && flowStore.compare && flowStore.baseline?.viewId && data.viewId) {
      const delta = await api(`/api/view/compare?viewId=${encodeURIComponent(data.viewId)}&baselineId=${encodeURIComponent(flowStore.baseline.viewId)}`, controller.signal);
      data = { ...data, semanticDelta: delta };
    }
    if (ticket !== sequence) return false;
    const adopted = flowStore.adopt(data, !preserve);
    if (adopted) {
      flowStore.home = false;
      const previous = restore && data.viewId ? viewNavigation.get(data.viewId) : undefined;
      if (previous) {
        flowStore.baseline = previous.baseline;
        flowStore.data = { ...data, semanticDelta: previous.delta };
        flowStore.savedNavigationState = previous.returnTo;
        flowStore.applyNavigationState(previous.navigation);
      }
      setURL(data.viewId);
      try { const saved = await api('/api/views'); if (ticket === sequence) flowStore.views = saved.views || []; } catch { /* Current result remains readable if listing fails. */ }
    }
    return adopted;
  } catch (error) {
    if (ticket === sequence) {
      const err = error as Error & { candidates?: string[]; code?: string };
      flowStore.notice = `흐름을 열지 못했습니다: ${err.message}`;
      flowStore.candidates = err.candidates || [];
      flowStore.errorCode = err.code || '';
    }
    return false;
  } finally {
    if (ticket === sequence) flowStore.busy = false;
  }
}

window.cancelFlowRequest = () => {
  ++sequence;
  controller?.abort();
  flowStore.abortReanalysis();
  flowStore.busy = false;
  flowStore.notice = '요청을 취소했습니다. 기존 화면을 유지합니다.';
};

window.openFlowView = (id) => load(`/api/view?viewId=${encodeURIComponent(id)}`, false, true);
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
  rememberView();
  window.cancelFlowRequest();
  const ticket = sequence;
  flowStore.home = true;
  flowStore.notice = '';
  flowStore.listError = '';
  flowStore.errorCode = '';
  flowStore.candidates = [];
  setURL();
  try {
    const saved = await api('/api/views');
    if (ticket !== sequence) return;
    flowStore.views = saved.views || [];
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
  else if (query.get('entrySymbol') || query.get('entry') || query.get('request') || query.get('query')) await window.fetchTaskView(query.get('request') || query.get('query') || '', query.get('entrySymbol') || query.get('entry') || '');
  else await window.showFlowHome();
}
window.addEventListener('popstate', () => { void bootstrap(); });
void bootstrap();
export default app;
