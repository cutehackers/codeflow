import { spawnSync } from 'node:child_process';
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {mkdirSync, readFileSync, unlinkSync, writeFileSync} from 'node:fs';
import path from 'node:path';

import {openPurposeView, openPurposeStep, verifyPurposeSources} from './purpose-flowview';
import {registerPurposeLifecycleTests} from './purpose-flowview-lifecycle';

const entry = 'app/page.tsx#HomePage.onQuickCheckout';
const headers = {'X-CodeFlow-Token':'testtoken'};

async function expectSelectedSource(page: Page, context: {displayedLines: Array<{selection?: {text: string}}>}) {
  const selectedText = context.displayedLines.flatMap(line => line.selection ? [line.selection.text] : []);
  expect(selectedText.length).toBeGreaterThan(0);
  expect(selectedText.every(text => text.length > 0)).toBe(true);
  await expect(page.locator('#code-flow > article > pre mark, #code-flow .compare-grid > div:last-child mark')).toHaveText(selectedText);
}

test('empty home, intermediate analysis, purpose navigation and immutable reopening', async ({page, request}) => {
  await page.goto('/?token=testtoken');
  await expect(page.getByText('첫 번째 흐름을 열어보세요.')).toBeVisible();
  await page.locator('#query-input').fill(entry);
  await page.locator('#request-submit').click();
  await expect(page.getByRole('button',{name:'중간 결과 확인',exact:true})).toBeVisible();
  await expect(page.locator('#requested-flow-overview')).toHaveCount(0);
  await page.getByRole('button',{name:'중간 결과 확인',exact:true}).click();
  await expect(page.getByRole('status',{name:'중간 결과',exact:true})).toBeVisible();
  const analysisId = new URL(page.url()).searchParams.get('viewId');
  const saved = await (await request.get(`/api/view?viewId=${analysisId}`,{headers})).json();
  expect(saved.presentationStatus).toBe('intermediate');
  const view = await openPurposeView(page,request,saved);
  const segment = view.requestedFlowOverview.segments.at(-1);
  await page.locator(`[data-segment="${segment.segmentId}"]`).click();
  const viewURL = page.url();
  let analyses = 0;
  page.on('request',req=>{if(new URL(req.url()).pathname==='/api/task/view') analyses++;});
  await page.reload();
  await expect(page.locator('#requested-flow-overview')).toBeVisible();
  await page.getByRole('button',{name:'CodeFlow',exact:true}).click();
  await page.getByRole('button',{name:new RegExp(view.semanticMap.summary.requested)}).first().click();
  await expect(page).toHaveURL(viewURL);
  expect(analyses).toBe(0);
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);

});

test('study layout reads the same saved code with more horizontal space', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  const view = await openPurposeView(page,request,saved);
  const stepID = view.requestedFlowOverview.segments[0].stepIds[0];
  await page.setViewportSize({width:1440,height:900});
  await openPurposeStep(page,view,stepID);
  const normalWidth = (await page.locator('#selected-flow').boundingBox())!.width;
  await page.goto(`/?token=testtoken&viewId=${view.viewId}&studyLayout=code-focused`);
  await openPurposeStep(page,view,stepID);
  expect((await page.locator('#selected-flow').boundingBox())!.width).toBeGreaterThan(normalWidth);
  const code = (await page.locator('#selected-flow').boundingBox())!;
  expect((await page.locator('main.layout aside').boundingBox())!.y).toBeGreaterThanOrEqual(code.y+code.height);
  await page.reload();
  await expect(page).toHaveURL(/studyLayout=code-focused/);
  await openPurposeStep(page,view,stepID);

});

test('a selected candidate keeps the original natural-language request', async ({page, request}) => {
  const rawRequest = '사용자가 빠른 결제를 완료하면 무엇이 실행되나';
  await page.goto('/?token=testtoken');
  const before = await (await request.get('/api/views', {headers})).json();
  await page.locator('#query-input').fill(rawRequest);
  await page.locator('#request-submit').click();
  const candidate = page.locator('.candidate-choice').filter({hasText: entry});
  await expect(candidate).toBeVisible();
  await expect(candidate.getByRole('button', {name: '이 진입점 선택'})).toBeDisabled();
  const unresolved = await (await request.get('/api/views', {headers})).json();
  expect(unresolved.views.length).toBe(before.views.length);
  await candidate.getByRole('button', {name: '코드 미리 보기'}).click();
  await expect(page.getByLabel('후보 진입 코드')).toContainText('onQuickCheckout');
  await candidate.getByRole('button', {name: '이 진입점 선택'}).click();
  await page.getByRole('button',{name:'중간 결과 확인',exact:true}).click();
  await expect(page.getByRole('status',{name:'중간 결과',exact:true})).toBeVisible();
  const viewId = new URL(page.url()).searchParams.get('viewId');
  expect(viewId).toBeTruthy();
  const saved = await (await request.get(`/api/view?viewId=${viewId}`, {headers})).json();
  expect(saved.flowResolution.rawRequest).toBe(rawRequest);
  expect(saved.flowResolution.entrySymbolPath).toBe(entry);
  await verifyPurposeSources(page,request,saved);
});

test('listing failure and invalid saved view never substitute samples', async ({page}) => {
  await page.route('**/api/views',route=>route.fulfill({status:500,json:{message:'list unavailable'}}));
  await page.goto('/?token=testtoken');
  await expect(page.getByRole('alert')).toContainText('목록을 불러오지 못했습니다');
  await expect(page.getByText('첫 번째 흐름을 열어보세요.')).toHaveCount(0);
  await page.goto('/?token=testtoken&viewId=missing');
  await expect(page.getByRole('status')).toContainText('흐름을 열지 못했습니다');
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toHaveCount(0);
});

test('home shows the latest analysis for each flow before older duplicates', async ({page}) => {
  await page.route('**/api/views', route => route.fulfill({json:{views:[
    {viewId:'latest',title:'Order checkout',savedAt:'2026-09-21T01:00:00Z'},
    {viewId:'older',title:'Order checkout',savedAt:'2026-09-20T01:00:00Z'},
    {viewId:'other',title:'Account sign in',savedAt:'2026-09-19T01:00:00Z'},
  ]}}));
  await page.goto('/?token=testtoken');
  await expect(page.getByRole('button', {name:/Order checkout/})).toHaveCount(1);
  await expect(page.getByRole('button', {name:'이전 분석 1개 보기'})).toBeVisible();
  await page.getByRole('button', {name:'이전 분석 1개 보기'}).click();
  await expect(page.getByRole('button', {name:/Order checkout/})).toHaveCount(2);
  await expect(page.getByRole('button', {name:'최근 흐름만 보기'})).toBeVisible();
});

test('browser back restores the selected purpose segment and its expanded source', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  const view = await openPurposeView(page,request,saved);
  const segment = view.requestedFlowOverview.segments.at(-1);
  await page.locator(`[data-segment="${segment.segmentId}"]`).click();
  await page.locator('.block-source-detail > summary').first().click();
  await page.getByRole('button',{name:'CodeFlow',exact:true}).click();
  await page.goBack();
  await expect(page.locator(`[data-segment="${segment.segmentId}"]`)).toHaveAttribute('aria-pressed','true');
  await expect(page.locator('.block-source-detail').first()).toHaveAttribute('open','');

});

test('keyboard-only reading selects a purpose segment and opens its source detail', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  const view = await openPurposeView(page,request,saved);
  const segment = page.locator('[data-segment]').last();
  await segment.focus();await segment.press('Enter');
  await expect(segment).toHaveAttribute('aria-pressed','true');
  const disclosure = page.locator('.block-source-detail > summary').first();
  await disclosure.focus();await disclosure.press('Space');
  const detail = page.locator('[data-block-detail]').first();
  await detail.focus();await detail.press('Enter');
  await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
  const close = page.getByRole('button',{name:'상세 닫기',exact:true});
  await close.focus();await close.press('Enter');
  await expect(detail).toBeFocused();
  await expect(page.locator('#code-flow')).toHaveCount(0);

});

