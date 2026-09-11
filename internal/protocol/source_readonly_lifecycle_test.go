package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"codeflow/internal/evidence"
	"codeflow/internal/workspace"
)

// TestSourceReadOnlyAdapterLifecycle exercises the actual framed adapter
// boundary with one VS-01 lease. Every terminal mode uses the same immutable
// request and verifies that the live source path and lease write audit remain
// unchanged by the adapter process.
func TestSourceReadOnlyAdapterLifecycle(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "lib", "feature.go")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("package disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := workspace.NewSnapshotEngine(root, 7)
	if err != nil {
		t.Fatal(err)
	}
	_, header, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "lib/feature.go", Content: []byte("package snapshot\nfunc Run() {}\n"), DocumentVersion: 1,
		Source: workspace.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.SnapshotVFS(header.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	snapshot, err := SnapshotFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	if expected := os.Getenv("CODEFLOW_A11_EXPECTED_TREE_DIGEST"); expected != "" && snapshot.RootTreeID != expected {
		t.Fatalf("lifecycle snapshot tree digest = %q, want runner lease %q", snapshot.RootTreeID, expected)
	}
	params := snapshot.Params()
	if _, ok := params["repoRoot"]; ok {
		t.Fatal("snapshot adapter request exposed repoRoot")
	}

	// Mutate the worktree after capture. The adapter receives only the captured
	// bytes and the mutation must remain untouched through every lifecycle path.
	liveAfterCapture := []byte("package main\nfunc LiveMutation() {}\n")
	if err := os.WriteFile(sourcePath, liveAfterCapture, 0o644); err != nil {
		t.Fatal(err)
	}
	expectedDigest := digestFile(t, sourcePath)
	audit := lease.SourceWriteAudit()
	bin := buildMockAdapter(t)
	disposableRoot := filepath.Join(t.TempDir(), "adapter-disposable")
	if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cwdLog := filepath.Join(t.TempDir(), "adapter-cwds.log")
	writeEnv := func(extra map[string]string) []string {
		overrides := map[string]string{
			"MOCK_WRITE_RELATIVE": "1",
			"MOCK_CWD_LOG":        cwdLog,
		}
		for key, value := range extra {
			overrides[key] = value
		}
		return faultEnv(overrides)
	}

	// Success: the response is bound to the captured snapshot identity.
	successPool := NewPool(Config{BinPath: bin, Env: writeEnv(nil), DisposableRoot: disposableRoot, DefaultTimeout: 2 * time.Second}, 1)
	var success evidence.Result
	if err := successPool.Call(context.Background(), OpDetect, params, &success); err != nil {
		t.Fatalf("snapshot success failed: %v", err)
	}
	if success.SnapshotID != snapshot.SnapshotID {
		t.Fatalf("response snapshotId = %q, want %q", success.SnapshotID, snapshot.SnapshotID)
	}
	successPool.Close()
	successEvidence := successPool.MountPermissionEvidence()
	assertSourceReadOnly(t, sourcePath, expectedDigest, lease, audit)
	assertDisposableCWDs(t, cwdLog, root, disposableRoot)

	// Cancellation must resolve the caller and reap the old subprocess.
	cancelConn := spawnConn(t, bin, map[string]string{"MOCK_HANG_OPS": "detect", "MOCK_WRITE_RELATIVE": "1", "MOCK_CWD_LOG": cwdLog}, func(cfg *Config) {
		cfg.DisposableRoot = disposableRoot
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- cancelConn.Call(ctx, OpDetect, params, nil) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		wantCode(t, err, ECancelled)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled snapshot call did not settle")
	}
	assertConnTerminated(t, cancelConn)
	cancelEvidence := cancelConn.MountPermissionEvidence()
	assertSourceReadOnly(t, sourcePath, expectedDigest, lease, audit)
	assertDisposableCWDs(t, cwdLog, root, disposableRoot)

	// Timeout uses the same cleanup path even without caller cancellation.
	timeoutConn := spawnConn(t, bin, map[string]string{"MOCK_HANG_OPS": "detect", "MOCK_WRITE_RELATIVE": "1", "MOCK_CWD_LOG": cwdLog}, func(cfg *Config) {
		cfg.DisposableRoot = disposableRoot
	})
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	err = timeoutConn.Call(timeoutCtx, OpDetect, params, nil)
	timeoutCancel()
	wantCode(t, err, ETimeout)
	assertConnTerminated(t, timeoutConn)
	timeoutEvidence := timeoutConn.MountPermissionEvidence()
	assertSourceReadOnly(t, sourcePath, expectedDigest, lease, audit)
	assertDisposableCWDs(t, cwdLog, root, disposableRoot)

	// Crash recovery starts a replacement and retries the identical snapshot
	// request. The crash state is outside the repository under test.
	crashState := filepath.Join(t.TempDir(), "crash-state.json")
	crashPool := NewPool(Config{
		BinPath:        bin,
		DisposableRoot: disposableRoot,
		Env: writeEnv(map[string]string{
			"MOCK_CRASH_AFTER_N_REQUESTS": "1",
			"MOCK_CRASH_STATE_FILE":       crashState,
		}),
	}, 1)
	var recovered evidence.Result
	if err := crashPool.Call(context.Background(), OpDetect, params, &recovered); err != nil {
		t.Fatalf("crash retry failed: %v", err)
	}
	if recovered.SnapshotID != snapshot.SnapshotID {
		t.Fatalf("recovered snapshotId = %q, want %q", recovered.SnapshotID, snapshot.SnapshotID)
	}
	crashPool.Close()
	crashEvidence := crashPool.MountPermissionEvidence()
	assertSourceReadOnly(t, sourcePath, expectedDigest, lease, audit)
	assertDisposableCWDs(t, cwdLog, root, disposableRoot)

	failedRoot := filepath.Join(t.TempDir(), "failed-disposable")
	if err := os.MkdirAll(failedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Spawn(context.Background(), Config{
		BinPath:        filepath.Join(t.TempDir(), "missing-adapter"),
		DisposableRoot: failedRoot,
	}); err == nil {
		t.Fatal("failed spawn unexpectedly succeeded")
	}
	failedSpawnClean := false
	if entries, err := os.ReadDir(failedRoot); err == nil {
		failedSpawnClean = len(entries) == 0
	} else {
		t.Fatal(err)
	}
	if !failedSpawnClean {
		t.Fatal("failed spawn leaked disposable directories")
	}
	if artifactPath := os.Getenv("CODEFLOW_A11_EVIDENCE_PATH"); artifactPath != "" {
		mergedEvidence := mergeMountEvidence(successEvidence, cancelEvidence, timeoutEvidence, crashEvidence)
		t.Logf("A11 isolation evidence success=%+v cancel=%+v timeout=%+v crash=%+v merged=%+v", successEvidence, cancelEvidence, timeoutEvidence, crashEvidence, mergedEvidence)
		writeLifecycleEvidenceArtifact(t, artifactPath, snapshot, audit, mergedEvidence, failedSpawnClean)
	}
}

func TestAdapterProcessIsolationAndCleanup(t *testing.T) {
	root := t.TempDir()
	bin := buildMockAdapter(t)
	workRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cwdLog := filepath.Join(t.TempDir(), "adapter-cwds.log")
	env := faultEnv(map[string]string{
		"MOCK_WRITE_RELATIVE": "1",
		"MOCK_CWD_LOG":        cwdLog,
	})
	snapshot, err := NewSnapshot(7, map[string]string{"main.go": "package main\n"}, "isolation-basis")
	if err != nil {
		t.Fatal(err)
	}
	params := snapshot.Params()
	cfg := Config{BinPath: bin, Env: env, DisposableRoot: workRoot}
	conn, err := Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var success evidence.Result
	if err := conn.Call(context.Background(), OpDetect, params, &success); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	assertDisposableCWDs(t, cwdLog, root, workRoot)

	cancelEnv := faultEnv(map[string]string{
		"MOCK_WRITE_RELATIVE": "1",
		"MOCK_CWD_LOG":        cwdLog,
		"MOCK_HANG_OPS":       "detect",
	})
	cancelConn, err := Spawn(context.Background(), Config{BinPath: bin, Env: cancelEnv, DisposableRoot: workRoot})
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- cancelConn.Call(cancelCtx, OpDetect, params, nil) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-cancelDone:
		wantCode(t, err, ECancelled)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled isolated call did not settle")
	}
	assertDisposableCWDs(t, cwdLog, root, workRoot)

	timeoutEnv := faultEnv(map[string]string{
		"MOCK_WRITE_RELATIVE": "1",
		"MOCK_CWD_LOG":        cwdLog,
		"MOCK_HANG_OPS":       "detect",
	})
	timeoutConn, err := Spawn(context.Background(), Config{BinPath: bin, Env: timeoutEnv, DisposableRoot: workRoot})
	if err != nil {
		t.Fatal(err)
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = timeoutConn.Call(timeoutCtx, OpDetect, params, nil)
	timeoutCancel()
	wantCode(t, err, ETimeout)
	assertDisposableCWDs(t, cwdLog, root, workRoot)

	crashState := filepath.Join(t.TempDir(), "crash-state.json")
	crashEnv := faultEnv(map[string]string{
		"MOCK_WRITE_RELATIVE":         "1",
		"MOCK_CWD_LOG":                cwdLog,
		"MOCK_CRASH_AFTER_N_REQUESTS": "1",
		"MOCK_CRASH_STATE_FILE":       crashState,
	})
	crashPool := NewPool(Config{BinPath: bin, Env: crashEnv, DisposableRoot: workRoot}, 1)
	var recovered evidence.Result
	if err := crashPool.Call(context.Background(), OpDetect, params, &recovered); err != nil {
		t.Fatal(err)
	}
	crashPool.Close()
	assertDisposableCWDs(t, cwdLog, root, workRoot)

	failedRoot := filepath.Join(t.TempDir(), "failed-disposable")
	if err := os.MkdirAll(failedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Spawn(context.Background(), Config{
		BinPath:        filepath.Join(t.TempDir(), "missing-adapter"),
		DisposableRoot: failedRoot,
	}); err == nil {
		t.Fatal("failed spawn unexpectedly succeeded")
	}
	if entries, err := os.ReadDir(failedRoot); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("failed spawn leaked disposable directories: %+v", entries)
	}
}

func TestConnPublishesMountPermissionEvidenceAfterCleanup(t *testing.T) {
	bin := buildMockAdapter(t)
	workRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	conn, err := Spawn(context.Background(), Config{BinPath: bin, DisposableRoot: workRoot})
	if err != nil {
		t.Fatal(err)
	}
	before := conn.MountPermissionEvidence()
	if before.SourceDelivery != "" || before.SourceMount != "unproven" ||
		before.WorkingDirectoryMode != "process_private_disposable" || before.WorkingDirectoryPermission != "0700" ||
		before.ReadOnlySource || !before.Disposable || before.RepositoryPathExposed || before.CleanupVerified {
		t.Fatalf("unexpected pre-close isolation evidence: %+v", before)
	}
	snapshot, err := NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "evidence-basis")
	if err != nil {
		t.Fatal(err)
	}
	var result evidence.Result
	if err := conn.Call(context.Background(), OpDetect, snapshot.Params(), &result); err != nil {
		t.Fatal(err)
	}
	verified := conn.MountPermissionEvidence()
	if verified.SourceDelivery != "protocol_snapshot_bytes" || verified.SourceMount != "not_mounted" || !verified.ReadOnlySource || verified.RepositoryPathExposed {
		t.Fatalf("snapshot delivery evidence was not verified after analysis: %+v", verified)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	after := conn.MountPermissionEvidence()
	if !after.CleanupVerified || !after.DependencyEnvironmentPreserved {
		t.Fatalf("cleanup/dependency evidence missing after close: %+v", after)
	}
}

func assertDisposableCWDs(t *testing.T, logPath, repositoryRoot, disposableRoot string) {
	t.Helper()
	repositoryRoot = canonicalTestPath(t, repositoryRoot)
	disposableRoot = canonicalTestPath(t, disposableRoot)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(strings.TrimSpace(raw), "\t")
		if len(fields) != 4 {
			t.Fatalf("adapter write audit has invalid fields: %q", raw)
		}
		cwd := canonicalTestPath(t, fields[0])
		pwd := canonicalTestPath(t, strings.TrimSpace(fields[1]))
		oldpwd := canonicalTestPath(t, strings.TrimSpace(fields[2]))
		if cwd == "" || pwd == "" {
			continue
		}
		if cwd == repositoryRoot || strings.HasPrefix(cwd, repositoryRoot+string(os.PathSeparator)) {
			t.Fatalf("adapter cwd exposed repository path: %q", cwd)
		}
		if pwd == repositoryRoot || strings.HasPrefix(pwd, repositoryRoot+string(os.PathSeparator)) {
			t.Fatalf("adapter PWD exposed repository path: %q", pwd)
		}
		if oldpwd == repositoryRoot || strings.HasPrefix(oldpwd, repositoryRoot+string(os.PathSeparator)) {
			t.Fatalf("adapter OLDPWD exposed repository path: %q", oldpwd)
		}
		if cwd == disposableRoot || !strings.HasPrefix(cwd, disposableRoot+string(os.PathSeparator)) {
			t.Fatalf("adapter cwd is not process-disposable: %q", cwd)
		}
		if pwd != cwd {
			t.Fatalf("adapter PWD differs from cwd: cwd=%q pwd=%q", cwd, pwd)
		}
		if oldpwd != cwd {
			t.Fatalf("adapter OLDPWD differs from cwd: cwd=%q oldpwd=%q", cwd, oldpwd)
		}
		if strings.TrimSpace(fields[3]) != "true" {
			t.Fatalf("adapter relative write was not observed in disposable cwd: %q", raw)
		}
		if _, err := os.Stat(cwd); !os.IsNotExist(err) {
			t.Fatalf("disposable adapter cwd was not cleaned: %q err=%v", cwd, err)
		}
	}
	if entries, err := os.ReadDir(disposableRoot); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("disposable root retained adapter state: %+v", entries)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if resolvedParent, parentErr := filepath.EvalSymlinks(parent); parentErr == nil {
		return filepath.Join(resolvedParent, filepath.Base(path))
	}
	return filepath.Clean(path)
}

func assertSourceReadOnly(t *testing.T, sourcePath, expectedDigest string, lease workspace.SnapshotLease, expectedAudit workspace.SourceWriteAudit) {
	t.Helper()
	if got := digestFile(t, sourcePath); got != expectedDigest {
		t.Fatalf("adapter changed repository source digest: got %s want %s", got, expectedDigest)
	}
	if got := lease.SourceWriteAudit(); !reflect.DeepEqual(got, expectedAudit) {
		t.Fatalf("adapter changed snapshot write audit: got %+v want %+v", got, expectedAudit)
	}
}

func digestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mergeMountEvidence(values ...evidence.MountPermissionEvidence) evidence.MountPermissionEvidence {
	var merged evidence.MountPermissionEvidence
	first := true
	for _, value := range values {
		if first {
			merged = value
			merged.TerminalModes = append([]string(nil), value.TerminalModes...)
			first = false
			continue
		}
		merged.SourceDelivery = firstNonEmpty(merged.SourceDelivery, value.SourceDelivery)
		merged.SourceMount = firstNonEmpty(merged.SourceMount, value.SourceMount)
		merged.WorkingDirectoryMode = firstNonEmpty(merged.WorkingDirectoryMode, value.WorkingDirectoryMode)
		merged.WorkingDirectoryPermission = firstNonEmpty(merged.WorkingDirectoryPermission, value.WorkingDirectoryPermission)
		merged.ReadOnlySource = merged.ReadOnlySource && value.ReadOnlySource
		merged.Disposable = merged.Disposable && value.Disposable
		merged.RepositoryPathExposed = merged.RepositoryPathExposed || value.RepositoryPathExposed
		merged.DependencyEnvironmentPreserved = merged.DependencyEnvironmentPreserved && value.DependencyEnvironmentPreserved
		merged.CleanupVerified = merged.CleanupVerified && value.CleanupVerified
		for _, mode := range value.TerminalModes {
			found := false
			for _, existing := range merged.TerminalModes {
				if existing == mode {
					found = true
					break
				}
			}
			if !found {
				merged.TerminalModes = append(merged.TerminalModes, mode)
			}
		}
	}
	sort.Strings(merged.TerminalModes)
	return merged
}

func firstNonEmpty(first, second string) string {
	if first != "" {
		return first
	}
	return second
}

func writeLifecycleEvidenceArtifact(t *testing.T, path string, snapshot Snapshot, audit workspace.SourceWriteAudit, isolation evidence.MountPermissionEvidence, failedSpawnClean bool) {
	t.Helper()
	artifact := evidence.IsolationLifecycleEvidence{
		SchemaID:                   evidence.IsolationLifecycleEvidenceSchemaID,
		SchemaVersion:              evidence.SchemaVersion,
		ExecutionID:                "protocol.TestSourceReadOnlyAdapterLifecycle",
		SnapshotID:                 snapshot.SnapshotID,
		SnapshotTreeDigest:         snapshot.RootTreeID,
		RepositoryPathWriteAudit:   audit,
		MountPermissionEvidence:    isolation,
		RelativeWriteObserved:      true,
		FailedSpawnCleanupVerified: failedSpawnClean,
		ProductionTests: []string{
			"protocol.TestSourceReadOnlyAdapterLifecycle",
			"protocol.TestAdapterProcessIsolationAndCleanup",
			"protocol.TestConnPublishesMountPermissionEvidenceAfterCleanup",
		},
		ArtifactRefs: []string{
			"snapshot:" + snapshot.SnapshotID,
			"tree:" + snapshot.RootTreeID,
			"isolation:protocol.TestSourceReadOnlyAdapterLifecycle",
		},
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("invalid lifecycle evidence artifact: %v (%+v)", err, artifact)
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
