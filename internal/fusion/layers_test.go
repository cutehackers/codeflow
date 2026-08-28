package fusion

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/slicing"
)

func TestNormalizeLayer_AliasesAndSlashTrim(t *testing.T) {
	cfg, err := LoadLayersConfig(t.TempDir()) // absent → default
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		raw  string
		want string
		unk  bool
	}{
		{"presentation", "presentation", false},
		{"Presentation", "presentation", false},
		{"  presentation  ", "presentation", false},
		{"feature/auth/presentation", "presentation", false},
		{"ui", "presentation", false},
		{"UI", "presentation", false},
		{"widget", "presentation", false},
		{"application", "usecase", false},
		{"service", "usecase", false},
		{"repository", "data", false},
		{"datasource", "data", false},
		{"infrastructure", "infra", false},
		{"api", "external", false},
		{"gateway", "external", false},
		{"unknown_raw", "unknown", true},
		{"", "unknown", true},
		{"a/b/c/usecase", "usecase", false},
	}
	for _, tc := range cases {
		got, unk := NormalizeLayer(tc.raw, cfg)
		if got != tc.want || unk != tc.unk {
			t.Errorf("NormalizeLayer(%q)=%q unk=%v want %q unk=%v", tc.raw, got, unk, tc.want, tc.unk)
		}
	}
}

func TestNormalizeLayer_WithCustomConfig(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `version: 1
strictOrder: true
allowUnknownLayer: true
layers:
  - name: presentation
    aliases: [myui]
    pathPatterns: ["**/presentation/**"]
  - name: controller
    aliases: [myctrl]
  - name: usecase
  - name: data
    pathPatterns: ["**/data/**"]
`
	if err := os.WriteFile(filepath.Join(dir, "codeflow.layers.yaml"), []byte(yamlContent), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadLayersConfig(dir)
	if err != nil {
		t.Fatalf("LoadLayersConfig: %v", err)
	}
	if got, unk := NormalizeLayer("myui", cfg); got != "presentation" || unk {
		t.Errorf("custom alias myui -> %q unk=%v", got, unk)
	}
	if got, unk := NormalizeLayer("myctrl", cfg); got != "controller" || unk {
		t.Errorf("custom alias myctrl -> %q unk=%v", got, unk)
	}
	// unknown with allowUnknownLayer true → unknown
	if got, unk := NormalizeLayer("weird", cfg); got != "unknown" || !unk {
		t.Errorf("unknown with allowUnknown -> %q unk=%v", got, unk)
	}
	if !cfg.AllowUnknownLayer {
		t.Errorf("AllowUnknownLayer should be true")
	}
}

func TestLoadLayersConfig_DefaultAndInvalid(t *testing.T) {
	// Missing file → default
	dir := t.TempDir()
	cfg, err := LoadLayersConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Layers) != 7 {
		t.Errorf("default layers count %d want 7", len(cfg.Layers))
	}
	if !cfg.StrictOrder {
		t.Errorf("default StrictOrder should be true")
	}
	// Invalid yaml
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "codeflow.layers.yaml"), []byte("version: 1\nlayers: [invalid"), 0644)
	if _, err := LoadLayersConfig(dir2); err == nil {
		t.Errorf("expected error for invalid yaml")
	}
	// Invalid version
	dir3 := t.TempDir()
	os.WriteFile(filepath.Join(dir3, "codeflow.layers.yaml"), []byte("version: 2\nlayers:\n  - name: presentation\n"), 0644)
	if _, err := LoadLayersConfig(dir3); err == nil {
		t.Errorf("expected error for version !=1")
	}
	// Unknown canonical name
	dir4 := t.TempDir()
	os.WriteFile(filepath.Join(dir4, "codeflow.layers.yaml"), []byte("version: 1\nlayers:\n  - name: notalayer\n"), 0644)
	if _, err := LoadLayersConfig(dir4); err == nil {
		t.Errorf("expected error for unknown layer name")
	}
}

func TestValidateLayerOrder_StrictAndBranchException(t *testing.T) {
	cfg := defaultLayersConfig() // strict true
	// Valid monotonic
	steps := []struct{Layer string; Kind string}{
		{"presentation", "call"},
		{"controller", "call"},
		{"usecase", "mutation"},
		{"data", "call"},
	}
	if _, err := ValidateLayerOrder(steps, []string{"presentation","controller","usecase","data"}, cfg); err != nil {
		t.Errorf("valid order should not error: %v", err)
	}
	// Backward without branch → error when strict
	steps2 := []struct{Layer string; Kind string}{
		{"presentation", "call"},
		{"data", "call"},
		{"controller", "call"},
	}
	if _, err := ValidateLayerOrder(steps2, []string{"presentation","controller","usecase","data"}, cfg); err == nil {
		t.Errorf("backward should error in strict mode")
	}
	// Backward with branch → allowed
	steps3 := []struct{Layer string; Kind string}{
		{"presentation", "call"},
		{"data", "call"},
		{"controller", "branch"},
	}
	if _, err := ValidateLayerOrder(steps3, []string{"presentation","controller","usecase","data"}, cfg); err != nil {
		t.Errorf("branch backward should be allowed: %v", err)
	}
	// Non-strict → warning not error
	cfg2 := defaultLayersConfig()
	cfg2.StrictOrder = false
	if warnings, err := ValidateLayerOrder(steps2, []string{"presentation","controller","usecase","data"}, cfg2); err != nil {
		t.Errorf("non-strict should not error: %v", err)
	} else if len(warnings) == 0 {
		t.Errorf("non-strict should produce warning")
	}
	// Inferred order (no declared) → never errors, just inferred
	steps4 := []struct{Layer string; Kind string}{
		{"controller", "call"},
		{"presentation", "call"},
	}
	if _, err := ValidateLayerOrder(steps4, nil, cfg); err != nil {
		t.Errorf("inferred order should not error even if backward, got %v", err)
	}
}

