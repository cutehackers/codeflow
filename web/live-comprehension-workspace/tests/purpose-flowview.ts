import {expect, type APIRequestContext, type Page} from '@playwright/test';

export const headers = {'X-CodeFlow-Token':'testtoken'};

export async function curatePurposeView(request: APIRequestContext, analysis: any) {
  expect(analysis.presentationStatus).toBe('intermediate');
  const unprovenFrames = new Set(analysis.flowSequence.summaryLimitations.filter((item: any)=>item.code === 'invocation_unproven').flatMap((item: any)=>item.frameRefs));
  const unprovenSteps = new Set(analysis.flowSequence.frames.filter((frame: any)=>unprovenFrames.has(frame.frameID)).flatMap((frame: any)=>frame.stepRefs));
  const groups = new Map<string, any[]>();
  for (const step of analysis.semanticMap.steps) {
    if (unprovenSteps.has(step.stepId)) {
      expect(analysis.flowSequence.summaryLimitations.some((item: any)=>item.code==='invocation_unproven' && item.frameRefs.some((id: string)=>analysis.flowSequence.frames.find((frame: any)=>frame.frameID===id)?.stepRefs.includes(step.stepId)))).toBe(true);
      continue;
    }
    expect(analysis.flowContexts[step.stepId]?.precision,`exact source for ${step.stepId}`).toBe('exact');
    expect(analysis.flowContexts[step.stepId]?.sourceValidationStatus,`verified source for ${step.stepId}`).toBe('verified');
    const key = `${step.anchor.repoRelativePath}#${step.anchor.enclosingSymbolPath}`;
    groups.set(key, [...groups.get(key) || [], step]);
  }
  const segments = [...groups].map(([implementation, steps], index) => ({
    segmentId: `process-${index + 1}`,
    purpose: `${implementation.split('#')[1]} 처리 과정`,
    inclusionReason: `${analysis.flowResolution.rawRequest} 요청에서 ${implementation}의 검증된 처리를 확인한다`,
    stepIds: steps.map(step => step.stepId),
    blocks: [{stepIds: steps.map(step => step.stepId)}],
  }));
  expect(segments.length).toBeGreaterThan(0);
  const response = await request.post('/api/view/overview', {headers, data: {
    viewId: analysis.viewId,
    categories: [{categoryId:'requested-processing',title:'요청한 처리 과정',segmentIds:segments.map(segment=>segment.segmentId)}],
    segments, relations: [],
  }});
  expect(response.ok(), await response.text()).toBe(true);
  const view = await response.json();
  expect(view.presentationStatus).toBe('ready');
  expect(view.viewId).not.toBe(analysis.viewId);
  expect(view.requestedFlowOverview.segments.flatMap((segment: any)=>segment.stepIds).sort()).toEqual(analysis.semanticMap.steps.filter((step: any)=>!unprovenSteps.has(step.stepId)).map((step: any)=>step.stepId).sort());
  expect(view.flowResolution.rawRequest).toBe(analysis.flowResolution.rawRequest);
  return view;
}

export async function expectSelectedSource(page: Page, context: {displayedLines: Array<{selection?: {text: string}}>}) {
  const selectedText = context.displayedLines.flatMap(line => line.selection ? [line.selection.text] : []);
  expect(selectedText.length).toBeGreaterThan(0);
  await expect(page.locator('#code-flow > article > pre mark, #code-flow .compare-grid > div:last-child mark')).toHaveText(selectedText);
}

export async function openPurposeView(page: Page, request: APIRequestContext, analysis: any) {
  const view = await curatePurposeView(request, analysis);
  await page.goto(`/?token=testtoken&viewId=${view.viewId}`);
  await expect(page.locator('#requested-flow-overview [data-segment]')).toHaveCount(view.requestedFlowOverview.segments.length);
  await expect(page.locator('#requested-flow-blocks')).toBeVisible();
  await expect(page.locator('#code-flow')).toHaveCount(0);
  return view;
}

export async function openPurposeStep(page: Page, view: any, stepId: string) {
  const segment = view.requestedFlowOverview.segments.find((item: any) => item.stepIds.includes(stepId));
  expect(segment, `purpose segment for ${stepId}`).toBeTruthy();
  await page.locator(`[data-segment="${segment.segmentId}"]`).click();
  for (const disclosure of await page.locator('.block-source-detail').all()) {
    if (await disclosure.getAttribute('open') === null) await disclosure.locator('summary').first().click();
  }
  const expand = page.getByRole('button',{name:'코드 블록 펼치기',exact:true});
  for (const button of await expand.all()) await button.click();
  const overlaps = page.locator('.overlap-details');
  for (const disclosure of await overlaps.all()) if (await disclosure.getAttribute('open') === null) await disclosure.locator('summary').click();
  await page.locator(`[data-block-detail="${stepId}"]`).first().click();
  await expectSelectedSource(page, view.flowContexts[stepId]);
  await expect(page.locator(`[data-segment="${segment.segmentId}"]`)).toHaveAttribute('aria-pressed','true');
}

export async function verifyPurposeSources(page: Page, request: APIRequestContext, analysis: any) {
  const view = await openPurposeView(page, request, analysis);
  for (const segment of view.requestedFlowOverview.segments) {
    for (const stepId of segment.stepIds) {
      await openPurposeStep(page, view, stepId);
      for (const [index,edge] of view.semanticMap.edges.entries()) {
        if (edge.fromStepId===stepId && edge.kind!=='control_flow' && edge.resolutionStatus==='resolved' && view.flowContexts[edge.toStepId]) {
          await expect(page.locator(`[data-navigation-focus="outgoing:${index}:${edge.fromStepId}:${edge.toStepId}:${edge.kind}"]`)).toBeVisible();
        }
      }
      await page.getByRole('button',{name:'상세 닫기',exact:true}).click();
      await expect(page.locator(`[data-segment="${segment.segmentId}"]`)).toHaveAttribute('aria-pressed','true');
      await expect(page.locator('#code-flow')).toHaveCount(0);
    }
  }
  return view;
}
