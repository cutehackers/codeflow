import { test, expect } from './approval-fixture';

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

    // 5. Candidate Answer strip appears without VS-03 current authority.
    const answerStrip = page.locator('#current-answer-strip');
    await expect(answerStrip).toBeVisible({ timeout: 10000 });

    const answerStatement = page.locator('#current-answer-statement');
    await expect(answerStatement).toContainText('Evidence-backed Implementation Fact:');

    const answerStage = page.locator('#current-answer-stage');
    await expect(answerStage).toHaveText(/Q[1-4]|awaiting proof|unknown/);
    await expect(answerStage).not.toContainText('Verified');
    await expect(page.locator('#current-answer-requested')).not.toBeEmpty();
    await expect(page.locator('#current-answer-intent')).toContainText('revision');
    await expect(page.locator('#current-answer-agent-status')).toHaveText('not reported');
    await expect(page.locator('#projection-summary')).toContainText('Projection:');

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
    await expect(epochTag).toHaveText(/\[\d+\]/);

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
    await expect(freshnessBadge).toHaveText(/(historical|unknown|candidate basis|미확인|후보 basis)/i);
    await expect(freshnessBadge).not.toHaveText(/Current|Last Verified/);

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

  test('measures structural selection and logical scroll preservation on the versioned A14 corpus', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000);
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    const report = await page.evaluate(() => {
      // @ts-ignore — production functions are defined by the embedded FlowView script.
      const render = renderSemanticTaskView;
      // @ts-ignore
      const choose = selectStep;
      const corpusVersion = 'rflsc-vs03-view-corpus-v1';
      const stableIdentity = 'checkout.submit|application|call';

      const payload = (version: number, mode: 'compatible' | 'removed' | 'duplicate') => {
        const steps = Array.from({ length: 16 }, (_, index) => ({
          stepId: `generation-${version}-step-${index}`,
          ordinal: index + 1,
          name: `Checkout step ${index}`,
          structuralIdentity: index === 8 ? stableIdentity : `checkout.step.${index}|application|call`,
          layer: 'application',
          kind: 'call',
          technicalName: `Checkout.step${index}`,
          anchor: { repoRelativePath: 'app/page.tsx', startLine: 1, endLine: 1, enclosingSymbolPath: `Checkout.step${index}` },
          rules: [],
          evidenceRefs: []
        }));
        if (mode === 'removed') {
          steps[8].structuralIdentity = 'checkout.removed|application|call';
        } else if (mode === 'duplicate') {
          steps[9].structuralIdentity = stableIdentity;
        }
        const refs = steps.map(step => step.stepId);
        return {
          taskIntent: { revision: 1, request: { rawRequest: 'checkout submit flow' } },
          semanticMap: {
            schemaId: 'https://codeflow.local/schemas/rflsc.semantic-map-ir.v2.schema.json',
            schemaVersion: 2,
            mapId: `map-${version}`,
            generationId: `generation-${version}`,
            computedBasisId: `basis-${version}`,
            freshness: 'candidate',
            quality: { stage: 'Q3' },
            settlement: 'passed',
            summary: { requested: 'checkout submit flow', current: 'versioned compatible flow' },
            steps,
            unknowns: []
          },
          projection: { visibleStepRefs: refs, preservedStepRefs: refs, unknownBoundaryRefs: [], foldedSubflows: [] },
          currentProofVerified: false,
          enrichmentStatus: 'unavailable'
        };
      };

      render(payload(0, 'compatible'), false);
      const list = document.getElementById('timeline-list') as HTMLElement;
      list.style.height = '120px';
      list.style.overflow = 'auto';
      choose(8, false);
      const selected = list.querySelector('[data-hstep="8"]') as HTMLElement;
      list.scrollTop = Math.max(0, selected.offsetTop - 24);

      let numerator = 0;
      const denominator = 100;
      for (let version = 1; version <= denominator; version += 1) {
        render(payload(version, 'compatible'), true);
        const state = (window as any).__codeflowViewState;
        if (state?.preserved === true &&
            state?.identityLoss === false &&
            state?.selectedStructuralIdentity === stableIdentity &&
            state?.logicalScrollAnchor?.structuralIdentity === stableIdentity &&
            Number.isFinite(state?.logicalScrollAnchor?.offsetPx)) {
          numerator += 1;
        }
      }

      let excludedIdentityLoss = 0;
      render(payload(101, 'removed'), true);
      if ((window as any).__codeflowViewState?.identityLoss === true) excludedIdentityLoss += 1;

      render(payload(102, 'compatible'), false);
      choose(8, false);
      const resetSelected = list.querySelector('[data-hstep="8"]') as HTMLElement;
      list.scrollTop = Math.max(0, resetSelected.offsetTop - 24);
      render(payload(103, 'duplicate'), true);
      if ((window as any).__codeflowViewState?.identityLoss === true) excludedIdentityLoss += 1;

      return {
        corpusVersion,
        eligibleUpdates: denominator,
        preservedUpdates: numerator,
        excludedIdentityLoss,
        numerator,
        denominator,
        preservationPercent: (numerator / denominator) * 100,
        finalViewState: (window as any).__codeflowViewState
      };
    });

    await testInfo.attach('rflsc-vs03-a14-view-state-corpus.json', {
      body: JSON.stringify(report, null, 2),
      contentType: 'application/json'
    });

    expect(report.corpusVersion).toBe('rflsc-vs03-view-corpus-v1');
    expect(report.numerator).toBe(100);
    expect(report.denominator).toBe(100);
    expect(report.excludedIdentityLoss).toBe(2);
    expect(report.preservationPercent).toBeGreaterThanOrEqual(99);
    for (const field of [
      'schemaId', 'schemaVersion', 'viewId', 'streamId', 'generationId', 'displayBasis',
      'activityStatus', 'qualityStage', 'settlement', 'enrichmentStatus', 'connectionStatus',
      'selectedStepId', 'selectedStructuralIdentity', 'logicalScrollAnchor', 'visibleStepRefs',
      'preservedStepRefs', 'corpusVersion', 'identityLoss', 'preserved'
    ]) {
      expect(report.finalViewState).toHaveProperty(field);
    }
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
    await expect(page.locator('#current-answer-requested')).toBeVisible();
    await expect(page.locator('#current-answer-intent')).toBeVisible();
    await expect(page.locator('#current-answer-agent-status')).toHaveText('not reported');
    await expect(page.locator('#current-answer-statement')).toContainText('Evidence-backed Implementation Fact:');

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

  test('displays Semantic Approval & Grounding section and records human approval (VS-09)', async ({ page, approvalFixture }) => {
    await approvalFixture.open(page);

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

    // 4. Actual published receipt and durable history establish active state.
    await expect(page.locator('#approval-history-state')).toHaveText('active');
    await expect(page.locator('#approval-history-version')).toHaveText('1');
    await expect(page.locator('#approval-result-msg')).toContainText('승인 기록 생성됨');
  });

  test('renders only grounded inferred proposals and exposes activation disclosure (VS-08)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    const payload = (enrichment: any) => ({
      taskIntent: { revision: 3, request: { rawRequest: 'checkout submit flow' } },
      semanticMap: {
        schemaId: 'https://codeflow.local/schemas/rflsc.semantic-map-ir.v2.schema.json',
        schemaVersion: 2,
        mapId: 'map-vs08-browser',
        generationId: 'generation-vs08-browser',
        computedBasisId: 'basis-vs08-browser',
        validatedAgainstSnapshotId: 'snapshot-vs08-browser',
        freshness: 'current',
        quality: { stage: 'Q3' },
        settlement: 'passed',
        summary: { requested: 'checkout submit flow', current: 'current verified flow' },
        steps: [{
          stepId: 'step-submit', ordinal: 1, name: 'Submit checkout',
          structuralIdentity: 'checkout.submit|application|call',
          layer: 'application', kind: 'call', technicalName: 'Checkout.submit',
          anchor: { repoRelativePath: 'app/checkout.ts', startLine: 1, endLine: 1, enclosingSymbolPath: 'Checkout.submit' },
          rules: [], evidenceRefs: ['evidence-submit']
        }],
        unknowns: []
      },
      projection: { visibleStepRefs: ['step-submit'], preservedStepRefs: [], unknownBoundaryRefs: [], foldedSubflows: [] },
      currentProofVerified: true,
      enrichment
    });

    await page.evaluate((data) => {
      // @ts-ignore — production functions are defined by the embedded FlowView script.
      renderSemanticTaskView(data, false);
    }, payload({
      state: { status: 'unavailable', reason: 'model host is not measured' },
      fallback: { status: 'unknown', reason: 'deterministic enrichment unavailable' }
    }));

    await expect(page.locator('#badge-enrichment')).toHaveText('Enrichment: unavailable');
    await expect(page.locator('#proposal-card')).toHaveAttribute('data-epistemic-status', 'unknown');
    await expect(page.locator('#enrichment-fallback')).toBeVisible();
    await expect(page.locator('#model-activation-disclosure')).toBeHidden();

    await page.evaluate((data) => {
      // @ts-ignore — production functions are defined by the embedded FlowView script.
      renderSemanticTaskView(data, false);
    }, payload({
      state: { status: 'available', reason: '', capability: { modelId: 'local-slm', revision: 'r1' } },
      proposal: {
        epistemicStatus: 'inferred', authority: 'model', claimScope: 'display_only',
        targetSymbolPath: 'Checkout.submit', proposedCategory: 'business_rule',
        proposedTitle: 'Submit checkout', proposedRationale: 'Grounded in current verified evidence.'
      },
      disclosure: {
        modelId: 'local-slm', revision: 'r1', license: 'Apache-2.0', checksum: 'sha256:vs08',
        runtime: 'sandbox', dataBoundary: 'bounded evidence pack only',
        capabilityChange: 'semantic proposal enrichment', choiceRequired: true
      }
    }));

    await expect(page.locator('#badge-enrichment')).toHaveText('Enrichment: available');
    await expect(page.locator('#proposal-card')).toHaveAttribute('data-epistemic-status', 'inferred');
    await expect(page.locator('#proposal-target-symbol')).toHaveText('Checkout.submit');
    await expect(page.locator('#proposal-title')).toHaveText('Submit checkout');
    await expect(page.locator('#model-activation-disclosure')).toBeVisible();
    await expect(page.locator('#model-activation-disclosure')).toContainText('local-slm');
    await expect(page.locator('#model-activation-disclosure')).toContainText('bounded evidence pack only');
    await expect(page.locator('#btn-model-activate')).toBeVisible();
    await expect(page.locator('#btn-model-decline')).toBeVisible();
  });

  test('displays evidence-backed Domain Architecture and drills into the same generation (VS-07)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Section is present
    const onbSection = page.locator('#onboarding-domains-section');
    await expect(onbSection).toBeVisible();

    const badge = page.locator('#onboarding-coverage-badge');
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText('Level 1: System Map');

    // 2. Onboarding is downstream of a loaded semantic task view. Without a
    // repository/basis/generation/snapshot identity the public seam must stay
    // empty instead of inventing a default domain set.
    await page.locator('#query-input').fill('HomePage.handleQuickCheckout');
    await page.locator('#query-submit').click();
    await expect(page.locator('#current-answer-strip')).toBeVisible({ timeout: 10000 });

    // 3. Click Explore Domains
    const exploreBtn = page.locator('#btn-explore-domains');
    await expect(exploreBtn).toBeVisible();
    await exploreBtn.click();

    // 4. Verify the response carries immutable basis/coverage evidence.
    await expect(page.locator('#domain-cards-grid')).not.toBeEmpty();
    await expect(page.locator('#onboarding-total-domains')).not.toHaveText('0');
    await expect(page.locator('#onboarding-evidence-status')).toContainText('basis:');
    await expect(page.locator('#onboarding-evidence-status')).toContainText('generation:');

    // 5. Keyboard-select a real candidate and fetch level 2 from the same
    // generation. The browser must not render a fabricated flow list.
    const domainButton = page.locator('#domain-cards-grid button[aria-label]').first();
    await expect(domainButton).toBeVisible();
    await domainButton.focus();
    await domainButton.press('Enter');
    await expect(page.locator('#onboarding-catalog-container')).toBeVisible();
    await expect(page.locator('#representative-flows-list')).not.toBeEmpty();
    await expect(page.locator('#representative-flows-list')).not.toContainText('기본 흐름');
  });

  test('displays Release Capability section and validates release readiness gates (VS-10)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Section is present
    const relSection = page.locator('#release-capability-section');
    await expect(relSection).toBeVisible();

    const badge = page.locator('#release-ready-badge');
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText('Release Ready: NOT MEASURED');

    // 2. Metrics and evidence scope are visible
    await expect(page.locator('#metric-latency-p95')).toHaveText('not measured');
    await expect(page.locator('#metric-precision')).toHaveText('not measured');
    await expect(page.locator('#metric-regressions')).toHaveText('not measured');
    await expect(page.locator('#release-fallback-tier')).toHaveText('not measured');

    // 3. Click Evaluate button
    const evalBtn = page.locator('#btn-eval-release');
    await expect(evalBtn).toBeVisible();
    await evalBtn.click();

    // 4. Empty UI input must remain unmeasured and cannot fabricate a pass.
    await expect(badge).toHaveText('Release Ready: NOT MEASURED');
    await expect(page.locator('#slm-capabilities-list')).toHaveText('not measured');
  });
});