func TestValidatePathPatterns(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `version: 1
layers:
  - name: presentation
    pathPatterns: ["**/presentation/**"]
  - name: controller
  - name: usecase
  - name: data
    pathPatterns: ["**/data/**"]
`
	os.WriteFile(filepath.Join(dir, "codeflow.layers.yaml"), []byte(yamlContent), 0644)
	cfg, _ := LoadLayersConfig(dir)
	steps := []struct{Layer string; RepoRelativePath string}{
		{"presentation", "lib/features/auth/presentation/join_page.dart"},
		{"presentation", "lib/features/auth/data/repo.dart"},
		{"data", "lib/data/repo.dart"},
		{"data", "lib/presentation/page.dart"},
	}
	warnings := ValidatePathPatterns(steps, cfg)
	if len(warnings) != 2 {
		t.Errorf("expected 2 warnings, got %d: %v", len(warnings), warnings)
	}
}

func TestMatchDoublestar(t *testing.T) {
	cases := []struct{
		path, pat string
		want bool
	}{
		{"lib/presentation/page.dart", "**/presentation/**", true},
		{"lib/features/auth/presentation/join_page.dart", "**/presentation/**", true},
		{"lib/data/repo.dart", "**/data/**", true},
		{"lib/data/repo.dart", "**/presentation/**", false},
		{"a/b/c.dart", "a/*/c.dart", true},
		{"a/b/c.dart", "a/*/d.dart", false},
		{"lib/features/auth/presentation/join_page.dart", "**/features/*/presentation/**", true},
		{"lib/presentation/page.dart", "**/features/*/presentation/**", false},
	}
	for _, tc := range cases {
		got := matchDoublestar(tc.path, tc.pat)
		if got != tc.want {
			t.Errorf("matchDoublestar(%q,%q)=%v want %v", tc.path, tc.pat, got, tc.want)
		}
	}
}

func TestLayerIndexAndSorted(t *testing.T) {
	if LayerIndex("presentation") != 0 {
		t.Errorf("presentation index")
	}
	if LayerIndex("external") != 6 {
		t.Errorf("external index")
	}
	if LayerIndex("unknown") != 99 {
		t.Errorf("unknown index")
	}
	sorted := SortedLayers([]string{"data","presentation","usecase"})
	want := []string{"presentation","usecase","data"}
	for i, w := range want {
		if sorted[i] != w {
			t.Errorf("sorted[%d]=%q want %q", i, sorted[i], w)
		}
	}
}

func TestFusePreservesLayerAndToLayer(t *testing.T) {
	sliced := &slicing.SlicedPayload{
		CandidateID:     "cand-layer-0001",
		Language:        "dart",
		EntrySymbolPath: "lib/src/sample.dart#Sample.submit",
		Steps: []slicing.SliceStep{
			{
				Ordinal:     1,
				Kind:        "call",
				Description: "present call",
				SymbolPath:  "Sample.submit",
				Layer:       "presentation",
				Anchor: slicing.Anchor{
					RepoRelativePath:        "lib/src/sample.dart",
					ByteRange:               [2]int{0, 10},
					FileHash:                "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					SpanHash:                "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
					EnclosingSymbolPath:     "Sample.submit",
					CanonicalAstFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
			},
			{
				Ordinal:     2,
				Kind:        "mutation",
				Description: "controller mutation",
				SymbolPath:  "Sample.ctrl",
				Layer:       "controller",
				Anchor: slicing.Anchor{
					RepoRelativePath:        "lib/src/sample.dart",
					ByteRange:               [2]int{0, 10},
					FileHash:                "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
					SpanHash:                "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
					EnclosingSymbolPath:     "Sample.ctrl",
					CanonicalAstFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				},
			},
		},
		Edges: []slicing.SliceEdge{
			{Kind: "resolved_cross_file", ToSymbolPath: "lib/src/api.dart#Api.run", ResolutionStatus: "resolved", ToLayer: "external"},
		},
	}
	spec, err := Fuse(sliced, FuseOptions{})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if spec.Steps[0].Layer != "presentation" {
		t.Errorf("step 0 layer = %q want presentation", spec.Steps[0].Layer)
	}
	if spec.Steps[1].Layer != "controller" {
		t.Errorf("step 1 layer = %q want controller", spec.Steps[1].Layer)
	}
	if len(spec.Edges) != 1 || spec.Edges[0].ToLayer != "external" {
		t.Errorf("edge ToLayer = %q want external", spec.Edges[0].ToLayer)
	}
	// Backward compat: payload without layer → step.Layer empty
	sliced2 := &slicing.SlicedPayload{
		CandidateID:     "cand-layer-0002",
		Language:        "dart",
		EntrySymbolPath: "lib/src/sample.dart#Sample.submit",
		Steps: []slicing.SliceStep{{
			Ordinal: 1, Kind: "call", Description: "no layer", SymbolPath: "Sample.submit",
			Anchor: slicing.Anchor{RepoRelativePath: "lib/src/sample.dart", ByteRange: [2]int{0, 10}, FileHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", SpanHash: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210", EnclosingSymbolPath: "Sample.submit", CanonicalAstFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		}},
	}
	spec2, err := Fuse(sliced2, FuseOptions{})
	if err != nil {
		t.Fatalf("Fuse2: %v", err)
	}
	if spec2.Steps[0].Layer != "" {
		t.Errorf("expected empty layer for legacy payload, got %q", spec2.Steps[0].Layer)
	}
}
