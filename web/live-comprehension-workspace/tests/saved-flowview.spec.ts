import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {readFileSync, unlinkSync, writeFileSync} from 'node:fs';
import path from 'node:path';

const entry = 'app/page.tsx#HomePage.onQuickCheckout';
const headers = {'X-CodeFlow-Token':'testtoken'};

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
  await preparation.click();
  const timeline = preparation.locator('xpath=following-sibling::button[1]');
  await timeline.click();
  await expect(timeline).toHaveAttribute('aria-expanded', 'true');
  await expect(timeline.locator('xpath=following-sibling::ol[1]').locator('[data-step]')).toHaveCount(6);
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
    await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
    await expect(page.getByRole('region', {name:'요청 흐름의 관문과 연결', exact:true})).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    const lastGateway = page.locator('[data-flow-frame]').last();
    await lastGateway.scrollIntoViewIfNeeded();
    await lastGateway.click();
    await expect(lastGateway).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('#code-flow .code-card')).toHaveAttribute('data-step-card', lastFrame.primaryStepRef);
    const sourcePanel = page.locator('#code-flow .source');
    await expect(sourcePanel).toBeVisible();
    expect(await sourcePanel.evaluate(element => element.getBoundingClientRect().right <= window.innerWidth)).toBe(true);
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

  await page.goto(`/?token=testtoken&viewId=${viewData.viewId}`);
  const requestRegion = page.getByRole('region', {name: '요청 영역', exact: true});
  await expect(requestRegion).toBeVisible();
  await expect(requestRegion).toContainText('흐름 확정');
  await expect(requestRegion).toContainText(query);
  await expect(requestRegion).toContainText(entry);
  await expect(requestRegion).toContainText('진입점:');
  await expect(requestRegion).toContainText('선택 근거:');
  await expect(requestRegion).toContainText('분석 한계:');
});
