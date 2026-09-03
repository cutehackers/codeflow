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

  test('displays Change Impact Trace section with direct, indirect, and unresolved boundaries (VS-06)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Change Impact Trace section is present
    const impactSection = page.locator('#change-impact-section');
    await expect(impactSection).toBeVisible();

    const impactTag = page.locator('#impact-status-tag');
    await expect(impactTag).toBeVisible();
    await expect(impactTag).toHaveText('Bounded');

    // 2. Direct, indirect, and unresolved boundary panels are present
    await expect(page.locator('#direct-impact-list')).toBeVisible();
    await expect(page.locator('#indirect-impact-list')).toBeVisible();
    await expect(page.locator('#unresolved-boundaries-list')).toBeVisible();

    // 3. Trigger impact analysis
    const impactBtn = page.locator('#btn-trigger-impact');
    await expect(impactBtn).toBeVisible();
    await impactBtn.click();

    // Verify direct impact list is updated
    await expect(page.locator('#direct-impact-list')).not.toBeEmpty();
  });

  test('displays Failure & Incident Investigation section with reverse cause nodes and timeline (VS-07)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Section is present
    const failSection = page.locator('#failure-investigation-section');
    await expect(failSection).toBeVisible();

    const modeTag = page.locator('#failure-mode-tag');
    await expect(modeTag).toBeVisible();

    // 2. Trigger Debug Mode investigation
    const debugBtn = page.locator('#btn-trigger-debug');
    await expect(debugBtn).toBeVisible();
    await debugBtn.click();

    // Verify cause chain nodes are populated
    await expect(page.locator('#failure-nodes-list')).not.toBeEmpty();

    // 3. Trigger Incident Mode investigation
    const incBtn = page.locator('#btn-trigger-incident');
    await expect(incBtn).toBeVisible();
    await incBtn.click();

    // Verify timeline events are populated
    await expect(page.locator('#failure-timeline-list')).not.toBeEmpty();
  });

  test('displays Semantic Approval & Grounding section and records human approval (VS-08)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Section is present
    const apprSection = page.locator('#semantic-approval-section');
    await expect(apprSection).toBeVisible();

    const statusBadge = page.locator('#approval-status-badge');
    await expect(statusBadge).toBeVisible();
    await expect(statusBadge).toHaveText('Awaiting Human Approval');

    // 2. Proposal card and evidence summary are visible
    await expect(page.locator('#proposal-card')).toBeVisible();
    await expect(page.locator('#evidence-grounding-summary')).toBeVisible();

    // 3. Click Approve button
    const approveBtn = page.locator('#btn-semantic-approve');
    await expect(approveBtn).toBeVisible();
    await approveBtn.click();

    // 4. Verify approval status changes to Approved and message is shown
    await expect(statusBadge).toHaveText('Approved');
    await expect(page.locator('#approval-result-msg')).toContainText('승인 기록 생성됨');
  });

  test('displays Domain Architecture section and renders domain cards (VS-09)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Section is present
    const onbSection = page.locator('#onboarding-domains-section');
    await expect(onbSection).toBeVisible();

    const badge = page.locator('#onboarding-coverage-badge');
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText('Level 1: System Map');

    // 2. Click Explore Domains
    const exploreBtn = page.locator('#btn-explore-domains');
    await expect(exploreBtn).toBeVisible();
    await exploreBtn.click();

    // 3. Verify domain cards grid is populated
    await expect(page.locator('#domain-cards-grid')).not.toBeEmpty();
    await expect(page.locator('#onboarding-total-domains')).not.toHaveText('0');
  });

  test('displays Release Capability section and validates release readiness gates (VS-10)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Section is present
    const relSection = page.locator('#release-capability-section');
    await expect(relSection).toBeVisible();

    const badge = page.locator('#release-ready-badge');
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText('Release Ready: PASSED');

    // 2. Metrics and fallback tier are visible
    await expect(page.locator('#metric-latency-p95')).toBeVisible();
    await expect(page.locator('#metric-precision')).toBeVisible();
    await expect(page.locator('#metric-regressions')).toBeVisible();
    await expect(page.locator('#release-fallback-tier')).toHaveText('local_slm');

    // 3. Click Evaluate button
    const evalBtn = page.locator('#btn-eval-release');
    await expect(evalBtn).toBeVisible();
    await evalBtn.click();

    // 4. Verify release readiness remains PASSED
    await expect(badge).toHaveText('Release Ready: PASSED');
  });
});