test('requested handler preserves preparation, awaited operation and decision evidence', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  const frames = saved.flowSequence.frames;
  expect(frames).toHaveLength(3);
  expect(frames.map((frame: {title: string}) => frame.title)).toEqual([
    'orderPayload 준비',
    'api.orders.checkout(orderPayload)',
    'if (response.success)',
  ]);
  expect(frames.map((frame: {stepRefs: string[]}) => frame.stepRefs.length)).toEqual([6, 4, 2]);

  await verifyPurposeSources(page,request,saved);
});

test('standalone TypeScript order flow opens every saved source step', async ({page, request}) => {
  const functionEntry = 'services/orderService.ts#processOrder';
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(functionEntry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  expect(saved.semanticMap.steps.length).toBeGreaterThan(1);
  expect(saved.flowSequence.frames.length).toBeGreaterThan(0);
    await verifyPurposeSources(page, request, saved);
});

test('TypeScript local result calculation remains readable in purpose source detail', async ({page, request}) => {
  const sourcePath = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'local-result.ts');
  writeFileSync(sourcePath, 'export function totalPrice(price: number, count: number) {\n  const units = count;\n  const total = price * units;\n  return total;\n}\n');
  try {
    const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent('local-result.ts#totalPrice')}`, {headers});
    expect(response.ok(), await response.text()).toBe(true);
    const saved = await response.json();
    const preparation = saved.semanticMap.steps.find((step: {name: string}) => step.name === 'const units = count;');
    const calculation = saved.semanticMap.steps.find((step: {name: string}) => step.name === 'const total = price * units;');
    expect(preparation).toBeTruthy();
    expect(calculation).toBeTruthy();
    expect(preparation.valueRole).toBe('local_binding');
    expect(calculation.valueRole).toBe('local_binding');
    expect(saved.flowContexts[calculation.stepId].precision).toBe('exact');
    const frame = saved.flowSequence.frames.find((item: {stepRefs: string[]}) => item.stepRefs.includes(calculation.stepId));
    expect(frame).toBeTruthy();
    expect(frame.stepRefs).toEqual([preparation.stepId, calculation.stepId]);
    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(sourcePath);
  }
});

test('TSX event flow opens the selected source for every saved step', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
    await verifyPurposeSources(page, request, saved);
});

test('long source path stays inside a narrow viewport while code scrolls locally', async ({page, request}) => {
  const relativePath = 'app/checkout_flow_with_a_descriptive_repository_relative_path_for_mobile_source_navigation.ts';
  const sourcePath = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, relativePath);
  writeFileSync(sourcePath, 'export function processMobileCheckout(amount: number) { const total = calculateMobileCheckout(amount); unresolvedDynamicTarget.process(total); return total; }\nfunction calculateMobileCheckout(amount: number) { return amount + 1; }\n');
  try {
    const sourceEntry = `${relativePath}#processMobileCheckout`;
    const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(sourceEntry)}`, {headers});
    expect(response.ok(), await response.text()).toBe(true);
    const saved = await response.json();
    expect(saved.semanticMap.edges.some((edge: {kind: string}) => edge.kind === 'resolved_cross_file')).toBe(true);
    expect(saved.semanticMap.unknowns.length).toBeGreaterThan(0);
    await page.setViewportSize({width:390, height:844});
    const view = await openPurposeView(page,request,saved);
    await openPurposeStep(page,view,view.requestedFlowOverview.segments[0].stepIds[0]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.getByRole('button', {name:'코드 더 보기', exact:true}).click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    expect(await page.locator('#code-flow .source').evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true);
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    await page.locator('#boundaries summary').click();
    await expect(page.locator('#boundaries')).toContainText('확인하지 못한 연결');
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  } finally {
    unlinkSync(sourcePath);
  }
});

test('Go call result opens the callee and every saved source step in a mixed repository', async ({page, request}) => {
  const goEntry = 'checkout.go#Service.Checkout';
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(goEntry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  expect(saved.flowResolution.entrySymbolPath).toBe(goEntry);
  const callee = saved.semanticMap.steps.find((step: {anchor: {enclosingSymbolPath: string}}) =>
    step.anchor.enclosingSymbolPath === 'calculateFee');
  expect(callee).toBeTruthy();
    await verifyPurposeSources(page, request, saved);
});

test('natural Go request in a mixed repository requires source preview and preserves the choice', async ({page, request}) => {
  const rawRequest = 'ProcessCheckout';
  const goEntry = 'checkout.go#Service.ProcessCheckout';
  await page.goto('/?token=testtoken');
  await page.locator('#query-input').fill(rawRequest);
  await page.locator('#request-submit').click();
  const candidate = page.locator('.candidate-choice').filter({hasText: goEntry});
  await expect(candidate).toBeVisible();
  await expect(candidate.getByRole('button', {name: '이 진입점 선택'})).toBeDisabled();
  await candidate.getByRole('button', {name: '코드 미리 보기'}).click();
  await expect(page.getByLabel('후보 진입 코드')).toContainText('ProcessCheckout');
  await candidate.getByRole('button', {name: '이 진입점 선택'}).click();
  await page.getByRole('button',{name:'중간 결과 확인',exact:true}).click();
  await expect(page.getByRole('status',{name:'중간 결과',exact:true})).toBeVisible();
  const viewId = new URL(page.url()).searchParams.get('viewId');
  const saved = await (await request.get(`/api/view?viewId=${viewId}`, {headers})).json();
  expect(saved.flowResolution.rawRequest).toBe(rawRequest);
  expect(saved.flowResolution.entrySymbolPath).toBe(goEntry);
  await verifyPurposeSources(page,request,saved);
});

test('explicit Go implementation outside harvest candidates opens its exact code', async ({page, request}) => {
  const rawRequest = 'How is the checkout fee calculated?';
  const goEntry = 'checkout.go#calculateFee';
  const response = await request.get(`/api/task/view?query=${encodeURIComponent(rawRequest)}&entrySymbol=${encodeURIComponent(goEntry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  expect(saved.flowResolution.rawRequest).toBe(rawRequest);
  expect(saved.flowResolution.entrySymbolPath).toBe(goEntry);
  expect(saved.flowContexts).toBeTruthy();
    await verifyPurposeSources(page, request, saved);
});

test('curated Go reading path retains its intermediate source step', async ({page, request}) => {
  const sourcePath = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'summary.go');
  writeFileSync(sourcePath, 'package checkout\nfunc readSummary(value int) int {\n  result := value\n  if result < 0 {\n    result = -result\n  }\n  return result\n}\n');
  try {
    const rawRequest = 'How is the summary normalized?';
    const entrySymbol = 'summary.go#readSummary';
    const analysisResponse = await request.get(`/api/task/view?query=${encodeURIComponent(rawRequest)}&entrySymbol=${encodeURIComponent(entrySymbol)}`, {headers});
    expect(analysisResponse.ok(), await analysisResponse.text()).toBe(true);
    const analysis = await analysisResponse.json();
    const steps = analysis.semanticMap.steps as Array<{stepId: string; kind: string}>;
    expect(steps.map(step => step.kind)).toEqual(['mutation', 'guard', 'mutation', 'return']);
    const edges = analysis.semanticMap.edges as Array<{fromStepId: string; toStepId: string; conditions?: Array<{outcome: string}>}>;
    const edgeIndex = (from: string, to: string) => edges.findIndex(edge => edge.fromStepId === from && edge.toStepId === to);
    const [entry, decision, detail, outcome] = steps;
    const entryEdge = edgeIndex(entry.stepId, decision.stepId);
    const detailEdge = edgeIndex(decision.stepId, detail.stepId);
    const outcomeEdge = edgeIndex(detail.stepId, outcome.stepId);
    expect([entryEdge, detailEdge, outcomeEdge].every(index => index >= 0)).toBe(true);
    expect(edges[detailEdge].conditions?.[0]?.outcome).toBe('truthy');

    const curatedResponse = await request.post('/api/view/reading-path', {
      headers,
      data: {
        viewId: analysis.viewId,
        selections: [
          {stepId: entry.stepId, role: 'entry', requestPart: 'summary', inclusionReason: '함수의 입력을 읽는다'},
          {stepId: decision.stepId, role: 'decision', requestPart: 'normalized', inclusionReason: '값을 바꿀 조건을 확인한다'},
          {stepId: outcome.stepId, role: 'outcome', requestPart: 'summary', inclusionReason: '계산 결과를 반환한다'},
        ],
        relations: [
          {fromStepId: entry.stepId, toStepId: decision.stepId, edgeIndexes: [entryEdge]},
          {fromStepId: decision.stepId, toStepId: outcome.stepId, edgeIndexes: [detailEdge, outcomeEdge]},
        ],
      },
    });
    expect(curatedResponse.ok(), await curatedResponse.text()).toBe(true);
    const curated = await curatedResponse.json();
    expect(curated.codeReadingPath.nodes).toHaveLength(3);
    const restored = await (await request.get(`/api/view?viewId=${curated.viewId}`, {headers})).json();
    expect(restored.codeReadingPath).toEqual(curated.codeReadingPath);
    for (const format of ['full','compact']) {
      const rpc = {jsonrpc:'2.0',id:1,method:'tools/call',params:{name:'get_flow_payload',arguments:{target:process.env.CODEFLOW_BROWSER_FIXTURE,viewId:curated.viewId,format}}};
      const executable = process.env.CODEFLOW_BROWSER_EXECUTABLE || path.resolve(__dirname,'../../../bin/codeflow');
      const result = spawnSync(executable,['mcp',process.env.CODEFLOW_BROWSER_FIXTURE!],{
        cwd:path.resolve(__dirname,'../../..'),input:JSON.stringify(rpc)+'\n',encoding:'utf8',timeout:30000,maxBuffer:5*1024*1024,
      });
      expect(result.status,result.stderr).toBe(0);
      const response = JSON.parse(result.stdout);
      expect(response.result.isError).not.toBe(true);
      const payload = JSON.parse(response.result.content[0].text);
      expect(payload.codeReadingPath).toEqual(curated.codeReadingPath);
      expect(payload.codeReadingPathLimitation).toBe(restored.codeReadingPathLimitation);
    }


    expect(restored.presentationStatus).toBe('intermediate');
    await page.goto(`/?token=testtoken&viewId=${curated.viewId}`);
    await expect(page.getByRole('status',{name:'중간 결과',exact:true})).toBeVisible();
    await expect(page.locator('#requested-flow-overview')).toHaveCount(0);
    await verifyPurposeSources(page,request,restored);
  } finally {
    unlinkSync(sourcePath);
  }
});


