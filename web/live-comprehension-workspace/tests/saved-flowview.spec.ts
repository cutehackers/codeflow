import { test, expect } from '@playwright/test';
import {readFileSync, writeFileSync} from 'node:fs';
import path from 'node:path';

const entry = 'app/page.tsx#HomePage.handleQuickCheckout';
const headers = {'X-CodeFlow-Token':'testtoken'};

test('empty home, analysis, scene navigation and immutable reopening', async ({page, request}) => {
  await page.goto('/?token=testtoken');
  await expect(page.getByText('아직 분석한 흐름이 없습니다.')).toBeVisible();
  await expect(page.locator('#macro-storyboard')).toHaveCount(0);
  await page.locator('#query-input').fill(entry);
  await page.locator('#request-submit').click();
  // Explicit entry input must also work through the ordinary request form.
  await expect(page.locator('#macro-storyboard')).toBeVisible();
  await expect(page).toHaveURL(/viewId=/);
  const viewURL = page.url();
  const viewId = new URL(viewURL).searchParams.get('viewId')!;
  const saved = await (await request.get(`/api/view?viewId=${viewId}`, {headers})).json();
  await expect(page.locator('[data-story-frame]')).toHaveCount(saved.storyboard.frames.length);
  const track = await page.locator('#storyboard-track').evaluate(el => ({wrap:getComputedStyle(el).flexWrap, overflow:getComputedStyle(el).overflowX}));
  expect(track).toEqual({wrap:'nowrap',overflow:'auto'});
  const initialLines = await page.locator('#code-flow .source .line').count();
  await page.getByRole('button',{name:'코드 더 보기',exact:true}).click();
  expect(await page.locator('#code-flow .source .line').count()).toBeGreaterThan(initialLines);
  await page.getByRole('button',{name:'코드 접기',exact:true}).click();
  await expect(page.locator('#code-flow .source .line')).toHaveCount(initialLines);
  await page.locator('[data-story-frame]').last().click();
  await expect(page.locator('[data-story-frame]').last()).toHaveAttribute('aria-pressed','true');
  await expect(page.locator('#code-flow .code-card')).toHaveCount(1);
  await page.getByRole('button',{name:'처리 흐름',exact:true}).click();
  const frame = saved.storyboard.frames.at(-1);
  await expect(page.locator('#process-flow .process-card')).toHaveCount(frame.stepRefs.length);
  await page.getByRole('button',{name:'코드 흐름',exact:true}).click();
  let analyses=0;
  page.on('request',req=>{if(new URL(req.url()).pathname==='/api/task/view') analyses++;});
  await page.reload();
  await expect(page.locator('#macro-storyboard')).toBeVisible();
  expect(analyses).toBe(0);
  await page.getByRole('button',{name:'CodeFlow',exact:true}).click();
  await expect(page.getByRole('heading',{name:'기존 FlowView'})).toBeVisible();
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
  await expect(page.getByText('아직 분석한 흐름이 없습니다.')).toHaveCount(0);
  await page.goto('/?token=testtoken&viewId=missing');
  await expect(page.getByRole('status')).toContainText('흐름을 열지 못했습니다');
  await expect(page.locator('#macro-storyboard')).toHaveCount(0);
});

test('failed reanalysis and cancelled responses preserve the current result', async ({page,request}) => {
  const response = await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`,{headers});
  expect(response.ok()).toBe(true);
  const saved=await response.json();
  await page.goto(`/?token=testtoken&viewId=${saved.viewId}`);
  await expect(page.locator('#macro-storyboard')).toBeVisible();
  await page.locator('[data-story-frame]').last().click();
  const selected=await page.locator('[data-story-frame][aria-pressed=true]').getAttribute('data-story-frame');
  await page.route('**/api/task/view?*',route=>route.fulfill({status:500,json:{message:'analysis unavailable'}}));
  await page.locator('#reanalyze').click();
  await expect(page.getByRole('status')).toContainText('analysis unavailable');
  await expect(page.locator('[data-story-frame][aria-pressed=true]')).toHaveAttribute('data-story-frame',selected!);
  await expect(page).toHaveURL(new RegExp(`viewId=${saved.viewId}`));
  await page.unroute('**/api/task/view?*');
  await page.route('**/api/task/view?*',async route=>{await new Promise(resolve=>setTimeout(resolve,1000));await route.fulfill({json:saved}).catch(()=>{});});
  await page.locator('#reanalyze').click();
  await page.getByRole('button',{name:'취소',exact:true}).click();
  await expect(page.getByRole('status')).toContainText('취소');
  await expect(page.locator('[data-story-frame][aria-pressed=true]')).toHaveAttribute('data-story-frame',selected!);
});

test('explicit comparison, successful reanalysis and unresolved relations', async ({page,request}) => {
  const analyze = async () => {
    const response=await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`,{headers});
    expect(response.ok()).toBe(true);
    return response.json();
  };
  const baseline=await analyze();
  const current=await analyze();
  await page.goto(`/?token=testtoken&viewId=${current.viewId}`);
  await expect(page.locator('#macro-storyboard')).toBeVisible();
  await expect(page.locator('#baseline option[value="'+baseline.viewId+'"]')).toHaveCount(1);
  await expect(page.locator('#compare')).toBeDisabled();
  await page.locator('#baseline').selectOption(baseline.viewId);
  await page.locator('#compare').click();
  await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
  await expect(page.locator('.compare-grid')).toHaveCount(1);
  await page.getByRole('button',{name:'비교 닫기',exact:true}).click();
  await page.locator('[data-story-frame]').last().focus();
  await page.keyboard.press('Enter');
  const selected=await page.locator('[data-story-frame][aria-pressed=true]').getAttribute('data-story-frame');
  await page.locator('#reanalyze').click();
  await expect(page.getByRole('status')).toContainText('저장된 분석');
  await expect(page.locator('[data-story-frame][aria-pressed=true]')).toHaveAttribute('data-story-frame',selected!);
  await page.locator('[data-story-frame]').first().click();
  await expect(page.locator('#radar-svg [role=button]')).toHaveCount(0);
  await expect(page.locator('#radar-caption')).toContainText('직접 연결을 확인하지 못했습니다');
});


test('compares an actual source edit against the selected saved analysis', async ({page,request}) => {
  const response=await request.get(`/api/task/view?entrySymbol=${encodeURIComponent(entry)}`,{headers});
  const baseline=await response.json();
  const source=path.join(process.env.CODEFLOW_BROWSER_FIXTURE!, 'app/page.tsx');
  const original=readFileSync(source,'utf8');
  try {
    writeFileSync(source,original.replace("setStatus('completed')", "setStatus('confirmed')"));
    await page.goto(`/?token=testtoken&viewId=${baseline.viewId}`);
    await expect(page.locator('#macro-storyboard')).toBeVisible();
    await page.locator('#reanalyze').click();
    await expect(page).not.toHaveURL(new RegExp(`viewId=${baseline.viewId}`));
    await page.locator('#baseline').selectOption(baseline.viewId);
    await page.locator('#compare').click();
    await expect(page.getByRole('button',{name:'비교 닫기',exact:true})).toBeVisible();
    await expect(page.getByText(/변경 [1-9][0-9]*건/)).toBeVisible();
  } finally { writeFileSync(source,original); }
});
