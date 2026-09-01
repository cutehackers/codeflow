package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/detect"
	"codeflow/internal/fusion"
	"codeflow/internal/initcmd"
	"codeflow/internal/mcp"
	"codeflow/internal/slicing"
)

// Helper to construct exact 6-field anchors from actual files in a repo
func buildRealAnchor(t *testing.T, repoRoot, relPath, enclosingSymbol, snippet string) slicing.Anchor {
	t.Helper()
	fullPath := filepath.Join(repoRoot, relPath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("buildRealAnchor: failed to read %s: %v", relPath, err)
	}

	start := strings.Index(string(data), snippet)
	if start < 0 {
		t.Fatalf("buildRealAnchor: snippet %q not found in %s", snippet, relPath)
	}
	end := start + len(snippet)

	fileHashBytes := sha256.Sum256(data)
	fileHash := hex.EncodeToString(fileHashBytes[:])

	spanBytes := data[start:end]
	spanHashBytes := sha256.Sum256(spanBytes)
	spanHash := hex.EncodeToString(spanHashBytes[:])

	astFingerprint := computeCanonicalAstFingerprint(snippet)

	return slicing.Anchor{
		RepoRelativePath:        relPath,
		ByteRange:               [2]int{start, end},
		FileHash:                fileHash,
		SpanHash:                spanHash,
		EnclosingSymbolPath:     enclosingSymbol,
		CanonicalAstFingerprint: astFingerprint,
	}
}

// ---------------------------------------------------------------------------
// 1. Stress-test codeflow init and DetectArchitecturePattern
// ---------------------------------------------------------------------------

func TestChallenger1_Detect_CorruptedPackageJSON(t *testing.T) {
	corruptPayloads := map[string]string{
		"syntax_error_unclosed_brace":      `{"name": "test-app", "dependencies": {`,
		"syntax_error_trailing_comma":      `{"name": "test-app", "dependencies": {"react": "18.0.0",},}`,
		"json_array_root":                  `["react", "next", "vue"]`,
		"json_primitive_string":            `"just a string"`,
		"json_primitive_number":            `12345.678`,
		"json_null":                        `null`,
		"binary_garbage_header":            "\x00\xff\xfe\x01\x02\x03\x04\x05",
		"empty_file":                       "",
		"dependencies_as_array":            `{"name": "test-app", "dependencies": ["react", "next"]}`,
		"dependencies_as_number":           `{"name": "test-app", "dependencies": 42}`,
		"dev_dependencies_as_boolean":      `{"name": "test-app", "devDependencies": true}`,
		"nested_corrupted_json":            `{"name": "test", "dependencies": {"next": { "version": "14" }}}`,
	}

	for name, payload := range corruptPayloads {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			pkgPath := filepath.Join(dir, "package.json")
			if err := os.WriteFile(pkgPath, []byte(payload), 0o644); err != nil {
				t.Fatalf("failed to write package.json: %v", err)
			}

			// 1. DetectArchitecturePattern must NOT panic and return a valid pattern
			pattern, err := detect.DetectArchitecturePattern(dir)
			if err != nil {
				t.Fatalf("DetectArchitecturePattern failed with error: %v", err)
			}
			if pattern == "" {
				t.Errorf("expected non-empty architecture pattern, got empty")
			}

			// 2. initcmd.Run must succeed and create workspace.json + starter codeflow.layers.yaml
			var stdout strings.Builder
			res, err := initcmd.Run(dir, &stdout)
			if err != nil {
				t.Fatalf("initcmd.Run failed on corrupted package.json: %v", err)
			}
			if res == nil {
				t.Fatalf("initcmd.Run result is nil")
			}

			layersPath := filepath.Join(dir, "codeflow.layers.yaml")
			if _, err := os.Stat(layersPath); err != nil {
				t.Errorf("codeflow.layers.yaml was not generated")
			}

			// Verify generated layers file is valid
			cfg, err := fusion.LoadLayersConfig(dir)
			if err != nil {
				t.Fatalf("LoadLayersConfig failed on generated layers: %v", err)
			}
			if len(cfg.Layers) != 7 {
				t.Errorf("expected 7 layers, got %d", len(cfg.Layers))
			}
		})
	}
}

