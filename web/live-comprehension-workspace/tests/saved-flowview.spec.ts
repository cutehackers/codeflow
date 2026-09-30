import { spawnSync } from 'node:child_process';
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {mkdirSync, readFileSync, unlinkSync, writeFileSync} from 'node:fs';
import path from 'node:path';

const entry = 'app/page.tsx#HomePage.onQuickCheckout';
const headers = {'X-CodeFlow-Token':'testtoken'};

async function expectSelectedSource(page: Page, context: {displayedLines: Array<{selection?: {text: string}}>}) {
  const selectedText = context.displayedLines.flatMap(line => line.selection ? [line.selection.text] : []);
  expect(selectedText.length).toBeGreaterThan(0);
  expect(selectedText.every(text => text.length > 0)).toBe(true);
  await expect(page.locator('#code-flow > article > pre mark, #code-flow .compare-grid > div:last-child mark')).toHaveText(selectedText);
}

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

test('empty home, analysis, scene navigation and immutable reopening', async ({page, request}) => {
  await page.goto('/?token=testtoken');
  await expect(page.getByText('첫 번째 흐름을 열어보세요.')).toBeVisible();
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toHaveCount(0);
  await page.locator('#query-input').fill(entry);
  await page.locator('#request-submit').click();
  // Explicit entry input must also work through the ordinary request form.
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  await expect(page).toHaveURL(/viewId=/);
  const viewURL = page.url();
  const viewId = new URL(viewURL).searchParams.get('viewId')!;
  const saved = await (await request.get(`/api/view?viewId=${viewId}`, {headers})).json();
  await expect(page.locator('[data-flow-frame]')).toHaveCount(saved.flowSequence.frames.length);
  await expect(page.getByRole('navigation',{name:'관문과 실행 타임라인'})).toBeVisible();
  const initialLines = await page.locator('#code-flow .source .line').count();
  await page.getByRole('button',{name:'코드 더 보기',exact:true}).click();
  expect(await page.locator('#code-flow .source .line').count()).toBeGreaterThan(initialLines);
  await page.getByRole('button',{name:'코드 접기',exact:true}).click();
  await expect(page.locator('#code-flow .source .line')).toHaveCount(initialLines);
  await page.locator('[data-flow-frame]').last().click();
  await expect(page.locator('[data-flow-frame]').last()).toHaveAttribute('aria-pressed','true');
  await expect(page.locator('#code-flow .code-card')).toHaveCount(1);
  await page.getByRole('button',{name:'처리 흐름',exact:true}).click();
  const frame = saved.flowSequence.frames.at(-1);
  await expect(page.locator('#process-flow .process-card')).toHaveCount(frame.stepRefs.length);
  await page.getByRole('button',{name:'코드 흐름',exact:true}).click();
  let analyses=0;
  page.on('request',req=>{if(new URL(req.url()).pathname==='/api/task/view') analyses++;});
  await page.reload();
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  expect(analyses).toBe(0);
  await page.getByRole('button',{name:'CodeFlow',exact:true}).click();
  await expect(page.getByRole('heading',{name:'다시 이어서 읽기'})).toBeVisible();
  await page.getByRole('button',{name:new RegExp(saved.semanticMap.summary.requested)}).first().click();
  await expect(page).toHaveURL(viewURL);
  expect(analyses).toBe(0);
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test('study layout reads the same saved code with more horizontal space', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  const frame = saved.flowSequence.frames[0];
  const stepID = frame.primaryStepRef;
  await page.setViewportSize({width: 1440, height: 900});

  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await page.locator(`[data-frame="${frame.frameID}"]`).click();
  await expectSelectedSource(page, saved.flowContexts[stepID]);
  const normalWidth = (await page.locator('#selected-flow').boundingBox())!.width;

  await page.goto(`/?token=testtoken&viewId=${saved.viewId}&studyLayout=code-focused`);
  await page.locator(`[data-frame="${frame.frameID}"]`).click();
  await expectSelectedSource(page, saved.flowContexts[stepID]);
  const focusedWidth = (await page.locator('#selected-flow').boundingBox())!.width;
  expect(focusedWidth).toBeGreaterThan(normalWidth);
  const codeBottom = (await page.locator('#selected-flow').boundingBox())!.y + (await page.locator('#selected-flow').boundingBox())!.height;
  const contextTop = (await page.locator('main.layout aside').boundingBox())!.y;
  expect(contextTop).toBeGreaterThanOrEqual(codeBottom);

  await page.reload();
  await expect(page).toHaveURL(/studyLayout=code-focused/);
  await page.locator(`[data-frame="${frame.frameID}"]`).click();
  await expectSelectedSource(page, saved.flowContexts[stepID]);

  const relationFrame = saved.flowSequence.frames.find((candidate: {frameID: string; primaryStepRef: string}) =>
    saved.semanticMap.edges.some((edge: {fromStepId: string; resolutionStatus: string}) =>
      edge.fromStepId === candidate.primaryStepRef && edge.resolutionStatus === 'resolved'
    )
  );
  expect(relationFrame).toBeTruthy();
  await page.locator(`[data-flow-frame="${relationFrame.frameID}"]`).click();
  await page.locator('[data-navigation-focus^="outgoing:"]').first().click();
  await page.getByRole('button', {name: '원래 장면 복귀', exact: true}).click();
  await expect(page.locator(`[data-flow-frame="${relationFrame.frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
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
  await expect(page.getByRole('region', {name: '요청 흐름의 관문과 연결', exact: true})).toBeVisible();
  const viewId = new URL(page.url()).searchParams.get('viewId');
  expect(viewId).toBeTruthy();
  const saved = await (await request.get(`/api/view?viewId=${viewId}`, {headers})).json();
  expect(saved.flowResolution.rawRequest).toBe(rawRequest);
  expect(saved.flowResolution.entrySymbolPath).toBe(entry);
  await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
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

test('browser back restores the selected gateway and its expanded timeline', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  const selected = page.locator('[data-frame]').last();
  const frameID = await selected.getAttribute('data-frame');
  await selected.click();
  const disclosure = selected.locator('xpath=following-sibling::button[1]');
  await disclosure.click();
  await expect(disclosure).toHaveAttribute('aria-expanded', 'true');
  await page.getByRole('button', {name:'CodeFlow', exact:true}).click();
  await expect(page.getByRole('heading', {name:'다시 이어서 읽기'})).toBeVisible();
  await page.goBack();
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  await expect(page.locator(`[data-frame="${frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator(`[data-frame="${frameID}"]`).locator('xpath=following-sibling::button[1]')).toHaveAttribute('aria-expanded', 'true');
});

test('keyboard-only flow reading selects a gateway, timeline step, and source expansion', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();

  const gateway = page.locator('[data-frame]').last();
  await gateway.focus();
  await gateway.press('Enter');
  await expect(gateway).toHaveAttribute('aria-pressed', 'true');

  const disclosure = gateway.locator('xpath=following-sibling::button[1]');
  await disclosure.focus();
  await disclosure.press('Space');
  await expect(disclosure).toHaveAttribute('aria-expanded', 'true');

  const timelineStep = page.locator('[data-step]').last();
  await timelineStep.focus();
  await timelineStep.press('Enter');
  await expect(timelineStep).toHaveAttribute('aria-pressed', 'true');

  const expand = page.getByRole('button', {name:'코드 더 보기', exact:true});
  await expand.focus();
  await expand.press('Space');
  await expect(page.getByRole('button', {name:'코드 접기', exact:true})).toBeFocused();
});

test('requested handler keeps preparation, awaited operation, and decision as 1:N gateways', async ({page, request}) => {
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

  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  const preparation = page.locator(`[data-frame="${frames[0].frameID}"]`);
  await expect(preparation.locator('strong')).toHaveText(frames[0].title);
  await preparation.click();
  const timeline = preparation.locator('xpath=following-sibling::button[1]');
  await timeline.click();
  await expect(timeline).toHaveAttribute('aria-expanded', 'true');
  await expect(timeline.locator('xpath=following-sibling::ol[1]').locator('[data-step]')).toHaveCount(6);
});

test('standalone TypeScript order flow opens every saved source step', async ({page, request}) => {
  const functionEntry = 'services/orderService.ts#processOrder';
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(functionEntry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  expect(saved.semanticMap.steps.length).toBeGreaterThan(1);
  expect(saved.flowSequence.frames.length).toBeGreaterThan(0);
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  for (const frame of saved.flowSequence.frames as Array<{frameID: string; stepRefs: string[]}>) {
    await page.locator(`[data-frame="${frame.frameID}"]`).click();
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
    const disclosure = page.locator(`[aria-controls="timeline-${frame.frameID}"]`);
    await disclosure.click();
    for (const stepID of frame.stepRefs) {
      await page.locator(`[data-flow-step="${stepID}"]`).click();
      await expectSelectedSource(page, saved.flowContexts[stepID]);
      expect(saved.flowContexts[stepID].precision).toBe('exact');
      const sourceStep = saved.semanticMap.steps.find((step: {stepId: string}) => step.stepId === stepID);
      expect(saved.flowContexts[stepID].canonicalPath).toBe(sourceStep.anchor.repoRelativePath);
    }
  }
});

test('TypeScript local result calculation remains readable in CodeView', async ({page, request}) => {
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
    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await page.locator(`[data-frame="${frame.frameID}"]`).click();
    await page.locator(`[aria-controls="timeline-${frame.frameID}"]`).click();
    await page.locator(`[data-flow-step="${calculation.stepId}"]`).click();
    await expectSelectedSource(page, saved.flowContexts[calculation.stepId]);
  } finally {
    unlinkSync(sourcePath);
  }
});

test('TSX event flow opens the selected source for every saved step', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok(), await response.text()).toBe(true);
  const saved = await response.json();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  for (const frame of saved.flowSequence.frames as Array<{frameID: string; stepRefs: string[]}>) {
    await page.locator(`[data-frame="${frame.frameID}"]`).click();
    await page.locator(`[aria-controls="timeline-${frame.frameID}"]`).click();
    for (const stepID of frame.stepRefs) {
      await page.locator(`[data-flow-step="${stepID}"]`).click();
      expect(saved.flowContexts[stepID].precision).toBe('exact');
      await expectSelectedSource(page, saved.flowContexts[stepID]);
    }
  }
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
    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.locator('[data-flow-frame]').last().click();
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
    await page.getByRole('button', {name:'코드 더 보기', exact:true}).click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    expect(await page.locator('#code-flow .source').evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true);
    await page.locator('#boundaries summary').click();
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
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  for (const frame of saved.flowSequence.frames as Array<{frameID: string; stepRefs: string[]}>) {
    await page.locator(`[data-frame="${frame.frameID}"]`).click();
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
    const disclosure = page.locator(`[aria-controls="timeline-${frame.frameID}"]`);
    await disclosure.click();
    for (const stepID of frame.stepRefs) {
      await page.locator(`[data-flow-step="${stepID}"]`).click();
      await expectSelectedSource(page, saved.flowContexts[stepID]);
      expect(saved.flowContexts[stepID].precision).toBe('exact');
    }
  }
  await page.locator(`[data-flow-step="${callee.stepId}"]`).click();
  await expect(page.locator('#code-flow .source mark').first()).toContainText('return amount * 2');
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
  await expect(page.getByRole('region', {name: '요청 흐름의 관문과 연결', exact: true})).toBeVisible();
  const viewId = new URL(page.url()).searchParams.get('viewId');
  const saved = await (await request.get(`/api/view?viewId=${viewId}`, {headers})).json();
  expect(saved.flowResolution.rawRequest).toBe(rawRequest);
  expect(saved.flowResolution.entrySymbolPath).toBe(goEntry);
  await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
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
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
  for (const frame of saved.flowSequence.frames as Array<{frameID: string; stepRefs: string[]}>) {
    await page.locator(`[data-frame="${frame.frameID}"]`).click();
    await page.locator(`[aria-controls="timeline-${frame.frameID}"]`).click();
    for (const stepID of frame.stepRefs) {
      await page.locator(`[data-flow-step="${stepID}"]`).click();
      await expectSelectedSource(page, saved.flowContexts[stepID]);
    }
  }
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
      const result = spawnSync('go',['run','./cmd/codeflow','mcp',process.env.CODEFLOW_BROWSER_FIXTURE!],{
        cwd:path.resolve(__dirname,'../../..'),input:JSON.stringify(rpc)+'\n',encoding:'utf8',timeout:30000,maxBuffer:5*1024*1024,
      });
      expect(result.status,result.stderr).toBe(0);
      const response = JSON.parse(result.stdout);
      expect(response.result.isError).not.toBe(true);
      const payload = JSON.parse(response.result.content[0].text);
      expect(payload.codeReadingPath).toEqual(curated.codeReadingPath);
      expect(payload.codeReadingPathLimitation).toBe(restored.codeReadingPathLimitation);
    }


    await page.goto(`/?token=testtoken&viewId=${curated.viewId}`);
    await expect(page.getByRole('navigation', {name:'요청 흐름 개요', exact:true})).toBeVisible();
    await expect(page.locator('[data-flow-frame]')).toHaveCount(0);
    await expect(page.locator('#execution-navigation')).toHaveCount(0);
    await expect(page.locator('#requested-flow-overview')).toContainText('조건 참');
    await expect(page.locator('#requested-flow-overview')).toContainText('조건 거짓');
    expect(curated.codeReadingPath.nodes.find((node: any) => node.stepId === outcome.stepId).previousNodeIds).toHaveLength(1);
    const join = page.locator(`[data-overview-join="${outcome.stepId}"]`);
    await expect(join).toContainText('2개 확인된 선행 연결');
    await expect(join.locator('[data-join-source]')).toHaveCount(2);
    await join.locator(`[data-join-source="${detail.stepId}"]`).click();
    await expectSelectedSource(page, curated.flowContexts[detail.stepId]);
    for (const node of curated.codeReadingPath.nodes as Array<{stepId: string}>) {
      await page.locator(`.reading-node[data-flow-step="${node.stepId}"]`).click();
      await expectSelectedSource(page, curated.flowContexts[node.stepId]);
    }
    const evidencePath = test.info().outputPath('overview-analysis-evidence.json');
    writeFileSync(evidencePath, JSON.stringify({request:rawRequest,analysisViewId:analysis.viewId,curatedViewId:curated.viewId,selectedStepIds:curated.codeReadingPath.nodes.map((node: any)=>node.stepId),httpAndMcpFormats:['full','compact']},null,2));
    await test.info().attach('overview-analysis-evidence', {path:evidencePath,contentType:'application/json'});
    const relationPanel = page.locator(`[data-reading-relation="${decision.stepId}:${outcome.stepId}"]`);
    await relationPanel.locator('button.disclosure').click();
    await relationPanel.locator(`[data-relation-step="${detail.stepId}"]`).click();
    await expectSelectedSource(page, curated.flowContexts[detail.stepId]);
  } finally {
    unlinkSync(sourcePath);
  }
});


