import {describe,expect,it,vi,afterEach} from 'vitest';
import {render} from 'svelte/server';
import App from '../App.svelte';
import {flowStore} from '../stores/flowStore.svelte';
import {samplePayload} from '../stores/sampleData';

afterEach(()=>vi.unstubAllGlobals());
describe('stored overview limitation',()=>{
  it('opens the preserved detail and displays the rejected overview reason',()=>{
    vi.stubGlobal('window',{location:{search:''}});
    const data=samplePayload(1);
    data.requestedFlowOverviewLimitation='처리 구간 개요의 참조가 유효하지 않습니다. 기존 문장별 탐색을 제공합니다.';
    expect(flowStore.adopt(data,true)).toBe(true);
    flowStore.home=false;
    expect(flowStore.explorationMode).toBe('statements');
    const body=render(App).body;
    expect(body).toContain(data.requestedFlowOverviewLimitation);
    expect(body).not.toContain('코드 근거 확인 완료');
    expect(body).not.toContain('id="requested-flow-blocks"');
    expect(body).toContain('id="code-flow"');
  });
});
