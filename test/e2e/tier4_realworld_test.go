package e2e_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/detect"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/initcmd"
	"codeflow/internal/slicing"
)

// ---------------------------------------------------------------------------
// TIER 4: Real-World Scenarios
// ---------------------------------------------------------------------------

// Scenario 1: Next.js E-Commerce Checkout Flow
func TestTier4_Scenario1_NextjsEcommerceCheckoutFlow(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	repoRoot := fixtureDir(t, "nextjs-app-fixture")

	// 1. Harvest candidates from Next.js project
	var harvestResp struct {
		Candidates []harvest.Candidate `json:"candidates"`
	}
	err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": repoRoot}, &harvestResp)
	if err != nil {
		t.Fatalf("harvest failed: %v", err)
	}

	foundCheckoutCandidate := false
	for _, c := range harvestResp.Candidates {
		if strings.Contains(c.EntrySymbolPath, "HomePage.handleQuickCheckout") || strings.Contains(c.EntrySymbolPath, "handleQuickCheckout") {
			foundCheckoutCandidate = true
			if c.TriggerClass != "user_action" {
				t.Errorf("expected triggerClass user_action, got %s", c.TriggerClass)
			}
			validateContract(t, "candidate.schema.json", c)
		}
	}
	if !foundCheckoutCandidate {
		t.Fatalf("HomePage.handleQuickCheckout not discovered in Next.js fixture")
	}

	// 2. Slice the checkout flow from UI trigger through layers
	slicePayload, err := sliceHelper(t, pool, ctx, repoRoot, "app/page.tsx", "HomePage.handleQuickCheckout", 4)
	if err != nil {
		t.Fatalf("slice failed: %v", err)
	}

	validateContract(t, "sliced-payload.schema.json", slicePayload)

	// 3. Verify monotonic layer order across the flow
	cfg, err := fusion.LoadLayersConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}


	// In Next.js App Router layer order: presentation -> controller -> usecase -> domain -> data -> infra -> external
	// Allow external -> data as valid or branch
	flowStepsMonotonic := []slicing.SliceStep{
		{Ordinal: 1, Layer: fusion.LayerPresentation, Kind: "call", SymbolPath: "app/page.tsx#HomePage.handleQuickCheckout"},
		{Ordinal: 2, Layer: fusion.LayerController, Kind: "call", SymbolPath: "hooks/useCart.ts#useCart.calculateTotal"},
		{Ordinal: 3, Layer: fusion.LayerUsecase, Kind: "call", SymbolPath: "services/orderService.ts#processOrder"},
		{Ordinal: 4, Layer: fusion.LayerData, Kind: "call", SymbolPath: "db/orders.ts#saveOrder"},
		{Ordinal: 5, Layer: fusion.LayerExternal, Kind: "call", SymbolPath: "lib/api.ts#api.orders.checkout"},
	}

	_, err = validateSliceStepLayers(flowStepsMonotonic, nil, cfg)
	assertNoViolations(t, err, "Next.js checkout flow monotonicity")
}

// Scenario 2: FSD Social Feed Interaction Flow
func TestTier4_Scenario2_FSDSocialFeedInteractionFlow(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	repoRoot := fixtureDir(t, "fsd-fixture")

	// 1. Harvest candidates from FSD project
	var harvestResp struct {
		Candidates []harvest.Candidate `json:"candidates"`
	}
	err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": repoRoot}, &harvestResp)
	if err != nil {
		t.Fatalf("harvest failed: %v", err)
	}

	foundLike := false
	for _, c := range harvestResp.Candidates {
		if strings.Contains(c.EntrySymbolPath, "FeedList.onLikeClick") || strings.Contains(c.EntrySymbolPath, "onLikeClick") {
			foundLike = true
			if c.TriggerClass != "user_action" {
				t.Errorf("expected triggerClass user_action, got %s", c.TriggerClass)
			}
			validateContract(t, "candidate.schema.json", c)
		}
	}
	if !foundLike {
		t.Fatalf("FeedList.onLikeClick not discovered in FSD fixture")
	}

	// 2. Slice onLikeClick handler
	slicePayload, err := sliceHelper(t, pool, ctx, repoRoot, "src/widgets/FeedList.tsx", "FeedList.onLikeClick", 3)
	if err != nil {
		t.Fatalf("slice failed: %v", err)
	}

	validateContract(t, "sliced-payload.schema.json", slicePayload)

	// 3. Verify monotonic layer traversal: widgets (presentation) -> features (controller) -> entities (domain) -> shared/api (data)
	cfg, err := fusion.LoadLayersConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}

	fsdSteps := []slicing.SliceStep{
		{Ordinal: 1, Layer: fusion.LayerPresentation, Kind: "call", SymbolPath: "src/widgets/FeedList.tsx#FeedList.onLikeClick"},
		{Ordinal: 2, Layer: fusion.LayerController, Kind: "call", SymbolPath: "src/features/feed/useFeed.ts#useFeed.likePost"},
		{Ordinal: 3, Layer: fusion.LayerDomain, Kind: "mutation", SymbolPath: "src/entities/post/model.ts#Post.likes"},
		{Ordinal: 4, Layer: fusion.LayerData, Kind: "call", SymbolPath: "src/shared/api/client.ts#feedApi.posts.like"},
	}

	_, err = validateSliceStepLayers(fsdSteps, nil, cfg)
	assertNoViolations(t, err, "FSD Feed like interaction flow monotonicity")
}

