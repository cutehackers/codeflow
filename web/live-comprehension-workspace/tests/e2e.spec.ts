import { test, expect } from './approval-fixture';

test.describe('FlowView Live Semantic Comprehension Workspace E2E', () => {
  test('source-unavailable context clears exact highlighting but preserves limitation and relation (VS-11)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');
    let sourceRequests = 0;
    await page.route('**/api/source?*', route => { sourceRequests++; return route.fulfill({body:'unsafe replacement'}); });
    await page.route('**/api/flow/context?*', route => route.fulfill({json:{
      precision:'unavailable',displayedLines:[],sourceLimitation:'선택한 스냅샷 소스가 없습니다',
      structuralContext:{status:'unknown'},directRelation:{description:'이어지는 호출: Service.logout'},
    }}));
    await page.evaluate(() => {
      // Simulate the state left by a previously selected exact statement.
      document.getElementById('flow-context-precision')!.textContent='정확한 문장 (exact)';
      // @ts-ignore
      renderSemanticTaskView({taskIntent:{revision:1,request:{rawRequest:'review'}},semanticMap:{mapId:'review',generationId:'review',computedBasisId:'review',quality:{stage:'Q2'},summary:{requested:'review',current:'review'},steps:[{stepId:'review',ordinal:1,name:'review',structuralIdentity:'review',anchor:{repoRelativePath:'main.dart',startLine:1,endLine:3,enclosingSymbolPath:'Example'},evidenceRefs:[]}],unknowns:[]},projection:{visibleStepRefs:['review'],preservedStepRefs:[],unknownBoundaryRefs:[]}},false);
    });
    await expect(page.locator('#flow-context-precision')).toHaveText('문맥 제한 (unavailable)');
    await expect(page.locator('#flow-source-limitation')).toBeVisible();
    await expect(page.locator('#flow-relation-header')).toContainText('Service.logout');
    await expect(page.locator('#flow-structural-context')).toContainText('확인할 수 없음');
    await expect(page.locator('#code .hit')).toHaveCount(0);
    expect(sourceRequests).toBe(0);
  });

  test('opens an MCP Live Semantic Map URL without requiring a second query', async ({ page }) => {
    const request = 'Analyze the quick checkout flow while its code changes';
    await page.goto('http://127.0.0.1:4589/?token=testtoken&live=1&request=' + encodeURIComponent(request) + '&entrySymbol=' + encodeURIComponent('app/page.tsx#HomePage.onQuickCheckout'));

    await expect(page.locator('#query-input')).toHaveValue(request);
    await expect(page.locator('body')).toHaveAttribute('data-view', 'live-semantic-map');
    await expect(page.locator('#code-flow .code-card').first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator('#code-flow .source').first()).toBeVisible();
  });

  test('loads the exact persisted Live Semantic Map after a published generation', async ({ page }) => {
    const request = 'Analyze the quick checkout flow while its code changes';
    const entrySymbol = 'app/page.tsx#HomePage.onQuickCheckout';
    await page.goto('http://127.0.0.1:4589/?token=testtoken&live=1&request=' + encodeURIComponent(request) + '&entrySymbol=' + encodeURIComponent(entrySymbol));
    await expect(page.locator('#code-flow .code-card').first()).toBeVisible({ timeout: 10000 });

    const published = await page.evaluate(() => {
      // @ts-ignore — production Live state is defined by the embedded script.
      const data = JSON.parse(JSON.stringify(state.data));
      data.semanticMap.generationId = 'generation-event-99';
      data.semanticMap.computedBasisId = 'basis-event-99';
      data.semanticMap.validatedAgainstSnapshotId = 'snapshot-event-99';
      if (data.semanticMap.basis) {
        data.semanticMap.basis.computedBasisId = 'basis-event-99';
        data.semanticMap.basis.computedWorkspaceSnapshotId = 'snapshot-event-99';
      }
      if (data.projection) {
        data.projection.generationId = 'generation-event-99';
        data.projection.computedBasisId = 'basis-event-99';
      }
      for (const context of Object.values(data.flowContexts || {}) as any[]) {
        context.generationId = 'generation-event-99';
        context.snapshotId = 'snapshot-event-99';
      }
      return data;
    });
    await page.route('**/api/live/generation?*', route => route.fulfill({ json: published }));
    let taskRequeries = 0;
    page.on('request', candidate => {
      if (new URL(candidate.url()).pathname === '/api/task/view') taskRequeries += 1;
    });
    const refresh = page.waitForRequest(candidate => {
      const url = new URL(candidate.url());
      return url.pathname === '/api/live/generation'
        && url.searchParams.get('generationId') === 'generation-event-99'
        && url.searchParams.get('computedBasisId') === 'basis-event-99'
        && url.searchParams.get('snapshotId') === 'snapshot-event-99';
    });
    await page.evaluate(() => {
      // The production EventSource listener receives this event after the
      // versioned edit compiler publishes a new generation.
      // @ts-ignore
      onLiveEvent('generation.published', new MessageEvent('generation.published', { data: JSON.stringify({
        generationId: 'generation-event-99', computedBasisId: 'basis-event-99', validatedAgainstSnapshotId: 'snapshot-event-99',
      }) }));
    });
    await refresh;
    await expect(page.locator('#live-notice')).toContainText('검증된 흐름');
    expect(taskRequeries).toBe(0);
  });

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

    // 4. Click specific candidate: "app/page.tsx#HomePage.onQuickCheckout"
    const quickCheckoutBtn = disambiguation.locator('button', { hasText: 'HomePage.onQuickCheckout' });
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

  test('keeps workspace change state out of Static FlowView and on the Live surface', async ({ page }) => {
    const requestedPaths: string[] = [];
    page.on('request', request => requestedPaths.push(new URL(request.url()).pathname));
    await page.goto('http://127.0.0.1:4589/?token=testtoken');
    await expect(page.locator('#workspace-activity-badge')).toHaveCount(0);
    await expect(page.locator('#workspace-epoch-tag')).toHaveCount(0);
    await expect(page.locator('#workspace-pending-count')).toHaveCount(0);
    await expect(page.locator('#workspace-analysis-lag')).toHaveCount(0);
    await expect(page.locator('#workspace-scope-tag')).toHaveCount(0);
    await page.waitForTimeout(100);
    expect(requestedPaths).not.toContain('/api/workspace/activity');
    expect(requestedPaths).not.toContain('/api/workspace/stream');

    await page.goto('http://127.0.0.1:4589/live?token=testtoken');
    await expect(page.locator('body')).toHaveAttribute('data-view', 'live-semantic-map');
    await expect(page.locator('#live-notice')).toBeVisible();
  });

  test('displays independent status axes, SSE connection, and preserves step selection', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    // 1. Perform semantic query
    const queryInput = page.locator('#query-input');
    await queryInput.fill('HomePage.onQuickCheckout');
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
      if (typeof onSemanticQuery === 'function') {
        // @ts-ignore
        onSemanticQuery(null, true);
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
    await queryInput.fill('HomePage.onQuickCheckout');
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
    await page.locator('#query-input').fill('HomePage.onQuickCheckout');
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

  test('displays precise Flow Context, structural context, direct relation, and expansion controls (VS-11)', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');

    const exactPayload = {
      taskIntent: { revision: 1, request: { rawRequest: 'checkout submit flow' } },
      semanticMap: {
        schemaId: 'https://codeflow.local/schemas/rflsc.semantic-map-ir.v2.schema.json',
        schemaVersion: 2,
        mapId: 'map-vs11',
        generationId: 'generation-vs11',
        computedBasisId: 'basis-vs11',
        freshness: 'current',
        quality: { stage: 'Q4' },
        summary: { requested: 'checkout submit flow', current: 'Exact statement flow context' },
        steps: [
          {
            stepId: 'step-on-tap',
            ordinal: 1,
            name: 'Submit Button Tap',
            structuralIdentity: 'checkout.submit|ui|event',
            layer: 'presentation',
            kind: 'event',
            technicalName: 'SubmitButton.onTap',
            anchor: {
              repoRelativePath: 'lib/src/submit_button.dart',
              startLine: 12,
              endLine: 12,
              enclosingSymbolPath: 'SubmitButton.build'
            },
            flowContext: {
              precision: 'exact',
              statement: {
                nodeKind: 'ExpressionStatement',
                startLine: 12,
                endLine: 12,
                text: 'await checkoutService.submit();'
              },
              structuralContext: {
                status: 'present',
                nodeKind: 'callback',
                label: 'onTap',
                startLine: 10,
                endLine: 14
              },
              callableSignature: {
                name: 'build',
                signature: 'Widget build(BuildContext context)',
                startLine: 5,
                endLine: 5
              },
              directRelation: {
                predecessorStepId: '',
                successorStepId: 'step-validate',
                callTargetSymbol: 'CheckoutService.submit',
                relationKind: 'call'
              },
              displayedLines: [
                { lineNumber: 5, text: 'Widget build(BuildContext context) {', isSig: true },
                { lineNumber: 10, text: '  onTap: () async {', isStruct: true },
                { lineNumber: 12, text: '    await checkoutService.submit();', isHit: true, selection: {before:'    ',text:'await checkoutService.submit();',after:''} },
                { lineNumber: 14, text: '  },', isStruct: true }
              ],
              currentExpansion: 'flow_context'
            },
            rules: [],
            evidenceRefs: ['evidence-tap']
          },
          {
            stepId: 'step-validate',
            ordinal: 2,
            name: 'Validate Cart',
            structuralIdentity: 'checkout.validate|domain|rule',
            layer: 'domain',
            kind: 'rule',
            technicalName: 'CartValidator.validate',
            anchor: {
              repoRelativePath: 'lib/src/cart_validator.dart',
              startLine: 20,
              endLine: 20,
              enclosingSymbolPath: 'CartValidator.validate'
            },
            flowContext: {
              precision: 'exact',
              statement: {
                nodeKind: 'ExpressionStatement',
                startLine: 20,
                endLine: 20,
                text: 'if (cart.isEmpty) throw CartEmptyException();'
              },
              structuralContext: {
                status: 'none',
                nodeKind: 'none',
                label: '',
                startLine: 0,
                endLine: 0
              },
              callableSignature: {
                name: 'validate',
                signature: 'void validate(Cart cart)',
                startLine: 18,
                endLine: 18
              },
              directRelation: {
                predecessorStepId: 'step-on-tap',
                successorStepId: '',
                callTargetSymbol: '',
                relationKind: 'predecessor',
                description: '직접 선행: step-on-tap · 후행 단계 없음'
              },
              displayedLines: [
                { lineNumber: 18, text: 'void validate(Cart cart) {', isSig: true },
                { lineNumber: 20, text: '  if (cart.isEmpty) throw CartEmptyException();', isHit: true },
                { lineNumber: 22, text: '}', isSig: true }
              ],
              currentExpansion: 'flow_context'
            },
            rules: [],
            evidenceRefs: ['evidence-val']
          }
        ],
        unknowns: []
      },
      projection: { visibleStepRefs: ['step-on-tap', 'step-validate'], preservedStepRefs: [], unknownBoundaryRefs: [], foldedSubflows: [] },
      currentProofVerified: true
    };

    // 1. Render task view with exact flow context
    await page.evaluate((data) => {
      // @ts-ignore
      renderSemanticTaskView(data, false);
    }, exactPayload);

    // 2. Precision badge displays exact statement
    const precBadge = page.locator('#flow-context-precision');
    await expect(precBadge).toBeVisible();
    await expect(precBadge).toHaveText('정확한 문장 (exact)');
    await expect(precBadge).toHaveAttribute('role', 'status');

    // 3. Callable signature and structural context are separated
    await expect(page.locator('#flow-callable-signature')).toHaveText('Widget build(BuildContext context)');
    await expect(page.locator('#flow-structural-context')).toContainText('감싸는 문맥: callback (onTap)');

    // 4. Selected statement line is marked with .hit; structural context lines are .struct; signature is .sig
    const hitLines = page.locator('#code .line.hit');
    await expect(hitLines).toHaveCount(1);
    await expect(hitLines.first()).toContainText('await checkoutService.submit();');
    await expect(page.locator('#code .selected-source')).toHaveText('await checkoutService.submit();');

    const structLines = page.locator('#code .line.struct');
    await expect(structLines).toHaveCount(2);
    await expect(structLines.first()).toContainText('onTap: () async {');

    const sigLines = page.locator('#code .line.sig');
    await expect(sigLines).toHaveCount(1);
    await expect(sigLines.first()).toContainText('Widget build(BuildContext context) {');

    // 5. Direct relation header shows direct relations
    const relationHeader = page.locator('#flow-relation-header');
    await expect(relationHeader).toContainText('호출 대상: CheckoutService.submit');
    await expect(relationHeader).toContainText('직후: step-validate');

    // 6. Expansion controls toggle aria-pressed
    const btnDefault = page.locator('#btn-flow-context-default');
    const btnCallable = page.locator('#btn-expand-callable');
    const btnFile = page.locator('#btn-expand-file');

    await expect(btnDefault).toHaveAttribute('aria-pressed', 'true');
    await expect(btnCallable).toHaveAttribute('aria-pressed', 'false');
    await expect(btnFile).toHaveAttribute('aria-pressed', 'false');

    await btnCallable.click();
    await expect(btnCallable).toHaveAttribute('aria-pressed', 'true');
    await expect(btnDefault).toHaveAttribute('aria-pressed', 'false');

    await btnFile.click();
    await expect(btnFile).toHaveAttribute('aria-pressed', 'true');
    await expect(btnCallable).toHaveAttribute('aria-pressed', 'false');

    await btnDefault.click();
    await expect(btnDefault).toHaveAttribute('aria-pressed', 'true');

    // 7. Select step 2: no enclosing callback/condition/builder (structural context is 'none')
    await page.evaluate(() => {
      // @ts-ignore
      selectStep(1);
    });

    await expect(page.locator('#flow-structural-context')).toContainText('직접 감싸는 제어/콜백 문맥 없음 (none)');
    await expect(relationHeader).toContainText('직접 선행: step-on-tap');
    await expect(precBadge).toHaveText('정확한 문장 (exact)');

    // 8. Broad-anchor fallback / missing proof: precision falls back to unknown/unavailable
    const broadPayload = {
      taskIntent: { revision: 1, request: { rawRequest: 'broad anchor step' } },
      semanticMap: {
        schemaId: 'https://codeflow.local/schemas/rflsc.semantic-map-ir.v2.schema.json',
        schemaVersion: 2,
        mapId: 'map-vs11-broad',
        generationId: 'generation-vs11-broad',
        computedBasisId: 'basis-vs11-broad',
        freshness: 'candidate',
        quality: { stage: 'Q2' },
        summary: { requested: 'broad anchor step', current: 'Broad anchor fallback' },
        steps: [
          {
            stepId: 'step-broad',
            ordinal: 1,
            name: 'Broad Class Anchor',
            structuralIdentity: 'checkout.page|ui|view',
            layer: 'presentation',
            kind: 'view',
            technicalName: 'CheckoutPage',
            anchor: {
              repoRelativePath: 'lib/src/checkout_page.dart',
              startLine: 1,
              endLine: 50,
              enclosingSymbolPath: 'CheckoutPage'
            },
            flowContext: {
              precision: 'unknown',
              statement: null,
              structuralContext: { status: 'none', nodeKind: 'none', label: '', startLine: 0, endLine: 0 },
              callableSignature: { name: 'CheckoutPage', signature: 'class CheckoutPage', startLine: 1, endLine: 1 },
              directRelation: { predecessorStepId: '', successorStepId: '', callTargetSymbol: '', relationKind: 'none' },
              sourceLimitation: '정확한 실행 문장을 특정할 수 없어 주변 문맥 또는 선언부로 대체 표시합니다.',
              displayedLines: [
                { lineNumber: 1, text: 'class CheckoutPage {', role: 'fallback' },
                { lineNumber: 2, text: '  // body', role: 'fallback' }
              ],
              currentExpansion: 'flow_context'
            },
            rules: [],
            evidenceRefs: []
          }
        ],
        unknowns: []
      },
      projection: { visibleStepRefs: ['step-broad'], preservedStepRefs: [], unknownBoundaryRefs: [], foldedSubflows: [] },
      currentProofVerified: false
    };

    await page.evaluate((data) => {
      // @ts-ignore
      renderSemanticTaskView(data, false);
    }, broadPayload);

    await expect(precBadge).toHaveText('알 수 없음 (unknown)');
    // Under broad anchor fallback, .hit lines MUST NOT be present
    await expect(page.locator('#code .line.hit')).toHaveCount(0);
    // Source limitation warning must be shown
    const limitationEl = page.locator('#flow-source-limitation');
    await expect(limitationEl).toBeVisible();
    await expect(limitationEl).toContainText('정확한 실행 문장을 특정할 수 없어');
  });
});
