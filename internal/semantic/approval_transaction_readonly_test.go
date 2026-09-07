package semantic

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestApprovalTransactionReadOnlySnapshotAcrossProcesses(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cross-process-read-only")
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-cross-process-key", "readonly-cross-process-command", "readonly-cross-process-event", "readonly-cross-process-approval", "readonly-cross-process-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	baseline, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("baseline snapshot: %v", err)
	}
	if len(baseline.targets) != 1 {
		t.Fatalf("baseline targets = %d, want one", len(baseline.targets))
	}
	baselineEpoch := baseline.targets[0].Target.WorkspaceEpoch
	controlRoot := filepath.Dir(root)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	writerPaths := approvalTransactionReadOnlyPathsForRole(controlRoot, "writer")
	writer := startApprovalTransactionReadOnlyProcess(ctx, t, root, writerPaths, "writer")
	if err := waitApprovalTransactionReadOnlySignal(ctx, writerPaths.ready); err != nil {
		t.Fatalf("writer readiness: %v", err)
	}
	if err := writeApprovalTransactionReadOnlySignal(writerPaths.start); err != nil {
		t.Fatalf("start writer: %v", err)
	}
	if err := waitApprovalTransactionReadOnlySignal(ctx, writerPaths.paused); err != nil {
		t.Fatalf("writer pause: %v", err)
	}

	beforeFirstReader, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot tree before first reader: %v", err)
	}
	readerOnePaths := approvalTransactionReadOnlyPathsForRole(controlRoot, "reader-one")
	readerOne := startApprovalTransactionReadOnlyProcess(ctx, t, root, readerOnePaths, "reader")
	if err := waitApprovalTransactionReadOnlySignal(ctx, readerOnePaths.ready); err != nil {
		t.Fatalf("first reader readiness: %v", err)
	}
	if err := writeApprovalTransactionReadOnlySignal(readerOnePaths.start); err != nil {
		t.Fatalf("start first reader: %v", err)
	}
	readerOneOutcome, err := waitApprovalTransactionReadOnlyProcess(ctx, readerOne)
	if err != nil {
		t.Fatalf("first reader completion: %v", err)
	}
	if readerOneOutcome.err != nil {
		t.Fatalf("first reader process: %v output=%s", readerOneOutcome.err, readerOneOutcome.output)
	}
	readerOneResult, err := readApprovalTransactionReadOnlyResult(readerOnePaths.result)
	if err != nil {
		t.Fatalf("first reader result: %v", err)
	}
	if readerOneResult.Role != "reader" || readerOneResult.Outcome != "snapshot" || readerOneResult.Epoch != baselineEpoch {
		t.Fatalf("first reader result = %#v, want committed epoch %d", readerOneResult, baselineEpoch)
	}
	afterFirstReader, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot tree after first reader: %v", err)
	}
	if !approvalTransactionManagedTreeEqual(beforeFirstReader, afterFirstReader) {
		t.Fatalf("first reader changed managed tree: before=%v after=%v", approvalTransactionManagedTreeMetadata(beforeFirstReader), approvalTransactionManagedTreeMetadata(afterFirstReader))
	}

	if err := writeApprovalTransactionReadOnlySignal(writerPaths.release); err != nil {
		t.Fatalf("release writer: %v", err)
	}
	if err := waitApprovalTransactionReadOnlySignal(ctx, writerPaths.committed); err != nil {
		t.Fatalf("writer commit signal: %v", err)
	}
	writerResult, err := readApprovalTransactionReadOnlyResult(writerPaths.result)
	if err != nil {
		t.Fatalf("writer result: %v", err)
	}
	if writerResult.Role != "writer" || writerResult.Outcome != "committed" || writerResult.Epoch != baselineEpoch+1 {
		t.Fatalf("writer result = %#v, want committed epoch %d", writerResult, baselineEpoch+1)
	}
	walInfo, err := os.Stat(store.databasePath() + "-wal")
	if err != nil || walInfo.Size() == 0 {
		var walSize int64
		if walInfo != nil {
			walSize = walInfo.Size()
		}
		t.Fatalf("writer did not leave a committed WAL frame: size/error=%d/%v", walSize, err)
	}

	beforeSecondReader, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot tree before second reader: %v", err)
	}
	readerTwoPaths := approvalTransactionReadOnlyPathsForRole(controlRoot, "reader-two")
	readerTwo := startApprovalTransactionReadOnlyProcess(ctx, t, root, readerTwoPaths, "reader")
	if err := waitApprovalTransactionReadOnlySignal(ctx, readerTwoPaths.ready); err != nil {
		t.Fatalf("second reader readiness: %v", err)
	}
	if err := writeApprovalTransactionReadOnlySignal(readerTwoPaths.start); err != nil {
		t.Fatalf("start second reader: %v", err)
	}
	readerTwoOutcome, err := waitApprovalTransactionReadOnlyProcess(ctx, readerTwo)
	if err != nil {
		t.Fatalf("second reader completion: %v", err)
	}
	if readerTwoOutcome.err != nil {
		t.Fatalf("second reader process: %v output=%s", readerTwoOutcome.err, readerTwoOutcome.output)
	}
	readerTwoResult, err := readApprovalTransactionReadOnlyResult(readerTwoPaths.result)
	if err != nil {
		t.Fatalf("second reader result: %v", err)
	}
	if readerTwoResult.Role != "reader" || readerTwoResult.Outcome != "snapshot" || readerTwoResult.Epoch != baselineEpoch+1 {
		t.Fatalf("second reader result = %#v, want committed epoch %d", readerTwoResult, baselineEpoch+1)
	}
	afterSecondReader, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot tree after second reader: %v", err)
	}
	if !approvalTransactionManagedTreeEqual(beforeSecondReader, afterSecondReader) {
		t.Fatalf("second reader changed managed tree: before=%v after=%v", approvalTransactionManagedTreeMetadata(beforeSecondReader), approvalTransactionManagedTreeMetadata(afterSecondReader))
	}
	if err := writeApprovalTransactionReadOnlySignal(writerPaths.finish); err != nil {
		t.Fatalf("finish writer: %v", err)
	}
	writerOutcome, err := waitApprovalTransactionReadOnlyProcess(ctx, writer)
	if err != nil {
		t.Fatalf("writer completion: %v", err)
	}
	if writerOutcome.err != nil {
		t.Fatalf("writer process: %v output=%s", writerOutcome.err, writerOutcome.output)
	}
	if entries, err := os.ReadDir(root); err != nil {
		t.Fatalf("read managed root after cross-process readers: %v", err)
	} else if len(entries) < 1 {
		t.Fatalf("managed root is empty after cross-process readers")
	}
}