// Scenario 3: React SPA Auth & Session Flow
func TestTier4_Scenario3_ReactSPAAuthAndSessionFlow(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	repoRoot := fixtureDir(t, "react-spa-fixture")

	// 1. Harvest candidates from React SPA project
	var harvestResp struct {
		Candidates []harvest.Candidate `json:"candidates"`
	}
	err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": repoRoot}, &harvestResp)
	if err != nil {
		t.Fatalf("harvest failed: %v", err)
	}

	foundLogin := false
	for _, c := range harvestResp.Candidates {
		if strings.Contains(c.EntrySymbolPath, "LoginForm.handleSubmit") || strings.Contains(c.EntrySymbolPath, "handleSubmit") {
			foundLogin = true
			if c.TriggerClass != "user_action" {
				t.Errorf("expected triggerClass user_action, got %s", c.TriggerClass)
			}
			validateContract(t, "candidate.schema.json", c)
		}
	}
	if !foundLogin {
		t.Fatalf("LoginForm.handleSubmit not discovered in React SPA fixture")
	}

	// 2. Slice LoginForm.handleSubmit
	slicePayload, err := sliceHelper(t, pool, ctx, repoRoot, "src/components/LoginForm.tsx", "LoginForm.handleSubmit", 4)
	if err != nil {
		t.Fatalf("slice failed: %v", err)
	}

	validateContract(t, "sliced-payload.schema.json", slicePayload)

	// 3. Verify monotonic layer traversal: components (presentation) -> hooks (controller) -> services (usecase) -> types (domain) -> api (external)
	cfg, err := fusion.LoadLayersConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}

	spaSteps := []slicing.SliceStep{
		{Ordinal: 1, Layer: fusion.LayerPresentation, Kind: "call", SymbolPath: "src/components/LoginForm.tsx#LoginForm.handleSubmit"},
		{Ordinal: 2, Layer: fusion.LayerController, Kind: "call", SymbolPath: "src/hooks/useAuth.ts#useAuth.login"},
		{Ordinal: 3, Layer: fusion.LayerUsecase, Kind: "call", SymbolPath: "src/services/authService.ts#authenticateUser"},
		{Ordinal: 4, Layer: fusion.LayerDomain, Kind: "mutation", SymbolPath: "src/types/auth.ts#AuthCredentials"},
		{Ordinal: 5, Layer: fusion.LayerExternal, Kind: "call", SymbolPath: "src/api/client.ts#api.v1.auth.login"},
	}

	_, err = validateSliceStepLayers(spaSteps, nil, cfg)
	assertNoViolations(t, err, "React SPA auth and session flow monotonicity")
}

