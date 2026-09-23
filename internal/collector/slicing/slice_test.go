package slicing_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/harvest"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/collector/storage"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source location")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
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
		t.Errorf("expected at least 1 step, got 0")
	}

	t.Logf("Sliced %d steps, %d edges from %s", len(payload.Steps), len(payload.Edges), entry)
}

func TestTypeScriptSlicingIntegration(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found in PATH: %v", err)
	}
	root := moduleRoot(t)
	spec := "noderun:" + filepath.Join(root, "adapters", "typescript")

	cfg, err := harvest.ResolveAdapter("typescript", spec)
	if err != nil {
		t.Fatalf("ResolveAdapter(typescript): %v", err)
	}
	cfg.DefaultTimeout = 60 * time.Second

	pool := protocol.NewPool(cfg, 1)
	defer pool.Close()

	runner := slicing.NewRunner(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exampleApp := filepath.Join(root, "testdata", "ts_example_app")
	candidateID := "cand-d2d1a08c8b3668f9"
	entry := "src/features/auth/LoginView.tsx#onSubmit"

	payload, err := runner.Slice(ctx, exampleApp, candidateID, entry, nil)
	if err != nil {
		t.Fatalf("runner.Slice failed: %v", err)
	}

	if payload.CandidateID != candidateID {
		t.Errorf("CandidateID = %q, want %q", payload.CandidateID, candidateID)
	}
	if payload.Language != "typescript" {
		t.Errorf("Language = %q, want typescript", payload.Language)
	}
	if len(payload.Steps) == 0 {
		t.Errorf("expected at least 1 step, got 0")
	}

	foundBoundary := false
	for _, edge := range payload.Edges {
		if edge.Kind == "boundary_call" {
			foundBoundary = true
			break
		}
	}
	if !foundBoundary {
		t.Errorf("expected boundary_call edge in slice payload, got: %+v", payload.Edges)
	}
}

var (
	mockAdapterBinaryOnce  sync.Once
	mockAdapterBinaryPath  string
	mockAdapterBinaryError error
)

func provideMockAdapterBinary(t *testing.T) string {
	t.Helper()
	mockAdapterBinaryOnce.Do(func() {
		tempDir, err := os.MkdirTemp("", "codeflow-slicing-mockadapter-*")
		if err != nil {
			mockAdapterBinaryError = err
			return
		}
		bin := filepath.Join(tempDir, "mockadapter")
		build := exec.Command("go", "build", "-o", bin, "./internal/analyzer/mockadapter")
		build.Dir = moduleRoot(t)
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := build.CombinedOutput(); err != nil {
			mockAdapterBinaryError = fmt.Errorf("build mock adapter: %w\n%s", err, output)
			return
		}
		mockAdapterBinaryPath = bin
	})
	if mockAdapterBinaryError != nil {
		t.Fatalf("build mock adapter: %v", mockAdapterBinaryError)
	}
	return mockAdapterBinaryPath
}

func TestSliceCacheHitIsSnapshotAndBasisBound(t *testing.T) {
	root := t.TempDir()
	bin := provideMockAdapterBinary(t)

	snapshot, err := protocol.NewSnapshot(4, map[string]string{
		"mock.dart": "class Mock { void run() {} }\n",
	}, "slice-cache-basis-a")
	if err != nil {
		t.Fatal(err)
	}
	const candidateID = "cand-cache0001"
	const entry = "mock.dart#Mock.run"
	p := protocol.NewPool(protocol.Config{BinPath: bin}, 1)
	runner := slicing.NewRunner(p)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := runner.SliceWithSnapshot(ctx, root, candidateID, entry, nil, snapshot)
	if err != nil {
		t.Fatalf("first slice: %v", err)
	}
	if first.CandidateID != candidateID || first.EntrySymbolPath != entry {
		t.Fatalf("first slice identity = %q/%q", first.CandidateID, first.EntrySymbolPath)
	}

	// Closing the pool makes any adapter call fail. A successful second call
	// therefore proves the cache hit returns the v2 operation payload directly.
	p.Close()
	second, err := runner.SliceWithSnapshot(ctx, root, candidateID, entry, nil, snapshot)
	if err != nil {
		t.Fatalf("cache hit after adapter pool close: %v", err)
	}
	if second.CandidateID != first.CandidateID || second.EntrySymbolPath != first.EntrySymbolPath {
		t.Fatalf("cache hit changed payload identity: first=%+v second=%+v", first, second)
	}

	wrongBasis, err := protocol.NewSnapshot(4, map[string]string{
		"mock.dart": "class Mock { void run() {} }\n",
	}, "slice-cache-basis-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.SliceWithSnapshot(ctx, root, candidateID, entry, nil, wrongBasis); err == nil {
		t.Fatal("cache entry from a different snapshot basis was reused")
	}
}

func TestSliceCacheRejectsPriorExecutionRelationSemantics(t *testing.T) {
	root := t.TempDir()
	const source = "class Mock { void run() {} }\n"
	snapshot, err := protocol.NewSnapshot(4, map[string]string{"mock.dart": source}, "binding-basis")
	if err != nil {
		t.Fatal(err)
	}
	pool := protocol.NewPool(protocol.Config{BinPath: provideMockAdapterBinary(t)}, 1)
	defer pool.Close()
	runner := slicing.NewRunner(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const candidateID = "cand-bindingcache"
	if _, err := runner.SliceWithSnapshot(ctx, root, candidateID, "mock.dart#Mock.run", nil, snapshot); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, ".codeflow", "facts", "slice")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v, error = %v", entries, err)
	}
	// Preserve a valid result under the immediately previous execution-relation
	// cache identity. A finalizer throw recovery cannot be inferred from that payload.
	legacyKey := storage.SliceCacheKey(fmt.Sprintf("%x", sha256.Sum256([]byte(source))), candidateID, "v34-prevailing-finally-return|binding-basis", "")
	if err := os.Rename(filepath.Join(dir, entries[0].Name()), filepath.Join(dir, legacyKey+".json")); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if _, err := runner.SliceWithSnapshot(ctx, root, candidateID, "mock.dart#Mock.run", nil, snapshot); err == nil {
		t.Fatal("analysis reused a result produced by the previous execution-relation implementation")
	}
}
