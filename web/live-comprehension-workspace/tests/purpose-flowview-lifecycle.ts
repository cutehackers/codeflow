import {test, expect, type Page, type APIRequestContext} from '@playwright/test';
import {readFileSync, unlinkSync, writeFileSync} from 'node:fs';
import path from 'node:path';
import {headers, curatePurposeView, openPurposeView, openPurposeStep, expectSelectedSource} from './purpose-flowview';

const entry = 'app/page.tsx#HomePage.onQuickCheckout';

function largeFlowSource() {
  const adjustments = Array.from({length: 80}, (_, index) => index + 1);
  return [
    'export function processLargeFlow(request: any) {',
    "  if (!request.authenticated) return 'authenticated';",
    '  let total = request.amount;',
    ...adjustments.map(index => `  total = applyAdjustment${String(index).padStart(2, '0')}(total);`),
    '  return total;',
    '}',
    ...adjustments.map(index => `function applyAdjustment${String(index).padStart(2, '0')}(value: number) { return value + ${index}; }`),
    '',
  ].join('\n');
}

async function analyze(request: APIRequestContext, symbol = entry) {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(symbol)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  return response.json();
}

function relationStep(view: any) {
  const edge = view.semanticMap.edges.find((item: any) => item.resolutionStatus === 'resolved' && view.flowContexts[item.fromStepId]?.precision === 'exact');
  expect(edge).toBeTruthy();
  return edge.fromStepId;
}

async function readingState(page: Page) {
  return page.evaluate(() => ({segment:window.flowStore.selectedSegmentId, block:window.flowStore.selectedBlockId, step:window.flowStore.selectedStepId}));
}