func TestChallenger1_Detect_MassivePackageJSON(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString(`{"name":"massive-monorepo","dependencies":{`)
	for i := 0; i < 5000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"pkg-%d":"^1.0.0"`, i)
	}
	b.WriteString(`,"react":"18.2.0"},"devDependencies":{`)
	for i := 0; i < 5000; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `"dev-pkg-%d":"^2.0.0"`, i)
	}
	b.WriteString(`}}`)

	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	pattern, err := detect.DetectArchitecturePattern(dir)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("DetectArchitecturePattern failed: %v", err)
	}
	if pattern != detect.PatternStandardReactSPA {
		t.Errorf("expected PatternStandardReactSPA, got %s", pattern)
	}
	if elapsed > 2*time.Second {
		t.Errorf("detection took too long: %v", elapsed)
	}
}

func TestChallenger1_Detect_EmptyAndMinimalRepos(t *testing.T) {
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
	}{
		{
			name:        "completely_empty_repo",
			directories: nil,
			files:       nil,
		},
		{
			name:        "only_hidden_git_and_ignore",
			directories: []string{".git/objects", ".git/refs"},
			files: map[string]string{
				".gitignore":  "node_modules\n.DS_Store\n",
				".git/config": "[core]\n\trepositoryformatversion = 0\n",
			},
		},
		{
			name:        "only_readme_and_license",
			directories: nil,
			files: map[string]string{
				"README.md": "# My Awesome Project\n",
				"LICENSE":   "MIT License\n",
			},
		},
		{
			name:        "empty_src_directory",
			directories: []string{"src"},
			files:       nil,
		},
		{
			name:        "empty_lib_directory",
			directories: []string{"lib"},
			files:       nil,
		},
		{
			name:        "only_nested_empty_folders",
			directories: []string{"a/b/c/d/e/f/g"},
			files:       nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tc.directories {
				os.MkdirAll(filepath.Join(dir, d), 0o755)
			}
			for f, content := range tc.files {
				os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
				os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644)
			}

			pattern, err := detect.DetectArchitecturePattern(dir)
			if err != nil {
				t.Fatalf("DetectArchitecturePattern failed: %v", err)
			}
			if pattern == "" {
				t.Errorf("expected non-empty fallback pattern")
			}

			var stdout strings.Builder
			res, err := initcmd.Run(dir, &stdout)
			if err != nil {
				t.Fatalf("initcmd.Run failed: %v", err)
			}
			if res == nil {
				t.Fatalf("initcmd.Run result is nil")
			}

			cfg, err := fusion.LoadLayersConfig(dir)
			if err != nil {
				t.Fatalf("LoadLayersConfig failed on created layers: %v", err)
			}
			if len(cfg.Layers) != 7 {
				t.Errorf("expected 7 layers in starter YAML, got %d", len(cfg.Layers))
			}
		})
	}
}

func TestChallenger1_Detect_MonorepoDirectoryLayouts(t *testing.T) {
	// 1. Turborepo / Lerna workspace root layout
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "apps", "web", "app"), 0o755)
	os.MkdirAll(filepath.Join(root, "packages", "feed", "src", "features"), 0o755)
	os.MkdirAll(filepath.Join(root, "packages", "feed", "src", "entities"), 0o755)
	os.MkdirAll(filepath.Join(root, "packages", "ui", "src", "components"), 0o755)

	os.WriteFile(filepath.Join(root, "package.json"), []byte(`{
		"name": "root-monorepo",
		"workspaces": ["apps/*", "packages/*"],
		"devDependencies": {"turbo": "^1.10.0"}
	}`), 0o644)

	// Web subapp (Next.js)
	webDir := filepath.Join(root, "apps", "web")
	os.WriteFile(filepath.Join(webDir, "package.json"), []byte(`{"name":"web","dependencies":{"next":"14.2.0","react":"18.2.0"}}`), 0o644)
	os.WriteFile(filepath.Join(webDir, "app", "page.tsx"), []byte(`export default function Page() { return <div>Web</div>; }`), 0o644)

	// Feed subpackage (FSD)
	feedDir := filepath.Join(root, "packages", "feed")
	os.WriteFile(filepath.Join(feedDir, "package.json"), []byte(`{"name":"feed-pkg","dependencies":{"react":"18.2.0"}}`), 0o644)

	// UI subpackage (React SPA)
	uiDir := filepath.Join(root, "packages", "ui")
	os.WriteFile(filepath.Join(uiDir, "package.json"), []byte(`{"name":"ui-pkg","dependencies":{"react":"18.2.0"}}`), 0o644)

	// Test detection on individual subprojects
	webPat, err := detect.DetectArchitecturePattern(webDir)
	if err != nil || webPat != detect.PatternNextAppRouter {
		t.Errorf("web subapp detection: got %s, err: %v, want %s", webPat, err, detect.PatternNextAppRouter)
	}

	feedPat, err := detect.DetectArchitecturePattern(feedDir)
	if err != nil || feedPat != detect.PatternFeatureSlicedDesign {
		t.Errorf("feed subpackage detection: got %s, err: %v, want %s", feedPat, err, detect.PatternFeatureSlicedDesign)
	}

	uiPat, err := detect.DetectArchitecturePattern(uiDir)
	if err != nil || uiPat != detect.PatternStandardReactSPA {
		t.Errorf("ui subpackage detection: got %s, err: %v, want %s", uiPat, err, detect.PatternStandardReactSPA)
	}
}

func TestChallenger1_Detect_ExoticFolderCombinations(t *testing.T) {
	cases := []struct {
		name        string
		directories []string
		files       map[string]string
		wantPattern detect.ArchitecturePattern
	}{
		{
			name:        "features_alone_without_entities_or_widgets",
			directories: []string{"src/features/user"},
			files: map[string]string{
				"package.json": `{"name":"feature-app","dependencies":{"react":"18.0.0"}}`,
			},
			wantPattern: detect.PatternStandardReactSPA, // fallback to SPA with react
		},
		{
			name:        "widgets_alone_with_components_no_features_no_entities_no_pages",
			directories: []string{"src/widgets/button", "src/components/modal"},
			files: map[string]string{
				"package.json": `{"name":"widget-app","dependencies":{"react":"18.0.0"}}`,
			},
			wantPattern: detect.PatternStandardReactSPA,
		},
		{
			name:        "next_config_without_app_or_pages",
			directories: []string{"src/components"},
			files: map[string]string{
				"next.config.js": "module.exports = {};",
				"package.json":   `{"name":"broken-next","dependencies":{"react":"18.0.0"}}`,
			},
			wantPattern: detect.PatternStandardReactSPA,
		},
		{
			name:        "app_folder_in_go_clean_arch_project",
			directories: []string{"app", "internal/domain", "internal/usecases"},
			files: map[string]string{
				"go.mod":        "module example.com/myapp\ngo 1.22\n",
				"app/server.go": "package app\n",
			},
			wantPattern: detect.PatternCleanArchitecture,
		},
		{
			name:        "components_and_hooks_at_root_without_src",
			directories: []string{"components/ui", "hooks/useAuth"},
			files: map[string]string{
				"package.json": `{"name":"root-spa","dependencies":{"react":"18.0.0"}}`,
			},
			wantPattern: detect.PatternStandardReactSPA,
		},
		{
			name:        "vue_vite_spa",
			directories: []string{"src/views", "src/components"},
			files: map[string]string{
				"package.json":   `{"name":"vue-app","dependencies":{"vue":"^3.4.0"}}`,
				"vite.config.ts": `import { defineConfig } from 'vite';`,
			},
			wantPattern: detect.PatternStandardReactSPA,
		},
		{
			name:        "svelte_spa",
			directories: []string{"src/components"},
			files: map[string]string{
				"package.json": `{"name":"svelte-app","dependencies":{"svelte":"^4.0.0"}}`,
			},
			wantPattern: detect.PatternStandardReactSPA,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tc.directories {
				os.MkdirAll(filepath.Join(dir, d), 0o755)
			}
			for f, content := range tc.files {
				os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
				os.WriteFile(filepath.Join(dir, f), []byte(content), 0o644)
			}

			pattern, err := detect.DetectArchitecturePattern(dir)
			if err != nil {
				t.Fatalf("DetectArchitecturePattern failed: %v", err)
			}
			if pattern != tc.wantPattern {
				t.Errorf("got pattern %q, want %q", pattern, tc.wantPattern)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Empirically verify ValidateLayerOrder & publish_core_flow across 4 Fixtures
// ---------------------------------------------------------------------------

func TestChallenger2_PublishCoreFlow_NextJsAppFixture(t *testing.T) {
	tempRepo := makeTempCopy(t, "nextjs-app-fixture")

	// 1. Run codeflow init to generate starter layer config
	if _, err := initcmd.Run(tempRepo, nil); err != nil {
		t.Fatalf("initcmd.Run failed: %v", err)
	}

	cfg, err := fusion.LoadLayersConfig(tempRepo)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}
	if len(cfg.Layers) != 7 {
		t.Fatalf("expected 7 layers in nextjs config, got %d", len(cfg.Layers))
	}

	// 2. Build real anchors for each step
	anchor1 := buildRealAnchor(t, tempRepo, "app/page.tsx", "HomePage.handleQuickCheckout", "handleQuickCheckout")
	anchor2 := buildRealAnchor(t, tempRepo, "hooks/useCart.ts", "useCart", "useCart")
	anchor3 := buildRealAnchor(t, tempRepo, "services/orderService.ts", "processOrder", "processOrder")
	anchor4 := buildRealAnchor(t, tempRepo, "db/orders.ts", "saveOrder", "saveOrder")
	anchor5 := buildRealAnchor(t, tempRepo, "lib/api.ts", "api.orders.checkout", "checkout")

	artifact := map[string]any{
		"flowId":          "flow-nextjs00000001",
		"entrySymbolPath": "app/page.tsx#HomePage.handleQuickCheckout",
		"title":           "Next.js E-Commerce Quick Checkout Flow",
		"description":     "End-to-end traversal from UI trigger down through layers to database and external payment API",
		"layers": []string{
			"presentation", "controller", "usecase", "data", "external",
		},
		"steps": []map[string]any{
			{
				"ordinal":     1,
				"name":        "HomePage.handleQuickCheckout",
				"layer":       "presentation",
				"kind":        "call",
				"description": "User clicks Quick Checkout button",
				"anchor":      anchor1,
			},
			{
				"ordinal":     2,
				"name":        "useCart",
				"layer":       "controller",
				"kind":        "call",
				"description": "Calculate cart total",
				"anchor":      anchor2,
			},
			{
				"ordinal":     3,
				"name":        "processOrder",
				"layer":       "usecase",
				"kind":        "call",
				"description": "Execute business order processing",
				"anchor":      anchor3,
			},
			{
				"ordinal":     4,
				"name":        "saveOrder",
				"layer":       "data",
				"kind":        "call",
				"description": "Persist order in DB",
				"anchor":      anchor4,
			},
			{
				"ordinal":     5,
				"name":        "api.orders.checkout",
				"layer":       "external",
				"kind":        "call",
				"description": "Send payment to external gateway",
				"anchor":      anchor5,
			},
		},
	}

	// 3. Test ValidateLayerOrder directly
	stepsForOrder := []struct {
		Layer string
		Kind  string
	}{
		{"presentation", "call"},
		{"controller", "call"},
		{"usecase", "call"},
		{"data", "call"},
		{"external", "call"},
	}
	declared := []string{"presentation", "controller", "usecase", "domain", "data", "infra", "external"}
	warnings, err := fusion.ValidateLayerOrder(stepsForOrder, declared, cfg)
	if err != nil {
		t.Fatalf("ValidateLayerOrder returned unexpected error: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("expected 0 warnings, got: %v", warnings)
	}

	// 4. Test publish_core_flow via MCP
	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     tempRepo,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": artifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf := bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve error: %v", err)
	}

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &pubResp); err != nil {
		t.Fatalf("failed to unmarshal MCP response: %v\nRaw: %s", err, outBuf.String())
	}
	if pubResp.Result.IsError {
		t.Fatalf("publish_core_flow returned isError=true: %s", pubResp.Result.Content[0].Text)
	}

	var pubResult struct {
		Status    string   `json:"status"`
		FlowID    string   `json:"flowId"`
		Title     string   `json:"title"`
		StepCount int      `json:"stepCount"`
		Warnings  []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(pubResp.Result.Content[0].Text), &pubResult); err != nil {
		t.Fatalf("failed to parse publish result JSON: %v", err)
	}
	if pubResult.Status != "published" {
		t.Errorf("status = %q, want 'published'", pubResult.Status)
	}
	if pubResult.StepCount != 5 {
		t.Errorf("stepCount = %d, want 5", pubResult.StepCount)
	}
}

func TestChallenger2_PublishCoreFlow_FSDFixture(t *testing.T) {
	tempRepo := makeTempCopy(t, "fsd-fixture")

	if _, err := initcmd.Run(tempRepo, nil); err != nil {
		t.Fatalf("initcmd.Run failed: %v", err)
	}

	cfg, err := fusion.LoadLayersConfig(tempRepo)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}

	anchor1 := buildRealAnchor(t, tempRepo, "src/widgets/FeedList.tsx", "FeedList.onLikeClick", "onLikeClick")
	anchor2 := buildRealAnchor(t, tempRepo, "src/features/feed/useFeed.ts", "useFeed", "useFeed")
	anchor3 := buildRealAnchor(t, tempRepo, "src/entities/post/model.ts", "Post", "Post")
	anchor4 := buildRealAnchor(t, tempRepo, "src/shared/api/client.ts", "feedApi", "feedApi")

	fsdSteps := []struct {
		Layer string
		Kind  string
	}{
		{"presentation", "call"},
		{"controller", "call"},
		{"domain", "mutation"},
		{"data", "call"},
	}
	if warnings, err := fusion.ValidateLayerOrder(fsdSteps, []string{"presentation", "controller", "domain", "data"}, cfg); err != nil {
		t.Fatalf("ValidateLayerOrder on FSD steps failed: %v", err)
	} else if len(warnings) > 0 {
		t.Errorf("expected 0 warnings on FSD steps, got: %v", warnings)
	}

	artifact := map[string]any{
		"flowId":          "flow-fsdflow00000001",
		"entrySymbolPath": "src/widgets/FeedList.tsx#FeedList.onLikeClick",
		"title":           "FSD Social Feed Like Interaction Flow",
		"description":     "FSD architecture slice: widgets -> features -> entities -> shared/api",
		"layers": []string{
			"presentation", "controller", "domain", "data",
		},
		"steps": []map[string]any{
			{
				"ordinal":     1,
				"name":        "FeedList.onLikeClick",
				"layer":       "presentation",
				"kind":        "call",
				"description": "User clicks like button on feed item",
				"anchor":      anchor1,
			},
			{
				"ordinal":     2,
				"name":        "useFeed",
				"layer":       "controller",
				"kind":        "call",
				"description": "Feed feature hook manages like state",
				"anchor":      anchor2,
			},
			{
				"ordinal":     3,
				"name":        "Post",
				"layer":       "domain",
				"kind":        "mutation",
				"description": "Entity model updates like count",
				"anchor":      anchor3,
			},
			{
				"ordinal":     4,
				"name":        "feedApi",
				"layer":       "data",
				"kind":        "call",
				"description": "API client sends like request",
				"anchor":      anchor4,
			},
		},
	}

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     tempRepo,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": artifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf := bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve error: %v", err)
	}

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf.Bytes(), &pubResp)
	if pubResp.Result.IsError {
		t.Fatalf("publish_core_flow returned isError=true on FSD fixture: %s", pubResp.Result.Content[0].Text)
	}
}

func TestChallenger2_PublishCoreFlow_ReactSPAFixture(t *testing.T) {
	tempRepo := makeTempCopy(t, "react-spa-fixture")

	if _, err := initcmd.Run(tempRepo, nil); err != nil {
		t.Fatalf("initcmd.Run failed: %v", err)
	}

	cfg, err := fusion.LoadLayersConfig(tempRepo)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}

	anchor1 := buildRealAnchor(t, tempRepo, "src/components/LoginForm.tsx", "LoginForm.handleSubmit", "handleSubmit")
	anchor2 := buildRealAnchor(t, tempRepo, "src/hooks/useAuth.ts", "useAuth", "useAuth")
	anchor3 := buildRealAnchor(t, tempRepo, "src/services/authService.ts", "authenticateUser", "authenticateUser")
	anchor4 := buildRealAnchor(t, tempRepo, "src/types/auth.ts", "AuthCredentials", "AuthCredentials")
	anchor5 := buildRealAnchor(t, tempRepo, "src/api/client.ts", "api", "api")

	spaSteps := []struct {
		Layer string
		Kind  string
	}{
		{"presentation", "call"},
		{"controller", "call"},
		{"usecase", "call"},
		{"domain", "mutation"},
		{"external", "call"},
	}
	if warnings, err := fusion.ValidateLayerOrder(spaSteps, []string{"presentation", "controller", "usecase", "domain", "external"}, cfg); err != nil {
		t.Fatalf("ValidateLayerOrder on SPA steps failed: %v", err)
	} else if len(warnings) > 0 {
		t.Errorf("expected 0 warnings on SPA steps, got: %v", warnings)
	}

	artifact := map[string]any{
		"flowId":          "flow-spaflow00000001",
		"entrySymbolPath": "src/components/LoginForm.tsx#LoginForm.handleSubmit",
		"title":           "React SPA Authentication Flow",
		"description":     "SPA flow: components -> hooks -> services -> types -> api",
		"layers": []string{
			"presentation", "controller", "usecase", "domain", "external",
		},
		"steps": []map[string]any{
			{
				"ordinal":     1,
				"name":        "LoginForm.handleSubmit",
				"layer":       "presentation",
				"kind":        "call",
				"description": "User submits login form",
				"anchor":      anchor1,
			},
			{
				"ordinal":     2,
				"name":        "useAuth",
				"layer":       "controller",
				"kind":        "call",
				"description": "Auth hook handles login state",
				"anchor":      anchor2,
			},
			{
				"ordinal":     3,
				"name":        "authenticateUser",
				"layer":       "usecase",
				"kind":        "call",
				"description": "Auth service coordinates credential authentication",
				"anchor":      anchor3,
			},
			{
				"ordinal":     4,
				"name":        "AuthCredentials",
				"layer":       "domain",
				"kind":        "mutation",
				"description": "Domain credentials entity",
				"anchor":      anchor4,
			},
			{
				"ordinal":     5,
				"name":        "api",
				"layer":       "external",
				"kind":        "call",
				"description": "API client sends login payload",
				"anchor":      anchor5,
			},
		},
	}

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     tempRepo,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": artifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf := bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve error: %v", err)
	}

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf.Bytes(), &pubResp)
	if pubResp.Result.IsError {
		t.Fatalf("publish_core_flow returned isError=true on React SPA fixture: %s", pubResp.Result.Content[0].Text)
	}
}

func TestChallenger2_PublishCoreFlow_CleanArchitectureFixture(t *testing.T) {
	tempRepo := makeTempCopy(t, "clean-arch-fixture")

	if _, err := initcmd.Run(tempRepo, nil); err != nil {
		t.Fatalf("initcmd.Run failed: %v", err)
	}

	cfg, err := fusion.LoadLayersConfig(tempRepo)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}

	anchor1 := buildRealAnchor(t, tempRepo, "controllers/UserController.ts", "UserController.handleCreateUser", "handleCreateUser")
	anchor2 := buildRealAnchor(t, tempRepo, "usecases/CreateUserUseCase.ts", "CreateUserUseCase.execute", "execute")
	anchor3 := buildRealAnchor(t, tempRepo, "domain/User.ts", "User", "User")
	anchor4 := buildRealAnchor(t, tempRepo, "repositories/UserRepositoryImpl.ts", "UserRepositoryImpl.save", "save")

	cleanSteps := []struct {
		Layer string
		Kind  string
	}{
		{"controller", "call"},
		{"usecase", "call"},
		{"domain", "mutation"},
		{"data", "call"},
	}
	if warnings, err := fusion.ValidateLayerOrder(cleanSteps, []string{"controller", "usecase", "domain", "data"}, cfg); err != nil {
		t.Fatalf("ValidateLayerOrder on Clean Arch steps failed: %v", err)
	} else if len(warnings) > 0 {
		t.Errorf("expected 0 warnings on Clean Arch steps, got: %v", warnings)
	}

	artifact := map[string]any{
		"flowId":          "flow-cleanarchempirical004",
		"entrySymbolPath": "controllers/UserController.ts#UserController.handleCreateUser",
		"title":           "Clean Architecture User Creation Flow",
		"description":     "Clean Architecture flow: controller -> usecase -> domain -> repository",
		"layers": []string{
			"controller", "usecase", "domain", "data",
		},
		"steps": []map[string]any{
			{
				"ordinal":     1,
				"name":        "UserController.handleCreateUser",
				"layer":       "controller",
				"kind":        "call",
				"description": "HTTP controller receives createUser request",
				"anchor":      anchor1,
			},
			{
				"ordinal":     2,
				"name":        "CreateUserUseCase.execute",
				"layer":       "usecase",
				"kind":        "call",
				"description": "Use case executes user creation logic",
				"anchor":      anchor2,
			},
			{
				"ordinal":     3,
				"name":        "User",
				"layer":       "domain",
				"kind":        "mutation",
				"description": "Domain model validates user entity",
				"anchor":      anchor3,
			},
			{
				"ordinal":     4,
				"name":        "UserRepositoryImpl.save",
				"layer":       "data",
				"kind":        "call",
				"description": "Repository persists user to database",
				"anchor":      anchor4,
			},
		},
	}

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     tempRepo,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": artifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf := bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve error: %v", err)
	}

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf.Bytes(), &pubResp)
	if pubResp.Result.IsError {
		t.Fatalf("publish_core_flow returned isError=true on Clean Architecture fixture: %s", pubResp.Result.Content[0].Text)
	}
}

func TestChallenger2_LayerOrder_NegativeAdversarialCases(t *testing.T) {
	tempRepo := makeTempCopy(t, "react-spa-fixture")
	initcmd.Run(tempRepo, nil)

	anchor1 := buildRealAnchor(t, tempRepo, "src/components/LoginForm.tsx", "LoginForm.handleSubmit", "handleSubmit")
	anchor2 := buildRealAnchor(t, tempRepo, "src/services/authService.ts", "authenticateUser", "authenticateUser")
	anchor3 := buildRealAnchor(t, tempRepo, "src/hooks/useAuth.ts", "useAuth", "useAuth")

	// 1. Backward jump: presentation -> usecase -> controller (backward without branch)
	backwardArtifact := map[string]any{
		"flowId":          "flow-backwarderr001",
		"entrySymbolPath": "src/components/LoginForm.tsx#LoginForm.handleSubmit",
		"title":           "Backward Layer Violation",
		"layers":          []string{"presentation", "controller", "usecase"},
		"steps": []map[string]any{
			{"ordinal": 1, "name": "LoginForm.handleSubmit", "layer": "presentation", "kind": "call", "anchor": anchor1},
			{"ordinal": 2, "name": "authenticateUser", "layer": "usecase", "kind": "call", "anchor": anchor2},
			{"ordinal": 3, "name": "useAuth", "layer": "controller", "kind": "call", "anchor": anchor3}, // backward!
		},
	}

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     tempRepo,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": backwardArtifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf := bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf := &bytes.Buffer{}

	_ = srv.Serve(ctx, inBuf, outBuf)

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf.Bytes(), &pubResp)
	if !pubResp.Result.IsError {
		t.Fatal("expected backward jump to be rejected with isError=true")
	}
	if !strings.Contains(pubResp.Result.Content[0].Text, "layer_order_violation") {
		t.Errorf("expected layer_order_violation code, got: %s", pubResp.Result.Content[0].Text)
	}

	// 2. Backward jump WITH branch kind -> MUST SUCCEED
	branchArtifact := map[string]any{
		"flowId":          "flow-branchallowed01",
		"entrySymbolPath": "src/components/LoginForm.tsx#LoginForm.handleSubmit",
		"title":           "Backward Branch Allowed",
		"layers":          []string{"presentation", "controller", "usecase"},
		"steps": []map[string]any{
			{"ordinal": 1, "name": "LoginForm.handleSubmit", "layer": "presentation", "kind": "call", "anchor": anchor1},
			{"ordinal": 2, "name": "authenticateUser", "layer": "usecase", "kind": "call", "anchor": anchor2},
			{"ordinal": 3, "name": "useAuth", "layer": "controller", "kind": "branch", "branch": "onErrorRetry", "anchor": anchor3}, // branch allowed!
		},
	}

	pubReq2 := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": branchArtifact,
			},
		},
	}
	pubBytes2, _ := json.Marshal(pubReq2)
	inBuf2 := bytes.NewBuffer(append(pubBytes2, '\n'))
	outBuf2 := &bytes.Buffer{}

	_ = srv.Serve(ctx, inBuf2, outBuf2)

	var pubResp2 struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf2.Bytes(), &pubResp2)
	if pubResp2.Result.IsError {
		t.Fatalf("expected branch backward jump to succeed, got error: %s", pubResp2.Result.Content[0].Text)
	}
}

// ---------------------------------------------------------------------------
// 3. Verify that the 7 Canonical Lanes are preserved in strict rank order
// ---------------------------------------------------------------------------

func TestChallenger3_Canonical7Lanes_StrictRankOrderAndInvariants(t *testing.T) {
	patternsToTest := []detect.ArchitecturePattern{
		detect.PatternNextAppRouter,
		detect.PatternFeatureSlicedDesign,
		detect.PatternStandardReactSPA,
		detect.PatternGenericFrontend,
		detect.PatternCleanArchitecture,
		detect.ArchitecturePattern("unknown_pattern"),
	}

	expectedCanonicalLanes := []string{
		fusion.LayerPresentation,
		fusion.LayerController,
		fusion.LayerUsecase,
		fusion.LayerDomain,
		fusion.LayerData,
		fusion.LayerInfra,
		fusion.LayerExternal,
	}

	for _, pattern := range patternsToTest {
		t.Run("pattern_"+string(pattern), func(t *testing.T) {
			dir := t.TempDir()

			// Write specific markers to trigger this pattern
			switch pattern {
			case detect.PatternNextAppRouter:
				os.MkdirAll(filepath.Join(dir, "app"), 0o755)
				os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"next","dependencies":{"next":"14.0.0"}}`), 0o644)
			case detect.PatternFeatureSlicedDesign:
				os.MkdirAll(filepath.Join(dir, "src", "features"), 0o755)
				os.MkdirAll(filepath.Join(dir, "src", "entities"), 0o755)
				os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"fsd"}`), 0o644)
			case detect.PatternStandardReactSPA:
				os.MkdirAll(filepath.Join(dir, "src", "components"), 0o755)
				os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"spa","dependencies":{"react":"18.0.0"}}`), 0o644)
			case detect.PatternGenericFrontend:
				os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"generic"}`), 0o644)
			default:
				// Clean Architecture default
			}

			// Run init
			res, err := initcmd.Run(dir, nil)
			if err != nil {
				t.Fatalf("initcmd.Run failed: %v", err)
			}
			if res == nil {
				t.Fatalf("initcmd.Run result is nil")
			}

			// Load and parse codeflow.layers.yaml
			cfg, err := fusion.LoadLayersConfig(dir)
			if err != nil {
				t.Fatalf("LoadLayersConfig failed: %v", err)
			}

			// Invariant 1: Version must be 1
			if cfg.Version != 1 {
				t.Errorf("Version = %d, want 1", cfg.Version)
			}

			// Invariant 2: StrictOrder must be true
			if !cfg.StrictOrder {
				t.Errorf("StrictOrder = %v, want true", cfg.StrictOrder)
			}

			// Invariant 3: AllowUnknownLayer must be false
			if cfg.AllowUnknownLayer {
				t.Errorf("AllowUnknownLayer = %v, want false", cfg.AllowUnknownLayer)
			}

			// Invariant 4: Exactly 7 Canonical Lanes
			if len(cfg.Layers) != 7 {
				t.Fatalf("expected exactly 7 layers, got %d", len(cfg.Layers))
			}

			// Invariant 5: Strict rank order matching CanonicalLayerOrder
			seenAliases := map[string]string{}
			for i, layer := range cfg.Layers {
				expectedName := expectedCanonicalLanes[i]
				if layer.Name != expectedName {
					t.Errorf("layer[%d].Name = %q, want canonical %q", i, layer.Name, expectedName)
				}

				// Check rank index matches
				if fusion.LayerIndex(layer.Name) != i {
					t.Errorf("LayerIndex(%q) = %d, want rank %d", layer.Name, fusion.LayerIndex(layer.Name), i)
				}

				// Verify aliases
				for _, alias := range layer.Aliases {
					if strings.TrimSpace(alias) == "" {
						t.Errorf("layer %q has empty alias", layer.Name)
					}
					if alias != strings.ToLower(alias) {
						t.Errorf("layer %q alias %q is not lowercased", layer.Name, alias)
					}

					// Verify alias normalizes to this exact layer
					norm, unk := fusion.NormalizeLayer(alias, cfg)
					if unk {
						t.Errorf("alias %q normalized to unknown", alias)
					}
					if norm != layer.Name {
						t.Errorf("alias %q normalized to %q, want %q", alias, norm, layer.Name)
					}

					// Check alias uniqueness across all 7 layers (no collisions)
					if prevLayer, exists := seenAliases[alias]; exists {
						t.Errorf("alias collision: %q is defined in both %q and %q", alias, prevLayer, layer.Name)
					}
					seenAliases[alias] = layer.Name
				}

				// Verify pathPatterns are valid
				for _, pat := range layer.PathPatterns {
					if strings.TrimSpace(pat) == "" {
						t.Errorf("layer %q has empty pathPattern", layer.Name)
					}
					// Test pattern against a sample path
					_ = fusion.ValidatePathPatterns([]struct {
						Ordinal          int
						Layer            string
						RepoRelativePath string
					}{
						{1, layer.Name, "src/components/Test.tsx"},
					}, cfg)
				}
			}
		})
	}
}
