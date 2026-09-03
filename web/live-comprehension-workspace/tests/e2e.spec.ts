import { test, expect } from '@playwright/test';

test.describe('FlowView Live Semantic Comprehension Workspace E2E', () => {
  test('natural language feature query and disambiguation workflow', async ({ page }) => {
    // 1. Load FlowView with auth token
    await page.goto('http://127.0.0.1:4589/?token=testtoken');
    await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');

    // 2. Query bar is present
    const queryInput = page.locator('#query-input');
    const querySubmit = page.locator('#query-submit');
    await expect(queryInput).toBeVisible();
    await expect(querySubmit).toBeVisible();

    // 3. Ambiguous query: "checkout"
    await queryInput.fill('checkout');
    await querySubmit.click();

    // Verify disambiguation dialog appears with candidate options
    const disambiguation = page.locator('#disambiguation-dialog');
    await expect(disambiguation).toBeVisible({ timeout: 10000 });
    const candidates = disambiguation.locator('button');
    const count = await candidates.count();
    expect(count).toBeGreaterThanOrEqual(2);

    // 4. Click specific candidate: "app/page.tsx#HomePage.handleQuickCheckout"
    const quickCheckoutBtn = disambiguation.locator('button', { hasText: 'HomePage.handleQuickCheckout' });
    await expect(quickCheckoutBtn).toBeVisible();
    await quickCheckoutBtn.click();

    // 5. Current Answer strip appears in presentation order (Answer first)
    const answerStrip = page.locator('#current-answer-strip');
    await expect(answerStrip).toBeVisible({ timeout: 10000 });

    const answerStatement = page.locator('#current-answer-statement');
    await expect(answerStatement).not.toBeEmpty();

    const answerStage = page.locator('#current-answer-stage');
    await expect(answerStage).toContainText('Verified');

    // 6. Timeline Flow Rail displays steps
    const timelineList = page.locator('#timeline-list');
    await expect(timelineList).toBeVisible();
    const timelineItems = timelineList.locator('.timeline-item');
    expect(await timelineItems.count()).toBeGreaterThanOrEqual(1);

    // 7. Code Panel displays CodeLens source anchor
    const codePath = page.locator('#code-path');
    await expect(codePath).not.toBeEmpty();
  });

  test('displays workspace activity status, pending revisions, analysis lag, and scope', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Activity badge
    const badge = page.locator('#workspace-activity-badge');
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText(/(idle|editing|analyzing|reconciling)/);

    // 2. Epoch tag
    const epochTag = page.locator('#workspace-epoch-tag');
    await expect(epochTag).toBeVisible();
    await expect(epochTag).toContainText('epoch');

    // 3. Pending revisions count (VS03-A6)
    const pendingCount = page.locator('#workspace-pending-count');
    await expect(pendingCount).toBeVisible();
    await expect(pendingCount).toContainText('pending');

    // 4. Analysis lag (VS03-A6)
    const analysisLag = page.locator('#workspace-analysis-lag');
    await expect(analysisLag).toBeVisible();
    await expect(analysisLag).toContainText('lag');

    // 5. Active scope (VS03-A6)
    const scopeTag = page.locator('#workspace-scope-tag');
    await expect(scopeTag).toBeVisible();
    await expect(scopeTag).not.toBeEmpty();
  });

  test('displays independent status axes, SSE connection, and preserves step selection', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Perform semantic query
    const queryInput = page.locator('#query-input');
    await queryInput.fill('HomePage.handleQuickCheckout');
    await page.locator('#query-submit').click();

    const answerStrip = page.locator('#current-answer-strip');
    await expect(answerStrip).toBeVisible({ timeout: 10000 });

    // 2. Independent status axes (VS04-A8)
    const freshnessBadge = page.locator('#badge-freshness');
    await expect(freshnessBadge).toBeVisible();
    await expect(freshnessBadge).toHaveText(/(Current|Last Verified)/);

    const settlementBadge = page.locator('#badge-settlement');
    await expect(settlementBadge).toBeVisible();
    await expect(settlementBadge).toContainText('Settlement:');

    const sseBadge = page.locator('#badge-connection');
    await expect(sseBadge).toBeVisible();
    await expect(sseBadge).toContainText('SSE:');

    // 3. Stable selection (VS04-A10): select first step
    const timelineItems = page.locator('.timeline-item');
    const firstItem = timelineItems.first();
    await firstItem.click();
    await expect(firstItem).toHaveAttribute('aria-current', 'step');

    // Trigger update and verify selection is preserved
    await page.evaluate(() => {
      // @ts-ignore
      if (typeof handleSemanticQuery === 'function') {
        // @ts-ignore
        handleSemanticQuery(null, true);
      }
    });

    await expect(firstItem).toHaveAttribute('aria-current', 'step');
  });

  test('displays Change Pulse, Requirement Alignment Board, and Evidence Dock with 4 tabs', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Perform semantic query
    const queryInput = page.locator('#query-input');
    await queryInput.fill('HomePage.handleQuickCheckout');
    await page.locator('#query-submit').click();

    await expect(page.locator('#current-answer-strip')).toBeVisible({ timeout: 10000 });

    // 2. Change Pulse section is present
    const changePulseSection = page.locator('#change-pulse-section');
    await expect(changePulseSection).toBeVisible();
    await expect(page.locator('#btn-toggle-review')).toBeVisible();

    // 3. Requirement Alignment Board is present with separate intent status tag (VS05-A9)
    const alignmentSection = page.locator('#requirement-alignment-section');
    await expect(alignmentSection).toBeVisible();
    const intentStatusTag = page.locator('#intent-status-tag');
    await expect(intentStatusTag).toBeVisible();
    await expect(intentStatusTag).toContainText('Intent:');

    const table = page.locator('#requirement-alignment-table');
    await expect(table).toBeVisible();
    await expect(table.locator('thead th').first()).toHaveText('요구사항 (Criterion)');

    // 4. Evidence Dock is present with 4 tabs: Why, Code, Test, History
    const dockSection = page.locator('#evidence-dock-section');
    await expect(dockSection).toBeVisible();

    const tabWhy = page.locator('#dock-tab-why');
    const tabCode = page.locator('#dock-tab-code');
    const tabTest = page.locator('#dock-tab-test');
    const tabHistory = page.locator('#dock-tab-history');

    await expect(tabWhy).toBeVisible();
    await expect(tabCode).toBeVisible();
    await expect(tabTest).toBeVisible();
    await expect(tabHistory).toBeVisible();

    // Tab switching test
    await tabCode.click();
    await expect(page.locator('#dock-pane-code')).toBeVisible();
    await expect(page.locator('#dock-pane-why')).toBeHidden();

    await tabTest.click();
    await expect(page.locator('#dock-pane-test')).toBeVisible();
    await expect(page.locator('#dock-pane-code')).toBeHidden();

    await tabWhy.click();
    await expect(page.locator('#dock-pane-why')).toBeVisible();
    await expect(page.locator('#dock-why-text')).not.toBeEmpty();
  });
});