type approvalTransactionReadOnlyPaths struct {
	ready     string
	start     string
	paused    string
	release   string
	committed string
	finish    string
	result    string
}

func approvalTransactionReadOnlyPathsForRole(root, role string) approvalTransactionReadOnlyPaths {
	prefix := filepath.Join(root, "approval-read-only-"+role)
	return approvalTransactionReadOnlyPaths{
		ready:     prefix + "-ready",
		start:     prefix + "-start",
		paused:    prefix + "-paused",
		release:   prefix + "-release",
		committed: prefix + "-committed",
		finish:    prefix + "-finish",
		result:    prefix + "-result.json",
	}
}

type approvalTransactionReadOnlyProcessOutcome struct {
	exitCode int
	output   string
	err      error
}

type approvalTransactionReadOnlyProcess struct {
	done           chan approvalTransactionReadOnlyProcessOutcome
	collected      chan struct{}
	cancel         context.CancelFunc
	waited         bool
	outcome        approvalTransactionReadOnlyProcessOutcome
	cleanupTimeout time.Duration
}

func startApprovalTransactionReadOnlyProcess(ctx context.Context, t *testing.T, root string, paths approvalTransactionReadOnlyPaths, role string) *approvalTransactionReadOnlyProcess {
	t.Helper()
	processContext, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(processContext, os.Args[0], "-test.run=^TestApprovalTransactionCrossProcessReadOnlyHelper$", "-test.v")
	command.WaitDelay = time.Second
	command.Env = append(os.Environ(),
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_HELPER=1",
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_ROLE="+role,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_ROOT="+root,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_READY="+paths.ready,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_START="+paths.start,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_PAUSED="+paths.paused,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_RELEASE="+paths.release,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_COMMITTED="+paths.committed,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_FINISH="+paths.finish,
		"CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_RESULT="+paths.result,
	)
	process := &approvalTransactionReadOnlyProcess{done: make(chan approvalTransactionReadOnlyProcessOutcome, 1), collected: make(chan struct{}), cancel: cancel}
	t.Cleanup(func() {
		if err := cleanupApprovalTransactionReadOnlyProcess(process); err != nil {
			t.Errorf("read-only child cleanup: %v", err)
		}
	})
	go func() {
		output, err := command.CombinedOutput()
		code := 0
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok && exitError.ProcessState != nil {
				code = exitError.ProcessState.ExitCode()
			} else {
				code = -1
			}
		}
		process.done <- approvalTransactionReadOnlyProcessOutcome{exitCode: code, output: string(output), err: err}
		close(process.collected)
	}()
	return process
}

