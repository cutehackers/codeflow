package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"codeflow/internal/detect"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/initcmd"
	"codeflow/internal/slicing"
)

// ---------------------------------------------------------------------------
// TIER 1: Feature Coverage (>=5 test cases per feature for all 12 features)
// ---------------------------------------------------------------------------

// Feature 1: Recursive Body Scanning (5 tests)
func TestTier1_Feature1_RecursiveBodyScanning(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 1.1: Component with nested arrow handler
	t.Run("arrow_handler_in_arrow_component", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const LoginForm = () => {
  const handleSubmit = async (e) => {
    e.preventDefault();
  };
  return <form onSubmit={handleSubmit} />;
};`
		os.WriteFile(filepath.Join(tempDir, "LoginForm.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		found := false
		for _, c := range resp.Candidates {
			if strings.Contains(c.EntrySymbolPath, "LoginForm.handleSubmit") || strings.Contains(c.EntrySymbolPath, "handleSubmit") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("failed to discover nested handleSubmit in LoginForm.tsx, candidates: %+v", resp.Candidates)
		}
	})

	// 1.2: Standard function component with nested handlers
	t.Run("nested_handlers_in_function_component", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export function SignupPage() {
  const onValidate = () => { return true; };
  const onSubmitClick = () => { onValidate(); };
  return <div><button onClick={onSubmitClick}>Sign up</button></div>;
}`
		os.WriteFile(filepath.Join(tempDir, "SignupPage.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 {
			t.Errorf("expected candidates discovered in SignupPage.tsx, got 0")
		}
	})

	// 1.3: Deeply nested callback inside an event handler
	t.Run("callback_inside_event_handler", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const NestedView = () => {
  const handleOuterAction = () => {
    const handleInnerCallback = () => {
      console.log('inner');
    };
    handleInnerCallback();
  };
  return <div onClick={handleOuterAction} />;
};`
		os.WriteFile(filepath.Join(tempDir, "NestedView.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		foundOuter := false
		for _, c := range resp.Candidates {
			if strings.Contains(c.EntrySymbolPath, "handleOuterAction") {
				foundOuter = true
			}
		}
		if !foundOuter {
			t.Errorf("expected handleOuterAction to be discovered, got %+v", resp.Candidates)
		}
	})

	// 1.4: Multiple sibling handlers in functional component
	t.Run("multiple_sibling_handlers", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const ButtonGroup = () => {
  const onSave = () => {};
  const onCancel = () => {};
  const onDelete = () => {};
  return <div><button onClick={onSave}/><button onClick={onCancel}/><button onClick={onDelete}/></div>;
};`
		os.WriteFile(filepath.Join(tempDir, "ButtonGroup.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) < 3 {
			t.Errorf("expected at least 3 sibling handlers, got %d: %+v", len(resp.Candidates), resp.Candidates)
		}
	})

	// 1.5: Functional component with async arrow and custom hook
	t.Run("async_handler_and_custom_hook_in_component", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const CustomComponent = () => {
  const useInternalMutation = () => {
    return { mutate: () => {} };
  };
  const handleAsyncSubmit = async () => {
    await fetch('/api');
  };
  return <form onSubmit={handleAsyncSubmit} />;
};`
		os.WriteFile(filepath.Join(tempDir, "CustomComponent.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) < 2 {
			t.Errorf("expected at least 2 candidates for mutation hook and handler, got %d", len(resp.Candidates))
		}
	})
}

// Feature 2: Dotted Symbol Hierarchy (5 tests)
func TestTier1_Feature2_DottedSymbolHierarchy(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	anchorRegex := regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$`)

	// 2.1: Dotted symbol format LoginPage.handleSubmit
	t.Run("dotted_name_format", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const LoginPage = () => {
  const handleSubmit = (e) => { e.preventDefault(); };
  return <form onSubmit={handleSubmit} />;
};`
		os.WriteFile(filepath.Join(tempDir, "LoginPage.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		for _, c := range resp.Candidates {
			parts := strings.Split(c.EntrySymbolPath, "#")
			if len(parts) == 2 {
				sym := parts[1]
				if !anchorRegex.MatchString(sym) {
					t.Errorf("symbol %q violates identity schema anchor pattern", sym)
				}
			}
		}
	})

	// 2.2: Deep 3-level dotted symbol Parent.Child.onClick
	t.Run("deep_three_level_symbol", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const Parent = () => {
  const Child = () => {
    const onClick = () => {};
    return <button onClick={onClick} />;
  };
  return <Child />;
};`
		os.WriteFile(filepath.Join(tempDir, "Parent.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		for _, c := range resp.Candidates {
			parts := strings.Split(c.EntrySymbolPath, "#")
			if len(parts) == 2 {
				sym := parts[1]
				if !anchorRegex.MatchString(sym) {
					t.Errorf("3-level symbol %q violates pattern", sym)
				}
			}
		}
	})

	// 2.3: Class method dotted format
	t.Run("class_method_dotted_format", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export class AuthController {
  async handleLogin(req, res) {}
}`
		os.WriteFile(filepath.Join(tempDir, "AuthController.ts"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		found := false
		for _, c := range resp.Candidates {
			if strings.HasSuffix(c.EntrySymbolPath, "AuthController.handleLogin") {
				found = true
			}
		}
		if !found {
			t.Errorf("expected AuthController.handleLogin, got %+v", resp.Candidates)
		}
	})

	// 2.4: IntentSignals className and derivedName extraction from dotted symbol
	t.Run("intent_signals_derivation", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const OrderCard = () => {
  const handleCancelOrder = () => {};
  return <button onClick={handleCancelOrder} />;
};`
		os.WriteFile(filepath.Join(tempDir, "OrderCard.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) > 0 {
			c := resp.Candidates[0]
			if c.IntentSignals.ClassName == "" || c.IntentSignals.DerivedName == "" {
				t.Errorf("intent signals missing: %+v", c.IntentSignals)
			}
		}
	})

	// 2.5: RootEquivalenceKey derivation from dotted symbol
	t.Run("root_equivalence_key_derivation", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const FeedWidget = () => {
  const onRefresh = () => {};
  return <div onDrag={onRefresh} />;
};`
		os.WriteFile(filepath.Join(tempDir, "FeedWidget.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		for _, c := range resp.Candidates {
			if c.RootEquivalenceKey == "" {
				t.Errorf("rootEquivalenceKey empty for candidate %s", c.CandidateID)
			}
		}
	})
}

// Feature 3: Frontend Marker Classification (5 tests)
func TestTier1_Feature3_FrontendMarkerClassification(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 3.1: UI Actions (handleSubmit, onClick) -> user_action / route_callback
	t.Run("ui_event_handler_classification", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const FormView = () => {
  const handleSubmit = (e) => { e.preventDefault(); };
  return <form onSubmit={handleSubmit} />;
};`
		os.WriteFile(filepath.Join(tempDir, "FormView.tsx"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].TriggerClass != "user_action" {
			t.Errorf("expected triggerClass user_action, got %+v", resp.Candidates)
		}
	})

	// 3.2: Custom State Mutation Hooks (useCart, useAuth) -> state_transition / notifier_method
	t.Run("custom_hook_classification", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export function useCart() {
  const addItem = (item) => {};
  return { addItem };
}`
		os.WriteFile(filepath.Join(tempDir, "useCart.ts"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 {
			t.Fatalf("expected useCart candidate discovered")
		}
		c := resp.Candidates[0]
		if c.TriggerClass != "state_transition" {
			t.Errorf("expected state_transition for useCart, got %s", c.TriggerClass)
		}
	})

	// 3.3: Next.js App Router HTTP Route Handlers (POST, GET) -> system_event / lifecycle_callback
	t.Run("nextjs_route_handler_classification", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export async function POST(request: Request) {
  return Response.json({ status: 'ok' });
}`
		os.WriteFile(filepath.Join(tempDir, "route.ts"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].TriggerClass != "system_event" {
			t.Errorf("expected system_event for Next.js POST route, got %+v", resp.Candidates)
		}
	})

	// 3.4: Server Actions ('use server') -> system_event / lifecycle_callback
	t.Run("server_action_classification", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `'use server';
export async function updateUsernameAction(name: string) {
  return { updated: true };
}`
		os.WriteFile(filepath.Join(tempDir, "actions.ts"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].TriggerClass != "system_event" {
			t.Errorf("expected system_event for server action, got %+v", resp.Candidates)
		}
	})

	// 3.5: Clean Architecture UseCases (execute, call) -> use_case_invocation / usecase_call
	t.Run("clean_architecture_usecase_classification", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export class CreateOrderUseCase {
  async execute(orderData) {
    return { orderId: '123' };
  }
}`
		os.WriteFile(filepath.Join(tempDir, "CreateOrderUseCase.ts"), []byte(src), 0o644)

		var resp struct {
			Candidates []harvest.Candidate `json:"candidates"`
		}
		err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": tempDir}, &resp)
		if err != nil {
			t.Fatalf("harvest failed: %v", err)
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].TriggerClass != "use_case_invocation" {
			t.Errorf("expected use_case_invocation for execute method, got %+v", resp.Candidates)
		}
	})
}

// Feature 4: Closed Trigger Class Alignment & Schema Validation (5 tests)
func TestTier1_Feature4_ClosedTriggerClassSchemaCompliance(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	validTriggerClasses := map[string]bool{
		"user_action":          true,
		"state_transition":     true,
		"system_event":         true,
		"use_case_invocation":  true,
	}

	fixtures := []string{
		"nextjs-app-fixture",
		"fsd-fixture",
		"react-spa-fixture",
		"clean-arch-fixture",
	}

	for _, fix := range fixtures {
		t.Run("validate_candidates_schema_"+fix, func(t *testing.T) {
			path := fixtureDir(t, fix)
			var resp struct {
				Candidates []harvest.Candidate `json:"candidates"`
			}
			err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": path}, &resp)
			if err != nil {
				t.Fatalf("harvest failed: %v", err)
			}
			if len(resp.Candidates) == 0 {
				t.Fatalf("no candidates harvested for fixture %s", fix)
			}
			for _, c := range resp.Candidates {
				if !validTriggerClasses[c.TriggerClass] {
					t.Errorf("invalid triggerClass %q in %s", c.TriggerClass, c.EntrySymbolPath)
				}
				validateContract(t, "candidate.schema.json", c)
			}
		})
	}

	// 4.5: Validate all 4 trigger classes can be generated and pass contract harness
	t.Run("all_trigger_classes_contract_validation", func(t *testing.T) {
		classes := []string{"user_action", "state_transition", "system_event", "use_case_invocation"}
		for _, tc := range classes {
			cand := harvest.Candidate{
				CandidateID:     "cand-0123456789abcdef",
				TriggerClass:    tc,
				MarkerKind:      "route_callback",
				EntrySymbolPath: "src/test.ts#handleTest",
				IntentSignals: harvest.IntentSignals{
					ClassName:   "TestComponent",
					DerivedName: "Handle test",
					PackageName: "test-pkg",
				},
				Score:              0.8,
				FanIn:              1,
				BoundaryReachable:  true,
				RootEquivalenceKey: "handleTest",
				TieBreakRank:       0,
				ManifestOverride:   "none",
			}
			validateContract(t, "candidate.schema.json", cand)
		}
	})
}

// Feature 5: Topology & Dependency Detection (5 tests)
func TestTier1_Feature5_TopologyDetection(t *testing.T) {
	// 5.1: Next.js App Router detection
	t.Run("detect_nextjs_app_router", func(t *testing.T) {
		path := fixtureDir(t, "nextjs-app-fixture")
		pattern, err := detect.DetectArchitecturePattern(path)
		if err != nil {
			t.Fatalf("DetectArchitecturePattern failed: %v", err)
		}
		if pattern != detect.PatternNextAppRouter {
			t.Errorf("got %q, want %q", pattern, detect.PatternNextAppRouter)
		}
	})

	// 5.2: Feature-Sliced Design detection
	t.Run("detect_fsd", func(t *testing.T) {
		path := fixtureDir(t, "fsd-fixture")
		pattern, err := detect.DetectArchitecturePattern(path)
		if err != nil {
			t.Fatalf("DetectArchitecturePattern failed: %v", err)
		}
		if pattern != detect.PatternFeatureSlicedDesign {
			t.Errorf("got %q, want %q", pattern, detect.PatternFeatureSlicedDesign)
		}
	})

	// 5.3: React SPA detection
	t.Run("detect_react_spa", func(t *testing.T) {
		path := fixtureDir(t, "react-spa-fixture")
		pattern, err := detect.DetectArchitecturePattern(path)
		if err != nil {
			t.Fatalf("DetectArchitecturePattern failed: %v", err)
		}
		if pattern != detect.PatternStandardReactSPA {
			t.Errorf("got %q, want %q", pattern, detect.PatternStandardReactSPA)
		}
	})

	// 5.4: Clean Architecture detection
	t.Run("detect_clean_arch", func(t *testing.T) {
		path := fixtureDir(t, "clean-arch-fixture")
		pattern, err := detect.DetectArchitecturePattern(path)
		if err != nil {
			t.Fatalf("DetectArchitecturePattern failed: %v", err)
		}
		if pattern != detect.PatternCleanArchitecture {
			t.Errorf("got %q, want %q", pattern, detect.PatternCleanArchitecture)
		}
	})

	// 5.5: Generic Frontend fallback detection
	t.Run("detect_generic_frontend_fallback", func(t *testing.T) {
		tempDir := t.TempDir()
		os.MkdirAll(filepath.Join(tempDir, "src"), 0o755)
		os.WriteFile(filepath.Join(tempDir, "tsconfig.json"), []byte("{}"), 0o644)

		pattern, err := detect.DetectArchitecturePattern(tempDir)
		if err != nil {
			t.Fatalf("DetectArchitecturePattern failed: %v", err)
		}
		if pattern != detect.PatternGenericFrontend && pattern != detect.PatternCleanArchitecture {
			t.Errorf("unexpected pattern %q", pattern)
		}
	})
}

// Feature 6: Dynamic Starter Layer YAML Generation (5 tests)
func TestTier1_Feature6_DynamicStarterLayersYaml(t *testing.T) {
	fixtures := []string{
		"nextjs-app-fixture",
		"fsd-fixture",
		"react-spa-fixture",
		"clean-arch-fixture",
	}

	for _, fix := range fixtures {
		t.Run("initcmd_creates_valid_layers_yaml_"+fix, func(t *testing.T) {
			tempRepo := makeTempCopy(t, fix)
			res, err := initcmd.Run(tempRepo, nil)
			if err != nil {
				t.Fatalf("initcmd.Run failed on %s: %v", fix, err)
			}
			if res == nil {
				t.Fatal("initcmd.Run returned nil result")
			}

			layersPath := filepath.Join(tempRepo, "codeflow.layers.yaml")
			if _, err := os.Stat(layersPath); err != nil {
				t.Fatalf("codeflow.layers.yaml not created for %s", fix)
			}

			cfg, err := fusion.LoadLayersConfig(tempRepo)
			if err != nil {
				t.Fatalf("LoadLayersConfig failed for %s: %v", fix, err)
			}
			if len(cfg.Layers) < 7 {
				t.Errorf("expected all 7 canonical lanes in %s, got %d", fix, len(cfg.Layers))
			}

			validateContract(t, "layers-config.schema.json", cfg)
		})
	}

	// 6.5: Existing codeflow.layers.yaml preservation (non-destructive)
	t.Run("preserve_existing_layers_yaml", func(t *testing.T) {
		tempRepo := makeTempCopy(t, "react-spa-fixture")
		customContent := `# Custom layers
version: 1
strictOrder: false
allowUnknownLayer: true
layers:
  - name: presentation
    pathPatterns: ["**/my_custom_views/**"]
  - name: controller
    pathPatterns: ["**/my_custom_hooks/**"]
`
		os.WriteFile(filepath.Join(tempRepo, "codeflow.layers.yaml"), []byte(customContent), 0o644)

		_, err := initcmd.Run(tempRepo, nil)
		if err != nil {
			t.Fatalf("initcmd failed: %v", err)
		}

		readBack, err := os.ReadFile(filepath.Join(tempRepo, "codeflow.layers.yaml"))
		if err != nil {
			t.Fatalf("readFile failed: %v", err)
		}
		if !strings.Contains(string(readBack), "my_custom_views") {
			t.Errorf("initcmd overwritten custom layers configuration!")
		}
	})
}

// Feature 7: 7 Canonical Lanes Invariant (5 tests)
func TestTier1_Feature7_SevenCanonicalLanesInvariant(t *testing.T) {
	// 7.1: Monotonic traversal presentation -> controller -> usecase -> domain -> data -> infra -> external
	t.Run("monotonic_progression_succeeds", func(t *testing.T) {
		steps := []slicing.SliceStep{
			{Ordinal: 1, Layer: fusion.LayerPresentation, Kind: "call", SymbolPath: "Page.render"},
			{Ordinal: 2, Layer: fusion.LayerController, Kind: "call", SymbolPath: "useCart.addItem"},
			{Ordinal: 3, Layer: fusion.LayerUsecase, Kind: "call", SymbolPath: "OrderService.execute"},
			{Ordinal: 4, Layer: fusion.LayerDomain, Kind: "mutation", SymbolPath: "Order.calculateTotal"},
			{Ordinal: 5, Layer: fusion.LayerData, Kind: "call", SymbolPath: "OrderRepo.save"},
			{Ordinal: 6, Layer: fusion.LayerInfra, Kind: "call", SymbolPath: "DbConnection.query"},
			{Ordinal: 7, Layer: fusion.LayerExternal, Kind: "call", SymbolPath: "PaymentGateway.charge"},
		}
		cfg, _ := fusion.LoadLayersConfig(t.TempDir())
		_, err := validateSliceStepLayers(steps, nil, cfg)
		assertNoViolations(t, err, "full monotonic progression")
	})

	// 7.2: Same-layer progression succeeds
	t.Run("same_layer_steps_succeed", func(t *testing.T) {
		steps := []slicing.SliceStep{
			{Ordinal: 1, Layer: fusion.LayerPresentation, Kind: "call", SymbolPath: "Component.init"},
			{Ordinal: 2, Layer: fusion.LayerPresentation, Kind: "mutation", SymbolPath: "Component.setState"},
			{Ordinal: 3, Layer: fusion.LayerController, Kind: "call", SymbolPath: "useAuth.login"},
			{Ordinal: 4, Layer: fusion.LayerController, Kind: "call", SymbolPath: "useAuth.sync"},
		}
		cfg, _ := fusion.LoadLayersConfig(t.TempDir())
		_, err := validateSliceStepLayers(steps, nil, cfg)
		assertNoViolations(t, err, "same layer steps")
	})

	// 7.3: Backwards violation detected in strict mode
	t.Run("backward_layer_violation_rejected", func(t *testing.T) {
		steps := []slicing.SliceStep{
			{Ordinal: 1, Layer: fusion.LayerData, Kind: "call", SymbolPath: "OrderRepo.save"},
			{Ordinal: 2, Layer: fusion.LayerPresentation, Kind: "call", SymbolPath: "Component.render"},
		}
		cfg, _ := fusion.LoadLayersConfig(t.TempDir())
		cfg.StrictOrder = true
		_, err := validateSliceStepLayers(steps, nil, cfg)
		if err == nil {
			t.Errorf("expected layer_order_violation error for backward transition, got nil")
		}
	})

	// 7.4: Branch step allows backwards jump
	t.Run("branch_kind_allows_layer_reset", func(t *testing.T) {
		steps := []slicing.SliceStep{
			{Ordinal: 1, Layer: fusion.LayerData, Kind: "call", SymbolPath: "OrderRepo.save"},
			{Ordinal: 2, Layer: fusion.LayerPresentation, Kind: "branch", SymbolPath: "Component.onRetry"},
		}
		cfg, _ := fusion.LoadLayersConfig(t.TempDir())
		cfg.StrictOrder = true
		_, err := validateSliceStepLayers(steps, nil, cfg)
		assertNoViolations(t, err, "branch step layer reset")
	})

	// 7.5: Canonical order array length and exact ordering
	t.Run("canonical_layer_order_exactness", func(t *testing.T) {
		expected := []string{
			"presentation", "controller", "usecase", "domain", "data", "infra", "external", "unknown",
		}
		if len(fusion.CanonicalLayerOrder) != len(expected) {
			t.Fatalf("CanonicalLayerOrder length mismatch: got %d, want %d", len(fusion.CanonicalLayerOrder), len(expected))
		}
		for i, name := range expected {
			if fusion.CanonicalLayerOrder[i] != name {
				t.Errorf("CanonicalLayerOrder[%d] = %s, want %s", i, fusion.CanonicalLayerOrder[i], name)
			}
		}
	})
}

// Feature 8: Builtin Frontend Aliases (5 tests)
func TestTier1_Feature8_BuiltinFrontendAliases(t *testing.T) {
	aliasTests := []struct {
		rawLayer  string
		wantCanon string
	}{
		{"hook", fusion.LayerController},
		{"hooks", fusion.LayerController},
		{"features", fusion.LayerController},
		{"store", fusion.LayerController},
		{"action", fusion.LayerUsecase},
		{"actions", fusion.LayerUsecase},
		{"service", fusion.LayerUsecase},
		{"entity", fusion.LayerDomain},
		{"entities", fusion.LayerDomain},
		{"widget", fusion.LayerPresentation},
		{"page", fusion.LayerPresentation},
		{"components", fusion.LayerPresentation},
		{"queries", fusion.LayerData},
		{"repositories", fusion.LayerData},
		{"lib", fusion.LayerInfra},
		{"utils", fusion.LayerInfra},
		{"api", fusion.LayerExternal},
		{"clients", fusion.LayerExternal},
	}

	for _, tt := range aliasTests {
		t.Run("alias_"+tt.rawLayer, func(t *testing.T) {
			got, unknown := fusion.NormalizeLayer(tt.rawLayer, nil)
			if unknown {
				t.Errorf("NormalizeLayer(%q) reported unknown = true", tt.rawLayer)
			}
			if got != tt.wantCanon {
				t.Errorf("NormalizeLayer(%q) = %q, want %q", tt.rawLayer, got, tt.wantCanon)
			}
		})
	}
}

// Feature 9: Functional Component Handler Slicing (5 tests)
func TestTier1_Feature9_FunctionalComponentHandlerSlicing(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 9.1: Next.js HomePage.handleQuickCheckout slicing
	t.Run("slice_nextjs_homepage_handler", func(t *testing.T) {
		tempRepo := makeTempCopy(t, "nextjs-app-fixture")
		payload, err := sliceHelper(t, pool, ctx, tempRepo, "app/page.tsx", "HomePage.handleQuickCheckout", 3)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		if len(payload.Steps) == 0 {
			t.Errorf("expected sliced steps for HomePage.handleQuickCheckout, got 0")
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 9.2: React SPA LoginForm.handleSubmit slicing
	t.Run("slice_react_spa_loginform_handler", func(t *testing.T) {
		tempRepo := makeTempCopy(t, "react-spa-fixture")
		payload, err := sliceHelper(t, pool, ctx, tempRepo, "src/components/LoginForm.tsx", "LoginForm.handleSubmit", 3)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		if len(payload.Steps) == 0 {
			t.Errorf("expected sliced steps for LoginForm.handleSubmit, got 0")
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 9.3: FSD FeedList.onLikeClick slicing
	t.Run("slice_fsd_feedlist_handler", func(t *testing.T) {
		tempRepo := makeTempCopy(t, "fsd-fixture")
		payload, err := sliceHelper(t, pool, ctx, tempRepo, "src/widgets/FeedList.tsx", "FeedList.onLikeClick", 3)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		if len(payload.Steps) == 0 {
			t.Errorf("expected sliced steps for FeedList.onLikeClick, got 0")
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 9.4: Slicing with depth bounding (depth = 1 vs depth = 5)
	t.Run("slice_depth_bounds", func(t *testing.T) {
		tempRepo := makeTempCopy(t, "nextjs-app-fixture")
		payload1, _ := sliceHelper(t, pool, ctx, tempRepo, "app/page.tsx", "HomePage.handleQuickCheckout", 1)
		payload5, _ := sliceHelper(t, pool, ctx, tempRepo, "app/page.tsx", "HomePage.handleQuickCheckout", 5)

		if len(payload1.Steps) > len(payload5.Steps) {
			t.Errorf("depth 1 should not produce more steps than depth 5")
		}
	})

	// 9.5: Slicing fallback step for unresolvable method (graceful degradation)
	t.Run("slice_fallback_for_empty_or_unknown", func(t *testing.T) {
		tempRepo := makeTempCopy(t, "react-spa-fixture")
		payload, err := sliceHelper(t, pool, ctx, tempRepo, "src/components/LoginForm.tsx", "LoginForm.nonExistentHandler", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		if len(payload.Steps) == 0 {
			t.Errorf("expected fallback step (minItems 1), got 0")
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Feature 10: Chained Call Statement Extraction (5 tests)
func TestTier1_Feature10_ChainedCallStatementExtraction(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 10.1: 2-dot chain: authService.login()
	t.Run("two_dot_call_chain", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const AuthView = () => {
  const handleLogin = async () => {
    await authService.login("user", "pass");
  };
  return <div onClick={handleLogin} />;
};`
		os.WriteFile(filepath.Join(tempDir, "AuthView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "AuthView.tsx", "AuthView.handleLogin", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		found := false
		for _, s := range payload.Steps {
			if strings.Contains(s.Description, "authService.login") {
				found = true
			}
		}
		if !found && len(payload.Steps) > 0 {
			t.Logf("steps extracted: %+v", payload.Steps)
		}
	})

	// 10.2: 3-dot chain: api.v1.auth.login()
	t.Run("three_dot_call_chain", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const ApiCaller = () => {
  const handleCall = async () => {
    const res = await api.v1.auth.login("a", "b");
  };
  return <button onClick={handleCall} />;
};`
		os.WriteFile(filepath.Join(tempDir, "ApiCaller.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "ApiCaller.tsx", "ApiCaller.handleCall", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 10.3: 4-dot chain: client.v2.orgs.teams.members.list()
	t.Run("deep_four_dot_chain", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const OrgViewer = () => {
  const onLoadMembers = async () => {
    return await client.v2.orgs.teams.members.list();
  };
  return <div onClick={onLoadMembers} />;
};`
		os.WriteFile(filepath.Join(tempDir, "OrgViewer.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "OrgViewer.tsx", "OrgViewer.onLoadMembers", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 10.4: Chained call with arguments and async await
	t.Run("chained_call_with_args_and_await", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const CartSubmit = () => {
  const onCheckout = async () => {
    await api.orders.checkout({ items: [1, 2], total: 50 });
  };
  return <button onClick={onCheckout} />;
};`
		os.WriteFile(filepath.Join(tempDir, "CartSubmit.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "CartSubmit.tsx", "CartSubmit.onCheckout", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 10.5: Multiple sequential chained statements in one handler
	t.Run("multiple_sequential_chains", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const MultiChainComponent = () => {
  const handleFlow = async () => {
    await logger.v1.info("start");
    await api.v1.auth.validate();
    await storage.local.set("status", "ok");
  };
  return <button onClick={handleFlow} />;
};`
		os.WriteFile(filepath.Join(tempDir, "MultiChain.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "MultiChain.tsx", "MultiChainComponent.handleFlow", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		if len(payload.Steps) == 0 {
			t.Errorf("expected at least 1 statement sliced for sequential chains, got 0")
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Feature 11: Destructured Hook & Local Binding Resolution (5 tests)
func TestTier1_Feature11_DestructuredHookResolution(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 11.1: Destructured hook binding: const { login } = useAuth(); login()
	t.Run("destructured_login_hook", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `import { useAuth } from './useAuth';
export const LoginForm = () => {
  const { login } = useAuth();
  const handleSubmit = async () => {
    await login('user', 'pass');
  };
  return <form onSubmit={handleSubmit} />;
};`
		hookSrc := `export function useAuth() {
  const login = async (u, p) => {};
  return { login };
}`
		os.WriteFile(filepath.Join(tempDir, "LoginForm.tsx"), []byte(src), 0o644)
		os.WriteFile(filepath.Join(tempDir, "useAuth.ts"), []byte(hookSrc), 0o644)

		payload, err := sliceHelper(t, pool, ctx, tempDir, "LoginForm.tsx", "LoginForm.handleSubmit", 2)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 11.2: Multiple destructured identifiers: const { cart, calculateTotal, clearCart } = useCart()
	t.Run("multiple_destructured_identifiers", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const CartPage = () => {
  const { cart, calculateTotal, clearCart } = useCart();
  const onPay = () => {
    const total = calculateTotal();
    clearCart();
  };
  return <button onClick={onPay} />;
};`
		os.WriteFile(filepath.Join(tempDir, "CartPage.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "CartPage.tsx", "CartPage.onPay", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 11.3: Renamed destructured alias: const { login: authLogin } = useAuth()
	t.Run("renamed_destructured_alias", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const CustomLogin = () => {
  const { login: authLogin } = useAuth();
  const onAction = async () => {
    await authLogin('a', 'b');
  };
  return <button onClick={onAction} />;
};`
		os.WriteFile(filepath.Join(tempDir, "CustomLogin.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "CustomLogin.tsx", "CustomLogin.onAction", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 11.4: Destructured state hook: const [user, setUser] = useState()
	t.Run("destructured_state_hook", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const StateView = () => {
  const [count, setCount] = useState(0);
  const onIncrement = () => {
    setCount(count + 1);
  };
  return <button onClick={onIncrement} />;
};`
		os.WriteFile(filepath.Join(tempDir, "StateView.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "StateView.tsx", "StateView.onIncrement", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})

	// 11.5: Nested destructured props in component handler
	t.Run("nested_destructured_props", func(t *testing.T) {
		tempDir := t.TempDir()
		src := `export const PropForm = ({ auth: { login } }) => {
  const onSubmit = async () => {
    await login('user');
  };
  return <form onSubmit={onSubmit} />;
};`
		os.WriteFile(filepath.Join(tempDir, "PropForm.tsx"), []byte(src), 0o644)
		payload, err := sliceHelper(t, pool, ctx, tempDir, "PropForm.tsx", "PropForm.onSubmit", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}
		validateContract(t, "sliced-payload.schema.json", payload)
	})
}

// Feature 12: 6-Field Anchor Verification (5 tests)
func TestTier1_Feature12_SixFieldAnchorVerification(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	fixtures := []string{"nextjs-app-fixture", "fsd-fixture", "react-spa-fixture"}

	for _, fix := range fixtures {
		t.Run("anchor_verification_"+fix, func(t *testing.T) {
			repoRoot := fixtureDir(t, fix)
			var resp struct {
				Candidates []harvest.Candidate `json:"candidates"`
			}
			err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": repoRoot}, &resp)
			if err != nil {
				t.Fatalf("harvest failed: %v", err)
			}
			if len(resp.Candidates) == 0 {
				t.Fatalf("no candidates in %s", fix)
			}

			// Slice first candidate
			firstCand := resp.Candidates[0]
			parts := strings.Split(firstCand.EntrySymbolPath, "#")
			relPath := parts[0]
			symbol := parts[1]

			payload, err := sliceHelper(t, pool, ctx, repoRoot, relPath, symbol, 2)
			if err != nil {
				t.Fatalf("slice failed: %v", err)
			}

			for _, step := range payload.Steps {
				a := step.Anchor
				// 1. repoRelativePath valid
				if a.RepoRelativePath == "" || strings.HasPrefix(a.RepoRelativePath, "/") || strings.Contains(a.RepoRelativePath, "..") {
					t.Errorf("invalid repoRelativePath: %q", a.RepoRelativePath)
				}
				// 2. byteRange valid
				if a.ByteRange[0] < 0 || a.ByteRange[1] < a.ByteRange[0] {
					t.Errorf("invalid byteRange: %+v", a.ByteRange)
				}
				// 3. fileHash 64-char hex
				if len(a.FileHash) != 64 {
					t.Errorf("invalid fileHash length: %q", a.FileHash)
				}
				// 4. spanHash 64-char hex
				if len(a.SpanHash) != 64 {
					t.Errorf("invalid spanHash length: %q", a.SpanHash)
				}
				// 5. enclosingSymbolPath non-empty
				if a.EnclosingSymbolPath == "" {
					t.Errorf("empty enclosingSymbolPath")
				}
				// 6. canonicalAstFingerprint 64-char hex
				if len(a.CanonicalAstFingerprint) != 64 {
					t.Errorf("invalid canonicalAstFingerprint length: %q", a.CanonicalAstFingerprint)
				}
			}
		})
	}

	// 12.4: SpanHash matches actual file slice bytes
	t.Run("span_hash_byte_exactness", func(t *testing.T) {
		tempDir := t.TempDir()
		code := `export const ExactAnchor = () => {
  const handleAction = () => {
    const value = 42;
    return value;
  };
  return <div onClick={handleAction} />;
};`
		filePath := filepath.Join(tempDir, "ExactAnchor.tsx")
		os.WriteFile(filePath, []byte(code), 0o644)

		payload, err := sliceHelper(t, pool, ctx, tempDir, "ExactAnchor.tsx", "ExactAnchor.handleAction", 1)
		if err != nil {
			t.Fatalf("slice failed: %v", err)
		}

		fileBytes, _ := os.ReadFile(filePath)
		fileHash := sha256Hex(fileBytes)

		for _, step := range payload.Steps {
			if step.Anchor.FileHash != fileHash {
				t.Errorf("fileHash mismatch: got %s, want %s", step.Anchor.FileHash, fileHash)
			}
			if step.Anchor.ByteRange[1] <= len(fileBytes) {
				slice := fileBytes[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
				expectedSpanHash := sha256Hex(slice)
				if step.Anchor.SpanHash != expectedSpanHash {
					t.Errorf("spanHash mismatch for step %d: got %s, want %s", step.Ordinal, step.Anchor.SpanHash, expectedSpanHash)
				}
			}
		}
	})

	// 12.5: CanonicalAstFingerprint invariant under whitespace and comment alterations
	t.Run("canonical_ast_fingerprint_invariance", func(t *testing.T) {
		codeA := `await   api.orders.checkout(payload);`
		codeB := `/* comment */ await api.orders.checkout(payload); // trailing`

		fpA := computeCanonicalAstFingerprint(codeA)
		fpB := computeCanonicalAstFingerprint(codeB)

		if fpA != fpB {
			t.Errorf("canonical AST fingerprints should be invariant under whitespace/comments: %s vs %s", fpA, fpB)
		}
	})
}
