package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs06"
	"codeflow/internal/workspace"
)

func TestOneShotExecutorSuccessClosesDisposableProcess(t *testing.T) {
	bin := buildRuntimeMockAdapter(t)
	disposableRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	cwdLog := filepath.Join(t.TempDir(), "cwd.log")
	snapshot, err := protocol.NewSnapshot(4, map[string]string{
		"main.go": "package main\nfunc Run() {}\n",
	}, "runtime-success-basis")
	if err != nil {
		t.Fatal(err)
	}

	adapterConfig := protocol.Config{
		BinPath:        bin,
		DisposableRoot: disposableRoot,
		Env: []string{
			"MOCK_WRITE_RELATIVE=1",
			"MOCK_CWD_LOG=" + cwdLog,
			"REPO_ROOT=/must-not-be-exposed",
		},
	}
	spec := testExecutionSpec(bin)
	executor := NewExecutor(ExecutorConfig{
		AdapterConfig:  adapterConfig,
		Spec:           spec,
		AuditProvider:  CleanSourceAuditProvider{},
		RequireConsent: true,
	})

	result, err := executor.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "exec-success",
		Nonce:       strings.Repeat("n", 16),
		Consent:     testConsent(snapshot, spec, "exec-success", strings.Repeat("n", 16)),
		Snapshot:    snapshot,
		Operation:   protocol.OpDetect,
	})
	if err != nil {
		t.Fatalf("one-shot execution failed: %v", err)
	}
	iso := result.Isolation
	if iso.Status != rflscvs06.RuntimeTerminalSuccess || iso.ResultCode != "ok" {
		t.Fatalf("unexpected terminal result: %+v", iso)
	}
	if !iso.ProcessObserved || !iso.InputTreeVerified {
		t.Fatalf("process/input identity was not observed: %+v", iso)
	}
	if !iso.Cleanup.LayerCreated || !iso.Cleanup.LayerDisposed || !iso.Cleanup.Verified {
		t.Fatalf("disposable layer was not cleaned: %+v", iso.Cleanup)
	}
	if !iso.MountPermissionEvidence.CleanupVerified || !iso.MountPermissionEvidence.ReadOnlySource || iso.MountPermissionEvidence.RepositoryPathExposed {
		t.Fatalf("isolation evidence is incomplete: %+v", iso.MountPermissionEvidence)
	}
	if iso.SourceWriteAuditStatus != rflscvs06.RuntimeAuditClean || iso.EvidencePromotion != rflscvs06.RuntimePromotionEligible {
		t.Fatalf("clean source audit was not promotable: %+v", iso)
	}
	if result.Analyzer == nil || result.Analyzer.SnapshotID != snapshot.SnapshotID {
		t.Fatalf("missing snapshot-bound analyzer result: %+v", result.Analyzer)
	}
	encoded, err := json.Marshal(iso)
	if err != nil {
		t.Fatalf("marshal runtime isolation result: %v", err)
	}
	if err := contractharness.ValidateRuntimeIsolationResultV1(encoded); err != nil {
		t.Fatalf("executor result does not satisfy public isolation contract: %v", err)
	}
	if _, err := os.Stat(disposableRoot); err != nil {
		t.Fatalf("disposable root should remain available for reuse: %v", err)
	}
	entries, err := os.ReadDir(disposableRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("one-shot executor left disposable state: %+v", entries)
	}
	if raw, err := os.ReadFile(cwdLog); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(raw), "/must-not-be-exposed") {
		t.Fatalf("repository path leaked through adapter environment: %q", raw)
	}
}

