package slicing_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source location")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func dartOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skipf("dart SDK not found in PATH: %v", err)
	}
}

func TestSlicingIntegration(t *testing.T) {
	dartOrSkip(t)
	root := moduleRoot(t)
	spec := "dartrun:" + filepath.Join(root, "adapters", "dart")

	cfg, err := harvest.ResolveDartAdapter(spec)
	if err != nil {
		t.Fatalf("ResolveDartAdapter: %v", err)
	}
	cfg.DefaultTimeout = 60 * time.Second

	pool := protocol.NewPool(cfg, 1)
	defer pool.Close()

	runner := slicing.NewRunner(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exampleApp := filepath.Join(root, "testdata", "example_app")
	candidateID := "cand-1234567890abcdef"
	entry := "lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit"

	payload, err := runner.Slice(ctx, exampleApp, candidateID, entry, nil)
	if err != nil {
		t.Fatalf("runner.Slice failed: %v", err)
	}

	if payload.CandidateID != candidateID {
		t.Errorf("got candidateId %q, want %q", payload.CandidateID, candidateID)
	}
	if payload.EntrySymbolPath != entry {
		t.Errorf("got entrySymbolPath %q, want %q", payload.EntrySymbolPath, entry)
	}
	if len(payload.Steps) == 0 {
		t.Errorf("expected slice steps, got 0")
	}

	t.Logf("Sliced %d steps, %d edges from %s", len(payload.Steps), len(payload.Edges), entry)
}