func waitApprovalTransactionReadOnlyProcess(ctx context.Context, process *approvalTransactionReadOnlyProcess) (approvalTransactionReadOnlyProcessOutcome, error) {
	if process == nil {
		return approvalTransactionReadOnlyProcessOutcome{}, errors.New("read-only process is nil")
	}
	if process.waited {
		return process.outcome, nil
	}
	select {
	case outcome := <-process.done:
		process.waited = true
		process.outcome = outcome
		if err := waitApprovalTransactionReadOnlyProcessCollected(ctx, process); err != nil {
			return approvalTransactionReadOnlyProcessOutcome{}, err
		}
		return outcome, nil
	case <-ctx.Done():
		return approvalTransactionReadOnlyProcessOutcome{}, ctx.Err()
	}
}

func cleanupApprovalTransactionReadOnlyProcess(process *approvalTransactionReadOnlyProcess) error {
	if process == nil {
		return nil
	}
	timeout := 3 * time.Second
	if process.cleanupTimeout > 0 {
		timeout = process.cleanupTimeout
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if process.cancel != nil {
		process.cancel()
	}
	if !process.waited {
		select {
		case outcome := <-process.done:
			process.waited = true
			process.outcome = outcome
		case <-cleanupContext.Done():
			return cleanupContext.Err()
		}
	}
	return waitApprovalTransactionReadOnlyProcessCollected(cleanupContext, process)
}

func waitApprovalTransactionReadOnlyProcessCollected(ctx context.Context, process *approvalTransactionReadOnlyProcess) error {
	if process == nil || process.collected == nil {
		return nil
	}
	select {
	case <-process.collected:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestApprovalTransactionReadOnlyProcessCleanupReportsUnreapedChild(t *testing.T) {
	process := &approvalTransactionReadOnlyProcess{
		done:           make(chan approvalTransactionReadOnlyProcessOutcome),
		cancel:         func() {},
		cleanupTimeout: time.Millisecond,
	}
	if err := cleanupApprovalTransactionReadOnlyProcess(process); err == nil {
		t.Fatal("read-only process cleanup hid a bounded wait timeout")
	}
	if process.waited {
		t.Fatal("read-only process cleanup marked an unreaped child as waited")
	}
}

func TestApprovalTransactionReadOnlyProcessCleanupBoundsCollectorWait(t *testing.T) {
	collected := make(chan struct{})
	process := &approvalTransactionReadOnlyProcess{
		done:           make(chan approvalTransactionReadOnlyProcessOutcome, 1),
		collected:      collected,
		cancel:         func() {},
		cleanupTimeout: 25 * time.Millisecond,
	}
	process.done <- approvalTransactionReadOnlyProcessOutcome{}
	result := make(chan error, 1)
	go func() {
		result <- cleanupApprovalTransactionReadOnlyProcess(process)
	}()
	closed := false
	closeCollected := func() {
		if !closed {
			close(collected)
			closed = true
		}
	}
	defer closeCollected()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cleanup accepted an incomplete collector")
		}
	case <-time.After(100 * time.Millisecond):
		// The current implementation waits forever after receiving done.
		// Release that wait before asserting the required bounded error so the
		// RED test never leaves its cleanup goroutine behind.
		closeCollected()
		select {
		case err := <-result:
			if err == nil {
				t.Fatal("cleanup hid an incomplete collector timeout")
			}
		case <-time.After(time.Second):
			t.Fatal("cleanup goroutine was not recovered after collector release")
		}
	}
}

func TestApprovalTransactionReadOnlyProcessCleanupReapsHangingHelper(t *testing.T) {
	root := filepath.Join(t.TempDir(), "hanging-reader-root")
	paths := approvalTransactionReadOnlyPathsForRole(t.TempDir(), "hanging-reader")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	process := startApprovalTransactionReadOnlyProcess(ctx, t, root, paths, "reader")
	if err := waitApprovalTransactionReadOnlySignal(ctx, paths.ready); err != nil {
		t.Fatalf("hanging reader readiness: %v", err)
	}
	// Deliberately leave start absent. The real helper is blocked in its
	// signal wait until cleanup cancels the CommandContext child.
	if err := cleanupApprovalTransactionReadOnlyProcess(process); err != nil {
		t.Fatalf("cleanup hanging reader: %v", err)
	}
	if !process.waited {
		t.Fatal("cleanup did not reap the hanging reader")
	}
	if process.outcome.err == nil || process.outcome.exitCode == 0 {
		t.Fatalf("hanging reader exited successfully after cancellation: %#v", process.outcome)
	}
	if err := waitApprovalTransactionReadOnlyProcessCollected(context.Background(), process); err != nil {
		t.Fatalf("collector did not complete: %v", err)
	}
	if err := cleanupApprovalTransactionReadOnlyProcess(process); err != nil {
		t.Fatalf("repeated cleanup was not idempotent: %v", err)
	}
}

func writeApprovalTransactionReadOnlySignal(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("read-only signal path is empty")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write([]byte("ready")); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func waitApprovalTransactionReadOnlySignal(ctx context.Context, path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("read-only signal path is empty")
	}
	for {
		info, err := os.Lstat(path)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				return errors.New("read-only signal is unsafe")
			}
			if info.Size() > 0 {
				return nil
			}
			// A writer creates the signal before writing and syncing its
			// contents. Keep waiting for the complete marker.
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

type approvalTransactionReadOnlyResult struct {
	Role    string `json:"role"`
	Outcome string `json:"outcome"`
	Epoch   int64  `json:"epoch"`
}

const approvalTransactionReadOnlyResultMaxBytes = 4096

func writeApprovalTransactionReadOnlyResult(t *testing.T, path string, result approvalTransactionReadOnlyResult) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil || len(data) == 0 || len(data) > approvalTransactionReadOnlyResultMaxBytes {
		t.Fatal("read-only result could not be encoded")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal("read-only result could not be created")
	}
	if written, err := file.Write(data); err != nil || written != len(data) {
		_ = file.Close()
		t.Fatal("read-only result could not be written")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal("read-only result could not be synced")
	}
	if err := file.Close(); err != nil {
		t.Fatal("read-only result could not be closed")
	}
}

func readApprovalTransactionReadOnlyResult(path string) (approvalTransactionReadOnlyResult, error) {
	var zero approvalTransactionReadOnlyResult
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > approvalTransactionReadOnlyResultMaxBytes {
		return zero, errors.New("read-only result is missing or unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return zero, errors.New("read-only result is unreadable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, approvalTransactionReadOnlyResultMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > approvalTransactionReadOnlyResultMaxBytes {
		return zero, errors.New("read-only result is oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return zero, errors.New("read-only result is not an object")
	}
	allowed := map[string]bool{"role": true, "outcome": true, "epoch": true}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || !allowed[key] || fields[key] != nil {
			return zero, errors.New("read-only result has an unknown or duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || string(value) == "null" {
			return zero, errors.New("read-only result has an invalid field")
		}
		fields[key] = value
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return zero, errors.New("read-only result is malformed")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return zero, errors.New("read-only result has trailing data")
	}
	for _, key := range []string{"role", "outcome", "epoch"} {
		if fields[key] == nil {
			return zero, errors.New("read-only result is incomplete")
		}
	}
	strictDecoder := json.NewDecoder(bytes.NewReader(data))
	strictDecoder.DisallowUnknownFields()
	var result approvalTransactionReadOnlyResult
	if err := strictDecoder.Decode(&result); err != nil {
		return zero, errors.New("read-only result has invalid types")
	}
	if _, err := strictDecoder.Token(); err != io.EOF {
		return zero, errors.New("read-only result has trailing data")
	}
	if (result.Role != "reader" && result.Role != "writer") || (result.Role == "reader" && result.Outcome != "snapshot") || (result.Role == "writer" && result.Outcome != "committed") || result.Epoch <= 0 {
		return zero, errors.New("read-only result has invalid values")
	}
	return result, nil
}

func TestApprovalTransactionCrossProcessReadOnlyHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_HELPER") != "1" {
		return
	}
	role := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_ROLE")
	root := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_ROOT")
	paths := approvalTransactionReadOnlyPaths{
		ready:     os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_READY"),
		start:     os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_START"),
		paused:    os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_PAUSED"),
		release:   os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_RELEASE"),
		committed: os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_COMMITTED"),
		finish:    os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_FINISH"),
		result:    os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READ_ONLY_RESULT"),
	}
	if (role != "reader" && role != "writer") || strings.TrimSpace(root) == "" || strings.TrimSpace(paths.ready) == "" || strings.TrimSpace(paths.start) == "" || strings.TrimSpace(paths.result) == "" {
		t.Fatal("read-only helper environment is incomplete")
	}
	if err := writeApprovalTransactionReadOnlySignal(paths.ready); err != nil {
		t.Fatalf("signal helper readiness: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := waitApprovalTransactionReadOnlySignal(ctx, paths.start); err != nil {
		t.Fatalf("wait helper start: %v", err)
	}
	if role == "reader" {
		readApprovalTransactionReadOnlySnapshot(t, ctx, root, paths)
		return
	}
	writeApprovalTransactionReadOnlySnapshot(t, ctx, root, paths)
}

func readApprovalTransactionReadOnlySnapshot(t *testing.T, ctx context.Context, root string, paths approvalTransactionReadOnlyPaths) {
	t.Helper()
	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(ctx)
	if err != nil {
		t.Fatalf("read-only child snapshot: %v", err)
	}
	if len(snapshot.targets) != 1 {
		t.Fatalf("read-only child targets = %d, want one", len(snapshot.targets))
	}
	writeApprovalTransactionReadOnlyResult(t, paths.result, approvalTransactionReadOnlyResult{
		Role:    "reader",
		Outcome: "snapshot",
		Epoch:   snapshot.targets[0].Target.WorkspaceEpoch,
	})
}

func writeApprovalTransactionReadOnlySnapshot(t *testing.T, ctx context.Context, root string, paths approvalTransactionReadOnlyPaths) {
	t.Helper()
	store := newFileApprovalTransactionStoreForTest(root)
	baseline, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("writer child baseline snapshot: %v", err)
	}
	if len(baseline.targets) != 1 {
		t.Fatalf("writer child targets = %d, want one", len(baseline.targets))
	}
	nextTarget := baseline.targets[0]
	nextTarget.Target.WorkspaceEpoch++
	nextTarget.TargetDigest = approvalTransactionTargetDigest(nextTarget.Target)
	db, err := store.openApprovalTransactionDatabaseLocked(ctx, false)
	if err != nil {
		t.Fatalf("writer child open database: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("writer child open connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatalf("writer child disable WAL autocheckpoint: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("writer child begin immediate: %v", err)
	}
	result, err := conn.ExecContext(ctx, "UPDATE approval_targets SET workspace_epoch=?,target_digest=? WHERE aggregate_id=?", nextTarget.Target.WorkspaceEpoch, nextTarget.TargetDigest, nextTarget.Aggregate.AggregateID)
	if err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatalf("writer child update target: %v", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatalf("writer child updated rows = %d, error=%v, want one", rows, err)
	}
	if err := writeApprovalTransactionReadOnlySignal(paths.paused); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatalf("signal writer pause: %v", err)
	}
	if err := waitApprovalTransactionReadOnlySignal(ctx, paths.release); err != nil {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatalf("wait writer release: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatalf("writer child commit: %v", err)
	}
	writeApprovalTransactionReadOnlyResult(t, paths.result, approvalTransactionReadOnlyResult{
		Role:    "writer",
		Outcome: "committed",
		Epoch:   nextTarget.Target.WorkspaceEpoch,
	})
	if err := writeApprovalTransactionReadOnlySignal(paths.committed); err != nil {
		t.Fatalf("signal writer commit: %v", err)
	}
	if err := waitApprovalTransactionReadOnlySignal(ctx, paths.finish); err != nil {
		t.Fatalf("wait writer finish: %v", err)
	}
}

func TestApprovalTransactionSnapshotFailsClosedWhenReadOnlyConnectionCleanupFails(t *testing.T) {
	root := t.TempDir()
	writer := newFileApprovalTransactionStoreForTest(root)
	if _, err := writer.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-connection-cleanup-key", "readonly-connection-cleanup-command", "readonly-connection-cleanup-event", "readonly-connection-cleanup-approval", "readonly-connection-cleanup-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	closeErr := errors.New("read-only connection close failed")
	var closeCalls atomic.Int32
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{
		currentAuthority: defaultApprovalTransactionAuthority,
		closeReadOnlyConnection: func(conn *sql.Conn) error {
			closeCalls.Add(1)
			_ = conn.Close()
			return closeErr
		},
	})

	snapshot, err := store.Snapshot(context.Background())
	if err == nil {
		t.Fatal("read-only snapshot succeeded after connection cleanup failure")
	}
	if !errors.Is(err, ErrApprovalTransactionPersistence) || !errors.Is(err, closeErr) {
		t.Fatalf("read-only snapshot error = %v, want typed persistence with connection close error", err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("read-only connection close calls = %d, want exactly one", closeCalls.Load())
	}
	if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
		t.Fatalf("read-only snapshot = %#v, want zero snapshot", snapshot)
	}
}

func TestApprovalTransactionSnapshotJoinsLoadAndReadOnlyConnectionCleanupErrors(t *testing.T) {
	root := t.TempDir()
	writer := newFileApprovalTransactionStoreForTest(root)
	if _, err := writer.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-connection-load-error-key", "readonly-connection-load-error-command", "readonly-connection-load-error-event", "readonly-connection-load-error-approval", "readonly-connection-load-error-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := writer.openApprovalTransactionDatabaseLocked(context.Background(), false)
	if err != nil {
		t.Fatalf("open writer database: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), "UPDATE approval_targets SET aggregate_json=?", []byte("not-json")); err != nil {
		_ = db.Close()
		t.Fatalf("corrupt aggregate payload: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close writer database: %v", err)
	}

	closeErr := errors.New("read-only connection close failed after load")
	var closeCalls atomic.Int32
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{
		currentAuthority: defaultApprovalTransactionAuthority,
		closeReadOnlyConnection: func(conn *sql.Conn) error {
			closeCalls.Add(1)
			_ = conn.Close()
			return closeErr
		},
	})

	snapshot, err := store.Snapshot(context.Background())
	if err == nil {
		t.Fatal("corrupt read-only snapshot succeeded")
	}
	if !errors.Is(err, ErrApprovalTransactionInvalid) || !errors.Is(err, ErrApprovalTransactionPersistence) || !errors.Is(err, closeErr) {
		t.Fatalf("corrupt read-only snapshot error = %v, want original invalid cause and connection cleanup persistence", err)
	}
	if closeCalls.Load() != 1 {
		t.Fatalf("read-only connection close calls = %d, want exactly one", closeCalls.Load())
	}
	if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
		t.Fatalf("corrupt read-only snapshot = %#v, want zero snapshot", snapshot)
	}
}

func TestApprovalTransactionSnapshotFailsClosedWhenReadCleanupFails(t *testing.T) {
	root := t.TempDir()
	writer := newFileApprovalTransactionStoreForTest(root)
	if _, err := writer.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-cleanup-key", "readonly-cleanup-command", "readonly-cleanup-event", "readonly-cleanup-approval", "readonly-cleanup-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	closeErr := errors.New("read-only database close failed")
	removeErr := errors.New("read-only temporary root removal failed")
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{
		currentAuthority: defaultApprovalTransactionAuthority,
		closeReadOnlyDatabase: func(db *sql.DB) error {
			_ = db.Close()
			return closeErr
		},
		removeReadOnlyTemporaryRoot: func(path string) error {
			_ = os.RemoveAll(path)
			return removeErr
		},
	})

	snapshot, err := store.Snapshot(context.Background())
	if err == nil {
		t.Fatal("read-only snapshot succeeded after cleanup failure")
	}
	if !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("read-only snapshot error = %v, want typed persistence failure", err)
	}
	if !errors.Is(err, closeErr) || !errors.Is(err, removeErr) {
		t.Fatalf("read-only snapshot error = %v, want close and removal errors preserved", err)
	}
	if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
		t.Fatalf("read-only snapshot = %#v, want zero snapshot", snapshot)
	}
}