func TestOneShotExecutorTerminalModesAlwaysCleanUp(t *testing.T) {
	bin := buildRuntimeMockAdapter(t)
	snapshot, err := protocol.NewSnapshot(4, map[string]string{"main.go": "package main\n"}, "runtime-terminal-basis")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  []string
		ctx  func() (context.Context, func())
		want string
	}{
		{name: "failure", env: []string{"MOCK_CRASH_AFTER_N_REQUESTS=1"}, ctx: func() (context.Context, func()) { return context.Background(), func() {} }, want: rflscvs06.RuntimeTerminalFailure},
		{name: "timeout", env: []string{"MOCK_HANG_OPS=detect"}, ctx: func() (context.Context, func()) {
			return context.WithTimeout(context.Background(), 150*time.Millisecond)
		}, want: rflscvs06.RuntimeTerminalTimeout},
		{name: "cancel", env: []string{"MOCK_HANG_OPS=detect"}, ctx: func() (context.Context, func()) { return context.WithCancel(context.Background()) }, want: rflscvs06.RuntimeTerminalCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disposableRoot := filepath.Join(t.TempDir(), "disposable")
			if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			spec := testExecutionSpec(bin)
			nonce := strings.Repeat("n", 16)
			executor := NewExecutor(ExecutorConfig{
				AdapterConfig: protocol.Config{BinPath: bin, Args: spec.Command.Args, Env: tc.env, DisposableRoot: disposableRoot},
				Spec:          spec, AuditProvider: CleanSourceAuditProvider{}, RequireConsent: true,
			})
			ctx, cancel := tc.ctx()
			defer cancel()
			request := ExecutionRequest{ExecutionID: "exec-" + tc.name, Nonce: nonce, Consent: testConsent(snapshot, spec, "exec-"+tc.name, nonce), Snapshot: snapshot, Operation: protocol.OpDetect}
			if tc.name == "cancel" {
				done := make(chan struct{})
				var got ExecutionResult
				var gotErr error
				go func() {
					got, gotErr = executor.Execute(ctx, request)
					close(done)
				}()
				time.Sleep(100 * time.Millisecond)
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("cancelled one-shot execution did not settle")
				}
				if gotErr == nil {
					t.Fatal("cancelled execution should return a typed cancellation error")
				}
				assertTerminalCleanup(t, got, tc.want, disposableRoot)
				return
			}
			got, gotErr := executor.Execute(ctx, request)
			if gotErr == nil {
				t.Fatalf("%s execution unexpectedly succeeded", tc.name)
			}
			assertTerminalCleanup(t, got, tc.want, disposableRoot)
		})
	}
}

func TestOneShotExecutorRejectsDigestMismatchAndRepositoryPath(t *testing.T) {
	bin := buildRuntimeMockAdapter(t)
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "runtime-digest-basis")
	if err != nil {
		t.Fatal(err)
	}
	spec := testExecutionSpec(bin)
	nonce := strings.Repeat("d", 16)
	executor := NewExecutor(ExecutorConfig{AdapterConfig: protocol.Config{BinPath: bin}, Spec: spec, RequireConsent: true})
	badTree := snapshot
	badTree.RootTreeID = strings.Repeat("f", 64)
	badTree.SourceWriteAudit.CapturedSnapshotTreeDigest = badTree.RootTreeID
	_, err = executor.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "exec-bad-tree", Nonce: nonce, Consent: testConsent(badTree, spec, "exec-bad-tree", nonce), Snapshot: badTree, Operation: protocol.OpDetect,
	})
	if !errors.Is(err, ErrSnapshotIdentityMismatch) {
		t.Fatalf("digest mismatch error = %v, want ErrSnapshotIdentityMismatch", err)
	}
	_, err = executor.Execute(context.Background(), ExecutionRequest{
		ExecutionID: "exec-repo-path", Nonce: nonce, Consent: testConsent(snapshot, spec, "exec-repo-path", nonce), Snapshot: snapshot, Operation: protocol.OpDetect,
		Params: map[string]any{"repoRoot": "/tmp/repository"},
	})
	if !errors.Is(err, ErrRepositoryPathRejected) {
		t.Fatalf("repoRoot error = %v, want ErrRepositoryPathRejected", err)
	}
}

func TestOneShotExecutorConsumesConsentNonceOnce(t *testing.T) {
	bin := buildRuntimeMockAdapter(t)
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "runtime-replay-basis")
	if err != nil {
		t.Fatal(err)
	}
	spec := testExecutionSpec(bin)
	nonce := strings.Repeat("r", 16)
	pidLog := filepath.Join(t.TempDir(), "pids.log")
	disposableRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(ExecutorConfig{
		AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: disposableRoot, Env: []string{"MOCK_PID_LOG=" + pidLog}},
		Spec:          spec, AuditProvider: CleanSourceAuditProvider{}, RequireConsent: true,
	})
	request := ExecutionRequest{
		ExecutionID: "exec-replay", Nonce: nonce,
		Consent: testConsent(snapshot, spec, "exec-replay", nonce), Snapshot: snapshot, Operation: protocol.OpDetect,
	}
	if _, err := executor.Execute(context.Background(), request); err != nil {
		t.Fatalf("first execution failed: %v", err)
	}
	second, err := executor.Execute(context.Background(), request)
	if !errors.Is(err, ErrConsentReplay) {
		t.Fatalf("second execution error = %v, want ErrConsentReplay", err)
	}
	if second.Isolation.ProcessObserved {
		t.Fatalf("replayed consent spawned a process: %+v", second.Isolation)
	}
	if pids := readPIDLog(t, pidLog); len(pids) != 1 {
		t.Fatalf("replayed consent spawned %d processes, want 1: %v", len(pids), pids)
	}
}

