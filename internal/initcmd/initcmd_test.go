package initcmd

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/workspace"
)

func writePubspec(t *testing.T, repoRoot, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoRoot, "pubspec.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readManifestRaw(t *testing.T, repoRoot string) string {
	t.Helper()
	data, err := os.ReadFile(workspace.FilePath(repoRoot))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInitCreatesWorkspaceOnDartProject(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, "name: super_app\n")

	var out strings.Builder
	res, err := Run(repoRoot, &out)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !res.Confident || res.Language != "dart" || res.ProjectName != "super_app" {
		t.Errorf("Result detection = %+v", res)
	}
	if !res.Created {
		t.Error("Created = false on fresh repo")
	}
	if res.Pins["dart"] != "0.1.0" {
		t.Errorf("pin dart = %q, want 0.1.0", res.Pins["dart"])
	}
	raw := readManifestRaw(t, repoRoot)
	for _, want := range []string{`"schemaVersion": "2.0"`, `"dart": "0.1.0"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("manifest missing %s:\n%s", want, raw)
		}
	}
	for _, want := range []string{"detected dart project", "pins    : dart@0.1.0", "next    : run 'codeflow flows'"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q; got:\n%s", want, out.String())
		}
	}
}

func TestInitProceedsUnconfidentWithoutMarker(t *testing.T) {
	repoRoot := t.TempDir()

	var out strings.Builder
	res, err := Run(repoRoot, &out)
	if err != nil {
		t.Fatalf("Run() error = %v (init must proceed unconfident)", err)
	}
	if res.Confident {
		t.Error("Confident = true without project marker")
	}
	if !workspace.Exists(repoRoot) {
		t.Error("workspace.json should still be created")
	}
	if !strings.Contains(out.String(), "warn: no project marker found") {
		t.Errorf("stdout missing warning; got:\n%s", out.String())
	}
}

func TestInitPurgesV1RemnantsAndReportsCount(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, "name: legacy_app\n")
	cfDir := filepath.Join(repoRoot, ".codeflow")
	if err := os.MkdirAll(cfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	v1Files := []string{"runtime.json", "codeflow.lock"}
	for _, name := range v1Files {
		if err := os.WriteFile(filepath.Join(cfDir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var out strings.Builder
	res, err := Run(repoRoot, &out)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.PurgedCount != len(v1Files) {
		t.Errorf("PurgedCount = %d, want %d", res.PurgedCount, len(v1Files))
	}
	for _, name := range v1Files {
		if _, err := os.Stat(filepath.Join(cfDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still exists after purge", name)
		}
	}
	if !strings.Contains(out.String(), "removed .codeflow/runtime.json") {
		t.Errorf("stdout should report removed paths; got:\n%s", out.String())
	}
	if !workspace.Exists(repoRoot) {
		t.Error("workspace.json must be created after purging v1 data")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, "name: idem_app\n")

	var firstOut, secondOut strings.Builder
	first, err := Run(repoRoot, &firstOut)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	rawAfterFirst := readManifestRaw(t, repoRoot)

	second, err := Run(repoRoot, &secondOut)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	rawAfterSecond := readManifestRaw(t, repoRoot)

	if rawAfterFirst != rawAfterSecond {
		t.Errorf("manifest changed between runs:\n--- first\n%s\n--- second\n%s", rawAfterFirst, rawAfterSecond)
	}
	if second.Created {
		t.Error("second run must not re-create workspace.json")
	}
	if second.PurgedCount != 0 {
		t.Errorf("second PurgedCount = %d, want 0", second.PurgedCount)
	}
	if len(second.UpdatedPins) != 0 {
		t.Errorf("second UpdatedPins = %v, want none", second.UpdatedPins)
	}
	if !maps.Equal(first.Pins, second.Pins) {
		t.Errorf("pins drifted between runs: %v vs %v", first.Pins, second.Pins)
	}
	if strings.Contains(secondOut.String(), "(created)") {
		t.Error("second summary must not claim created")
	}
}

func TestInitUpdatesPinOnlyWhenCompatibilityChanged(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, "name: pin_app\n")
	if err := os.MkdirAll(workspace.Dir(repoRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{
  "schemaVersion": "2.0",
  "createdAt": "2026-01-01T00:00:00Z",
  "adapterPins": {"dart": "9.9.9"},
  "basisFingerprint": ""
}`
	if err := os.WriteFile(workspace.FilePath(repoRoot), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	res, err := Run(repoRoot, &out)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Created {
		t.Error("existing manifest must be loaded, not re-created")
	}
	foundDartUpdate := false
	for _, p := range res.UpdatedPins {
		if p == "dart" {
			foundDartUpdate = true
			break
		}
	}
	if !foundDartUpdate {
		t.Errorf("UpdatedPins = %v, want to include dart", res.UpdatedPins)
	}
	if res.Pins["dart"] != "0.1.0" {
		t.Errorf("pin dart = %q, want refreshed to 0.1.0", res.Pins["dart"])
	}
	raw := readManifestRaw(t, repoRoot)
	if !strings.Contains(raw, `"createdAt": "2026-01-01T00:00:00Z"`) {
		t.Errorf("CreatedAt must be preserved across pin updates; got:\n%s", raw)
	}
	if !strings.Contains(out.String(), "dart@0.1.0 (pin updated)") {
		t.Errorf("summary should flag the updated pin; got:\n%s", out.String())
	}
}

func TestInitFailsOnCorruptExistingManifest(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(workspace.Dir(repoRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace.FilePath(repoRoot), []byte(`{"schemaVersion":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(repoRoot, nil); err == nil {
		t.Error("Run() should refuse to silently overwrite a corrupt manifest")
	}
}

func TestInitRejectsMissingTargetDirectory(t *testing.T) {
	repoRoot := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := Run(repoRoot, nil); err == nil {
		t.Error("Run() should error for a nonexistent target path")
	}
}

func TestInitGeneratesTailoredLayers_NextJs(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"my-next-app","dependencies":{"next":"14.2.0","react":"18.2.0"}}`
	if err := os.WriteFile(filepath.Join(repoRoot, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if _, err := Run(repoRoot, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	cfg, err := fusion.LoadLayersConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}
	if len(cfg.Layers) != 7 {
		t.Errorf("expected 7 layers, got %d", len(cfg.Layers))
	}
	if cfg.Layers[0].Name != fusion.LayerPresentation {
		t.Errorf("layer 0 = %q, want presentation", cfg.Layers[0].Name)
	}
	foundAppPattern := false
	for _, p := range cfg.Layers[0].PathPatterns {
		if strings.Contains(p, "app") {
			foundAppPattern = true
			break
		}
	}
	if !foundAppPattern {
		t.Errorf("expected app pattern in presentation layer pathPatterns: %v", cfg.Layers[0].PathPatterns)
	}
}

func TestInitGeneratesTailoredLayers_FSD(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "src", "features"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "src", "entities"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if _, err := Run(repoRoot, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	cfg, err := fusion.LoadLayersConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}
	if len(cfg.Layers) != 7 {
		t.Errorf("expected 7 layers, got %d", len(cfg.Layers))
	}
	// Controller layer in FSD should include feature in aliases
	foundFeatureAlias := false
	for _, a := range cfg.Layers[1].Aliases {
		if a == "feature" {
			foundFeatureAlias = true
			break
		}
	}
	if !foundFeatureAlias {
		t.Errorf("expected 'feature' in controller aliases for FSD: %v", cfg.Layers[1].Aliases)
	}
}

func TestInitGeneratesTailoredLayers_ReactSPA(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "src", "components"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"my-spa","dependencies":{"react":"18.2.0"}}`
	if err := os.WriteFile(filepath.Join(repoRoot, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if _, err := Run(repoRoot, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	cfg, err := fusion.LoadLayersConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}
	if len(cfg.Layers) != 7 {
		t.Errorf("expected 7 layers, got %d", len(cfg.Layers))
	}
	if cfg.Layers[0].Name != fusion.LayerPresentation {
		t.Errorf("layer 0 = %q, want presentation", cfg.Layers[0].Name)
	}
}

func TestInitPreservesExistingLayersYaml(t *testing.T) {
	repoRoot := t.TempDir()
	customYaml := `version: 1
strictOrder: false
allowUnknownLayer: true
layers:
  - name: presentation
    aliases: [my_custom_ui]
`
	if err := os.WriteFile(filepath.Join(repoRoot, "codeflow.layers.yaml"), []byte(customYaml), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if _, err := Run(repoRoot, &out); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(repoRoot, "codeflow.layers.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customYaml {
		t.Errorf("codeflow.layers.yaml was modified:\n%s", string(data))
	}
}
