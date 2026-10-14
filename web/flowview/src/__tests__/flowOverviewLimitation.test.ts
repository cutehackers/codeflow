import {describe,expect,it,vi,afterEach} from 'vitest';
import {render} from 'svelte/server';
import App from '../App.svelte';
import {flowStore} from '../stores/flowStore.svelte';
import {samplePayload} from '../stores/sampleData';

afterEach(()=>vi.unstubAllGlobals());
describe('stored overview limitation',()=>{
  it('marks a rejected overview as intermediate without showing legacy detail',()=>{
    vi.stubGlobal('window',{location:{search:''}});
    const data=samplePayload(1);
    data.requestedFlowOverviewLimitation='처리 구간 개요의 참조가 유효하지 않습니다. Overview를 다시 생성해야 합니다.';
    expect(flowStore.adopt(data,true)).toBe(true);
    flowStore.home=false;
    expect(flowStore.explorationMode).toBe('overview');
    const body=render(App).body;
    expect(body).toContain(data.requestedFlowOverviewLimitation);
    expect(body).not.toContain('코드 근거 확인 완료');
    expect(body).not.toContain('id="requested-flow-blocks"');
    expect(body).not.toContain('id="code-flow"');
    expect(body).toContain('중간 결과');
    expect(body).not.toContain('문장별 탐색');
  });
});
