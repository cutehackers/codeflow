import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const base = 'http://127.0.0.1:4589';
const entry = 'app/page.tsx#HomePage.handleQuickCheckout';

function payload(version: number) {
  const generationId = 'generation-' + version, snapshotId = 'snapshot-' + version;
  const steps = [
    {stepId:'check-'+version,structuralIdentity:'check',ordinal:1,name:'재고를 확인한다',technicalName:'Inventory.check',layer:'domain',branch:'requestedQty > availableQty',anchor:{repoRelativePath:'inventory.ts'}},
    {stepId:'save-'+version,structuralIdentity:'save',ordinal:2,name:'주문을 저장한다',technicalName:'Orders.save',layer:'data',anchor:{repoRelativePath:'orders.ts'}},
  ];
  return {
    semanticMap:{generationId,validatedAgainstSnapshotId:snapshotId,summary:{requested:'주문 처리',current:'주문 처리'},steps,edges:[{fromStepId:steps[0].stepId,toStepId:steps[1].stepId,kind:'call',resolutionStatus:'resolved'}]},
    flowContexts:Object.fromEntries(steps.map((step,i)=>[step.stepId,{stepId:step.stepId,generationId,snapshotId,canonicalPath:step.anchor.repoRelativePath,precision:'exact',displayedLines:[{lineNumber:10,text:i?'return database.save(order);':version===1?'return orders.save(order);':'if (requestedQty > availableQty) throw new OutOfStock();',isHit:true}]}])),
    unknowns:[],
  };
}

test('Live route restores the requested flow and shows source without replacing Static FlowView', async ({page})=>{
  const requests:string[]=[];
  page.on('request',request=>requests.push(new URL(request.url()).pathname));
  await page.goto(base+'/live?token=testtoken&request='+encodeURIComponent('quick checkout')+'&entrySymbol='+encodeURIComponent(entry));
  await expect(page).toHaveTitle('CodeFlow · Live Semantic Map');
  await expect(page.locator('#code-flow .source').first()).toBeVisible();
  await expect(page.locator('#code-flow')).toContainText('handleQuickCheckout');
  expect(requests).not.toContain('/api/flows');
  expect(requests).not.toContain('/api/source');
  await expect(page.locator('#current-answer-strip')).toHaveCount(0);
  await expect(page.locator('#workspace-analysis-lag')).toHaveCount(0);
  await page.locator('#static-link').click();
  await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
  await expect(page.locator('body')).not.toHaveAttribute('data-view','live-semantic-map');
});

test('Live natural language request resolves ambiguity on the same screen', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.locator('#query-input').fill('checkout');
  await page.locator('#query-submit').click();
  await expect(page.locator('#choices')).toBeVisible();
  await page.locator('#choices button').filter({hasText:'HomePage.handleQuickCheckout'}).click();
  await expect(page.locator('#code-flow .source').first()).toBeVisible();
  expect(new URL(page.url()).pathname).toBe('/live');
  expect(new URL(page.url()).searchParams.get('entrySymbol')).toBe(entry);
});

test('paused updates keep the displayed source and preserve selection by structural identity', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(data=>(window as any).receive(data),payload(1));
  await page.locator('#step-nav [data-select="save-1"]').click();
  await page.locator('#pause').click();
  await page.evaluate(data=>(window as any).receive(data),payload(2));
  await expect(page.locator('[data-card="check-1"]')).toContainText('return orders.save(order);');
  await expect(page.locator('[data-card="check-2"]')).toHaveCount(0);
  await page.locator('#apply').click();
  await expect(page.locator('[data-card="save-2"]')).toHaveClass(/selected/);
  await expect(page.locator('[data-card="check-2"]')).toContainText('OutOfStock');
  await page.locator('#compare').click();
  await expect(page.locator('.compare-grid')).toContainText('return orders.save(order);');
  await expect(page.locator('.compare-grid')).toContainText('OutOfStock');
  await page.locator('[data-card="check-2"] .relation button').click();
  await expect(page.locator('[data-card="save-2"]')).toHaveClass(/selected/);
  const returned=payload(2);
  returned.semanticMap.edges[0].kind='return';
  await page.locator('#pause').click();
  await page.evaluate(value=>(window as any).receive(value),returned);
  await expect(page.locator('#context button[data-select="check-2"]')).toHaveCount(0);
});

test('source from a different snapshot cannot appear under the selected analysis', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  const data=payload(1);
  data.flowContexts['check-1'].snapshotId='different-snapshot';
  await page.evaluate(value=>(window as any).receive(value),data);
  await expect(page.locator('[data-card="check-1"] .source')).toHaveCount(0);
  await expect(page.locator('[data-card="check-1"]')).toContainText('소스가 없습니다');
  await expect(page.locator('[data-card="save-1"] .source')).toBeVisible();
});

test('gap invalidates a queued update and unavailable precision never highlights a statement', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  const data=payload(1);data.flowContexts['check-1'].precision='unavailable';
  await page.evaluate(value=>(window as any).receive(value),data);
  await expect(page.locator('[data-card="check-1"] .hit')).toHaveCount(0);
  await page.locator('#pause').click();
  await page.evaluate(value=>(window as any).receive(value),payload(2));
  await expect(page.locator('#apply')).toBeVisible();
  await page.evaluate(()=>(window as any).handleLiveEvent('generation.gap',new MessageEvent('generation.gap',{data:'{}'})));
  await expect(page.locator('#apply')).toBeHidden();
  await expect(page.locator('#live-notice')).toContainText('이전 코드');
  await expect(page.locator('[data-card="check-1"]')).toBeVisible();
});

