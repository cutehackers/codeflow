import { test, expect } from './approval-fixture';

test('no grounded proposal cannot create approval or claim current freshness', async ({ page }) => {
  await page.goto('http://127.0.0.1:4589/?token=testtoken');
  let submissions = 0;
  page.on('request', request => { if (request.url().includes('/api/semantic/approve')) submissions++; });
  await page.locator('#btn-semantic-approve').click();
  await expect(page.locator('#approval-history-state')).toHaveText('none');
  await expect(page.locator('#approval-history-freshness')).toHaveText('unknown');
  await expect(page.locator('#approval-result-msg')).not.toContainText('승인 기록 생성됨');
  expect(submissions).toBe(0);
});

for (const terminal of ['reject', 'revoke', 'supersede']) {
  test('durable approve, edit and ' + terminal + ' history survives restart', async ({ page, approvalFixture }) => {
    await approvalFixture.open(page);
    const before = await page.evaluate(() => ({
      fact: document.querySelector('#current-answer-statement')?.textContent,
      settlement: document.querySelector('#badge-settlement')?.textContent,
      proposal: document.querySelector('#proposal-epistemic-status')?.textContent,
    }));
    for (const decision of ['approve', 'edit_then_approve', terminal]) {
      if (decision === 'edit_then_approve') await page.locator('#approval-edited-text').fill('Reviewed semantic explanation');
      const responsePromise = page.waitForResponse(response => response.url().includes('/api/semantic/approve') && response.request().method() === 'POST');
      await page.locator('#btn-semantic-' + decision.replaceAll('_', '-')).click();
      const response = await responsePromise;
      expect(response.status()).toBe(200);
      expect((await response.json()).receipt.outbox.deliveryState).toBe('published');
      await expect(page.locator('#approval-result-msg')).toContainText('승인 기록 생성됨');
      await expect(page.locator('#approval-history-version')).toHaveText(decision === 'approve' ? '1' : decision === 'edit_then_approve' ? '2' : '3');
    }
    const state = terminal === 'reject' ? 'rejected' : terminal === 'revoke' ? 'revoked' : 'superseded';
    await expect(page.locator('#approval-history-state')).toHaveText(state);
    await expect(page.locator('#approval-history-events li')).toHaveText(['v1 · Approved', 'v2 · Edited and approved', 'v3 · ' + state[0].toUpperCase() + state.slice(1)]);
    expect(await page.evaluate(() => ({
      fact: document.querySelector('#current-answer-statement')?.textContent,
      settlement: document.querySelector('#badge-settlement')?.textContent,
      proposal: document.querySelector('#proposal-epistemic-status')?.textContent,
    }))).toEqual(before);
    await approvalFixture.control('restart');
    await approvalFixture.open(page);
    await expect(page.locator('#approval-history-state')).toHaveText(state);
    await expect(page.locator('#approval-history-version')).toHaveText('3');
    await expect(page.locator('#approval-history-freshness')).toHaveText('current');
    await approvalFixture.control('advance');
    await page.evaluate(() => eval("lastSeenEventId='evicted-test-cursor';initLiveStream();"));
    await expect(page.locator('#approval-history-freshness')).toHaveText('historical');
    await expect(page.locator('#approval-history-state')).toHaveText(state);
    await expect(page.locator('#approval-history-version')).toHaveText('3');
  });
}

