import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { flowStore, type SavedNavigationState, type ExplorationNavigationState } from './stores/flowStore.svelte';
import type { FlowTaskViewData } from './types/flow';

const target = document.getElementById('app');
if (!target) throw new Error('Target #app element not found');
const app = mount(App, { target });

declare global {
  interface Window {
    flowStore: typeof flowStore;
    fetchTaskView: (query?: string, entry?: string, flow?: string, reanalyze?: boolean, expectedSnapshotId?: string) => Promise<boolean>;
    openFlowView: (id: string) => Promise<boolean>;
    openFlowDetail: () => Promise<boolean>;
    showFlowHome: () => Promise<void>;
    compareFlowViews: (baselineId: string) => Promise<void>;
    cancelFlowRequest: () => void;
  }
}
window.flowStore = flowStore;
const params = new URLSearchParams(location.search);
const token = params.get('token') || '';
const studyLayout = params.get('studyLayout') === 'code-focused' ? 'code-focused' : '';
(window as any).__codeflowToken = token;
let sequence = 0;
let controller: AbortController | null = null;
let detailPendingRequest: {preserve: boolean; originViewId?: string} | null = null;
const viewNavigation = new Map<string, { navigation: SavedNavigationState; baseline: FlowTaskViewData | null; delta: FlowTaskViewData['semanticDelta']; returnTo: SavedNavigationState | null; exploration: ExplorationNavigationState }>();
function rememberView() {
  if (!flowStore.home && flowStore.data?.viewId) {
    viewNavigation.set(flowStore.data.viewId, { navigation: flowStore.captureNavigationState(), baseline: flowStore.baseline, delta: flowStore.data.semanticDelta, returnTo: flowStore.savedNavigationState, exploration: flowStore.explorationNavigation });
  }
}

async function api(path: string, signal?: AbortSignal) {
  const url = new URL(path, location.origin);
  const response = await fetch(url, { signal, headers: token ? { 'X-CodeFlow-Token': token } : {} });
  const data = await response.json().catch(() => null);
  if (!response.ok) {
    const message = data?.code === 'analysis_failed'
      ? [data?.target ? `대상 ${data.target}` : '', data?.reason || data?.message, data?.nextAction].filter(Boolean).join(' · ')
      : data?.message || `요청 실패 (${response.status})`;
    throw Object.assign(new Error(message), { candidates: data?.candidateTargets || [], code: data?.code || data?.error || '' });
  }
  return data;
}

function setURL(viewId?: string) {
  const url = new URL(location.href);
  url.search = '';
  if (token) url.searchParams.set('token', token);
  if (viewId) url.searchParams.set('viewId', viewId);
  if (studyLayout) url.searchParams.set('studyLayout', studyLayout);
  if (url.href !== location.href) history.pushState({}, '', url);
}

async function load(path: string, preserve: boolean, restore = false): Promise<boolean> {
  rememberView();
  controller?.abort();
  controller = new AbortController();
  const ticket = ++sequence;
  flowStore.overviewDetailPending = null;
  detailPendingRequest = null;
  flowStore.busy = true;
  flowStore.candidates = [];
  flowStore.errorCode = '';
  flowStore.notice = preserve ? '다시 분석 중…' : '흐름을 여는 중…';
  try {
    let data: FlowTaskViewData = await api(path, controller.signal);
    if (ticket !== sequence) return false;
    if (path.startsWith('/api/task/view?') && !data.requestedFlowOverview) {
      detailPendingRequest = {preserve, originViewId: flowStore.data?.viewId};
      flowStore.overviewDetailPending = data;
      flowStore.notice = '처리 구간 개요가 생성되지 않았습니다. 브라우저 직접 분석에는 구간을 제안하는 에이전트가 없습니다. 현재 결과는 중간 결과입니다. 에이전트에게 Overview 생성을 요청하세요.';
      return false;
    }
    // Do not replace the comparison until its fixed baseline has a matching delta.
    if (preserve && flowStore.compare && flowStore.baseline?.viewId && data.viewId) {
      const delta = await api(`/api/view/compare?viewId=${encodeURIComponent(data.viewId)}&baselineId=${encodeURIComponent(flowStore.baseline.viewId)}`, controller.signal);
      data = { ...data, semanticDelta: delta };
    }
    if (ticket !== sequence) return false;
    const adopted = flowStore.adopt(data, !preserve);
    if (adopted) {
      flowStore.pendingRequest = '';
      flowStore.home = false;
      const previous = restore && data.viewId ? viewNavigation.get(data.viewId) : undefined;
      if (previous && flowStore.validateNavigationState(previous.navigation)) {
        flowStore.baseline = previous.baseline;
        flowStore.data = { ...data, semanticDelta: previous.delta };
        flowStore.savedNavigationState = previous.returnTo;
        flowStore.explorationNavigation = previous.exploration;
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
  flowStore.overviewDetailPending = null;
  detailPendingRequest = null;
  flowStore.notice = '요청을 취소했습니다. 기존 화면을 유지합니다.';
};

window.openFlowDetail = async () => {
  const candidate = flowStore.overviewDetailPending;
  const request = detailPendingRequest;
  if (!candidate || !request || request.originViewId !== flowStore.data?.viewId) return false;
  const baseline = flowStore.baseline;
  const compare = flowStore.compare;
  const ticket = ++sequence;
  controller?.abort();
  controller = new AbortController();
  flowStore.busy = true;
  try {
    let data = candidate;
    if (compare && baseline?.viewId && data.viewId) {
      const delta = await api(`/api/view/compare?viewId=${encodeURIComponent(data.viewId)}&baselineId=${encodeURIComponent(baseline.viewId)}`, controller.signal);
      data = {...data, semanticDelta: delta};
    }
    if (ticket !== sequence || candidate !== flowStore.overviewDetailPending || request.originViewId !== flowStore.data?.viewId || baseline !== flowStore.baseline || compare !== flowStore.compare) return false;
    rememberView();
    if (!flowStore.adopt(data, !request.preserve)) return false;
    // Explicit detail opening retains the selected comparison basis.
    flowStore.baseline = baseline;
    flowStore.compare = compare;
    flowStore.overviewDetailPending = null;
    detailPendingRequest = null;
    flowStore.explorationMode = 'overview';
    flowStore.home = false;
    setURL(data.viewId);
    try { const saved = await api('/api/views'); if (ticket === sequence) flowStore.views = saved.views || []; } catch { /* Detail remains readable when listing fails. */ }
    return true;
  } catch (error) {
    if (ticket === sequence) flowStore.notice = `중간 결과를 열지 못했습니다: ${(error as Error).message}`;
    return false;
  } finally {
    if (ticket === sequence) flowStore.busy = false;
  }
};

window.openFlowView = (id) => load(`/api/view?viewId=${encodeURIComponent(id)}`, false, true);
window.fetchTaskView = (query = '', entry = '', flow = '', reanalyze = false, expectedSnapshotId = '') => {
  const saved = reanalyze ? flowStore.data?.request : undefined;
  const search = new URLSearchParams({ mode: 'feature' });
  const isSymbol = query.includes('#');
  const request = saved?.request || (isSymbol ? '' : query);
  const symbol = saved?.entrySymbol || entry || (isSymbol ? query : '');
  if (!reanalyze) flowStore.pendingRequest = request;
  if (request) search.set('query', request);
  if (symbol) search.set('entrySymbol', symbol);
  if (saved?.flowId || flow) search.set('flowId', saved?.flowId || flow);
  if (expectedSnapshotId) search.set('expectedSnapshotId', expectedSnapshotId);
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
  flowStore.pendingRequest = '';
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