test('published event fetches only its persisted generation and exposes semantic changes', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  const requested:string[]=[];
  await page.route('**/api/live/generation?*',async route=>{
    requested.push(route.request().url());
    const data:any=payload(2);
    data.semanticDelta={changes:[
      {kind:'changed_rule',targetStepId:'check-2',summary:'재고 분기 조건 변경'},
      {kind:'evidence_updated',targetStepId:'save-2',summary:'저장 근거 갱신'},
    ]};
    await route.fulfill({json:data});
  });
  await page.evaluate(()=>(window as any).handleLiveEvent('generation.published',new MessageEvent('generation.published',{data:JSON.stringify({generationId:'generation-2',computedBasisId:'basis-2',validatedAgainstSnapshotId:'snapshot-2'})})));
  await expect(page.locator('#change-pulse')).toBeVisible();
  await expect(page.locator('#pulse-list')).toContainText('재고 분기 조건 변경');
  await page.locator('[data-delta-step="save-2"]').click();
  await expect(page.locator('[data-card="save-2"]')).toHaveClass(/selected/);
  expect(requested).toHaveLength(1);
  const url=new URL(requested[0]);
  expect(url.searchParams.get('generationId')).toBe('generation-2');
  expect(url.searchParams.get('computedBasisId')).toBe('basis-2');
  expect(url.searchParams.get('snapshotId')).toBe('snapshot-2');
});

test('hybrid views share selection and conditions without adopting pending changes', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  await page.locator('[data-expand="check-1"]').click();
  await page.locator('#condition-focus').selectOption('check');
  await page.evaluate(value=>(window as any).receive(value),payload(2));
  const rail=await page.locator('#step-nav').elementHandle();
  await page.locator('#view-process').click();
  await expect(page.locator('#code-flow')).toBeHidden();
  await expect(page.locator('[data-process="check-1"]')).toBeVisible();
  await expect(page.locator('#apply')).toBeVisible();
  await expect(page.locator('#condition-focus')).toHaveValue('check');
  expect(await rail!.evaluate(node=>node===document.getElementById('step-nav'))).toBe(true);
  await page.locator('[data-process="save-1"] .process-select').click();
  await expect(page.locator('#context h2')).toHaveText('주문을 저장한다');
  await page.locator('#inspect-code').click();
  await expect(page.locator('[data-card="save-1"]')).toHaveClass(/selected/);
  await expect(page.locator('[data-expand="check-1"]')).toHaveText('코드 접기');
  await expect(page.locator('[data-card="check-2"]')).toHaveCount(0);
  await page.locator('#view-process').click();
  await page.locator('#apply').click();
  await expect(page.locator('[data-process="save-2"]')).toHaveClass(/selected/);
  await expect(page.locator('#condition-focus')).toHaveValue('check');
  await page.emulateMedia({reducedMotion:'reduce'});
  expect(await page.locator('#process-flow').evaluate(node=>getComputedStyle(node).animationName)).toBe('none');
  expect((await new AxeBuilder({page}).analyze()).violations).toEqual([]);
});

test('code cards extend the page and the header describes the displayed flow', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  const data=payload(1);
  data.flowContexts['check-1'].displayedLines=Array.from({length:16},(_,i)=>({lineNumber:i+1,text:'return orders.save(order);',isHit:true}));
  await page.evaluate(value=>(window as any).receive(value),data);
  await expect(page.locator('#flow-scope')).toHaveText('2개 처리 단계 · 포함 계층: Domain · Repository');
  expect(await page.locator('#code-flow').evaluate(node=>getComputedStyle(node).overflowY)).toBe('visible');
  await page.locator('#step-nav [data-select="save-1"]').click();
  expect(await page.evaluate(()=>window.scrollY)).toBeGreaterThan(0);
  await expect(page.locator('[data-card="save-1"]')).toBeInViewport();
  await page.evaluate(value=>(window as any).receive(value),payload(2));
  await expect(page.locator('#flow-change')).toContainText('재고를 확인한다');
});

test('view switch aligns with the content on large screens in both modes', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  for(const width of [1440,1600,1920,2560,3440]){
    await page.setViewportSize({width,height:1080});
    for(const view of ['code','process']){
      await page.locator('#view-'+view).click();
      const toolbar=await page.locator('.view-toolbar').boundingBox();
      const layout=await page.locator('.layout').boundingBox();
      expect(Math.abs(toolbar!.x-layout!.x)).toBeLessThan(1);
      expect(Math.abs(toolbar!.width-layout!.width)).toBeLessThan(1);
    }
  }
});

test('monochrome source layout remains accessible and fits a narrow viewport', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  const result=await new AxeBuilder({page}).analyze();
  expect(result.violations).toEqual([]);
  const colors=await page.locator('.bar,.notice,.source,.hit,.step-link,.code-card').evaluateAll(elements=>elements.flatMap(element=>{const style=getComputedStyle(element);return [style.color,style.backgroundColor,style.borderTopColor];}));
  for(const color of colors){const channels=color.match(/[\d.]+/g)?.map(Number);if(channels&&channels.length>=3)expect(channels[0]===channels[1]&&channels[1]===channels[2]).toBe(true);}
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