test('purpose overview and source detail restore code, scroll, focus and per-view comparison', async ({page, request}) => {
  const fixture = process.env.CODEFLOW_BROWSER_FIXTURE!;
  const sourcePath = path.join(fixture, 'overview-flow.ts');
  const helperPath = path.join(fixture, 'overview-effects.ts');
  const indexes = Array.from({length:18}, (_, index) => index + 1);
  writeFileSync(helperPath, indexes.map(index => `export function applyChange${index}(value: number) { return value + ${index}${' + 0'.repeat(80)}; }`).join('\n'));
  writeFileSync(sourcePath, [
    `import {${indexes.map(index => `applyChange${index}`).join(',')}} from './overview-effects';`,
    'export function processOverview(amount: number) {',
    '  const input = amount;',
    '  let total = input;',
    ...indexes.map(index => `  total = applyChange${index}(total);`),
    '  return total;',
    '}',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?query=How+does+the+amount+change%3F&entrySymbol=overview-flow.ts%23processOverview', {headers});
    expect(response.ok(), await response.text()).toBe(true);
    const analysis = await response.json();
    const steps = analysis.semanticMap.steps;
    const edges = analysis.semanticMap.edges;
    const start = steps[0].stepId;
    const outcome = steps.find((step: any) => step.kind === 'return' && step.anchor.repoRelativePath === 'overview-flow.ts').stepId;
    const findPath = (from: string, seen = new Set<string>()): number[] | null => {
      if (from === outcome) return [];
      if (seen.has(from)) return null;
      const visited = new Set([...seen, from]);
      for (const [index, edge] of edges.entries()) {
        if (edge.fromStepId !== from || edge.resolutionStatus !== 'resolved' || edge.kind === 'callback_argument') continue;
        const rest = findPath(edge.toStepId, visited);
        if (rest) return [index, ...rest];
      }
      return null;
    };
    const route = findPath(start);
    expect(route).toBeTruthy();
    const routeSteps = [start, ...route!.map(index => edges[index].toStepId)];
    const points = routeSteps.filter(id => steps.find((step: any) => step.stepId === id).anchor.repoRelativePath === 'overview-flow.ts');
    expect(points.length).toBeGreaterThan(15);
    const compactResponse = await request.post('/api/view/reading-path', {headers, data:{
      viewId:analysis.viewId,
      selections:[
        {stepId:start,role:'entry',requestPart:'amount',inclusionReason:'입력 값을 읽는다'},
        {stepId:outcome,role:'outcome',requestPart:'amount',inclusionReason:'누적한 값을 반환한다'},
      ],
      relations:[{fromStepId:start,toStepId:outcome,edgeIndexes:route}],
    }});
    expect(compactResponse.ok(), await compactResponse.text()).toBe(true);
    const compact = await compactResponse.json();
    expect(compact.presentationStatus).toBe('intermediate');
    const view = await openPurposeView(page,request,analysis);
    const otherFile = steps.find((step: any)=>step.anchor.repoRelativePath==='overview-effects.ts');
    await openPurposeStep(page,view,otherFile.stepId);
    const source = page.locator('#code-flow .source');
    await source.evaluate(el=>{el.scrollLeft=170;});
    const sourceLeft = await source.evaluate(el=>el.scrollLeft);
    expect(sourceLeft).toBeGreaterThan(0);
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    const origin = page.locator(`[data-block-detail="${otherFile.stepId}"]`).first();
    await expect(origin).toBeFocused();
    const position = await page.evaluate(()=>window.scrollY);
    await origin.click();
    await expectSelectedSource(page,view.flowContexts[otherFile.stepId]);
    await expect.poll(()=>page.locator('#source-current').evaluate(el=>el.scrollLeft)).toBe(sourceLeft);
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    await expect(origin).toBeFocused();
    await expect.poll(()=>page.evaluate(()=>window.scrollY)).toBe(position);
    await page.evaluate(async id=>await window.openFlowView(id),analysis.viewId);
    await page.evaluate(async id=>await window.openFlowView(id),view.viewId);
    await expect(page.locator('[data-segment][aria-pressed="true"]')).toContainText(otherFile.anchor.enclosingSymbolPath);
    await openPurposeStep(page,view,otherFile.stepId);
    await page.locator('#baseline').selectOption(analysis.viewId);
    await page.locator('#compare').click();
    await expect(page.locator('.compare-grid')).toHaveCount(1);
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    await origin.click();
    await expect(page.locator('.compare-grid')).toHaveCount(1);
    expect(await page.evaluate(()=>window.flowStore.baseline?.viewId)).toBe(analysis.viewId);
  } finally {
    unlinkSync(sourcePath);
    unlinkSync(helperPath);
  }
});

test('unreadable detail shows its source boundary without a false code highlight', async ({page, request}) => {
  const sourcePath = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'hooks/useCart.ts');
  const original = readFileSync(sourcePath, 'utf8');
  try {
    writeFileSync(sourcePath, original.replace(
      'sum + item.price * item.quantity, 0',
      '({ apiKey: sum + item.price * item.quantity }).apiKey, 0'
    ));
    const response = await request.get(`/api/task/view?query=quick+checkout&entrySymbol=${encodeURIComponent(entry)}`, {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const detail = saved.semanticMap.steps.find((step: {name: string}) => step.name.startsWith('cart.reduce('));
    expect(detail).toBeTruthy();
    const frame = saved.flowSequence.frames.find((item: {stepRefs: string[]}) => item.stepRefs.includes(detail.stepId));
    expect(frame).toBeTruthy();
    expect(saved.flowSequence.summaryLimitations.some((item: {code: string; frameRefs: string[]}) =>
      item.code === 'source_code_unavailable' && item.frameRefs.includes(frame.frameID))).toBe(true);

    expect(saved.presentationStatus).toBe('intermediate');
    const unsafeOverview = await request.post('/api/view/overview',{headers,data:{viewId:saved.viewId,segments:[{segmentId:'cart-price',purpose:'장바구니 금액 계산',inclusionReason:'결제 금액을 계산한다',stepIds:[detail.stepId],blocks:[{stepIds:[detail.stepId]}]}],relations:[]}});
    expect(unsafeOverview.ok()).toBe(false);
    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await expect(page.getByRole('status',{name:'중간 결과',exact:true})).toBeVisible();
    await expect(page.locator('#requested-flow-overview')).toHaveCount(0);
    await expect(page.locator('.source mark')).toHaveCount(0);
    expect(JSON.stringify(saved)).not.toContain('sk-1234567890abcdef');
  } finally {
    writeFileSync(sourcePath, original);
  }
});

test('unreadable entry failure keeps the prior purpose view and explains the target', async ({page, request}) => {
  const baselineResponse = await request.get(`/api/task/view?query=quick+checkout&entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(baselineResponse.ok()).toBe(true);
  const baseline = await baselineResponse.json();
  const sourcePath = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app/page.tsx');
  const original = readFileSync(sourcePath, 'utf8');
  try {
    const view = await openPurposeView(page,request,baseline);
    await openPurposeStep(page,view,view.requestedFlowOverview.segments[0].stepIds[0]);
    writeFileSync(sourcePath, original.replace("setStatus('processing');", "setStatus({ apiKey: 'sk-1234567890abcdef' });"));
    await page.locator('#reanalyze').click();
    await expect(page.getByRole('status').filter({hasText:'대상 app/page.tsx#HomePage.onQuickCheckout'})).toBeVisible();
    await expect(page.getByRole('status').filter({hasText:'다시 분석하세요'})).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`viewId=${view.viewId}`));
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
  } finally {
    writeFileSync(sourcePath, original);
  }
});

test('saved FlowView core reading surface has no serious accessibility violations', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  await openPurposeView(page,request,saved);

  const scan = await new AxeBuilder({page})
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();
  expect(scan.violations.filter(violation => violation.impact === 'critical' || violation.impact === 'serious')).toEqual([]);
});

test('home and expanded purpose source have no serious accessibility violations', async ({page, request}) => {
  await page.goto('/?token=testtoken');
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toHaveCount(0);
  const homeScan = await new AxeBuilder({page})
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();
  expect(homeScan.violations.filter(violation => violation.impact === 'critical' || violation.impact === 'serious')).toEqual([]);

  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  await openPurposeView(page,request,saved);
  await page.locator('.block-source-detail > summary').first().click();
  await expect(page.locator('#requested-flow-blocks .source')).toBeVisible();
  const processScan = await new AxeBuilder({page})
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();
  expect(processScan.violations.filter(violation => violation.impact === 'critical' || violation.impact === 'serious')).toEqual([]);
});

test('purpose navigation uses the enclosing implementation for source-only state changes', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  const mutation = saved.semanticMap.steps.find((step: any) => step.kind === 'mutation' && step.anchor?.enclosingSymbolPath);
  expect(mutation).toBeTruthy();
  const frame = saved.flowSequence.frames.find((candidate: any) => candidate.primaryStepRef === mutation.stepId);
  expect(frame).toBeTruthy();

  const view = await openPurposeView(page,request,saved);
  const segment = view.requestedFlowOverview.segments.find((item: any)=>item.stepIds.includes(mutation.stepId));
  await expect(page.locator(`[data-segment="${segment.segmentId}"]`)).toContainText(mutation.anchor.enclosingSymbolPath);
  await expect(page.locator(`[data-segment="${segment.segmentId}"]`)).not.toContainText(mutation.name);
  await openPurposeStep(page,view,mutation.stepId);

});

test('cross-file process source opens every verified step in its purpose segment', async ({page, request}) => {
  const fixtureRoot = process.env.CODEFLOW_BROWSER_FIXTURE!;
  const entrySource = path.join(fixtureRoot, 'app', 'cross-file-flow.ts');
  const helperSource = path.join(fixtureRoot, 'app', 'payment-helper.ts');
  writeFileSync(entrySource, [
    "import { evaluatePayment } from './payment-helper';",
    'export async function checkoutCrossFile(request: any) {',
    '  return await evaluatePayment(request);',
    '}',
    '',
  ].join('\n'));
  writeFileSync(helperSource, [
    'export async function evaluatePayment(request: any) {',
    "  if (!request.allowed) return 'denied';",
    "  return 'approved';",
    '}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fcross-file-flow.ts%23checkoutCrossFile', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const helperStep = saved.semanticMap.steps.find((step: {stepId: string; anchor: {repoRelativePath: string}}) =>
      step.anchor.repoRelativePath === 'app/payment-helper.ts'
    );
    const parentFrame = saved.flowSequence.frames.find((frame: {frameID: string; stepRefs: string[]}) =>
      helperStep && frame.stepRefs.includes(helperStep.stepId)
    );
    expect(helperStep).toBeTruthy();
    expect(parentFrame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(entrySource);
    unlinkSync(helperSource);
  }
});

test('Dart callback delivery opens callback code without claiming it ran', async ({page, request}) => {
  const fixtureRoot = process.env.CODEFLOW_BROWSER_FIXTURE!;
  const sourcePath = path.join(fixtureRoot, 'lib', 'callback.dart');
  mkdirSync(path.dirname(sourcePath), {recursive:true});
  const source = [
    'class Service {',
    '  void run() {',
    '    final timer = Timer(const Duration(milliseconds: 10), () {',
    '      if (done) return;',
    '    });',
    '    final selected = <int>[1, 2].where((item) => item > 1).firstOrNull;',
    '    final finished = true;',
    '  }',
    '}',
    ''
  ].join('\n');
  writeFileSync(sourcePath, source);
  try {
    const response = await request.get('/api/task/view?entrySymbol=lib%2Fcallback.dart%23Service.run', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const relation = saved.semanticMap.edges.find((edge: {kind: string}) => edge.kind === 'callback_argument');
    expect(relation).toBeTruthy();
    const callbackStep = saved.semanticMap.steps.find((step: {stepId: string}) => step.stepId === relation.toStepId);
    expect(callbackStep).toBeTruthy();
    expect(saved.flowContexts[callbackStep.stepId].precision).toBe('exact');
    const callbackFrame = saved.flowSequence.frames.find((frame: {stepRefs: string[]}) => frame.stepRefs.includes(callbackStep.stepId));
    expect(callbackFrame.status).toBe('partial');
    const arrowStep = saved.semanticMap.steps.find((step: {stepId: string; anchor: {byteRange: [number, number]}}) =>
      source.slice(...step.anchor.byteRange) === 'item > 1'
    );
    expect(arrowStep).toBeTruthy();
    expect(saved.flowContexts[arrowStep.stepId].precision).toBe('exact');
    const arrowFrame = saved.flowSequence.frames.find((frame: {stepRefs: string[]}) => frame.stepRefs.includes(arrowStep.stepId));
    expect(arrowFrame.status).toBe('partial');

    const view = await openPurposeView(page,request,saved);
    expect(view.requestedFlowOverview.segments.flatMap((segment: any)=>segment.stepIds)).not.toContain(callbackStep.stepId);
    await expect(page.locator('#requested-flow-overview')).toContainText('실행 여부 미확인');
    await openPurposeStep(page,view,relation.fromStepId);
    await page.locator('[data-navigation-focus^="outgoing:"]').filter({hasText:callbackStep.name}).first().click();
    await expectSelectedSource(page,view.flowContexts[callbackStep.stepId]);
    await expect(page.locator('#code-flow .source')).toContainText('if (done) return;');
    await expect(page.getByRole('region',{name:'요청 흐름 안의 전후 연결'}).getByRole('button',{name:/콜백 전달 · 실행 여부 미확인/})).toBeVisible();
    await page.getByRole('button',{name:'원래 장면 복귀',exact:true}).click();
    await expectSelectedSource(page,view.flowContexts[relation.fromStepId]);
    const arrowRelation = saved.semanticMap.edges.find((edge: any)=>edge.kind==='callback_argument' && edge.toStepId===arrowStep.stepId);
    expect(arrowRelation).toBeTruthy();
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    await openPurposeStep(page,view,arrowRelation.fromStepId);
    await page.locator('[data-navigation-focus^="outgoing:"]').filter({hasText:arrowStep.name}).first().click();
    await expectSelectedSource(page,view.flowContexts[arrowStep.stepId]);
    await expect(page.locator('#code-flow .source')).toContainText('item > 1');
  } finally {
    unlinkSync(sourcePath);
  }
});

test('exclusive branches and their shared continuation remain separately navigable', async ({page, request}) => {
  const fixtureRoot = process.env.CODEFLOW_BROWSER_FIXTURE!;
  const sources = new Map([
    ['payment-flow.ts', [
      "import { chargeCard } from './card';",
      "import { transferFunds } from './bank';",
      "import { receipt } from './receipt';",
      'export function checkoutPayment(card: boolean, amount: number) {',
      '  let payment;',
      '  if (card) { payment = chargeCard(amount); }',
      '  else { payment = transferFunds(amount); }',
      '  return receipt(payment);',
      '}',
      '',
    ].join('\n')],
    ['card.ts', "export function chargeCard(amount: number) { return { method: 'card', amount }; }\n"],
    ['bank.ts', "export function transferFunds(amount: number) { return { method: 'bank', amount }; }\n"],
    ['receipt.ts', 'export function receipt(payment: unknown) { return { payment }; }\n'],
  ]);
  for (const [file, source] of sources) writeFileSync(path.join(fixtureRoot, 'app', file), source);
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fpayment-flow.ts%23checkoutPayment', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const findStep = (name: string) => saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === name);
    const decision = findStep('if (card)');
    const cardCall = findStep('chargeCard(amount)');
    const bankCall = findStep('transferFunds(amount)');
    const receiptCall = findStep('receipt(payment)');
    expect(decision && cardCall && bankCall && receiptCall).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({fromStepId: decision.stepId, toStepId: cardCall.stepId, conditions: [expect.objectContaining({outcome:'truthy'})]}),
      expect.objectContaining({fromStepId: decision.stepId, toStepId: bankCall.stepId, conditions: [expect.objectContaining({outcome:'falsy'})]}),
    ]));
    const memberships = saved.flowSequence.frames.flatMap((frame: {stepRefs: string[]}) => frame.stepRefs);
    expect(memberships.filter((stepID: string) => stepID === receiptCall.stepId)).toHaveLength(1);

    const decisionFrame = saved.flowSequence.frames.find((frame: {frameID: string; stepRefs: string[]}) => frame.stepRefs.includes(decision.stepId));
    const receiptFrame = saved.flowSequence.frames.find((frame: {frameID: string; stepRefs: string[]}) => frame.stepRefs.includes(receiptCall.stepId));
    expect(decisionFrame && receiptFrame).toBeTruthy();
    await verifyPurposeSources(page, request, saved);
  } finally {
    for (const file of sources.keys()) unlinkSync(path.join(fixtureRoot, 'app', file));
  }
});

test('awaited async throw links its source step to caller recovery', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'awaited-throw.ts');
  writeFileSync(source, [
    'export async function checkoutAwaitedThrow() {',
    '  try { await charge(); complete(); } catch { recover(); } after();',
    '}',
    'async function charge() { throw failure; }',
    'function complete() {}',
    'function recover() {}',
    'function after() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fawaited-throw.ts%23checkoutAwaitedThrow', {headers});
    if (!response.ok()) throw new Error(await response.text());
    const saved = await response.json();
    const thrown = saved.semanticMap.steps.find((step: {stepId: string; name: string; anchor: {enclosingSymbolPath: string}}) =>
      step.name === 'throw failure;' && step.anchor.enclosingSymbolPath === 'charge'
    );
    const recovery = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'recover()');
    expect(thrown && recovery).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'failure', fromStepId:thrown.stepId, toStepId:recovery.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(thrown.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});

test('async caller catches a direct synchronous callee throw', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'async-caller-throw.ts');
  writeFileSync(source, [
    'export async function checkoutAsyncCallerThrow() {',
    '  try { charge(); complete(); } catch { recover(); } after();',
    '}',
    'function charge() { throw failure; }',
    'function complete() {}',
    'function recover() {}',
    'function after() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fasync-caller-throw.ts%23checkoutAsyncCallerThrow', {headers});
    if (!response.ok()) throw new Error(await response.text());
    const saved = await response.json();
    const thrown = saved.semanticMap.steps.find((step: {stepId: string; name: string; anchor: {enclosingSymbolPath: string}}) =>
      step.name === 'throw failure;' && step.anchor.enclosingSymbolPath === 'charge'
    );
    const recovery = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'recover()');
    expect(thrown && recovery).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'failure', fromStepId:thrown.stepId, toStepId:recovery.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(thrown.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});

test('finalizer throw links its source step to caller recovery', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'finalizer-throw.ts');
  writeFileSync(source, [
    'export function checkoutFinalizerThrow() {',
    '  try { charge(); complete(); } catch { recover(); } after();',
    '}',
    'function charge() { try { prepare(); } finally { throw failure; } }',
    'function prepare() {}',
    'function complete() {}',
    'function recover() {}',
    'function after() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Ffinalizer-throw.ts%23checkoutFinalizerThrow', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const thrown = saved.semanticMap.steps.find((step: {stepId: string; name: string; anchor: {enclosingSymbolPath: string}}) =>
      step.name === 'throw failure;' && step.anchor.enclosingSymbolPath === 'charge'
    );
    const recovery = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'recover()');
    expect(thrown && recovery).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'failure', fromStepId:thrown.stepId, toStepId:recovery.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(thrown.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});

test('nested condition selections retain only reachable path choices', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'nested-conditions.ts');
  writeFileSync(source, [
    'export function reviewNestedConditions(first: boolean, second: boolean) {',
    '  if (first) {',
    '    if (second) accept(); else reject();',
    '  } else {',
    '    cancel();',
    '  }',
    '  after();',
    '}',
    'function accept() {}',
    'function reject() {}',
    'function cancel() {}',
    'function after() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fnested-conditions.ts%23reviewNestedConditions', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const first = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'if (first)');
    const second = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'if (second)');
    expect(first && second).toBeTruthy();
    const firstTruthy = JSON.stringify({stepId:first.stepId, outcome:'truthy'});
    const firstFalsy = JSON.stringify({stepId:first.stepId, outcome:'falsy'});
    const secondTruthy = JSON.stringify({stepId:second.stepId, outcome:'truthy'});

    const view = await openPurposeView(page,request,saved);
    await openPurposeStep(page,view,first.stepId);
    const add = page.locator('#condition-path-add');
    await add.selectOption(firstTruthy);
    await expect(page.getByRole('list', {name:'선택한 경로 조건'})).toHaveCount(1);
    await add.selectOption(secondTruthy);
    await expect(page.getByRole('list', {name:'선택한 경로 조건'})).toHaveCount(1);
    await expect(page.getByRole('list', {name:'선택한 경로 조건'}).locator('li')).toHaveCount(2);
    await expect(page.locator('#code-flow article.condition-match')).toHaveCount(1);

    const firstChoice = page.locator(`[data-navigation-focus="condition:${first.stepId}"]`);
    await firstChoice.selectOption('falsy');
    await expect(page.getByRole('list', {name:'선택한 경로 조건'}).locator('li')).toHaveCount(1);
    await page.getByRole('button', {name:'전체 조건 해제'}).click();
    await expect(page.getByRole('list', {name:'선택한 경로 조건'})).toHaveCount(0);
  } finally {
    unlinkSync(source);
  }
});

test('prevailing finally return is the only caller resumption', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'finally-return.ts');
  writeFileSync(source, [
    'export function checkoutFinallyReturn() {',
    '  let result;',
    '  result = finish();',
    '  record(result);',
    '}',
    'function finish() { try { return original(); } finally { return replacement(); } }',
    'function original() { return 1; }',
    'function replacement() { return 2; }',
    'function record(value: unknown) { return value; }',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Ffinally-return.ts%23checkoutFinallyReturn', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const finalReturn = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'return replacement();');
    const earlierReturn = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'return original();');
    const assignment = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'result = finish()');
    expect(finalReturn && earlierReturn && assignment).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'return', fromStepId:finalReturn.stepId, toStepId:assignment.stepId, resolutionStatus:'resolved'}),
    ]));
    expect(saved.semanticMap.edges).not.toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'return', fromStepId:earlierReturn.stepId, toStepId:assignment.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(finalReturn.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});


test('direct synchronous return resumes an async caller in FlowView', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'async-direct-return.ts');
  writeFileSync(source, [
    'export async function checkoutAsyncDirectReturn() {',
    '  const amount = price(true);',
    '  record(amount);',
    '}',
    'function price(flag: boolean) { if (flag) return 1; return 2; }',
    'function record(value: number) { return value; }',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fasync-direct-return.ts%23checkoutAsyncDirectReturn', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const firstReturn = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'return 1;');
    const secondReturn = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'return 2;');
    const assignment = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'amount = price(true)');
    expect(firstReturn && secondReturn && assignment).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'return', fromStepId:firstReturn.stepId, toStepId:assignment.stepId, resolutionStatus:'resolved'}),
      expect.objectContaining({kind:'return', fromStepId:secondReturn.stepId, toStepId:assignment.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(firstReturn.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});


test('async iterable wait, iteration, and exit remain distinct in FlowView', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'async-iterable-flow.ts');
  writeFileSync(source, [
    'export async function processAsyncItems(items: AsyncIterable<string>) {',
    '  for await (const item of items) { apply(item); }',
    '  finish();',
    '}',
    'function apply(item: string) {}',
    'function finish() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fasync-iterable-flow.ts%23processAsyncItems', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const wait = saved.semanticMap.steps.find((step: {stepId: string; name: string; kind: string}) => step.kind === 'await' && step.name === 'for await (const item of items)');
    const decision = saved.semanticMap.steps.find((step: {stepId: string; name: string; kind: string}) => step.kind === 'branch' && step.name === 'for await (const item of items)');
    const apply = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'apply(item)');
    const finish = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'finish()');
    expect(wait && decision && apply && finish).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'await_resume', fromStepId:wait.stepId, toStepId:decision.stepId, resolutionStatus:'resolved'}),
      expect.objectContaining({kind:'control_flow', fromStepId:decision.stepId, toStepId:apply.stepId, resolutionStatus:'resolved'}),
      expect.objectContaining({kind:'control_flow', fromStepId:decision.stepId, toStepId:finish.stepId, resolutionStatus:'resolved'}),
      expect.objectContaining({kind:'loop_back', fromStepId:apply.stepId, toStepId:wait.stepId, resolutionStatus:'resolved'}),
    ]));
    expect(saved.semanticMap.unknowns.some((unknown: {reason?: string}) => unknown.reason?.includes('비동기 반복자'))).toBe(true);
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(apply.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});


test('async iterable body rejection reaches its own recovery in FlowView', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'async-iterable-recovery.ts');
  writeFileSync(source, [
    'export async function processRecoveredAsyncItems(items: AsyncIterable<string>) {',
    '  for await (const item of items) { try { await apply(item); } catch { recover(item); } }',
    '  finish();',
    '}',
    'async function apply(item: string) {}',
    'function recover(item: string) {}',
    'function finish() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fasync-iterable-recovery.ts%23processRecoveredAsyncItems', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const rejectedWait = saved.semanticMap.steps.find((step: {stepId: string; name: string; kind: string}) => step.kind === 'await' && step.name === 'await apply(item)');
    const recovery = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'recover(item)');
    expect(rejectedWait && recovery).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'failure', fromStepId:rejectedWait.stepId, toStepId:recovery.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(rejectedWait.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});


test('finalizing async retry waits for cleanup before the next condition in FlowView', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'finalizing-retry.ts');
  writeFileSync(source, [
    'export async function retryUntilDone(done: boolean) {',
    '  while (!done) { try { await retry(); } finally { cleanup(); } }',
    '  finish();',
    '}',
    'async function retry() {}',
    'function cleanup() {}',
    'function finish() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Ffinalizing-retry.ts%23retryUntilDone', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const waiting = saved.semanticMap.steps.find((step: {stepId: string; name: string; kind: string}) => step.kind === 'await' && step.name === 'await retry()');
    const cleanup = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'cleanup()');
    const condition = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'while (!done)');
    expect(waiting && cleanup && condition).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'await_resume', fromStepId:waiting.stepId, toStepId:cleanup.stepId, resolutionStatus:'resolved'}),
      expect.objectContaining({kind:'loop_back', fromStepId:cleanup.stepId, toStepId:condition.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(cleanup.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});


test('recovered finalizing retry reaches cleanup before the next condition in FlowView', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'recovered-finalizing-retry.ts');
  writeFileSync(source, [
    'export async function retryUntilDone(done: boolean) {',
    '  while (!done) { try { await retry(); } catch { recover(); } finally { cleanup(); } }',
    '  finish();',
    '}',
    'async function retry() {}',
    'function recover() {}',
    'function cleanup() {}',
    'function finish() {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Frecovered-finalizing-retry.ts%23retryUntilDone', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const waiting = saved.semanticMap.steps.find((step: {stepId: string; name: string; kind: string}) => step.kind === 'await' && step.name === 'await retry()');
    const recovery = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'recover()');
    const cleanup = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'cleanup()');
    expect(waiting && recovery && cleanup).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'failure', fromStepId:waiting.stepId, toStepId:recovery.stepId, resolutionStatus:'resolved'}),
      expect.objectContaining({kind:'control_flow', fromStepId:recovery.stepId, toStepId:cleanup.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(recovery.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});


test('later await does not hide an earlier synchronous return in FlowView', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'async-return-before-wait.ts');
  writeFileSync(source, [
    'export async function checkoutAsyncReturnBeforeWait() {',
    '  const amount = price(true);',
    '  await signal();',
    '  record(amount);',
    '}',
    'function price(flag: boolean) { if (flag) return 1; return 2; }',
    'async function signal() {}',
    'function record(amount: number) {}',
    '',
  ].join('\n'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Fasync-return-before-wait.ts%23checkoutAsyncReturnBeforeWait', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const firstReturn = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'return 1;');
    const secondReturn = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'return 2;');
    const assignment = saved.semanticMap.steps.find((step: {stepId: string; name: string}) => step.name === 'amount = price(true)');
    expect(firstReturn && secondReturn && assignment).toBeTruthy();
    expect(saved.semanticMap.edges).toEqual(expect.arrayContaining([
      expect.objectContaining({kind:'return', fromStepId:firstReturn.stepId, toStepId:assignment.stepId, resolutionStatus:'resolved'}),
      expect.objectContaining({kind:'return', fromStepId:secondReturn.stepId, toStepId:assignment.stepId, resolutionStatus:'resolved'}),
    ]));
    const frame = saved.flowSequence.frames.find((candidate: {frameID: string; stepRefs: string[]}) => candidate.stepRefs.includes(firstReturn.stepId));
    expect(frame).toBeTruthy();

    await verifyPurposeSources(page, request, saved);
  } finally {
    unlinkSync(source);
  }
});

registerPurposeLifecycleTests();

test('purpose blocks from MCP preserve real branch loop handoff and reading restoration', async ({page, request}) => {
  const fixture = process.env.CODEFLOW_BROWSER_FIXTURE!;
  const mainSource = [
    "import {adjustAmount} from './purpose-adjustment';",
    'export function processPurposeCheckout(amount: number) {',
    '  const input = amount;',
    '  if (input < 0) return -1;',
    '  let total = input;',
    ...Array.from({length:36}, (_,i)=>`  // keep snapshot context ${i}`),
    '  for (let index = 0; index < 3; index++) {',
    '    total = adjustAmount(total);',
    '  }',
    '  return total;',
    '}',
  ].join('\n');
  const helperSource = ['export function adjustAmount(value: number) {','  const next = value + 2;','  return next;','}'].join('\n');
  const sourcePath = path.join(fixture, 'purpose-checkout.ts');
  const helperPath = path.join(fixture, 'purpose-adjustment.ts');
  writeFileSync(sourcePath, mainSource); writeFileSync(helperPath, helperSource);
  const executable = process.env.CODEFLOW_BROWSER_EXECUTABLE || path.resolve(__dirname,'../../../bin/codeflow');
  function mcp(name: string, args: any) {
    const rpc = {jsonrpc:'2.0',id:1,method:'tools/call',params:{name,arguments:{target:fixture,...args}}};
    const result = spawnSync(executable,['mcp',fixture],{input:JSON.stringify(rpc)+'\n',encoding:'utf8',timeout:60000,maxBuffer:16*1024*1024});
    expect(result.status,result.stderr).toBe(0);
    const response = JSON.parse(result.stdout);
    expect(response.result?.isError,JSON.stringify(response)).not.toBe(true);
    return JSON.parse(response.result.content[0].text);
  }
  try {
    const rawRequest = '음수 결제를 거부하고 세 번 금액을 조정한 뒤 최종 금액을 반환하는 흐름';
    const response = await request.get(`/api/task/view?query=${encodeURIComponent(rawRequest)}&entrySymbol=purpose-checkout.ts%23processPurposeCheckout`,{headers});
    expect(response.ok(),await response.text()).toBe(true);
    const analysis = await response.json();
    expect(analysis.presentationStatus).toBe('intermediate');
    expect(mcp('open_review',{viewId:analysis.viewId}).status).toBe('intermediate');
    const steps = analysis.semanticMap.steps;
    const sourceOf = (step: any) => Buffer.from(step.anchor.repoRelativePath === 'purpose-checkout.ts' ? mainSource : helperSource).subarray(...step.anchor.byteRange).toString();
    const validation = steps.filter((step: any)=> step.anchor.repoRelativePath==='purpose-checkout.ts' && (sourceOf(step).includes('const input') || sourceOf(step).includes('input < 0') || sourceOf(step).includes('return -1')));
    const outcome = steps.filter((step: any)=> step.anchor.repoRelativePath==='purpose-checkout.ts' && sourceOf(step).trim()==='return total;');
    const adjustment = steps.filter((step: any)=>!validation.includes(step)&&!outcome.includes(step));
    expect(validation.length).toBeGreaterThan(1);expect(outcome).toHaveLength(1);
    const segment = (segmentId: string,purpose: string,selected: any[]) => {
      const groups = new Map<string,string[]>();
      for (const step of selected) {const key=step.anchor.repoRelativePath+'#'+step.anchor.enclosingSymbolPath;groups.set(key,[...(groups.get(key)||[]),step.stepId]);}
      return {segmentId,purpose,inclusionReason:purpose+'을 구현하는 검증된 코드',stepIds:selected.map(step=>step.stepId),blocks:[...groups.values()].map(stepIds=>({stepIds}))};
    };
    const proposal = {viewId:analysis.viewId,categories:[{categoryId:'checkout-preparation',title:'결제 준비와 금액 처리',segmentIds:['input-validation','amount-adjustment']},{categoryId:'checkout-response',title:'결제 결과 전달',segmentIds:['amount-result']}],segments:[segment('input-validation','음수 입력 거부',validation),segment('amount-adjustment','세 번 금액 조정',adjustment),segment('amount-result','최종 금액 반환',outcome)],relations:[]};
    const result = mcp('curate_requested_flow_overview',proposal);
    const viewId = result.viewId;
    expect(viewId).not.toBe(analysis.viewId);
    const view = await (await request.get(`/api/view?viewId=${viewId}`,{headers})).json();
    expect(view.requestedFlowOverview).toEqual(result.requestedFlowOverview);
    expect(view.requestedFlowOverview.schemaVersion).toBe(2);
    expect(view.requestedFlowOverview.categories).toEqual(proposal.categories);
    for (const format of ['full','compact']) expect(mcp('get_flow_payload',{viewId,format}).requestedFlowOverview).toEqual(result.requestedFlowOverview);
    await page.setViewportSize({width:1200,height:800});
    const opened = mcp('open_review',{viewId});
    expect(opened.viewId).toBe(viewId);
    expect(opened.status).toBe('ready');
    await page.goto(opened.url);
    await expect(page.locator('#requested-flow-blocks')).toContainText('음수 입력 거부');
    await expect(page.locator('#code-flow')).toHaveCount(0);
    await expect(page.locator('[data-category]')).toHaveCount(2);
    await expect(page.locator('#requested-flow-overview [data-segment]')).toHaveCount(3);
    await expect(page.locator('.detail-actions')).toHaveCount(0);
    await expect(page.locator('#requested-flow-blocks .source-statement').first()).not.toBeVisible();
    await page.locator('.block-source-detail > summary').first().click();
    await expect(page.locator('#requested-flow-blocks .source-statement').first()).toBeInViewport();
    const overviewAccessibility=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa']).analyze();
    expect(overviewAccessibility.violations.filter(violation=>['critical','serious'].includes(violation.impact || ''))).toEqual([]);
    await expect(page.locator('[data-block-step]')).toHaveCount(validation.length);
    await expect(page.locator('#requested-flow-overview')).toContainText('조건 참');
    await expect(page.locator('#requested-flow-overview')).toContainText('조건 거짓');
    const validationSegment=page.locator('[data-segment="input-validation"]');
    const adjustmentSegment=page.locator('[data-segment="amount-adjustment"]');
    const resultSegment=page.locator('[data-segment="amount-result"]');
    await validationSegment.click();
    await validationSegment.press('ArrowUp');
    await expect(validationSegment).toBeFocused();
    await expect(validationSegment).toHaveAttribute('aria-pressed','true');
    await validationSegment.press('ArrowDown');
    await expect(adjustmentSegment).toBeFocused();
    await expect(adjustmentSegment).toHaveAttribute('aria-pressed','true');
    await adjustmentSegment.press('ArrowRight');
    await expect(resultSegment).toBeFocused();
    await expect(page.locator('[data-category="checkout-response"]')).toHaveAttribute('aria-pressed','true');
    await expect(page.locator('#requested-flow-blocks')).toContainText('return total;');
    await resultSegment.press('ArrowDown');
    await expect(resultSegment).toBeFocused();
    await expect(resultSegment).toHaveAttribute('aria-pressed','true');
    await resultSegment.press('ArrowLeft');
    await expect(adjustmentSegment).toBeFocused();
    await adjustmentSegment.press('ArrowUp');
    await expect(validationSegment).toBeFocused();
    await validationSegment.press('ArrowRight');
    await expect(adjustmentSegment).toBeFocused();
    await expect(page.locator('[data-category="checkout-preparation"]')).toHaveAttribute('aria-pressed','true');
    await expect(page.locator('[data-code-block]')).toHaveCount(2);
    await expect(page.locator('#requested-flow-blocks')).toContainText('purpose-adjustment.ts');
    await expect(page.locator('#requested-flow-blocks')).toContainText('const next = value + 2;');
    for (const disclosure of await page.locator('.block-source-detail > summary').all()) await disclosure.click();
    await expect(page.locator('#requested-flow-overview .reading-node[aria-pressed="true"]')).toContainText('반복');
    await page.locator('[data-segment-context="amount-adjustment"] > summary').click();
    await expect(page.locator('#requested-flow-overview')).toContainText('다음 반복');
    await page.locator('[data-segment="amount-result"]').click();
    await expect(page.locator('[data-category="checkout-response"]')).toHaveAttribute('aria-pressed','true');
    await expect(page.locator('#requested-flow-overview [data-segment]')).toHaveCount(3);
    await expect(page.locator('[data-segment="amount-result"]')).toHaveAttribute('aria-pressed','true');
    await expect(page.locator('#requested-flow-blocks')).toContainText('return total;');
    await page.locator('[data-category="checkout-preparation"]').click();
    await expect(page.locator('[data-segment="amount-adjustment"]')).toHaveAttribute('aria-pressed','true');
    const relationContext=page.locator('[data-segment-context="amount-adjustment"]');
    await expect(relationContext).toHaveAttribute('open','');
    const relationDetails=relationContext.locator('.relation details').first();
    await relationDetails.locator('summary').click();
    const relationSource=relationDetails.locator('[data-navigation-focus]').first();
    await relationSource.click();
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    await expect(relationContext).toHaveAttribute('open','');
    await expect(relationDetails).toHaveAttribute('open','');
    await expect(relationSource).toBeFocused();
    const block = view.requestedFlowOverview.segments[1].blocks[0];
    await page.locator(`[data-navigation-focus="block-expand:${block.blockId}"]`).click();
    const detailStep = adjustment.find((step: any)=>step.anchor.repoRelativePath==='purpose-adjustment.ts' && sourceOf(step).includes('return next'));
    const detailButton = page.locator(`[data-block-detail="${detailStep.stepId}"]`).first();
    await detailButton.focus();
    const position = await page.evaluate(()=>window.scrollY);
    await detailButton.click();
    await expectSelectedSource(page,view.flowContexts[detailStep.stepId]);
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    await expect(page.locator('[data-segment="amount-adjustment"]')).toHaveAttribute('aria-pressed','true');
    await expect(page.locator('[data-category="checkout-preparation"]')).toHaveAttribute('aria-pressed','true');
    await expect(page.locator(`[data-navigation-focus="block-expand:${block.blockId}"]`)).toHaveAttribute('aria-expanded','true');
    await expect(detailButton).toBeFocused();
    await expect.poll(()=>page.evaluate(()=>window.scrollY)).toBe(position);
    // Reopening another saved analysis does not mix segment/detail state.
    await page.evaluate(async id=>await (window as any).openFlowView(id),analysis.viewId);
    await page.evaluate(async id=>await (window as any).openFlowView(id),viewId);
    await expect(page.locator('[data-segment="amount-adjustment"]')).toHaveAttribute('aria-pressed','true');
    await detailButton.click();
    await page.evaluate(async id=>await (window as any).openFlowView(id),analysis.viewId);
    await page.evaluate(async id=>await (window as any).openFlowView(id),viewId);
    await expectSelectedSource(page,view.flowContexts[detailStep.stepId]);
    await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
    await page.setViewportSize({width:390,height:844});
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
    await adjustmentSegment.click();
    await adjustmentSegment.press('ArrowDown');
    await expect(resultSegment).toBeFocused();
    await expect(resultSegment).toBeInViewport();
    await expect(resultSegment).toHaveAttribute('aria-pressed','true');
    const segmentBounds=await resultSegment.evaluate(button=>{const item=button.getBoundingClientRect();const rail=button.closest('nav')!.getBoundingClientRect();return {left:item.left-rail.left,top:item.top-rail.top,right:rail.right-item.right,bottom:rail.bottom-item.bottom};});
    expect(Math.min(segmentBounds.left,segmentBounds.top,segmentBounds.right,segmentBounds.bottom)).toBeGreaterThanOrEqual(4);
    await page.setViewportSize({width:1200,height:800});
    await page.locator('[data-category="checkout-preparation"]').click();
    // Direct browser analysis stays staged until the explicit detail action.
    await page.evaluate(async()=>await (window as any).fetchTaskView('purpose-checkout.ts#processPurposeCheckout'));
    await expect(page).toHaveURL(new RegExp(viewId));
    await expect(page.getByRole('button',{name:'중간 결과 확인',exact:true})).toBeVisible();
    await expect(page.locator('#requested-flow-blocks')).toBeVisible();
    await page.getByRole('button',{name:'중간 결과 확인',exact:true}).click();
    await expect(page.locator('#code-flow')).toHaveCount(0);
    await expect(page.getByRole('status',{name:'중간 결과'})).toBeVisible();
    await expect(page.locator('#requested-flow-blocks')).toHaveCount(0);
    await page.evaluate(async id=>await (window as any).openFlowView(id),viewId);
    const screenshot = test.info().outputPath('purpose-blocks.png');await page.screenshot({path:screenshot,fullPage:true});
    const evidence = test.info().outputPath('purpose-block-evidence.json');
    const {createHash} = await import('node:crypto');
    writeFileSync(evidence,JSON.stringify({request:rawRequest,entry:'purpose-checkout.ts#processPurposeCheckout',snapshotId:analysis.flowSequence.snapshotID,analysisViewId:analysis.viewId,overviewViewId:viewId,executable,executableHash:createHash('sha256').update(readFileSync(executable)).digest('hex'),overview:view.requestedFlowOverview,expected:{start:'const input = amount',decision:'input < 0',handoff:'adjustAmount(total)',loop:'index < 3',failure:'return -1',result:'return total'},participantObservation:'pending'},null,2));
    await test.info().attach('purpose-block-evidence',{path:evidence,contentType:'application/json'});
    await test.info().attach('purpose-blocks',{path:screenshot,contentType:'image/png'});
  } finally {unlinkSync(sourcePath);unlinkSync(helperPath);}
});