func TestOneShotExecutorRejectsConcurrentConsentNonceReplay(t *testing.T) {
	bin := buildRuntimeMockAdapter(t)
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "runtime-concurrent-replay-basis")
	if err != nil {
		t.Fatal(err)
	}
	spec := testExecutionSpec(bin)
	nonce := strings.Repeat("q", 16)
	pidLog := filepath.Join(t.TempDir(), "pids.log")
	disposableRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(ExecutorConfig{
		AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: disposableRoot, Env: []string{"MOCK_HANG_OPS=detect", "MOCK_PID_LOG=" + pidLog}},
		Spec:          spec, AuditProvider: CleanSourceAuditProvider{}, RequireConsent: true,
	})
	request := ExecutionRequest{
		ExecutionID: "exec-concurrent-replay", Nonce: nonce,
		Consent: testConsent(snapshot, spec, "exec-concurrent-replay", nonce), Snapshot: snapshot, Operation: protocol.OpDetect,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := make(chan struct{})
	type outcome struct {
		result ExecutionResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			result, runErr := executor.Execute(ctx, request)
			outcomes <- outcome{result: result, err: runErr}
		}()
	}
	close(start)
	waitForPIDLog(t, pidLog)
	time.Sleep(50 * time.Millisecond)
	cancel()
	var replayCount, spawnedCount int
	for range 2 {
		select {
		case got := <-outcomes:
			if errors.Is(got.err, ErrConsentReplay) {
				replayCount++
			}
			if got.result.Isolation.ProcessObserved {
				spawnedCount++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent replay executions did not settle")
		}
	}
	if replayCount != 1 || spawnedCount != 1 {
		t.Fatalf("concurrent replay outcomes = replay %d, spawned %d, want one each", replayCount, spawnedCount)
	}
	if pids := readPIDLog(t, pidLog); len(pids) != 1 {
		t.Fatalf("concurrent replay spawned %d processes, want 1: %v", len(pids), pids)
	}
}

func TestOneShotExecutorBlocksPromotionOnAuditState(t *testing.T) {
	bin := buildRuntimeMockAdapter(t)
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "runtime-audit-basis")
	if err != nil {
		t.Fatal(err)
	}
	spec := testExecutionSpec(bin)
	for _, tc := range []struct {
		name     string
		provider SourceAuditProvider
		status   string
		reason   string
	}{
		{name: "indeterminate-default", provider: nil, status: rflscvs06.RuntimeAuditIndeterminate, reason: "indeterminate"},
		{name: "unavailable", provider: SourceAuditProviderFunc(func(context.Context, SourceAuditRequest) (SourceAuditReport, error) {
			return SourceAuditReport{}, errors.New("audit unavailable")
		}), status: rflscvs06.RuntimeAuditUnavailable, reason: "unavailable"},
		{name: "attributed-write", provider: AttributedWriteSourceAuditProvider{Path: "src/main.go"}, status: rflscvs06.RuntimeAuditViolation, reason: rflscvs06.RuntimeAuditViolation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disposableRoot := filepath.Join(t.TempDir(), "disposable")
			if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			executor := NewExecutor(ExecutorConfig{
				AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: disposableRoot}, Spec: spec, AuditProvider: tc.provider,
			})
			nonce := strings.Repeat("a", 16)
			got, err := executor.Execute(context.Background(), ExecutionRequest{
				ExecutionID: "exec-audit-" + tc.name, Nonce: nonce,
				Consent: testConsent(snapshot, spec, "exec-audit-"+tc.name, nonce), Snapshot: snapshot, Operation: protocol.OpDetect,
			})
			if err != nil {
				t.Fatalf("audit test execution failed before outcome: %v", err)
			}
			if got.Isolation.SourceWriteAuditStatus != tc.status || got.Isolation.EvidencePromotion != rflscvs06.RuntimePromotionBlocked {
				t.Fatalf("unexpected audit outcome: %+v", got.Isolation)
			}
			if !strings.Contains(got.Isolation.PromotionBlockedReason, tc.reason) {
				t.Fatalf("promotion reason %q does not mention %q", got.Isolation.PromotionBlockedReason, tc.reason)
			}
		})
	}
}