func TestApprovalTransactionReadOnlySnapshotCleansInjectedTemporaryRoot(t *testing.T) {
	root := t.TempDir()
	temporaryBase := t.TempDir()
	writer := newFileApprovalTransactionStoreForTest(root)
	if _, err := writer.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-cleanup-repeat-key", "readonly-cleanup-repeat-command", "readonly-cleanup-repeat-event", "readonly-cleanup-repeat-approval", "readonly-cleanup-repeat-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{
		currentAuthority:          defaultApprovalTransactionAuthority,
		readOnlyTemporaryRootBase: temporaryBase,
	})
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := store.Snapshot(context.Background()); err != nil {
			t.Fatalf("read-only snapshot %d: %v", attempt+1, err)
		}
		entries, err := os.ReadDir(temporaryBase)
		if err != nil {
			t.Fatalf("read temporary base after snapshot %d: %v", attempt+1, err)
		}
		if len(entries) != 0 {
			t.Fatalf("read-only snapshot %d leaked temporary directories: %v", attempt+1, entries)
		}
	}
}

func TestApprovalTransactionSnapshotJoinsLoadAndCleanupErrors(t *testing.T) {
	root := t.TempDir()
	writer := newFileApprovalTransactionStoreForTest(root)
	if _, err := writer.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-load-error-key", "readonly-load-error-command", "readonly-load-error-event", "readonly-load-error-approval", "readonly-load-error-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := writer.openApprovalTransactionDatabaseLocked(context.Background(), false)
	if err != nil {
		t.Fatalf("open writer database: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), "UPDATE approval_targets SET aggregate_json=?", []byte("not-json")); err != nil {
		_ = db.Close()
		t.Fatalf("corrupt aggregate payload: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close writer database: %v", err)
	}

	closeErr := errors.New("read-only database close failed after load")
	removeErr := errors.New("read-only temporary root removal failed after load")
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{
		currentAuthority: defaultApprovalTransactionAuthority,
		closeReadOnlyDatabase: func(db *sql.DB) error {
			_ = db.Close()
			return closeErr
		},
		removeReadOnlyTemporaryRoot: func(path string) error {
			_ = os.RemoveAll(path)
			return removeErr
		},
	})

	snapshot, err := store.Snapshot(context.Background())
	if err == nil {
		t.Fatal("corrupt read-only snapshot succeeded")
	}
	if !errors.Is(err, ErrApprovalTransactionInvalid) || !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("corrupt read-only snapshot error = %v, want invalid persistence failure", err)
	}
	if !errors.Is(err, closeErr) || !errors.Is(err, removeErr) {
		t.Fatalf("corrupt read-only snapshot error = %v, want load, close, and removal errors preserved", err)
	}
	if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
		t.Fatalf("corrupt read-only snapshot = %#v, want zero snapshot", snapshot)
	}
}