async function followAndReturn(page: Page, preserveScroll = false) {
  const relation = page.locator('[data-navigation-focus^="outgoing:"]').first();
  await expect(relation).toBeVisible();
  const focus = await relation.getAttribute('data-navigation-focus');
  const state = await readingState(page);
  if (preserveScroll) await relation.evaluate(element => (element as HTMLElement).focus({preventScroll:true}));
  else await relation.focus();
  await relation.click({force:preserveScroll});
  await expect(page.getByRole('button',{name:'원래 장면 복귀',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'원래 장면 복귀',exact:true}).click();
  await expect.poll(() => readingState(page)).toEqual(state);
  await expect(page.locator(`[data-navigation-focus="${focus}"]`)).toBeFocused();
}

async function compare(page: Page, baseline: any) {
  await page.locator('#baseline').selectOption(baseline.viewId);
  await page.locator('#compare').click();
  await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
}

async function containedSources(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const sources = page.locator('.compare-grid .source');
  await expect(sources).toHaveCount(2);
  for (const source of await sources.all()) expect(await source.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true);
}

export function registerPurposeLifecycleTests() {
  test('relation return restores reading positions in a large flow', async ({page, request}) => {
    const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'large-return-flow.ts');
    writeFileSync(source, largeFlowSource().replace('processLargeFlow', 'processLargeReturnFlow'));
    try {
      const view = await openPurposeView(page, request, await analyze(request,'app/large-return-flow.ts#processLargeReturnFlow'));
      await openPurposeStep(page, view, relationStep(view));
      // The source workspace now scrolls with the document. Both positions are part of the reading snapshot.
      await page.locator('[data-navigation-focus^="outgoing:"]').first().scrollIntoViewIfNeeded();
      const positions = await page.evaluate(() => {
        const panel = document.getElementById('requested-flow-overview')!;
        panel.scrollTop = Math.min(120, panel.scrollHeight - panel.clientHeight);
        return {overview:panel.scrollTop,document:window.scrollY};
      });
      expect(positions.overview).toBeGreaterThan(0);
      expect(positions.document).toBeGreaterThan(0);
      await followAndReturn(page,true);
      await expect.poll(async () => Math.abs(await page.locator('#requested-flow-overview').evaluate(element => element.scrollTop) - positions.overview)).toBeLessThanOrEqual(1);
      await expect.poll(async () => Math.abs(await page.evaluate(() => window.scrollY) - positions.document)).toBeLessThanOrEqual(1);
    } finally { unlinkSync(source); }
  });

  test('relation return restores the source segment, block and relation focus', async ({page, request}) => {
    const view = await openPurposeView(page, request, await analyze(request));
    await openPurposeStep(page, view, relationStep(view));
    await followAndReturn(page);
    await expectSelectedSource(page, view.flowContexts[relationStep(view)]);
  });

  test('relation return restores comparison mode, source segment and focus', async ({page, request}) => {
    const baseline = await curatePurposeView(request, await analyze(request));
    const current = await openPurposeView(page, request, await analyze(request));
    await compare(page, baseline);
    await openPurposeStep(page, current, relationStep(current));
    await followAndReturn(page);
    await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
    await expect(page.locator('.compare-grid')).toHaveCount(1);
    expect(await page.evaluate(() => window.flowStore.baseline?.viewId)).toBe(baseline.viewId);
  });

  test('large analyzed flow keeps its distant segment reachable at narrow width', async ({page, request}) => {
    const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'large-flow.ts');
    writeFileSync(source, largeFlowSource());
    try {
      const analysis = await analyze(request,'app/large-flow.ts#processLargeFlow');
      expect(analysis.semanticMap.steps.length).toBeGreaterThan(150);
      await page.setViewportSize({width:390,height:844});
      const started = Date.now();
      const view = await openPurposeView(page,request,analysis);
      expect(view.requestedFlowOverview.segments.length).toBeGreaterThan(7);
      const last = view.requestedFlowOverview.segments.at(-1);
      const lastStep = last.stepIds.at(-1);
      await page.locator(`[data-segment="${last.segmentId}"]`).scrollIntoViewIfNeeded();
      await openPurposeStep(page,view,lastStep);
      await expectSelectedSource(page,view.flowContexts[lastStep]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      expect(await page.locator('#code-flow .source').evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true);
      const segments = view.requestedFlowOverview.segments;
      const edge = view.semanticMap.edges.find((item:any) => item.resolutionStatus === 'resolved' && segments.findIndex((segment:any)=>segment.stepIds.includes(item.toStepId)) > segments.findIndex((segment:any)=>segment.stepIds.includes(item.fromStepId)) && segments.findIndex((segment:any)=>segment.stepIds.includes(item.toStepId)) < segments.length-7);
      expect(edge).toBeTruthy();
      await openPurposeStep(page,view,edge.fromStepId);
      await page.locator(`[data-segment="${last.segmentId}"]`).scrollIntoViewIfNeeded();
      const target = segments.find((segment:any)=>segment.stepIds.includes(edge.toStepId));
      expect(await page.locator(`[data-segment="${target.segmentId}"]`).evaluate(element => {const rect=element.getBoundingClientRect(); return rect.top >= innerHeight || rect.bottom <= 0;})).toBe(true);
      await followAndReturn(page);
      console.log('large-flow navigation '+JSON.stringify({stepCount:analysis.semanticMap.steps.length,segmentCount:segments.length,readAndReturnMs:Date.now()-started}));
    } finally { unlinkSync(source); }
  });

  test('failed reanalysis and cancelled responses preserve the current result', async ({page,request}) => {
    const view=await openPurposeView(page,request,await analyze(request));
    await openPurposeStep(page,view,view.requestedFlowOverview.segments.at(-1).stepIds.at(-1));
    const selected=await readingState(page);
    await page.route('**/api/task/view?*',route=>route.fulfill({status:500,json:{message:'analysis unavailable'}}));
    await page.locator('#reanalyze').click();
    await expect(page.getByRole('status')).toContainText('analysis unavailable');
    expect(await readingState(page)).toEqual(selected);
    await expect(page).toHaveURL(new RegExp(`viewId=${view.viewId}`));
    await page.unroute('**/api/task/view?*');
    const delayed = await analyze(request,'services/orderService.ts#processOrder');
    expect(delayed.viewId).not.toBe(view.viewId);
    await page.route('**/api/task/view?*',async route=>{await new Promise(resolve=>setTimeout(resolve,1000));await route.fulfill({json:delayed}).catch(()=>{});});
    await page.locator('#reanalyze').click();
    await page.getByRole('button',{name:'취소',exact:true}).click();
    await expect(page.getByRole('status')).toContainText('취소');
    await page.waitForTimeout(1200);
    await expect(page.getByRole('status')).toContainText('취소');
    await expect(page).toHaveURL(new RegExp(`viewId=${view.viewId}`));
    expect(await readingState(page)).toEqual(selected);
  });

  test('explicit comparison, staged reanalysis and direct relation context', async ({page,request}) => {
    const baseline=await curatePurposeView(request,await analyze(request));
    const source=path.join(process.env.CODEFLOW_BROWSER_FIXTURE!,'app/page.tsx');
    const original=readFileSync(source,'utf8');
    let current:any;
    try {writeFileSync(source,original+'\n// comparison source revision\n');current=await openPurposeView(page,request,await analyze(request));}
    finally {writeFileSync(source,original);}
    await expect(page.locator('#compare')).toBeDisabled();
    await compare(page,baseline);
    await openPurposeStep(page,current,relationStep(current));
    const selected=await readingState(page);
    await page.locator('#reanalyze').click();
    await expect(page.getByRole('status')).toContainText('처리 구간 개요가 생성되지 않았습니다');
    await expect(page).toHaveURL(new RegExp(`viewId=${current.viewId}`));
    expect(await readingState(page)).toEqual(selected);
    expect(await page.evaluate(()=>window.flowStore.baseline?.viewId)).toBe(baseline.viewId);
    await expect(page.locator('.compare-grid')).toHaveCount(1);
    await page.getByRole('button',{name:'중간 결과 확인',exact:true}).click();
    await expect(page).not.toHaveURL(new RegExp(`viewId=${current.viewId}`));
    await expect(page.locator('#requested-flow-overview')).toHaveCount(0);
    await expect(page.locator('#code-flow')).toHaveCount(0);
    expect(await page.evaluate(()=>window.flowStore.baseline?.viewId)).toBe(baseline.viewId);
    await page.goto(`/?token=testtoken&viewId=${current.viewId}`);
    await openPurposeStep(page,current,relationStep(current));
    const connections=page.getByRole('region',{name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('heading',{name:'이곳으로 연결'})).toBeVisible();
    await expect(connections.getByRole('heading',{name:'이곳에서 연결'})).toBeVisible();
  });

  test('compares an actual source edit against the selected saved analysis', async ({page,request}) => {
    const baseline=await openPurposeView(page,request,await analyze(request));
    const source=path.join(process.env.CODEFLOW_BROWSER_FIXTURE!,'app/page.tsx');
    const original=readFileSync(source,'utf8');
    try {
      writeFileSync(source,original.replace("setStatus('completed')","setStatus('confirmed')"));
      await page.locator('#reanalyze').click();
      await expect(page.getByRole('status')).toContainText('처리 구간 개요가 생성되지 않았습니다');
      await expect(page).toHaveURL(new RegExp(`viewId=${baseline.viewId}`));
      const intermediate=await page.evaluate(()=>window.flowStore.overviewDetailPending);
      expect(intermediate.presentationStatus).toBe('intermediate');
      const current=await openPurposeView(page,request,intermediate);
      await compare(page,baseline);
      await expect(page.getByText(/변경 [1-9][0-9]*건/)).toBeVisible();
      const step=[...current.semanticMap.steps].reverse().find((item:any)=>item.name.includes('setStatus'));
      expect(step).toBeTruthy();
      await openPurposeStep(page,current,step.stepId);
      await page.setViewportSize({width:390,height:844});
      await containedSources(page);
    } finally {writeFileSync(source,original);}
  });

  test('long comparison source stays contained and remains horizontally readable', async ({page,request}) => {
    const baseline=await curatePurposeView(request,await analyze(request));
    const source=path.join(process.env.CODEFLOW_BROWSER_FIXTURE!,'app/page.tsx');
    const original=readFileSync(source,'utf8');
    const longStatus=`confirmed-${'important-status-'.repeat(32)}`;
    try {
      writeFileSync(source,original.replace("setStatus('completed')",`setStatus('${longStatus}')`));
      await page.setViewportSize({width:390,height:844});
      const current=await openPurposeView(page,request,await analyze(request));
      const step=[...current.semanticMap.steps].reverse().find((item:any)=>item.name.includes('setStatus'));
      expect(step).toBeTruthy();
      await openPurposeStep(page,current,step.stepId);
      await compare(page,baseline);
      await containedSources(page);
      const currentSource=page.getByLabel('현재 코드',{exact:true});
      expect(await currentSource.evaluate(element=>element.scrollWidth > element.clientWidth)).toBe(true);
      await currentSource.evaluate(element=>{element.scrollLeft=element.scrollWidth;});
      expect(await currentSource.evaluate(element=>element.scrollLeft > 0)).toBe(true);
      await page.setViewportSize({width:195,height:422});
      await containedSources(page);
    } finally {writeFileSync(source,original);}
  });

  test('home entry remains readable at a 200 percent zoom equivalent width', async ({page}) => {
    await page.setViewportSize({width:195,height:422});
    await page.goto('/?token=testtoken');
    await expect(page.getByRole('region',{name:'FlowView 시작',exact:true})).toBeVisible();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await expect(page.locator('#query-input')).toBeVisible();
    await expect(page.locator('#request-submit')).toBeVisible();
  });

  test('resolved flow displays request region with entry point evidence and limitations', async ({page,request}) => {
    const query='결제 진행';
    const response=await request.get(`/api/task/view?request=${encodeURIComponent(query)}&entrySymbol=${encodeURIComponent(entry)}`,{headers});
    expect(response.ok()).toBe(true);
    const analysis=await response.json();
    expect(analysis.flowResolution.status).toBe('resolved');
    expect(analysis.flowResolution.rawRequest).toBe(query);
    expect(analysis.flowResolution.evidence[0].sourceAnchor).toEqual(analysis.semanticMap.steps[0].anchor);
    await openPurposeView(page,request,analysis);
    const region=page.getByRole('region',{name:'요청 영역',exact:true});
    await expect(region).toContainText('요청 흐름');
    await expect(region).toContainText('진입점 확인');
    await expect(region).toContainText(query);
    await region.locator('summary').click();
    await expect(region).toContainText(entry);
    await expect(region).toContainText('진입점:');
    for (const evidence of analysis.flowResolution.evidence) {
      await expect(region).toContainText(evidence.description);
      await expect(region).toContainText(`${evidence.sourceAnchor.repoRelativePath}#${evidence.sourceAnchor.enclosingSymbolPath}`);
    }
    await expect(region).toContainText('요청과 코드의 의미 연결은 선택자의 판단');
    if (analysis.semanticMap.unknowns.length || analysis.flowSequence.summaryLimitations.length) await expect(region).toContainText('분석 한계:');
  });
}