test('lost HTTP response keeps the frozen command for exact retry while SSE refresh waits', async ({ page, approvalFixture }) => {
  await approvalFixture.open(page);
  let original: any;
  await page.route(/\/api\/semantic\/approve(?:\?|$)/, async route => {
    original = route.request().postDataJSON();
    const committed = await route.fetch();
    expect(committed.status()).toBe(200);
    await route.abort('failed');
  });
  await page.locator('#btn-semantic-approve').click();
  await expect(page.locator('#approval-result-msg')).toHaveText('approval request could not be sent; please retry');
  await expect(page.locator('#approval-history-state')).toHaveText('none');
  expect(await page.evaluate(() => eval('viewState.approvalPendingRequest'))).toEqual(original);
  await page.unroute(/\/api\/semantic\/approve(?:\?|$)/);
  const responsePromise = page.waitForResponse(response => response.url().includes('/api/semantic/approve') && response.request().method() === 'POST');
  await page.locator('#btn-semantic-approve').click();
  const response = await responsePromise;
  expect(response.request().postDataJSON()).toEqual(original);
  expect((await response.json()).receipt.replayed).toBe(true);
  await expect(page.locator('#approval-history-state')).toHaveText('active');
  await expect(page.locator('#approval-history-events li')).toHaveCount(1);
  expect(await page.evaluate(() => eval('viewState.approvalPendingRequest'))).toBeNull();

  let release!: () => void;
  let captured!: () => void;
  const capturedResponse = new Promise<void>(resolve => captured = resolve);
  const delayedResponse = new Promise<void>(resolve => release = resolve);
  let delayFirst = true;
  await page.route(/\/api\/semantic\/approval-history(?:\?|$)/, async route => {
    if (!delayFirst) { await route.continue(); return; }
    delayFirst = false;
    const old = await route.fetch();
    expect((await old.json()).aggregate.version).toBe(1);
    captured();
    await delayedResponse;
    await route.fulfill({ response: old, headers: { ...old.headers(), 'X-CodeFlow-Test-Delayed-History': 'v1' } });
  });
  await page.evaluate(() => eval("lastSeenEventId='unavailable-for-stale-history-test';initLiveStream();"));
  await capturedResponse;
  await page.locator('#approval-edited-text').fill('Second reviewed meaning');
  await page.locator('#btn-semantic-edit-then-approve').click();
  await expect(page.locator('#approval-history-version')).toHaveText('2');
  const oldResponse = page.waitForResponse(response => response.headers()['x-codeflow-test-delayed-history'] === 'v1');
  release();
  const released = await oldResponse;
  await released.finished();
  expect((await released.json()).aggregate.version).toBe(1);
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => resolve())));
  await expect(page.locator('#approval-history-version')).toHaveText('2');
  await expect(page.locator('#approval-history-state')).toHaveText('active');
  await expect(page.locator('#approval-history-events li')).toHaveText(['v1 · Approved', 'v2 · Edited and approved']);
});

test('a still-open browser reconnects after server restart and refreshes an external approval decision', async ({ page, approvalFixture, request }) => {
  await approvalFixture.open(page);
  const firstResponse = page.waitForResponse(response => response.url().includes('/api/semantic/approve') && response.request().method() === 'POST');
  await page.locator('#btn-semantic-approve').click();
  const initial = await (await firstResponse).json();
  await expect(page.locator('#approval-history-version')).toHaveText('1');
  const reconnect = page.waitForRequest(request => request.url().includes('/api/workspace/stream'));
  await approvalFixture.control('restart');
  await reconnect;
  const event = initial.receipt.event;
  const endpoint = new URL('/api/semantic/approve', approvalFixture.url);
  const response = await request.post(endpoint.toString(), {
    headers: { 'X-CodeFlow-Token': approvalFixture.token },
    data: {
      commandId: 'external-restart-reject', idempotencyKey: 'external-restart-reject-key',
      proposalId: event.proposalId, evidencePackId: event.evidencePackId,
      computedBasisId: event.computedBasisId, generationId: event.generationId,
      intentRevision: event.intentRevision, decision: 'reject',
      expectedApprovalVersion: 1, expectedState: 'active', predecessorApprovalId: event.approvalId,
    },
  });
  expect(response.status()).toBe(200);
  // With no backlog, the server flushes headers with the next event or its
  // ten-second heartbeat. The external decision supplies the event here.
  await expect(page.locator('#badge-connection')).toHaveText('SSE: connected');
  await expect(page.locator('#approval-history-state')).toHaveText('rejected');
  await expect(page.locator('#approval-history-version')).toHaveText('2');
  await expect(page.locator('#approval-history-events li')).toHaveText(['v1 · Approved', 'v2 · Rejected']);
});