type approvalTransactionManagedTreeEntry struct {
	Path string
	Mode os.FileMode
	Size int64
	Data []byte
}

func TestApprovalTransactionReadOnlySnapshotPreservesManagedTree(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-tree-key", "readonly-tree-command", "readonly-tree-event", "readonly-tree-approval", "readonly-tree-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	before, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot managed tree before read: %v", err)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read-only snapshot: %v", err)
	}
	after, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot managed tree after read: %v", err)
	}
	if !approvalTransactionManagedTreeEqual(before, after) {
		t.Fatalf("read-only snapshot changed managed approval transaction tree: before=%v after=%v", approvalTransactionManagedTreeMetadata(before), approvalTransactionManagedTreeMetadata(after))
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("read-only snapshot counts: events=%d aggregates=%d idempotency=%d outbox=%d, want one each", len(snapshot.Events), len(snapshot.Aggregates), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
	}
}

func TestApprovalTransactionReadOnlySnapshotSeesCommittedWALWithoutMutation(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-wal-key", "readonly-wal-command", "readonly-wal-event", "readonly-wal-approval", "readonly-wal-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	baseline, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("baseline snapshot: %v", err)
	}
	if len(baseline.targets) != 1 {
		t.Fatalf("baseline targets=%d, want one", len(baseline.targets))
	}
	nextTarget := baseline.targets[0]
	nextTarget.Target.WorkspaceEpoch++
	nextTarget.TargetDigest = approvalTransactionTargetDigest(nextTarget.Target)

	writerDB, err := store.openApprovalTransactionDatabaseLocked(context.Background(), false)
	if err != nil {
		t.Fatalf("open writer database: %v", err)
	}
	defer writerDB.Close()
	writerConn, err := writerDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("open writer connection: %v", err)
	}
	defer writerConn.Close()
	if _, err := writerConn.ExecContext(context.Background(), "PRAGMA wal_autocheckpoint=0"); err != nil {
		t.Fatalf("disable writer auto-checkpoint: %v", err)
	}
	if _, err := writerConn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin writer transaction: %v", err)
	}
	if _, err := writerConn.ExecContext(context.Background(), "UPDATE approval_targets SET workspace_epoch=?,target_digest=? WHERE aggregate_id=?", nextTarget.Target.WorkspaceEpoch, nextTarget.TargetDigest, nextTarget.Aggregate.AggregateID); err != nil {
		_, _ = writerConn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatalf("write WAL target: %v", err)
	}
	if _, err := writerConn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatalf("commit WAL target: %v", err)
	}
	walInfo, err := os.Stat(store.databasePath() + "-wal")
	if err != nil || walInfo.Size() == 0 {
		var walSize int64
		if walInfo != nil {
			walSize = walInfo.Size()
		}
		t.Fatalf("committed writer did not leave WAL frame: size/error=%d/%v", walSize, err)
	}

	before, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot managed tree before WAL read: %v", err)
	}
	observed, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read committed WAL snapshot: %v", err)
	}
	after, err := snapshotApprovalTransactionManagedTree(root)
	if err != nil {
		t.Fatalf("snapshot managed tree after WAL read: %v", err)
	}
	if !approvalTransactionManagedTreeEqual(before, after) {
		t.Fatalf("read-only WAL snapshot changed managed approval transaction tree: before=%v after=%v", approvalTransactionManagedTreeMetadata(before), approvalTransactionManagedTreeMetadata(after))
	}
	if len(observed.targets) != 1 {
		t.Fatalf("read-only snapshot targets=%d, want one", len(observed.targets))
	}
	if observed.targets[0].Target.WorkspaceEpoch != nextTarget.Target.WorkspaceEpoch {
		t.Fatalf("read-only snapshot did not observe committed WAL target epoch: got=%d want=%d", observed.targets[0].Target.WorkspaceEpoch, nextTarget.Target.WorkspaceEpoch)
	}
}