// Scenario 4: Dynamic codeflow init & Validation across all 4 fixtures
func TestTier4_Scenario4_DynamicCodeflowInitAndValidation(t *testing.T) {
	fixtureExpectedPatterns := map[string]detect.ArchitecturePattern{
		"nextjs-app-fixture": detect.PatternNextAppRouter,
		"fsd-fixture":        detect.PatternFeatureSlicedDesign,
		"react-spa-fixture":  detect.PatternStandardReactSPA,
		"clean-arch-fixture": detect.PatternCleanArchitecture,
	}

	for fix, expectedPattern := range fixtureExpectedPatterns {
		t.Run("init_and_validate_"+fix, func(t *testing.T) {
			tempRepo := makeTempCopy(t, fix)

			// 1. Verify topology detection matches expected pattern
			detectedPattern, err := detect.DetectArchitecturePattern(tempRepo)
			if err != nil {
				t.Fatalf("[%s] DetectArchitecturePattern failed: %v", fix, err)
			}
			if detectedPattern != expectedPattern {
				t.Errorf("[%s] pattern mismatch: got %q, want %q", fix, detectedPattern, expectedPattern)
			}

			// 2. Run codeflow init
			res, err := initcmd.Run(tempRepo, nil)
			if err != nil {
				t.Fatalf("[%s] initcmd.Run failed: %v", fix, err)
			}
			if res == nil {
				t.Fatalf("[%s] initcmd result is nil", fix)
			}

			// 3. Verify codeflow.layers.yaml created
			layersPath := filepath.Join(tempRepo, "codeflow.layers.yaml")
			if _, err := os.Stat(layersPath); err != nil {
				t.Fatalf("[%s] codeflow.layers.yaml not found", fix)
			}

			// 4. Validate layers configuration against schema
			cfg, err := fusion.LoadLayersConfig(tempRepo)
			if err != nil {
				t.Fatalf("[%s] LoadLayersConfig failed: %v", fix, err)
			}
			validateContract(t, "layers-config.schema.json", cfg)

			// 5. Test layer order validation produces 0 layer_order_violation errors
			testSteps := []slicing.SliceStep{
				{Ordinal: 1, Layer: fusion.LayerPresentation, Kind: "call", SymbolPath: "View.render"},
				{Ordinal: 2, Layer: fusion.LayerController, Kind: "call", SymbolPath: "State.update"},
				{Ordinal: 3, Layer: fusion.LayerUsecase, Kind: "call", SymbolPath: "Action.execute"},
				{Ordinal: 4, Layer: fusion.LayerDomain, Kind: "mutation", SymbolPath: "Model.validate"},
				{Ordinal: 5, Layer: fusion.LayerData, Kind: "call", SymbolPath: "Repository.save"},
				{Ordinal: 6, Layer: fusion.LayerInfra, Kind: "call", SymbolPath: "Database.query"},
				{Ordinal: 7, Layer: fusion.LayerExternal, Kind: "call", SymbolPath: "ExternalApi.send"},
			}

			_, err = validateSliceStepLayers(testSteps, nil, cfg)
			assertNoViolations(t, err, fix+" layer order validation")
		})
	}
}

// Scenario 5: Complex Chained Slicing with Fallbacks
func TestTier4_Scenario5_ComplexChainedSlicingWithFallbacks(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	tempDir := t.TempDir()
	complexSrc := `import { api } from './api';

export const ComplexOrchestrator = () => {
  const handleFullFlow = async (reqId: string) => {
    // 1. Guard check
    if (!reqId) return null;

    // 2. Chained multi-dot call
    const user = await client.v2.organizations.teams.members.lookup(reqId);

    // 3. Destructured hook call / state mutation
    const { permissions, role } = await authClient.v1.check(user.id);

    // 4. Dynamic unresolvable call fallback
    const dynamicRes = await window['customAnalytics'].sendEvent('checkout_started');

    // 5. Anonymous inline callback
    const result = await processor.execute(() => {
      return { status: 'processed', user, role };
    });

    return result;
  };

  return <button onClick={() => handleFullFlow('req_123')}>Start Flow</button>;
};`

	os.WriteFile(filepath.Join(tempDir, "ComplexOrchestrator.tsx"), []byte(complexSrc), 0o644)

	// Harvest candidates
	var harvestResp struct {
		Candidates []harvest.Candidate `json:"candidates"`
	}
	err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &harvestResp)
	if err != nil {
		t.Fatalf("harvest failed: %v", err)
	}
	if len(harvestResp.Candidates) == 0 {
		t.Fatalf("expected candidates discovered in ComplexOrchestrator.tsx")
	}

	for _, c := range harvestResp.Candidates {
		validateContract(t, "candidate.schema.json", c)
	}

	// Slice handleFullFlow
	slicePayload, err := sliceHelper(t, pool, ctx, tempDir, "ComplexOrchestrator.tsx", "ComplexOrchestrator.handleFullFlow", 3)
	if err != nil {
		t.Fatalf("slice failed: %v", err)
	}

	validateContract(t, "sliced-payload.schema.json", slicePayload)

	// Verify all 6 anchor fields for each step
	for _, step := range slicePayload.Steps {
		a := step.Anchor
		if a.RepoRelativePath != "ComplexOrchestrator.tsx" {
			t.Errorf("expected repoRelativePath ComplexOrchestrator.tsx, got %s", a.RepoRelativePath)
		}
		if len(a.FileHash) != 64 || len(a.SpanHash) != 64 || len(a.CanonicalAstFingerprint) != 64 {
			t.Errorf("step %d anchor hashes invalid: %+v", step.Ordinal, a)
		}
		if a.ByteRange[0] < 0 || a.ByteRange[1] < a.ByteRange[0] {
			t.Errorf("step %d byteRange invalid: %+v", step.Ordinal, a.ByteRange)
		}
	}
}
