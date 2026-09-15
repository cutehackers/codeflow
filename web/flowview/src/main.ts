import './app.css';
import { mount } from 'svelte';
import App from './App.svelte';
import { flowStore } from './stores/flowStore.svelte';
import { samplePayload } from './stores/sampleData';
import type { FlowTaskViewData } from './types/flow';

// Mount the Svelte application
const target = document.getElementById('app');
if (!target) {
  throw new Error('Target #app element not found');
}

const app = mount(App, { target });

// Expose global test interface and compatibility layer on window
declare global {
  interface Window {
    flowStore: typeof flowStore;
    receive: (data: FlowTaskViewData) => void;
    select: (stepId: string) => void;
    setView: (mode: 'code' | 'process') => void;
    samplePayload: typeof samplePayload;
    fetchTaskView: (query?: string, entrySymbol?: string, flowId?: string) => Promise<boolean>;
    INITIAL_DATA?: FlowTaskViewData;
  }
}

window.flowStore = flowStore;
window.receive = (data: FlowTaskViewData) => flowStore.receive(data);
window.select = (stepId: string) => flowStore.select(stepId);
window.setView = (mode: 'code' | 'process') => flowStore.setViewMode(mode);
window.samplePayload = samplePayload;

const params = new URLSearchParams(window.location.search);
const token = params.get('token') || '';
const requestQuery = params.get('request') || params.get('query') || '';
const entrySymbol = params.get('entrySymbol') || params.get('entry') || '';
const flowId = params.get('flow') || params.get('flowId') || '';

async function api(path: string): Promise<Response> {
  const url = new URL(path, window.location.origin);
  if (token) {
    url.searchParams.set('token', token);
  }
  const headers: Record<string, string> = {};
  if (token) {
    headers['X-CodeFlow-Token'] = token;
  }
  return fetch(url.toString(), { headers });
}

export async function fetchTaskView(query?: string, entry?: string, flow?: string): Promise<boolean> {
  if (!query && !entry && !flow) return false;
  flowStore.notice = `흐름 '${query || entry || flow}' 분석 중...`;

  try {
    const isSymbol = query?.includes('#') || (query?.includes('.') && query?.includes('/'));
    const resolvedEntry = entry || (isSymbol ? query : '');
    const urlParams = new URLSearchParams({ mode: 'feature' });
    if (query && !isSymbol) urlParams.set('query', query);
    if (resolvedEntry) urlParams.set('entrySymbol', resolvedEntry);
    if (flow) urlParams.set('flowId', flow);

    const searchUrl = `/api/task/view?${urlParams.toString()}`;
    const res = await api(searchUrl);
    if (!res.ok) {
      const errJson = await res.json().catch(() => null);
      throw new Error(errJson?.message || `서버 응답 오류 (${res.status})`);
    }
    const data: FlowTaskViewData = await res.json();
    if (data?.semanticMap?.steps?.length) {
      flowStore.receive(data);
      return true;
    } else {
      flowStore.notice = `분석 결과 '${query || entry || flow}'에 해당하는 단계가 없습니다.`;
      return false;
    }
  } catch (err) {
    console.warn('Task view fetch failed:', err);
    flowStore.notice = `코드를 확인하지 못했습니다: ${(err as Error).message}`;
    return false;
  }
}

window.fetchTaskView = fetchTaskView;

// Bootstrap data handling
async function bootstrap() {
  if (window.INITIAL_DATA) {
    flowStore.receive(window.INITIAL_DATA);
    return;
  }

  // Attempt real backend query if parameters are available
  if (flowId || requestQuery || entrySymbol) {
    const ok = await fetchTaskView(requestQuery, entrySymbol, flowId);
    if (ok) {
      setupWorkspaceStream();
      return;
    }
  }

  // If running on a live server, check if published flows exist
  if (window.location.protocol.startsWith('http')) {
    try {
      const res = await api('/api/flows');
      if (res.ok) {
        const idx = await res.json();
        const firstFlow = idx?.flows?.[0]?.flowId || idx?.flows?.[0]?.id;
        if (firstFlow) {
          const ok = await fetchTaskView('', '', firstFlow);
          if (ok) {
            setupWorkspaceStream();
            return;
          }
        }
      }
    } catch {
      // Backend not running / not accessible
    }
  }

  // Fallback: load the canonical 5-frame business flow payload by default
  flowStore.receive(samplePayload(1));
}

function setupWorkspaceStream() {
  if (!window.location.protocol.startsWith('http')) return;
  try {
    const sseUrl = `/api/workspace/stream${token ? `?token=${encodeURIComponent(token)}` : ''}`;
    const stream = new EventSource(sseUrl);

    stream.addEventListener('generation.published', () => {
      const current = flowStore.flowTitle;
      if (current) {
        fetchTaskView(current);
      }
    });

    stream.addEventListener('error', () => {
      // Retain active view; EventSource will auto-reconnect
    });
  } catch (e) {
    console.warn('Workspace stream not established', e);
  }
}

// Handle query dispatch from UI
window.addEventListener('codeflow:query', async (event: Event) => {
  const custom = event as CustomEvent<{ query: string }>;
  const query = custom.detail?.query;
  if (!query) return;

  if (window.location.protocol.startsWith('http')) {
    const ok = await fetchTaskView(query);
    if (!ok && !flowStore.data) {
      flowStore.receive(samplePayload(2));
    }
  } else {
    // Standalone sample toggle
    flowStore.receive(samplePayload(2));
  }
});

bootstrap();

export default app;