func TestApprovalTransactionReadOnlySnapshotAllowsConcurrentWriter(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "readonly-concurrent-key", "readonly-concurrent-command", "readonly-concurrent-event", "readonly-concurrent-approval", "readonly-concurrent-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	baseline, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("baseline snapshot: %v", err)
	}
	if len(baseline.targets) != 1 {
		t.Fatalf("baseline targets=%d, want one", len(baseline.targets))
	}
	nextTarget := baseline.targets[0]
	nextTarget.Target.WorkspaceEpoch++
	nextTarget.TargetDigest = approvalTransactionTargetDigest(nextTarget.Target)

	writerDB, err := store.openApprovalTransactionDatabaseLocked(context.Background(), false)
	if err != nil {
		t.Fatalf("open writer database: %v", err)
	}
	defer writerDB.Close()
	writerConn, err := writerDB.Conn(context.Background())
	if err != nil {
		t.Fatalf("open writer connection: %v", err)
	}
	defer writerConn.Close()
	if _, err := writerConn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin writer transaction: %v", err)
	}
	if _, err := writerConn.ExecContext(context.Background(), "UPDATE approval_targets SET workspace_epoch=?,target_digest=? WHERE aggregate_id=?", nextTarget.Target.WorkspaceEpoch, nextTarget.TargetDigest, nextTarget.Aggregate.AggregateID); err != nil {
		_, _ = writerConn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatalf("write uncommitted target: %v", err)
	}

	readDone := make(chan struct{})
	var observed ApprovalTransactionSnapshot
	var readErr error
	go func() {
		observed, readErr = store.Snapshot(context.Background())
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		_, _ = writerConn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatal("read-only snapshot blocked behind an independent writer")
	}
	if readErr != nil {
		_, _ = writerConn.ExecContext(context.Background(), "ROLLBACK")
		t.Fatalf("concurrent read-only snapshot: %v", readErr)
	}
	if len(observed.targets) != 1 {
		t.Fatalf("concurrent read targets=%d, want one", len(observed.targets))
	}
	if observed.targets[0].Target.WorkspaceEpoch != baseline.targets[0].Target.WorkspaceEpoch {
		t.Fatalf("concurrent read observed uncommitted target: got=%d want=%d", observed.targets[0].Target.WorkspaceEpoch, baseline.targets[0].Target.WorkspaceEpoch)
	}
	if _, err := writerConn.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatalf("commit concurrent writer: %v", err)
	}
	committed, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after concurrent writer: %v", err)
	}
	if len(committed.targets) != 1 {
		t.Fatalf("snapshot after concurrent writer targets=%d, want one", len(committed.targets))
	}
	if committed.targets[0].Target.WorkspaceEpoch != nextTarget.Target.WorkspaceEpoch {
		t.Fatalf("snapshot after concurrent writer did not observe commit: got=%d want=%d", committed.targets[0].Target.WorkspaceEpoch, nextTarget.Target.WorkspaceEpoch)
	}
}