test('overview and statements restore exact code, panel positions, focus and per-view comparison', async ({page, request}) => {
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
    await page.goto(`/?token=testtoken&viewId=${compact.viewId}`);
    const compactOverview = page.locator('#requested-flow-overview');
    await expect(compactOverview.locator('.reading-node')).toHaveCount(2);
    await expect(compactOverview.locator('.detail-connection')).toHaveCount(0);
    await expect(compactOverview.locator('[data-relation-step]')).toHaveCount(0);
    await compactOverview.locator('button.disclosure').click();
    await expect(compactOverview.locator('.detail-connection')).toHaveCount(route!.length);
    await expect(compactOverview.locator('[data-relation-step]')).toHaveCount(routeSteps.length-2);
    await compactOverview.locator(`[data-relation-step="${routeSteps[1]}"]`).click();
    await expectSelectedSource(page, compact.flowContexts[routeSteps[1]]);
    const relations = points.slice(0,-1).map((fromStepId, index) => ({
      fromStepId, toStepId:points[index+1], edgeIndexes:route!.slice(routeSteps.indexOf(fromStepId), routeSteps.indexOf(points[index+1]))
    }));
    const curatedResponse = await request.post('/api/view/reading-path', {headers, data:{
      viewId:analysis.viewId,
      selections:points.map((stepId,index) => ({stepId,role:index===0?'entry':index===points.length-1?'outcome':steps.find((step: any)=>step.stepId===stepId).kind==='call'?'handoff':'transform',requestPart:'amount',inclusionReason:index===points.length-1?'누적한 값을 반환한다':`입력 값에 확인된 변경을 적용한다 ${index+1}`})),
      relations,
    }});
    expect(curatedResponse.ok(), await curatedResponse.text()).toBe(true);
    const curated = await curatedResponse.json();
    expect(curated.codeReadingPath.nodes[0].frameId).toBe(curated.codeReadingPath.nodes[1].frameId);
    await page.setViewportSize({width:1200,height:800});
    await page.goto(`/?token=testtoken&viewId=${curated.viewId}`);
    const overview = page.locator('#requested-flow-overview');
    await expect(overview).toBeVisible();
    for (const stepId of points.slice(0,2)) {
      await overview.locator(`[data-flow-step="${stepId}"]`).click();
      await expectSelectedSource(page, curated.flowContexts[stepId]);
    }
    await page.locator('#baseline').selectOption(analysis.viewId);
    await page.locator('#compare').click();
    await expect(page.getByRole('button', {name:'비교 닫기',exact:true})).toBeVisible();
    const requests: string[] = [];
    await page.getByRole('button', {name:'문장별 탐색',exact:true}).click();
    await page.locator('#view-process').click();
    await page.getByRole('button', {name:'요청 흐름 개요',exact:true}).click();
    await expect(page.locator('#view-process')).toHaveAttribute('aria-pressed','true');
    await page.locator('#view-code').click();
    await page.getByRole('button', {name:'문장별 탐색',exact:true}).click();
    await expect(page.locator('#view-code')).toHaveAttribute('aria-pressed','true');
    await expect(page.getByRole('button', {name:'비교 닫기',exact:true})).toBeVisible();
    await page.getByRole('button', {name:'요청 흐름 개요',exact:true}).click();
    page.on('request', req => {requests.push(new URL(req.url()).pathname);});
    const origin = points.at(-3)!;
    await overview.locator(`[data-flow-step="${origin}"]`).click();
    const disclosure = overview.locator('[data-reading-relation] button.disclosure').last();
    await disclosure.click();
    await overview.locator(`[data-flow-step="${origin}"]`).focus();
    const originPositions = await page.evaluate(() => {
      const rail = document.getElementById('requested-flow-overview')!;
      rail.scrollTop = 160;
      window.scrollTo(0,80);
      return {top:rail.scrollTop, window:window.scrollY};
    });
    expect(originPositions.top).toBeGreaterThan(0);
    const overviewImagePath = test.info().outputPath('requested-flow-overview.png');
    await page.screenshot({path:overviewImagePath,fullPage:true});
    await test.info().attach('overview-source', {path:overviewImagePath,contentType:'image/png'});

    await page.getByRole('button', {name:'문장별 탐색',exact:true}).click();
    await expect(page.locator('#execution-navigation')).toBeVisible();
    await expectSelectedSource(page, curated.flowContexts[origin]);
    const otherFile = steps.find((step: any) => step.anchor.repoRelativePath === 'overview-effects.ts');
    const otherFrame = curated.flowSequence.frames.find((frame: any) => frame.stepRefs.includes(otherFile.stepId));
    await page.locator(`[data-frame="${otherFrame.frameID}"]`).click();
    const detailToggle = page.locator(`[aria-controls="timeline-${otherFrame.frameID}"]`);
    if (await detailToggle.getAttribute('aria-expanded') !== 'true') await detailToggle.click();
    await page.locator(`[data-step="${otherFile.stepId}"]`).click();
    await expectSelectedSource(page, curated.flowContexts[otherFile.stepId]);
    await page.locator(`[data-step="${otherFile.stepId}"]`).focus();
    const detailPositions = await page.evaluate(() => {
      const rail = document.getElementById('execution-navigation')!;
      rail.scrollTop = 220;
      const track = document.getElementById('flow-overview')!;
      track.scrollTop = 150;
      const source = document.getElementById('source-current')!;
      source.scrollLeft = 170;
      return {rail:rail.scrollTop, track:track.scrollTop, source:source.scrollLeft};
    });
    expect(detailPositions.rail).toBeGreaterThan(0);
    expect(detailPositions.source).toBeGreaterThan(0);
    await page.getByRole('button', {name:'요청 흐름 개요',exact:true}).click();
    await expect(overview.locator(`[data-flow-step="${origin}"]`)).toHaveAttribute('aria-pressed','true');
    await expect(overview.locator(`[data-flow-step="${origin}"]`)).toBeFocused();
    await expect(disclosure).toHaveAttribute('aria-expanded','true');
    await expect.poll(() => overview.evaluate(el=>el.scrollTop)).toBe(originPositions.top);
    await expect.poll(() => page.evaluate(()=>window.scrollY)).toBe(originPositions.window);
    await page.getByRole('button', {name:'문장별 탐색',exact:true}).click();
    await expectSelectedSource(page, curated.flowContexts[otherFile.stepId]);
    await expect(page.locator(`[data-step="${otherFile.stepId}"]`)).toBeFocused();
    await expect.poll(() => page.locator('#execution-navigation').evaluate(el=>el.scrollTop)).toBe(detailPositions.rail);
    await expect.poll(() => page.locator('#flow-overview').evaluate(el=>el.scrollTop)).toBe(detailPositions.track);
    await expect.poll(() => page.locator('#source-current').evaluate(el=>el.scrollLeft)).toBe(detailPositions.source);
    expect(requests.filter(url => ['/api/task/view','/api/view/reading-path','/api/view/compare'].includes(url))).toEqual([]);
    await page.evaluate((id) => window.openFlowView(id), analysis.viewId);
    await expect(page.locator('#requested-flow-overview')).toHaveCount(0);
    await page.evaluate((id) => window.openFlowView(id), curated.viewId);
    await expectSelectedSource(page, curated.flowContexts[otherFile.stepId]);
    await expect(page.getByRole('button',{name:'문장별 탐색',exact:true})).toHaveAttribute('aria-pressed','true');
    await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
    expect(await page.evaluate(()=>window.flowStore.baseline?.viewId)).toBe(analysis.viewId);
    await page.getByRole('button', {name:'요청 흐름 개요',exact:true}).click();
    await overview.locator(`[data-flow-step="${points[0]}"]`).click();
    await page.getByRole('button', {name:'문장별 탐색',exact:true}).click();
    await expectSelectedSource(page, curated.flowContexts[points[0]]);
    await expect(page.locator(`[data-step="${points[0]}"]`)).toBeFocused();
    const preserved = await page.evaluate(() => ({
      viewId:window.flowStore.data?.viewId, mode:window.flowStore.explorationMode,
      step:window.flowStore.selectedStepId, baseline:window.flowStore.baseline?.viewId,
      compare:window.flowStore.compare, overviewStep:window.flowStore.explorationNavigation.positions.overview?.stepId,
    }));
    const currentState = () => page.evaluate(() => ({
      viewId:window.flowStore.data?.viewId, mode:window.flowStore.explorationMode,
      step:window.flowStore.selectedStepId, baseline:window.flowStore.baseline?.viewId,
      compare:window.flowStore.compare, overviewStep:window.flowStore.explorationNavigation.positions.overview?.stepId,
    }));
    await page.route('**/api/task/view?*',route=>route.fulfill({status:500,json:{message:'overview analysis unavailable'}}));
    await page.locator('#reanalyze').click();
    await expect(page.getByRole('status')).toContainText('overview analysis unavailable');
    expect(await currentState()).toEqual(preserved);
    await page.unroute('**/api/task/view?*');
    let release: () => void = () => {};
    const responseGate = new Promise<void>(resolve => {release = resolve;});
    let arrived: () => void = () => {};
    const requestGate = new Promise<void>(resolve => {arrived = resolve;});
    let finished: () => void = () => {};
    const responseFinished = new Promise<void>(resolve => {finished = resolve;});
    await page.route('**/api/task/view?*',async route=>{
      arrived(); await responseGate;
      await route.fulfill({json:analysis}).catch(()=>{}); finished();
    });
    await page.locator('#reanalyze').click();
    await requestGate;
    await page.getByRole('button',{name:'취소',exact:true}).click();
    release(); await responseFinished;
    await expect(page.getByRole('status')).toContainText('취소');
    expect(await currentState()).toEqual(preserved);
    await page.unroute('**/api/task/view?*');
    await page.getByRole('button',{name:'요청 흐름 개요',exact:true}).click();
    await expect(overview.locator(`[data-flow-step="${points[0]}"]`)).toHaveAttribute('aria-pressed','true');
    await page.setViewportSize({width:390,height:844});
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    // Reading and switching never rewrites the immutable source View.
    expect(await (await request.get(`/api/view?viewId=${curated.viewId}`,{headers})).json()).toEqual(curated);
    const evidencePath = test.info().outputPath('overview-statement-roundtrip-evidence.json');
    writeFileSync(evidencePath, JSON.stringify({request:curated.codeReadingPath.request,analysisViewId:analysis.viewId,curatedViewId:curated.viewId,originStepId:origin,detailStepId:otherFile.stepId,originPositions,detailPositions,baselineViewId:analysis.viewId,failuresAndLateResponsesPreserved:true,immutableView:true},null,2));
    await test.info().attach('overview-statement-roundtrip-evidence', {path:evidencePath,contentType:'application/json'});


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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await expect(page.locator(`[data-frame="${frame.frameID}"]`)).toBeVisible();
    await page.locator(`[aria-controls="timeline-${frame.frameID}"]`).click();
    await page.locator(`[data-flow-step="${detail.stepId}"]`).click();
    await expect(page.locator('#code-flow .source mark')).toHaveCount(0);
    await expect(page.getByText('주변 코드 · 정확한 코드 범위 미표시')).toBeVisible();
    await expect(page.getByText('상세 코드 미표시 · 읽기 경계').first()).toBeVisible();
  } finally {
    writeFileSync(sourcePath, original);
  }
});

