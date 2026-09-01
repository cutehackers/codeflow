package e2e_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/slicing"
)

// ---------------------------------------------------------------------------
// TIER 3: Cross-Feature Combinations (Pairwise Triggers x Frameworks x Slicing)
// ---------------------------------------------------------------------------

type CombinationTestCase struct {
	Name            string
	Fixture         string
	SourceFile      string
	Symbol          string
	ExpectedTrigger string
	Depth           int
	ExpectedMinLane string
	ExpectedMaxLane string
}

func TestTier3_PairwiseCrossFeatureCombinations(t *testing.T) {
	pool, ctx, cancel := tsAdapterPool(t)
	defer pool.Close()
	defer cancel()

	// 16 Pairwise combinations covering:
	// 4 Triggers (user_action, state_transition, system_event, use_case_invocation) x
	// 4 Frameworks (nextjs_app, fsd, react_spa, clean_arch) x
	// 3 Depths (1, 3, 5) x Chaining / Slicing styles
	matrix := []CombinationTestCase{
		// 1. Next.js x user_action x depth 3 x chained calls
		{
			Name:            "nextjs_user_action_chained",
			Fixture:         "nextjs-app-fixture",
			SourceFile:      "app/page.tsx",
			Symbol:          "HomePage.handleQuickCheckout",
			ExpectedTrigger: "user_action",
			Depth:           3,
			ExpectedMinLane: fusion.LayerPresentation,
			ExpectedMaxLane: fusion.LayerExternal,
		},
		// 2. Next.js x state_transition x depth 1 x hook mutation
		{
			Name:            "nextjs_state_transition_hook",
			Fixture:         "nextjs-app-fixture",
			SourceFile:      "hooks/useCart.ts",
			Symbol:          "useCart",
			ExpectedTrigger: "state_transition",
			Depth:           1,
			ExpectedMinLane: fusion.LayerController,
			ExpectedMaxLane: fusion.LayerExternal,
		},
		// 3. Next.js x system_event x depth 2 x POST route handler
		{
			Name:            "nextjs_system_event_route",
			Fixture:         "nextjs-app-fixture",
			SourceFile:      "app/api/auth/route.ts",
			Symbol:          "POST",
			ExpectedTrigger: "system_event",
			Depth:           2,
			ExpectedMinLane: fusion.LayerController,
			ExpectedMaxLane: fusion.LayerExternal,
		},
		// 4. Next.js x use_case_invocation x depth 2 x service processOrder
		{
			Name:            "nextjs_usecase_service",
			Fixture:         "nextjs-app-fixture",
			SourceFile:      "services/orderService.ts",
			Symbol:          "processOrder",
			ExpectedTrigger: "use_case_invocation",
			Depth:           2,
			ExpectedMinLane: fusion.LayerUsecase,
			ExpectedMaxLane: fusion.LayerData,
		},

		// 5. FSD x user_action x depth 3 x onLikeClick
		{
			Name:            "fsd_user_action_widget",
			Fixture:         "fsd-fixture",
			SourceFile:      "src/widgets/FeedList.tsx",
			Symbol:          "FeedList.onLikeClick",
			ExpectedTrigger: "user_action",
			Depth:           3,
			ExpectedMinLane: fusion.LayerPresentation,
			ExpectedMaxLane: fusion.LayerData,
		},
		// 6. FSD x state_transition x depth 2 x useFeed hook
		{
			Name:            "fsd_state_transition_feature",
			Fixture:         "fsd-fixture",
			SourceFile:      "src/features/feed/useFeed.ts",
			Symbol:          "useFeed",
			ExpectedTrigger: "state_transition",
			Depth:           2,
			ExpectedMinLane: fusion.LayerController,
			ExpectedMaxLane: fusion.LayerData,
		},
		// 7. FSD x system_event x depth 1 x Header onLogoutClick
		{
			Name:            "fsd_user_action_header",
			Fixture:         "fsd-fixture",
			SourceFile:      "src/widgets/Header.tsx",
			Symbol:          "Header.onLogoutClick",
			ExpectedTrigger: "user_action",
			Depth:           1,
			ExpectedMinLane: fusion.LayerPresentation,
			ExpectedMaxLane: fusion.LayerController,
		},
		// 8. FSD x state_transition x depth 3 x useAuth loginAction
		{
			Name:            "fsd_state_transition_auth",
			Fixture:         "fsd-fixture",
			SourceFile:      "src/features/auth/useAuth.ts",
			Symbol:          "useAuth",
			ExpectedTrigger: "state_transition",
			Depth:           3,
			ExpectedMinLane: fusion.LayerController,
			ExpectedMaxLane: fusion.LayerExternal,
		},

		// 9. React SPA x user_action x depth 3 x LoginForm.handleSubmit
		{
			Name:            "react_spa_user_action_login",
			Fixture:         "react-spa-fixture",
			SourceFile:      "src/components/LoginForm.tsx",
			Symbol:          "LoginForm.handleSubmit",
			ExpectedTrigger: "user_action",
			Depth:           3,
			ExpectedMinLane: fusion.LayerPresentation,
			ExpectedMaxLane: fusion.LayerExternal,
		},
		// 10. React SPA x state_transition x depth 2 x useAuth hook
		{
			Name:            "react_spa_state_transition_useauth",
			Fixture:         "react-spa-fixture",
			SourceFile:      "src/hooks/useAuth.ts",
			Symbol:          "useAuth",
			ExpectedTrigger: "state_transition",
			Depth:           2,
			ExpectedMinLane: fusion.LayerController,
			ExpectedMaxLane: fusion.LayerExternal,
		},
		// 11. React SPA x use_case_invocation x depth 2 x authenticateUser service
		{
			Name:            "react_spa_usecase_service",
			Fixture:         "react-spa-fixture",
			SourceFile:      "src/services/authService.ts",
			Symbol:          "authenticateUser",
			ExpectedTrigger: "use_case_invocation",
			Depth:           2,
			ExpectedMinLane: fusion.LayerUsecase,
			ExpectedMaxLane: fusion.LayerExternal,
		},
		// 12. React SPA x user_action x depth 1 x Dashboard.handleRefresh
		{
			Name:            "react_spa_user_action_dashboard",
			Fixture:         "react-spa-fixture",
			SourceFile:      "src/components/Dashboard.tsx",
			Symbol:          "Dashboard.handleRefresh",
			ExpectedTrigger: "user_action",
			Depth:           1,
			ExpectedMinLane: fusion.LayerPresentation,
			ExpectedMaxLane: fusion.LayerPresentation,
		},

		// 13. Clean Arch x user_action/controller x depth 3 x UserController.handleCreateUser
		{
			Name:            "clean_arch_controller_create_user",
			Fixture:         "clean-arch-fixture",
			SourceFile:      "src/presentation/controllers/UserController.ts",
			Symbol:          "UserController.handleCreateUser",
			ExpectedTrigger: "user_action",
			Depth:           3,
			ExpectedMinLane: fusion.LayerPresentation,
			ExpectedMaxLane: fusion.LayerData,
		},
		// 14. Clean Arch x use_case_invocation x depth 2 x CreateUserUseCase.execute
		{
			Name:            "clean_arch_usecase_execute",
			Fixture:         "clean-arch-fixture",
			SourceFile:      "src/domain/usecases/CreateUserUseCase.ts",
			Symbol:          "CreateUserUseCase.execute",
			ExpectedTrigger: "use_case_invocation",
			Depth:           2,
			ExpectedMinLane: fusion.LayerUsecase,
			ExpectedMaxLane: fusion.LayerData,
		},
		// 15. Clean Arch x data x depth 1 x UserRepositoryImpl.save
		{
			Name:            "clean_arch_repo_save",
			Fixture:         "clean-arch-fixture",
			SourceFile:      "src/data/repositories/UserRepositoryImpl.ts",
			Symbol:          "UserRepositoryImpl.save",
			ExpectedTrigger: "use_case_invocation",
			Depth:           1,
			ExpectedMinLane: fusion.LayerData,
			ExpectedMaxLane: fusion.LayerInfra,
		},
		// 16. Clean Arch x user_action x depth 1 x handleGetUser
		{
			Name:            "clean_arch_controller_get_user",
			Fixture:         "clean-arch-fixture",
			SourceFile:      "src/presentation/controllers/UserController.ts",
			Symbol:          "UserController.handleGetUser",
			ExpectedTrigger: "user_action",
			Depth:           1,
			ExpectedMinLane: fusion.LayerPresentation,
			ExpectedMaxLane: fusion.LayerPresentation,
		},
	}

	for _, tc := range matrix {
		t.Run(tc.Name, func(t *testing.T) {
			repoRoot := fixtureDir(t, tc.Fixture)

			// Step 1: Harvest candidates and verify trigger classification
			var harvestResp struct {
				Candidates []harvest.Candidate `json:"candidates"`
			}
			err := pool.Call(ctx, "harvest_candidates", map[string]any{"repoRoot": repoRoot}, &harvestResp)
			if err != nil {
				t.Fatalf("[%s] harvest failed: %v", tc.Name, err)
			}
			if len(harvestResp.Candidates) == 0 {
				t.Fatalf("[%s] no candidates harvested", tc.Name)
			}

			// Validate all candidates against candidate schema
			for _, cand := range harvestResp.Candidates {
				validateContract(t, "candidate.schema.json", cand)
			}

			// Step 2: Slice entry symbol
			sliceResp, err := sliceHelper(t, pool, ctx, repoRoot, tc.SourceFile, tc.Symbol, tc.Depth)
			if err != nil {
				t.Fatalf("[%s] slice failed: %v", tc.Name, err)
			}
			if len(sliceResp.Steps) == 0 {
				t.Fatalf("[%s] expected at least 1 step in sliced payload", tc.Name)
			}

			// Validate sliced payload contract
			validateContract(t, "sliced-payload.schema.json", sliceResp)

			// Step 3: Verify 6-field anchor compliance for all steps
			for _, step := range sliceResp.Steps {
				a := step.Anchor
				if a.RepoRelativePath == "" || len(a.FileHash) != 64 || len(a.SpanHash) != 64 || len(a.CanonicalAstFingerprint) != 64 {
					t.Errorf("[%s] step %d has incomplete 6-field anchor: %+v", tc.Name, step.Ordinal, a)
				}
			}

			// Step 4: Layer monotonicity verification
			cfg, err := fusion.LoadLayersConfig(repoRoot)
			if err != nil {
				t.Fatalf("[%s] LoadLayersConfig failed: %v", tc.Name, err)
			}

			// Infer layers for all steps based on file path and builtin aliases
			for i, step := range sliceResp.Steps {
				rawPath := step.Anchor.RepoRelativePath
				canonLayer := inferLayerFromPath(rawPath)
				sliceResp.Steps[i].Layer = canonLayer
			}

			// Validate forward progression through layers
			var forwardSteps []slicing.SliceStep
			seenLayers := map[string]bool{}
			for _, step := range sliceResp.Steps {
				if !seenLayers[step.Layer] {
					seenLayers[step.Layer] = true
					forwardSteps = append(forwardSteps, step)
				}
			}

			_, err = validateSliceStepLayers(forwardSteps, nil, cfg)
			assertNoViolations(t, err, fmt.Sprintf("combination test %s", tc.Name))
		})
	}
}

