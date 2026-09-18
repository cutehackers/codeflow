package protocol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAdapterBundledPrecedenceOverGlobalBin(t *testing.T) {
	// Create simulated HOME with global adapter in ~/.local/bin
	tempHome := t.TempDir()
	globalBinDir := filepath.Join(tempHome, ".local", "bin")
	if err := os.MkdirAll(globalBinDir, 0o755); err != nil {
		t.Fatalf("create global bin dir: %v", err)
	}
	globalTSAdapter := filepath.Join(globalBinDir, "codeflow_ts_adapter")
	if err := os.WriteFile(globalTSAdapter, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write global adapter: %v", err)
	}

	// Create simulated checkout with bundled adapters/typescript
	tempCheckout := t.TempDir()
	bundledDir := filepath.Join(tempCheckout, "adapters", "typescript", "bin")
	if err := os.MkdirAll(bundledDir, 0o755); err != nil {
		t.Fatalf("create bundled dir: %v", err)
	}
	bundledEntry := filepath.Join(bundledDir, "codeflow_ts_adapter.js")
	if err := os.WriteFile(bundledEntry, []byte("// bundled\n"), 0o644); err != nil {
		t.Fatalf("write bundled entry: %v", err)
	}

	t.Setenv("HOME", tempHome)
	t.Setenv(TypeScriptAdapterEnvVar, "")
	t.Setenv(TSAdapterEnvVar, "")

	cfg, err := ResolveAdapterForRepo(tempCheckout, tempCheckout, "typescript", "")
	if err != nil {
		t.Fatalf("ResolveAdapterForRepo failed: %v", err)
	}

	// Must resolve to node executing the checkout's bundled entry, NOT the global bin
	if len(cfg.Args) == 0 {
		t.Fatalf("expected args with bundled entrypoint, got: %+v", cfg)
	}
	resolvedEntry := filepath.Clean(cfg.Args[0])
	if resolvedEntry != filepath.Clean(bundledEntry) {
		t.Fatalf("resolved adapter = %q, want bundled entry %q", resolvedEntry, bundledEntry)
	}
}

func TestResolveAdapterEnvVarPrecedence(t *testing.T) {
	tempDir := t.TempDir()
	customBin := filepath.Join(tempDir, "custom-adapter")
	if err := os.WriteFile(customBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write custom binary: %v", err)
	}

	t.Setenv(TypeScriptAdapterEnvVar, customBin)

	root := repoRootDir(t)
	cfg, err := ResolveAdapterForRepo(root, root, "typescript", "")
	if err != nil {
		t.Fatalf("ResolveAdapterForRepo failed: %v", err)
	}

	if cfg.BinPath != customBin {
		t.Fatalf("resolved BinPath = %q, want env override %q", cfg.BinPath, customBin)
	}
}

func TestResolveAdapterFallbackToLocalBin(t *testing.T) {
	tempHome := t.TempDir()
	globalBinDir := filepath.Join(tempHome, ".local", "bin")
	if err := os.MkdirAll(globalBinDir, 0o755); err != nil {
		t.Fatalf("create global bin dir: %v", err)
	}
	globalTSAdapter := filepath.Join(globalBinDir, "codeflow_ts_adapter")
	if err := os.WriteFile(globalTSAdapter, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write global adapter: %v", err)
	}

	// Isolated directory without any bundled adapters
	emptyTarget := t.TempDir()

	t.Setenv("HOME", tempHome)
	t.Setenv(TypeScriptAdapterEnvVar, "")
	t.Setenv(TSAdapterEnvVar, "")

	cfg, err := ResolveAdapterForRepo(emptyTarget, emptyTarget, "typescript", "")
	if err != nil {
		t.Fatalf("ResolveAdapterForRepo failed: %v", err)
	}

	if cfg.BinPath != globalTSAdapter {
		t.Fatalf("resolved BinPath = %q, want global bin %q", cfg.BinPath, globalTSAdapter)
	}
}

func TestResolveAdapterBundledInCurrentRepository(t *testing.T) {
	root := repoRootDir(t)
	target := filepath.Join(root, "testdata", "ts_example_app")

	cfg, err := ResolveAdapterForRepo(target, root, "typescript", "")
	if err != nil {
		t.Fatalf("ResolveAdapterForRepo failed: %v", err)
	}

	expectedEntry := filepath.Join(root, "adapters", "typescript", "bin", "codeflow_ts_adapter.js")
	if len(cfg.Args) == 0 || filepath.Clean(cfg.Args[0]) != expectedEntry {
		t.Fatalf("resolved Args = %v, want entry %q", cfg.Args, expectedEntry)
	}
}

func TestResolveAdapterLanguageNormalization(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		wantLang string
	}{
		{name: "empty defaults to dart", input: "", wantLang: "dart"},
		{name: "whitespace defaults to dart", input: "   ", wantLang: "dart"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(DartAdapterEnvVar, "/tmp/mock-dart-adapter")
			// Create dummy file so stat passes if needed
			if err := os.WriteFile("/tmp/mock-dart-adapter", []byte("#!/bin/sh\n"), 0o755); err == nil {
				defer os.Remove("/tmp/mock-dart-adapter")
			}
			cfg, err := ResolveAdapter(tc.input, "/tmp/mock-dart-adapter")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(cfg.BinPath, "mock-dart-adapter") {
				t.Fatalf("resolved BinPath = %q, want mock-dart-adapter", cfg.BinPath)
			}
		})
	}
}

func TestResolveAdapterFallbackToShareDirectory(t *testing.T) {
	tempHome := t.TempDir()
	shareTSDir := filepath.Join(tempHome, ".local", "share", "codeflow", "adapters", "typescript", "bin")
	if err := os.MkdirAll(shareTSDir, 0o755); err != nil {
		t.Fatalf("create share dir: %v", err)
	}
	shareEntry := filepath.Join(shareTSDir, "codeflow_ts_adapter.js")
	if err := os.WriteFile(shareEntry, []byte("// share\n"), 0o644); err != nil {
		t.Fatalf("write share entry: %v", err)
	}

	emptyTarget := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv(TypeScriptAdapterEnvVar, "")
	t.Setenv(TSAdapterEnvVar, "")

	cfg, err := ResolveAdapterForRepo(emptyTarget, emptyTarget, "typescript", "")
	if err != nil {
		t.Fatalf("ResolveAdapterForRepo failed: %v", err)
	}

	if len(cfg.Args) == 0 || filepath.Clean(cfg.Args[0]) != filepath.Clean(shareEntry) {
		t.Fatalf("resolved Args = %v, want share entry %q", cfg.Args, shareEntry)
	}
}

func TestResolveAdapterUnconfiguredError(t *testing.T) {
	tempHome := t.TempDir()
	emptyTarget := t.TempDir()

	t.Setenv("HOME", tempHome)
	t.Setenv(TypeScriptAdapterEnvVar, "")
	t.Setenv(TSAdapterEnvVar, "")

	_, err := ResolveAdapterForRepo(emptyTarget, emptyTarget, "typescript", "")
	if err == nil {
		t.Fatal("expected error for unconfigured adapter, got nil")
	}
	if !strings.Contains(err.Error(), "no typescript adapter configured") {
		t.Fatalf("expected 'no typescript adapter configured' in error, got %v", err)
	}
}