test('unreadable gateway failure keeps the prior view and explains the target', async ({page, request}) => {
  const baselineResponse = await request.get(`/api/task/view?query=quick+checkout&entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(baselineResponse.ok()).toBe(true);
  const baseline = await baselineResponse.json();
  const sourcePath = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app/page.tsx');
  const original = readFileSync(sourcePath, 'utf8');
  try {
    await page.goto(`/?token=testtoken&viewId=${baseline.viewId}`);
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
    writeFileSync(sourcePath, original.replace("setStatus('processing');", "setStatus({ apiKey: 'sk-1234567890abcdef' });"));
    await page.locator('#reanalyze').click();
    await expect(page.getByRole('status').filter({hasText:'대상 app/page.tsx#HomePage.onQuickCheckout'})).toBeVisible();
    await expect(page.getByRole('status').filter({hasText:'다시 분석하세요'})).toBeVisible();
    await expect(page).toHaveURL(new RegExp(`viewId=${baseline.viewId}`));
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
  } finally {
    writeFileSync(sourcePath, original);
  }
});

test('saved FlowView core reading surface has no serious accessibility violations', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();

  const scan = await new AxeBuilder({page})
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();
  expect(scan.violations.filter(violation => violation.impact === 'critical' || violation.impact === 'serious')).toEqual([]);
});

test('home and process view have no serious accessibility violations', async ({page, request}) => {
  await page.goto('/?token=testtoken');
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toHaveCount(0);
  const homeScan = await new AxeBuilder({page})
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();
  expect(homeScan.violations.filter(violation => violation.impact === 'critical' || violation.impact === 'serious')).toEqual([]);

  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await page.getByRole('button', {name:'처리 흐름', exact:true}).click();
  await expect(page.locator('#process-flow')).toBeVisible();
  const processScan = await new AxeBuilder({page})
    .withTags(['wcag2a', 'wcag2aa'])
    .analyze();
  expect(processScan.violations.filter(violation => violation.impact === 'critical' || violation.impact === 'serious')).toEqual([]);
});

test('execution navigation uses the enclosing implementation for source-only state changes', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  const mutation = saved.semanticMap.steps.find((step: any) => step.kind === 'mutation' && step.anchor?.enclosingSymbolPath);
  expect(mutation).toBeTruthy();
  const frame = saved.flowSequence.frames.find((candidate: any) => candidate.primaryStepRef === mutation.stepId);
  expect(frame).toBeTruthy();

  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  const item = page.locator(`[data-frame="${frame.frameID}"]`);
  await expect(item).toContainText(mutation.anchor.enclosingSymbolPath);
  await expect(item).toContainText('상태 변경');
  await expect(item).not.toContainText(mutation.name);
});

test('cross-file timeline child keeps its gateway and opens its own source evidence', async ({page, request}) => {
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();
    const gateway = page.locator(`[data-frame="${parentFrame.frameID}"]`);
    await gateway.click();
    const disclosure = gateway.locator('xpath=following-sibling::button[1]');
    await disclosure.click();
    const child = page.locator(`[data-step="${helperStep.stepId}"]`);
    await child.click();
    await expect(gateway).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('#code-flow .path')).toContainText('app/payment-helper.ts');
    await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const unprovenCount = saved.flowSequence.summaryLimitations
      .find((item: {code: string}) => item.code === 'invocation_unproven').frameRefs.length;
    const unprovenToggle = page.getByRole('button', {name: `실행 연결 미확인 코드 ${unprovenCount}관문 보기`});
    await expect(unprovenToggle).toHaveCount(2);
    await expect(unprovenToggle.first()).toHaveAttribute('aria-expanded', 'false');
    await expect(page.locator(`[data-frame="${callbackFrame.frameID}"]`)).toHaveCount(0);
    await expect(page.locator(`[data-flow-frame="${callbackFrame.frameID}"]`)).toHaveCount(0);
    const sourceFrame = saved.flowSequence.frames.find((frame: {stepRefs: string[]}) => frame.stepRefs.includes(relation.fromStepId));
    await page.locator(`[data-flow-frame="${sourceFrame.frameID}"]`).click();
    await page.locator('[data-navigation-focus^="outgoing:"]').filter({hasText: callbackStep.name}).first().click();
    await expect(page.locator(`[data-frame="${callbackFrame.frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator(`[data-flow-frame="${callbackFrame.frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
    await expectSelectedSource(page, saved.flowContexts[callbackStep.stepId]);
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
    await expect(page.locator('#code-flow .source')).toContainText('if (done) return;');
    await page.reload();
    await page.getByRole('region', {name: '요청 흐름의 관문과 연결', exact: true})
      .getByRole('button', {name: `실행 연결 미확인 코드 ${unprovenCount}관문 보기`}).click();
    await page.locator(`[data-flow-frame="${callbackFrame.frameID}"]`).click();
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
    await expect(page.getByText('콜백 전달 · 실행 여부 미확인').first()).toBeVisible();
    await page.locator(`[data-flow-frame="${arrowFrame.frameID}"]`).click();
    await expect(page.locator('#code-flow .source mark').first()).toBeVisible();
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
    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();

    const decisionGateway = page.locator(`[data-frame="${decisionFrame.frameID}"]`);
    await decisionGateway.click();
    await decisionGateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${decision.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/chargeCard\(amount\)/})).toBeVisible();
    await expect(connections.getByRole('button', {name:/transferFunds\(amount\)/})).toBeVisible();

    const receiptGateway = page.locator(`[data-frame="${receiptFrame.frameID}"]`);
    await receiptGateway.click();
    await receiptGateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${receiptCall.stepId}"]`).click();
    await expect(connections.getByRole('button', {name:/payment = chargeCard\(amount\)/})).toBeVisible();
    await expect(connections.getByRole('button', {name:/payment = transferFunds\(amount\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${thrown.stepId}"]`).click();
    await expect(page.getByRole('region', {name:'요청 흐름 안의 전후 연결'}).getByRole('button', {name:/recover\(\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${thrown.stepId}"]`).click();
    await expect(page.getByRole('region', {name:'요청 흐름 안의 전후 연결'}).getByRole('button', {name:/recover\(\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${thrown.stepId}"]`).click();
    await expect(page.getByRole('region', {name:'요청 흐름 안의 전후 연결'}).getByRole('button', {name:/recover\(\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const overviewOpen = page.getByRole('button', {name:'전체 흐름 보기', exact:true});
    if (await overviewOpen.count()) await overviewOpen.click();
    const add = page.locator('#condition-path-add');
    await add.selectOption(firstTruthy);
    await expect(page.getByRole('list', {name:'선택한 경로 조건'})).toHaveCount(1);
    await add.selectOption(secondTruthy);
    await expect(page.getByRole('list', {name:'선택한 경로 조건'})).toHaveCount(1);
    await expect(page.getByRole('list', {name:'선택한 경로 조건'}).locator('li')).toHaveCount(2);
    expect(await page.locator('article.path-match').count()).toBeGreaterThan(0);

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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${finalReturn.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/result = finish\(\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${firstReturn.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/amount = price\(true\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${apply.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/for await \(const item of items\)/})).toHaveCount(2);
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${rejectedWait.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/recover\(item\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${cleanup.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/while \(!done\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${recovery.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/cleanup\(\)/})).toBeVisible();
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

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    const gateway = page.locator(`[data-frame="${frame.frameID}"]`);
    await gateway.click();
    await gateway.locator('xpath=following-sibling::button[1]').click();
    await page.locator(`[data-step="${firstReturn.stepId}"]`).click();
    const connections = page.getByRole('region', {name:'요청 흐름 안의 전후 연결'});
    await expect(connections.getByRole('button', {name:/amount = price\(true\)/})).toBeVisible();
  } finally {
    unlinkSync(source);
  }
});

test('relation return restores reading positions in a large flow', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'large-return-flow.ts');
  writeFileSync(source, largeFlowSource().replace('processLargeFlow', 'processLargeReturnFlow'));
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Flarge-return-flow.ts%23processLargeReturnFlow', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    const sourceFrame = saved.flowSequence.frames.find((frame: {frameID: string; primaryStepRef: string}) =>
      saved.semanticMap.edges.some((edge: {fromStepId: string; resolutionStatus: string}) => edge.fromStepId === frame.primaryStepRef && edge.resolutionStatus === 'resolved')
    );
    expect(sourceFrame).toBeTruthy();

    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await page.locator(`[data-flow-frame="${sourceFrame.frameID}"]`).click();
    const positions = await page.evaluate(() => {
      const move = (id: string) => {
        const panel = document.getElementById(id)!;
        panel.scrollTop = Math.min(120, Math.max(0, panel.scrollHeight - panel.clientHeight));
        return panel.scrollTop;
      };
      return {overview: move('flow-overview'), navigation: move('execution-navigation')};
    });
    expect(positions.overview).toBeGreaterThan(0);
    expect(positions.navigation).toBeGreaterThan(0);
    const relation = page.locator('[data-navigation-focus^="outgoing:"]').first();
    await relation.click();
    await page.getByRole('button', {name:'원래 장면 복귀', exact:true}).click();
    await expect(page.locator(`[data-flow-frame="${sourceFrame.frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
    const restored = await page.evaluate(() => ({
      overview: document.getElementById('flow-overview')!.scrollTop,
      navigation: document.getElementById('execution-navigation')!.scrollTop,
    }));
    expect(restored.overview).toBeGreaterThanOrEqual(positions.overview - 1);
    expect(restored.navigation).toBeGreaterThanOrEqual(positions.navigation - 1);
  } finally {
    unlinkSync(source);
  }
});

test('relation return restores the source gateway and relation focus', async ({page, request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const saved = await response.json();
  const sourceFrame = saved.flowSequence.frames.find((frame: {frameID: string; primaryStepRef: string}) =>
    saved.semanticMap.edges.some((edge: {fromStepId: string; resolutionStatus: string}) => edge.fromStepId === frame.primaryStepRef && edge.resolutionStatus === 'resolved')
  );
  expect(sourceFrame).toBeTruthy();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  await page.locator(`[data-flow-frame="${sourceFrame.frameID}"]`).click();
  const relation = page.locator('[data-navigation-focus^="outgoing:"]').first();
  await expect(relation).toBeVisible();
  const relationFocus = await relation.getAttribute('data-navigation-focus');
  await relation.focus();
  await relation.click();
  await expect(page.getByRole('button', {name:'원래 장면 복귀',exact:true})).toBeVisible();
  await page.getByRole('button', {name:'원래 장면 복귀',exact:true}).click();
  await expect(page.locator(`[data-flow-frame="${sourceFrame.frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator(`[data-navigation-focus="${relationFocus}"]`)).toBeFocused();
});


test('relation return restores comparison mode, source gateway, and focus', async ({page, request}) => {
  const baselineResponse = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(baselineResponse.ok()).toBe(true);
  const baseline = await baselineResponse.json();
  const currentResponse = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(currentResponse.ok()).toBe(true);
  const current = await currentResponse.json();
  await page.goto(`/?token=testtoken&viewId=${current.viewId}`);
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  await page.locator('#baseline').selectOption(baseline.viewId);
  await page.locator('#compare').click();
  await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
  const sourceFrame = current.flowSequence.frames.find((frame: {frameID: string; primaryStepRef: string}) =>
    current.semanticMap.edges.some((edge: {fromStepId: string; resolutionStatus: string}) => edge.fromStepId === frame.primaryStepRef && edge.resolutionStatus === 'resolved')
  );
  expect(sourceFrame).toBeTruthy();
  await page.locator(`[data-flow-frame="${sourceFrame.frameID}"]`).click();
  const relation = page.locator('[data-navigation-focus^="outgoing:"]').first();
  await expect(relation).toBeVisible();
  const relationFocus = await relation.getAttribute('data-navigation-focus');
  await relation.focus();
  await relation.click();
  await expect(page.getByRole('button',{name:'원래 장면 복귀',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'원래 장면 복귀',exact:true}).click();
  await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
  await expect(page.locator(`[data-flow-frame="${sourceFrame.frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator(`[data-navigation-focus="${relationFocus}"]`)).toBeFocused();
});

test('large analyzed flow keeps its distant gateway reachable at narrow width', async ({page, request}) => {
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'large-flow.ts');
  writeFileSync(source, largeFlowSource());
  try {
    const response = await request.get('/api/task/view?entrySymbol=app%2Flarge-flow.ts%23processLargeFlow', {headers});
    expect(response.ok()).toBe(true);
    const saved = await response.json();
    expect(saved.flowSequence.frames.length).toBeGreaterThan(7);
    expect(saved.semanticMap.steps.length).toBeGreaterThan(150);
    const lastFrame = saved.flowSequence.frames.at(-1);
    expect(lastFrame).toBeTruthy();

    await page.setViewportSize({width:390, height:844});
    const openStarted = Date.now();
    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();
    const openMs = Date.now() - openStarted;
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    const selectStarted = Date.now();
    const lastGateway = page.locator('[data-flow-frame]').last();
    await lastGateway.scrollIntoViewIfNeeded();
    await lastGateway.click();
    await expect(lastGateway).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('#code-flow .code-card')).toHaveAttribute('data-step-card', lastFrame.primaryStepRef);
    const selectMs = Date.now() - selectStarted;
    const sourcePanel = page.locator('#code-flow .source');
    await expect(sourcePanel).toBeVisible();
    expect(await sourcePanel.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true);

    const expandStarted = Date.now();
    await page.locator(`[aria-controls="timeline-${lastFrame.frameID}"]`).click();
    const lastStepID = lastFrame.stepRefs.at(-1);
    expect(lastStepID).toBeTruthy();
    await page.locator(`[data-flow-step="${lastStepID}"]`).click();
    await expectSelectedSource(page, saved.flowContexts[lastStepID]);
    const expandAndReadMs = Date.now() - expandStarted;

    const frames = saved.flowSequence.frames as Array<{frameID: string; stepRefs: string[]}>;
    const offscreenRelation = saved.semanticMap.edges.find((edge: {fromStepId: string; toStepId: string; resolutionStatus: string}) => {
      if (edge.resolutionStatus !== 'resolved') return false;
      const fromIndex = frames.findIndex(frame => frame.stepRefs.includes(edge.fromStepId));
      const toIndex = frames.findIndex(frame => frame.stepRefs.includes(edge.toStepId));
      return fromIndex >= 0 && toIndex > fromIndex && toIndex < frames.length - 7;
    });
    expect(offscreenRelation).toBeTruthy();
    const relationSource = frames.find(frame => frame.stepRefs.includes(offscreenRelation.fromStepId))!;
    const relationTarget = frames.find(frame => frame.stepRefs.includes(offscreenRelation.toStepId))!;
    const relationStarted = Date.now();
    await page.locator(`[data-flow-frame="${relationSource.frameID}"]`).click();
    await lastGateway.scrollIntoViewIfNeeded();
    const targetOutsideViewport = await page.locator(`[data-flow-frame="${relationTarget.frameID}"]`).evaluate(element => {
      const rect = element.getBoundingClientRect();
      return rect.top >= window.innerHeight || rect.bottom <= 0;
    });
    expect(targetOutsideViewport).toBe(true);
    await page.locator('[data-navigation-focus^="outgoing:"]').first().click();
    await expect(page.getByRole('button', {name: '원래 장면 복귀', exact: true})).toBeVisible();
    await page.getByRole('button', {name: '원래 장면 복귀', exact: true}).click();
    await expect(page.locator(`[data-flow-frame="${relationSource.frameID}"]`)).toHaveAttribute('aria-pressed', 'true');
    const relationAndReturnMs = Date.now() - relationStarted;
    console.log('large-flow navigation ' + JSON.stringify({stepCount: saved.semanticMap.steps.length, gatewayCount: frames.length, openMs, selectMs, expandAndReadMs, relationAndReturnMs}));
  } finally {
    unlinkSync(source);
  }
});

test('failed reanalysis and cancelled responses preserve the current result', async ({page,request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`,{headers});
  expect(response.ok()).toBe(true);
  const saved=await response.json();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  await page.locator('[data-flow-frame]').last().click();
  const selected=await page.locator('[data-flow-frame][aria-pressed=true]').getAttribute('data-flow-frame');
  await page.route('**/api/task/view?*',route=>route.fulfill({status:500,json:{message:'analysis unavailable'}}));
  await page.locator('#reanalyze').click();
  await expect(page.getByRole('status')).toContainText('analysis unavailable');
  await expect(page.locator('[data-flow-frame][aria-pressed=true]')).toHaveAttribute('data-flow-frame',selected!);
  await expect(page).toHaveURL(new RegExp(`viewId=${saved.viewId}`));
  await page.unroute('**/api/task/view?*');
  const delayedResponse = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent('services/orderService.ts#processOrder')}`,{headers});
  expect(delayedResponse.ok()).toBe(true);
  const delayedView = await delayedResponse.json();
  expect(delayedView.viewId).not.toBe(saved.viewId);
  await page.route('**/api/task/view?*',async route=>{await new Promise(resolve=>setTimeout(resolve,1000));await route.fulfill({json:delayedView}).catch(()=>{});});
  await page.locator('#reanalyze').click();
  await page.getByRole('button',{name:'취소',exact:true}).click();
  await expect(page.getByRole('status')).toContainText('취소');
  await page.waitForTimeout(1200);
  await expect(page.getByRole('status')).toContainText('취소');
  await expect(page).toHaveURL(new RegExp(`viewId=${saved.viewId}`));
  await expect(page.locator('[data-flow-frame][aria-pressed=true]')).toHaveAttribute('data-flow-frame',selected!);
});

test('explicit comparison, successful reanalysis and direct relation context', async ({page,request}) => {
  const analyze = async () => {
    const response=await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`,{headers});
    expect(response.ok()).toBe(true);
    return response.json();
  };
  const baseline=await analyze();
  const comparisonSource=path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app/page.tsx');
  const comparisonOriginal=readFileSync(comparisonSource,'utf8');
  let current;
  try {
    writeFileSync(comparisonSource,comparisonOriginal+'\n// comparison source revision\n');
    current=await analyze();
  } finally { writeFileSync(comparisonSource,comparisonOriginal); }
  expect(current.viewId).not.toBe(baseline.viewId);
  await page.goto(`/?token=testtoken&viewId=${current.viewId}`);
  await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
  await expect(page.locator('#baseline option[value="'+baseline.viewId+'"]')).toHaveCount(1);
  await expect(page.locator('#compare')).toBeDisabled();
  await page.locator('#baseline').selectOption(baseline.viewId);
  await page.locator('#compare').click();
  await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
  await expect(page.locator('.compare-grid')).toHaveCount(1);
  await page.getByRole('button',{name:'비교 닫기',exact:true}).click();
  await page.locator('[data-flow-frame]').last().focus();
  await page.keyboard.press('Enter');
  const selected=await page.locator('[data-flow-frame][aria-pressed=true]').getAttribute('data-flow-frame');
  await page.locator('#reanalyze').click();
  await expect(page.getByRole('status')).toContainText(/저장된 분석|선택한 장면이 새 분석에서 대응되지 않아 이전 화면을 유지합니다/);
  await expect(page.locator('[data-flow-frame][aria-pressed=true]')).toHaveAttribute('data-flow-frame',selected!);
  await page.locator('[data-flow-frame]').first().click();
  const connections = page.getByRole('region',{name:'요청 흐름 안의 전후 연결'});
  await expect(connections).toBeVisible();
  await expect(connections.getByRole('heading',{name:'이곳으로 연결'})).toBeVisible();
  await expect(connections.getByRole('heading',{name:'이곳에서 연결'})).toBeVisible();
});


test('compares an actual source edit against the selected saved analysis', async ({page,request}) => {
  const response=await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`,{headers});
  const baseline=await response.json();
  const source=path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app/page.tsx');
  const original=readFileSync(source,'utf8');
  try {
    writeFileSync(source,original.replace("setStatus('completed')", "setStatus('confirmed')"));
    await page.goto(`/?token=testtoken&viewId=${baseline.viewId}`);
    await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결',exact:true})).toBeVisible();
    await page.locator('#reanalyze').click();
    await expect(page).not.toHaveURL(new RegExp(`viewId=${baseline.viewId}`));
    await page.locator('#baseline').selectOption(baseline.viewId);
    await page.locator('#compare').click();
    await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
    await expect(page.getByText(/변경 [1-9][0-9]*건/)).toBeVisible();
    await page.setViewportSize({width:390, height:844});
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    const comparisonSources = page.locator('.compare-grid .source');
    await expect(comparisonSources).toHaveCount(2);
    for (const sourcePanel of await comparisonSources.all()) {
      expect(await sourcePanel.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true);
    }
  } finally { writeFileSync(source,original); }
});

test('long comparison source stays contained and remains horizontally readable', async ({page, request}) => {
  const baselineResponse = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(baselineResponse.ok()).toBe(true);
  const baseline = await baselineResponse.json();
  const source = path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app', 'page.tsx');
  const original = readFileSync(source, 'utf8');
  const longStatus = `confirmed-${'important-status-'.repeat(32)}`;
  try {
    writeFileSync(source, original.replace("setStatus('completed')", `setStatus('${longStatus}')`));
    const currentResponse = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`, {headers});
    expect(currentResponse.ok()).toBe(true);
    const current = await currentResponse.json();
    const statusStep = [...current.semanticMap.steps].reverse().find((step: {name: string}) => step.name.includes('setStatus'));
    expect(statusStep).toBeTruthy();
    const statusFrame = current.flowSequence.frames.find((frame: {stepRefs: string[]}) => frame.stepRefs.includes(statusStep.stepId));
    expect(statusFrame).toBeTruthy();

    await page.setViewportSize({width:390, height:844});
    await page.goto(`/?token=testtoken&viewId=${current.viewId}`);
    await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();
    await page.locator(`[data-flow-frame="${statusFrame.frameID}"]`).click();
    const timeline = page.locator(`[data-frame="${statusFrame.frameID}"]`).locator('xpath=following-sibling::button[1]');
    await timeline.click();
    await page.locator(`[data-step="${statusStep.stepId}"]`).click();
    await page.locator('#baseline').selectOption(baseline.viewId);
    await page.locator('#compare').click();

    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    const comparisonSources = page.locator('.compare-grid .source');
    await expect(comparisonSources).toHaveCount(2);
    for (const sourcePanel of await comparisonSources.all()) {
      expect(await sourcePanel.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true);
    }
    const currentSource = page.getByLabel('현재 코드');
    expect(await currentSource.evaluate(element => element.scrollWidth > element.clientWidth)).toBe(true);
    await currentSource.evaluate(element => { element.scrollLeft = element.scrollWidth; });
    expect(await currentSource.evaluate(element => element.scrollLeft > 0)).toBe(true);

    // A 195px CSS viewport is equivalent to reading a 390px-wide layout at 200% zoom.
    await page.setViewportSize({width:195, height:422});
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    for (const sourcePanel of await comparisonSources.all()) {
      expect(await sourcePanel.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true);
    }
  } finally {
    writeFileSync(source, original);
  }
});

test('home entry remains readable at a 200 percent zoom equivalent width', async ({page}) => {
  await page.setViewportSize({width:195, height:422});
  await page.goto('/?token=testtoken');
  await expect(page.getByRole('region', {name:'FlowView 시작', exact:true})).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(page.locator('#query-input')).toBeVisible();
  await expect(page.locator('#request-submit')).toBeVisible();
});

test('resolved flow displays request region with entry point evidence and limitations', async ({page, request}) => {
  const query = '결제 진행';
  const response = await request.get(`/api/task/view?request=${encodeURIComponent(query)}&entrySymbol=${encodeURIComponent(entry)}`, {headers});
  expect(response.ok()).toBe(true);
  const viewData = await response.json();
  expect(viewData.flowResolution).toBeTruthy();
  expect(viewData.flowResolution.status).toBe('resolved');
  expect(viewData.flowResolution.rawRequest).toBe(query);
  expect(viewData.flowResolution.evidence[0].sourceAnchor).toEqual(viewData.semanticMap.steps[0].anchor);

  await page.goto(`/?token=testtoken&viewId=${viewData.viewId}`);
  const requestRegion = page.getByRole('region', {name: '요청 영역', exact: true});
  await expect(requestRegion).toBeVisible();
  await expect(requestRegion).toContainText('흐름 확정');
  await expect(requestRegion).toContainText(query);
  await expect(requestRegion).toContainText(entry);
  await expect(requestRegion).toContainText('진입점:');
  await expect(requestRegion).toContainText('후보 정보:');
  await expect(requestRegion.locator('.request-detail').filter({hasText: '후보 정보:'})).toContainText('app/page.tsx#HomePage.onQuickCheckout');
  await expect(requestRegion).toContainText('요청과 코드의 의미 연결은 선택자의 판단');
  await expect(requestRegion).toContainText('분석 한계:');
});