func snapshotApprovalTransactionManagedTree(root string) ([]approvalTransactionManagedTreeEntry, error) {
	entries := make([]approvalTransactionManagedTreeEntry, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := approvalTransactionManagedTreeEntry{Path: relative, Mode: info.Mode(), Size: info.Size()}
		if info.Mode().IsRegular() {
			entry.Data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		entries = append(entries, entry)
		return nil
	})
	return entries, err
}

func approvalTransactionManagedTreeMetadata(entries []approvalTransactionManagedTreeEntry) []string {
	metadata := make([]string, 0, len(entries))
	for _, entry := range entries {
		metadata = append(metadata, entry.Path+"/"+entry.Mode.String()+"/"+strconv.FormatInt(entry.Size, 10)+"/"+strconv.Itoa(len(entry.Data)))
	}
	return metadata
}

func approvalTransactionManagedTreeEqual(before, after []approvalTransactionManagedTreeEntry) bool {
	// SQLite uses -shm as transient WAL-index and lock storage. Its contents
	// may change while a read transaction participates in WAL, but identity,
	// mode, size, and presence remain strict. Database and WAL bytes remain
	// authoritative and must be unchanged by Snapshot.
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		left, right := before[index], after[index]
		if left.Path != right.Path || left.Mode != right.Mode || left.Size != right.Size {
			return false
		}
		if strings.HasSuffix(left.Path, "-shm") {
			continue
		}
		if !bytes.Equal(left.Data, right.Data) {
			return false
		}
	}
	return true
}
