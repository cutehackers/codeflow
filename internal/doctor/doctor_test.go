package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

func TestDiagnoseUninitializedWorkspace(t *testing.T) {
	dir := t.TempDir()
	results := Diagnose(dir, "")

	if len(results) == 0 {
		t.Fatal("expected diagnostic results, got none")
	}

	var foundManifestCheck, foundSchemaCheck bool
	for _, r := range results {
		if r.Name == "Workspace manifest" {
			foundManifestCheck = true
			if r.Passed {
				t.Errorf("Workspace manifest check should fail for uninitialized dir: %+v", r)
			}
			if !strings.Contains(r.Message, "not initialized") {
				t.Errorf("expected 'not initialized' in message, got: %q", r.Message)
			}
		}
		if r.Name == "Contract schemas" {
			foundSchemaCheck = true
			if !r.Passed {
				t.Errorf("Contract schemas should pass: %+v", r)
			}
		}
	}
	if !foundManifestCheck {
		t.Error("missing 'Workspace manifest' check in results")
	}
	if !foundSchemaCheck {
		t.Error("missing 'Contract schemas' check in results")
	}
}

func TestDiagnoseInitializedTypeScriptWorkspace(t *testing.T) {
	dir := t.TempDir()

	// 1. Initialize workspace manifest
	ws := workspace.New(dir)
	if err := ws.Save(); err != nil {
		t.Fatalf("failed to save workspace manifest: %v", err)
	}

	// 2. Create package.json
	pkgJSON := `{"name": "my-ts-app", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	results := Diagnose(dir, "")
	names := make(map[string]CheckResult)
	for _, r := range results {
		names[r.Name] = r
	}

	if wsRes, ok := names["Workspace manifest"]; !ok || !wsRes.Passed {
		t.Errorf("Workspace manifest should pass: %+v", wsRes)
	}
	if nodeCheck, ok := names["Node.js Runtime"]; !ok {
		t.Errorf("expected Node.js Runtime check for TypeScript project")
	} else if !nodeCheck.Passed {
		t.Logf("node not in path during test: %s", nodeCheck.Message)
	}
	if _, ok := names["TypeScript adapter"]; !ok {
		t.Errorf("expected TypeScript adapter check in results")
	}
}

func TestDiagnoseWithPublishedGeneration(t *testing.T) {
	dir := t.TempDir()

	// Initialize workspace
	ws := workspace.New(dir)
	if err := ws.Save(); err != nil {
		t.Fatalf("failed to save workspace manifest: %v", err)
	}

	st := storage.New(dir)
	if err := st.InitLayout(); err != nil {
		t.Fatalf("InitLayout failed: %v", err)
	}

	sess, err := st.BeginGeneration("basis-sha-1234567890abcdef")
	if err != nil {
		t.Fatalf("BeginGeneration failed: %v", err)
	}
	flowID := "flow-1234567890abcdef"
	summary := storage.FlowSummary{
		FlowID:          flowID,
		Title:           "Login Flow",
		Description:     "User authentication flow",
		EntrySymbolPath: "lib/auth.dart#login",
		StepCount:       3,
	}
	if err := sess.AddFlowSpec(flowID, []byte(`{"flowId":"`+flowID+`"}`), summary); err != nil {
		t.Fatalf("AddFlowSpec failed: %v", err)
	}
	if err := sess.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	results := Diagnose(dir, "")
	names := make(map[string]CheckResult)
	for _, r := range results {
		names[r.Name] = r
	}

	if ptr, ok := names["Generation pointer"]; !ok || !ptr.Passed {
		t.Errorf("Generation pointer should pass: %+v", ptr)
	}
	if idx, ok := names["Generation index"]; !ok || !idx.Passed {
		t.Errorf("Generation index should pass: %+v", idx)
	}
}

func TestDiagnoseDartProjectWithAdapterSpec(t *testing.T) {
	dir := t.TempDir()

	// Create pubspec.yaml
	pubspec := "name: my_dart_app\n"
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(pubspec), 0o644); err != nil {
		t.Fatalf("write pubspec: %v", err)
	}

	// Create dummy adapter binary
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dummyAdapter := filepath.Join(binDir, "dummy_adapter")
	if err := os.WriteFile(dummyAdapter, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	results := Diagnose(dir, dummyAdapter)
	names := make(map[string]CheckResult)
	for _, r := range results {
		names[r.Name] = r
	}

	if adapterCheck, ok := names["Dart adapter"]; !ok || !adapterCheck.Passed {
		t.Errorf("Dart adapter should pass with valid spec: %+v", adapterCheck)
	}
}

func TestDiagnoseResultProperties(t *testing.T) {
	cr := CheckResult{
		Name:    "Test Check",
		Passed:  true,
		Message: "Everything OK",
	}
	if cr.Name != "Test Check" || !cr.Passed || cr.Message != "Everything OK" {
		t.Errorf("CheckResult fields mismatch: %+v", cr)
	}
}