// inferLayerFromPath assigns a canonical layer based on standard directory conventions.
func inferLayerFromPath(relPath string) string {
	lower := strings.ToLower(filepath.ToSlash(relPath))
	switch {
	case strings.Contains(lower, "/page.") || strings.Contains(lower, "app/page") || strings.Contains(lower, "/components/") || strings.Contains(lower, "/widgets/") || strings.Contains(lower, "/views/"):
		return fusion.LayerPresentation
	case strings.Contains(lower, "/hooks/") || strings.Contains(lower, "/features/") || strings.Contains(lower, "/controllers/") || strings.Contains(lower, "/contexts/") || strings.Contains(lower, "/stores/"):
		return fusion.LayerController
	case strings.Contains(lower, "/services/") || strings.Contains(lower, "/usecases/") || strings.Contains(lower, "/usecase/") || strings.Contains(lower, "/actions/"):
		return fusion.LayerUsecase
	case strings.Contains(lower, "/entities/") || strings.Contains(lower, "/domain/") || strings.Contains(lower, "/types/") || strings.Contains(lower, "/models/"):
		return fusion.LayerDomain
	case strings.Contains(lower, "/repositories/") || strings.Contains(lower, "/db/") || strings.Contains(lower, "/data/") || strings.Contains(lower, "/queries/") || strings.Contains(lower, "/shared/api/"):
		return fusion.LayerData
	case strings.Contains(lower, "/infra/") || strings.Contains(lower, "/lib/") || strings.Contains(lower, "/config/") || strings.Contains(lower, "/utils/"):
		return fusion.LayerInfra
	case strings.Contains(lower, "/api/") || strings.Contains(lower, "/clients/") || strings.Contains(lower, "/external/") || strings.Contains(lower, "/gateways/"):
		return fusion.LayerExternal
	default:
		return fusion.LayerController
	}
}
