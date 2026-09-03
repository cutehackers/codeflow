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
});