func TestOneShotExecutorReconcilesConcurrentLiveEditAsUnattributed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := workspace.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, head, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	bin := buildRuntimeMockAdapter(t)
	spec := testExecutionSpec(bin)
	disposableRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(ExecutorConfig{
		AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: disposableRoot, Env: []string{"MOCK_HANG_OPS=detect"}},
		Spec:          spec, Engine: engine, AuditProvider: CleanSourceAuditProvider{},
	})
	nonce := strings.Repeat("c", 16)
	request := ExecutionRequest{ExecutionID: "exec-concurrent", Nonce: nonce, Consent: testConsent(snapshot, spec, "exec-concurrent", nonce), Snapshot: snapshot, Operation: protocol.OpDetect}
	executionCtx, executionCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer executionCancel()
	done := make(chan ExecutionResult, 1)
	errCh := make(chan error, 1)
	go func() {
		got, runErr := executor.Execute(executionCtx, request)
		done <- got
		errCh <- runErr
	}()
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n// live edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got ExecutionResult
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent edit execution did not settle")
	}
	if err := <-errCh; err == nil {
		t.Fatal("hung adapter should terminate with a timeout/cancel error")
	}
	if got.Isolation.ConcurrentWorktree.Classification != "reconciled_unattributed" {
		t.Fatalf("concurrent edit was misclassified: %+v", got.Isolation.ConcurrentWorktree)
	}
	if got.Isolation.SourceWriteAuditStatus != rflscvs06.RuntimeAuditClean {
		t.Fatalf("concurrent live edit became a runtime source violation: %+v", got.Isolation)
	}
}

func TestOneShotExecutorTerminatesAdapterProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group assertion uses the Unix ps lifecycle probe")
	}
	bin := buildRuntimeMockAdapter(t)
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "runtime-process-group-basis")
	if err != nil {
		t.Fatal(err)
	}
	spec := testExecutionSpec(bin)
	for _, tc := range []struct {
		name string
		env  []string
		ctx  func() (context.Context, context.CancelFunc, func())
		want string
	}{
		{name: "success", want: rflscvs06.RuntimeTerminalSuccess, ctx: func() (context.Context, context.CancelFunc, func()) {
			return context.Background(), func() {}, func() {}
		}},
		{name: "failure", env: []string{"MOCK_CRASH_AFTER_N_REQUESTS=1"}, want: rflscvs06.RuntimeTerminalFailure, ctx: func() (context.Context, context.CancelFunc, func()) {
			return context.Background(), func() {}, func() {}
		}},
		{name: "timeout", env: []string{"MOCK_HANG_OPS=detect"}, want: rflscvs06.RuntimeTerminalTimeout, ctx: func() (context.Context, context.CancelFunc, func()) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			return ctx, cancel, func() {}
		}},
		{name: "cancel", env: []string{"MOCK_HANG_OPS=detect"}, want: rflscvs06.RuntimeTerminalCancel, ctx: func() (context.Context, context.CancelFunc, func()) {
			ctx, cancel := context.WithCancel(context.Background())
			return ctx, cancel, func() { time.Sleep(100 * time.Millisecond); cancel() }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pidLog := filepath.Join(t.TempDir(), "child-pids.log")
			disposableRoot := filepath.Join(t.TempDir(), "disposable")
			if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			env := append([]string{}, tc.env...)
			env = append(env, "MOCK_SPAWN_CHILD=1", "MOCK_CHILD_PID_LOG="+pidLog)
			executor := NewExecutor(ExecutorConfig{
				AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: disposableRoot, Env: env},
				Spec:          spec, AuditProvider: CleanSourceAuditProvider{}, RequireConsent: true,
			})
			nonce := strings.Repeat("p", 16)
			request := ExecutionRequest{ExecutionID: "exec-process-group-" + tc.name, Nonce: nonce, Consent: testConsent(snapshot, spec, "exec-process-group-"+tc.name, nonce), Snapshot: snapshot, Operation: protocol.OpDetect}
			ctx, cancel, triggerCancel := tc.ctx()
			defer cancel()
			if tc.name == "cancel" {
				done := make(chan struct{})
				var got ExecutionResult
				var runErr error
				go func() { got, runErr = executor.Execute(ctx, request); close(done) }()
				triggerCancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled process-group execution did not settle")
				}
				assertTerminalCleanup(t, got, tc.want, disposableRoot)
				if runErr == nil {
					t.Fatal("cancelled process-group execution unexpectedly succeeded")
				}
			} else {
				got, runErr := executor.Execute(ctx, request)
				if tc.want == rflscvs06.RuntimeTerminalSuccess && runErr != nil {
					t.Fatalf("success process-group execution failed: %v", runErr)
				}
				if tc.want != rflscvs06.RuntimeTerminalSuccess && runErr == nil {
					t.Fatalf("%s process-group execution unexpectedly succeeded", tc.name)
				}
				assertTerminalCleanup(t, got, tc.want, disposableRoot)
			}
			childPIDs := readPIDLog(t, pidLog)
			if len(childPIDs) != 1 {
				t.Fatalf("adapter did not start exactly one child: %v", childPIDs)
			}
			waitPIDGone(t, childPIDs[0])
		})
	}
}

func assertTerminalCleanup(t *testing.T, got ExecutionResult, wantStatus, disposableRoot string) {
	t.Helper()
	if got.Isolation.Status != wantStatus {
		t.Fatalf("status = %q, want %q: %+v", got.Isolation.Status, wantStatus, got.Isolation)
	}
	if !got.Isolation.ProcessObserved || !got.Isolation.Cleanup.LayerCreated || !got.Isolation.Cleanup.LayerDisposed || !got.Isolation.Cleanup.Verified {
		t.Fatalf("terminal cleanup evidence incomplete: %+v", got.Isolation)
	}
	if entries, err := os.ReadDir(disposableRoot); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("terminal execution retained disposable state: %+v", entries)
	}
}

func buildRuntimeMockAdapter(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	bin := filepath.Join(t.TempDir(), "mockadapter")
	cmd := exec.Command("go", "build", "-o", bin, "./internal/mockadapter")
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock adapter: %v\n%s", err, output)
	}
	return bin
}

func readPIDLog(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatalf("invalid pid log entry %q: %v", line, err)
		}
		pids = append(pids, pid)
	}
	return pids
}

func waitForPIDLog(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for adapter pid log %s", path)
}

func waitPIDGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("adapter child process %d survived process-group cleanup", pid)
}

func processAlive(pid int) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func testExecutionSpec(bin string) rflscvs06.RuntimeExecutionSpec {
	return rflscvs06.RuntimeExecutionSpec{
		Command:       rflscvs06.RuntimeCommand{Command: bin},
		CommandDigest: commandDigest(rflscvs06.RuntimeCommand{Command: bin}),
		AccessScope: rflscvs06.RuntimeAccessScope{
			Source: "immutable_snapshot", Network: "disabled", Credentials: "not_available",
		},
		IsolationScope: rflscvs06.RuntimeIsolationScope{
			Level: "trusted_local", SourceMount: "not_mounted", SourcePermission: "read_only_protocol",
			WorkingDirectory: "process_private_disposable", WritableLayer: "discarded_after_terminal",
			RepositoryPathExposed: false,
		},
	}
}

func testConsent(snapshot protocol.Snapshot, spec rflscvs06.RuntimeExecutionSpec, executionID, nonce string) rflscvs06.RuntimeConsent {
	now := time.Now().UTC()
	return rflscvs06.RuntimeConsent{
		SchemaID:           rflscvs06.RuntimeConsentSchemaID,
		SchemaVersion:      rflscvs06.RuntimeSchemaVersion,
		ConsentID:          "consent-" + executionID,
		ActorID:            "test-actor",
		ApprovedBy:         "test-actor",
		Approved:           true,
		IssuedAt:           now.Add(-time.Second).Format(time.RFC3339Nano),
		ExpiresAt:          now.Add(time.Minute).Format(time.RFC3339Nano),
		Command:            spec.Command.Command,
		Args:               append([]string(nil), spec.Command.Args...),
		CommandDigest:      spec.CommandDigest,
		AccessScope:        spec.AccessScope,
		IsolationScope:     spec.IsolationScope,
		SnapshotID:         snapshot.SnapshotID,
		SnapshotTreeDigest: snapshot.RootTreeID,
		Nonce:              nonce,
	}
}
