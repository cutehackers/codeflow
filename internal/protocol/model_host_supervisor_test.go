//go:build darwin && cgo

package protocol

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type modelHostSupervisorStartCall struct {
	command string
	args    []string
	env     []string
	workDir string
	limits  ModelHostResourceLimits
}

type recordingModelHostSupervisor struct {
	delegate   modelHostSupervisor
	startErr   error
	memoryErr  error
	backend    string
	backendSet bool

	mu     sync.Mutex
	starts []modelHostSupervisorStartCall
}

func (s *recordingModelHostSupervisor) Start(command string, args, env []string, workDir string, limits ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	s.mu.Lock()
	s.starts = append(s.starts, modelHostSupervisorStartCall{
		command: command, args: append([]string(nil), args...), env: append([]string(nil), env...),
		workDir: workDir, limits: limits,
	})
	s.mu.Unlock()
	if s.startErr != nil {
		return nil, nil, nil, nil, s.startErr
	}
	return s.delegate.Start(command, args, env, workDir, limits)
}

func (s *recordingModelHostSupervisor) PhysicalMemoryUsage(pid int) (uint64, error) {
	if s.memoryErr != nil {
		return 0, s.memoryErr
	}
	return s.delegate.PhysicalMemoryUsage(pid)
}

func (s *recordingModelHostSupervisor) ResourceLimitBackend() string {
	if s.backendSet {
		return s.backend
	}
	return s.delegate.ResourceLimitBackend()
}

func (s *recordingModelHostSupervisor) snapshotStarts() []modelHostSupervisorStartCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	starts := make([]modelHostSupervisorStartCall, len(s.starts))
	copy(starts, s.starts)
	return starts
}

func newRecordingModelHostSupervisor() *recordingModelHostSupervisor {
	return &recordingModelHostSupervisor{delegate: defaultModelHostSupervisor{}}
}

func modelHostSupervisorTestConfig(root string, supervisor modelHostSupervisor, env ...string) ModelHostConfig {
	return ModelHostConfig{
		BinPath:        os.Args[0],
		Args:           []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            append([]string{"CODEFLOW_MODEL_HOST_HELPER=1"}, env...),
		DisposableRoot: root,
		DefaultTimeout: 2 * time.Second,
		ResourceLimits: DefaultModelHostResourceLimits(),
		supervisor:     supervisor,
	}
}

func TestSpawnModelHost_SupervisorBindsStartInputsAndResourceEvidence(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	supervisor := newRecordingModelHostSupervisor()
	limits := DefaultModelHostResourceLimits()
	root := t.TempDir()
	cfg := modelHostSupervisorTestConfig(root, supervisor, "CODEFLOW_MODEL_HOST_MODE=resource-probe")
	cfg.ResourceLimits = limits
	host, err := SpawnModelHost(context.Background(), cfg)
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	if host == nil {
		t.Fatal("spawn returned nil host")
	}
	t.Cleanup(func() {
		if closeErr := host.Close(); closeErr != nil {
			t.Errorf("close model host: %v", closeErr)
		}
	})

	starts := supervisor.snapshotStarts()
	if len(starts) != 1 {
		t.Fatalf("supervisor start calls = %d, want 1", len(starts))
	}
	start := starts[0]
	if start.command == "" || len(start.args) < 3 {
		t.Fatalf("supervisor received incomplete process invocation: %+v", start)
	}
	policyWorkDir, err := filepath.EvalSymlinks(start.workDir)
	if err != nil {
		t.Fatalf("resolve supervisor workdir: %v", err)
	}
	if start.args[0] != "-f" || start.args[1] != filepath.Join(policyWorkDir, "model-host.sb") {
		t.Fatalf("supervisor received unbound sandbox invocation: %+v", start)
	}
	if start.workDir != host.workDir {
		t.Fatalf("supervisor workdir = %q, host workdir = %q", start.workDir, host.workDir)
	}
	expectedEnv, err := modelHostEnvironment(cfg.Env, start.workDir)
	if err != nil {
		t.Fatalf("rebuild expected model-host environment: %v", err)
	}
	if !reflect.DeepEqual(start.env, expectedEnv) {
		t.Fatalf("supervisor environment = %#v, want %#v", start.env, expectedEnv)
	}
	if start.limits != limits {
		t.Fatalf("supervisor limits = %+v, want %+v", start.limits, limits)
	}
	if host.supervisor != supervisor {
		t.Fatalf("host did not retain the injected supervisor instance")
	}
	evidence := host.Capability().ResourceLimits
	if evidence == nil || evidence.Backend != ModelHostResourceBackendDarwinHostTree || evidence.EnforcementStatus != ModelHostResourceEnforcementEnforced {
		t.Fatalf("resource evidence did not come from the injected supervisor backend: %+v", evidence)
	}
}

func TestSpawnModelHost_SupervisorStartFailureCleansDisposableState(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	supervisor := newRecordingModelHostSupervisor()
	supervisor.startErr = errors.New("injected supervisor start failure")
	root := t.TempDir()
	_, err := SpawnModelHost(context.Background(), modelHostSupervisorTestConfig(root, supervisor))
	if err == nil || !strings.Contains(err.Error(), "injected supervisor start failure") {
		t.Fatalf("spawn error = %v, want injected start failure", err)
	}
	if entries, readErr := os.ReadDir(root); readErr != nil {
		t.Fatalf("read disposable root: %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("disposable root retained failed host state: %v", entries)
	}
	if starts := supervisor.snapshotStarts(); len(starts) != 1 {
		t.Fatalf("supervisor start calls = %d, want 1", len(starts))
	}
}

func TestSpawnModelHost_StartFailurePreservesWorkdirRemovalError(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	supervisor := newRecordingModelHostSupervisor()
	startErr := errors.New("injected supervisor start failure")
	removeErr := errors.New("injected disposable workdir removal failure")
	supervisor.startErr = startErr
	root := t.TempDir()
	cfg := modelHostSupervisorTestConfig(root, supervisor)
	cfg.removeWorkDir = func(path string) error {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return removeErr
	}
	_, err := SpawnModelHost(context.Background(), cfg)
	if err == nil || !errors.Is(err, startErr) || !errors.Is(err, removeErr) {
		t.Fatalf("spawn error = %v, want start and workdir removal failures", err)
	}
	if entries, readErr := os.ReadDir(root); readErr != nil {
		t.Fatalf("read disposable root: %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("disposable root retained failed host state: %v", entries)
	}
}

func TestSpawnModelHost_EnvironmentFailurePreservesWorkdirRemovalError(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	supervisor := newRecordingModelHostSupervisor()
	removeErr := errors.New("injected disposable workdir removal failure")
	root := t.TempDir()
	cfg := modelHostSupervisorTestConfig(root, supervisor)
	cfg.Env = append(cfg.Env, "CODEFLOW_MODEL_HOST_UNALLOWLISTED=value")
	cfg.removeWorkDir = func(path string) error {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		return removeErr
	}
	_, err := SpawnModelHost(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "not allowlisted") || !errors.Is(err, removeErr) {
		t.Fatalf("spawn error = %v, want environment and workdir removal failures", err)
	}
	if entries, readErr := os.ReadDir(root); readErr != nil {
		t.Fatalf("read disposable root: %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("disposable root retained environment failure state: %v", entries)
	}
}

type modelHostTestWriteCloser struct {
	io.WriteCloser
	closeErr error
	mu       sync.Mutex
	closed   bool
}

func (c *modelHostTestWriteCloser) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	var err error
	if c.WriteCloser != nil {
		err = c.WriteCloser.Close()
	}
	return errors.Join(err, c.closeErr)
}

func (c *modelHostTestWriteCloser) wasClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

type modelHostTestReadCloser struct {
	io.ReadCloser
	closeErr error
	mu       sync.Mutex
	closed   bool
}

func (c *modelHostTestReadCloser) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	var err error
	if c.ReadCloser != nil {
		err = c.ReadCloser.Close()
	}
	return errors.Join(err, c.closeErr)
}

func (c *modelHostTestReadCloser) wasClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

type partialStartModelHostSupervisor struct {
	delegate   modelHostSupervisor
	primaryErr error
	stdinErr   error
	stdoutErr  error
	stderrErr  error
	stdin      *modelHostTestWriteCloser
	stdout     *modelHostTestReadCloser
	stderr     *modelHostTestReadCloser
	cmd        *exec.Cmd
}

func (s *partialStartModelHostSupervisor) Start(command string, args, env []string, workDir string, limits ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	cmd, stdin, stdout, stderr, err := s.delegate.Start(command, args, env, workDir, limits)
	if err != nil {
		return cmd, stdin, stdout, stderr, err
	}
	s.cmd = cmd
	s.stdin = &modelHostTestWriteCloser{WriteCloser: stdin, closeErr: s.stdinErr}
	s.stdout = &modelHostTestReadCloser{ReadCloser: stdout, closeErr: s.stdoutErr}
	s.stderr = &modelHostTestReadCloser{ReadCloser: stderr, closeErr: s.stderrErr}
	return cmd, s.stdin, s.stdout, s.stderr, s.primaryErr
}

func (s *partialStartModelHostSupervisor) PhysicalMemoryUsage(pid int) (uint64, error) {
	return s.delegate.PhysicalMemoryUsage(pid)
}

func (s *partialStartModelHostSupervisor) ResourceLimitBackend() string {
	return s.delegate.ResourceLimitBackend()
}

func TestSpawnModelHost_PartialSupervisorStartReapsAndJoinsCleanupErrors(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	baseline, err := modelHostOpenFileDescriptorCount()
	if err != nil {
		t.Fatalf("count baseline file descriptors: %v", err)
	}
	maxOpen := baseline
	for attempt := 0; attempt < 3; attempt++ {
		root := t.TempDir()
		primaryErr := errors.New("injected partial supervisor start failure")
		stdinErr := errors.New("injected stdin close failure")
		stdoutErr := errors.New("injected stdout close failure")
		stderrErr := errors.New("injected stderr close failure")
		supervisor := &partialStartModelHostSupervisor{
			delegate: defaultModelHostSupervisor{}, primaryErr: primaryErr,
			stdinErr: stdinErr, stdoutErr: stdoutErr, stderrErr: stderrErr,
		}
		started := time.Now()
		_, spawnErr := SpawnModelHost(context.Background(), modelHostSupervisorTestConfig(root, supervisor))
		if spawnErr == nil {
			t.Fatalf("partial start error = %v, want primary and descriptor cleanup errors", spawnErr)
		}
		assertModelHostOnlyErrorLeaves(t, spawnErr, primaryErr, stdinErr, stdoutErr, stderrErr)
		if strings.Contains(spawnErr.Error(), "did not exit before timeout") {
			t.Fatalf("partial start retained stale process-group wait error: %v", spawnErr)
		}
		if elapsed := time.Since(started); elapsed >= processGroupExitWait {
			t.Fatalf("partial start cleanup waited for stale process-group timeout: %s", elapsed)
		}
		if supervisor.cmd == nil || supervisor.cmd.ProcessState == nil {
			var state *os.ProcessState
			if supervisor.cmd != nil {
				state = supervisor.cmd.ProcessState
			}
			t.Fatalf("partial start child was not reaped: cmd=%v state=%v", supervisor.cmd, state)
		}
		if !supervisor.cmd.ProcessState.Exited() {
			status, ok := supervisor.cmd.ProcessState.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() {
				t.Fatalf("partial start child has nonterminal state: %v", supervisor.cmd.ProcessState)
			}
		}
		if supervisor.cmd.Process != nil {
			if signalErr := supervisor.cmd.Process.Signal(syscall.Signal(0)); signalErr == nil {
				t.Fatalf("partial start child still accepts signals after cleanup")
			}
		}
		if supervisor.stdin == nil || !supervisor.stdin.wasClosed() || supervisor.stdout == nil || !supervisor.stdout.wasClosed() || supervisor.stderr == nil || !supervisor.stderr.wasClosed() {
			t.Fatalf("partial start descriptors were not all closed: stdin=%v stdout=%v stderr=%v", supervisor.stdin, supervisor.stdout, supervisor.stderr)
		}
		if entries, readErr := os.ReadDir(root); readErr != nil {
			t.Fatalf("read disposable root after partial start: %v", readErr)
		} else if len(entries) != 0 {
			t.Fatalf("partial start retained disposable state: %v", entries)
		}
		if open, countErr := modelHostOpenFileDescriptorCount(); countErr == nil && open > maxOpen {
			maxOpen = open
		}
	}
	after, err := modelHostOpenFileDescriptorCount()
	if err != nil {
		t.Fatalf("count final file descriptors: %v", err)
	}
	if maxOpen > baseline+3 || after > baseline+3 {
		t.Fatalf("partial starts leaked descriptors: baseline=%d max=%d after=%d", baseline, maxOpen, after)
	}
}

type driftingBackendModelHostSupervisor struct {
	delegate modelHostSupervisor
	mu       sync.Mutex
	calls    int
	cmd      *exec.Cmd
	stdin    *modelHostTestWriteCloser
	stdout   *modelHostTestReadCloser
	stderr   *modelHostTestReadCloser
}

func (s *driftingBackendModelHostSupervisor) Start(command string, args, env []string, workDir string, limits ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	cmd, stdin, stdout, stderr, err := s.delegate.Start(command, args, env, workDir, limits)
	if err != nil {
		return cmd, stdin, stdout, stderr, err
	}
	s.cmd = cmd
	s.stdin = &modelHostTestWriteCloser{WriteCloser: stdin}
	s.stdout = &modelHostTestReadCloser{ReadCloser: stdout}
	s.stderr = &modelHostTestReadCloser{ReadCloser: stderr}
	return cmd, s.stdin, s.stdout, s.stderr, nil
}

func (s *driftingBackendModelHostSupervisor) PhysicalMemoryUsage(pid int) (uint64, error) {
	return s.delegate.PhysicalMemoryUsage(pid)
}

func (s *driftingBackendModelHostSupervisor) ResourceLimitBackend() string {
	s.mu.Lock()
	s.calls++
	calls := s.calls
	s.mu.Unlock()
	if calls == 1 {
		return ModelHostResourceBackendDarwinHostTree
	}
	return "drifted-resource-backend"
}

func TestSpawnModelHost_BackendDriftReapsAndCleansBeforeExposure(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	root := t.TempDir()
	supervisor := &driftingBackendModelHostSupervisor{delegate: defaultModelHostSupervisor{}}
	started := time.Now()
	host, err := SpawnModelHost(context.Background(), modelHostSupervisorTestConfig(root, supervisor))
	if host != nil {
		_ = host.Close()
		t.Fatal("backend drift returned a host")
	}
	if err == nil || !strings.Contains(err.Error(), "backend changed") {
		t.Fatalf("backend drift error = %v, want fail-closed backend mismatch", err)
	}
	assertModelHostOnlyErrorLeaves(t, err, ErrUnsupportedVersion)
	if strings.Contains(err.Error(), "did not exit before timeout") {
		t.Fatalf("backend drift retained stale process-group wait error: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= processGroupExitWait {
		t.Fatalf("backend drift cleanup waited for stale process-group timeout: %s", elapsed)
	}
	if supervisor.calls < 2 {
		t.Fatalf("backend identity was checked %d times, want pre- and post-start checks", supervisor.calls)
	}
	if supervisor.cmd == nil || supervisor.cmd.ProcessState == nil {
		var state *os.ProcessState
		if supervisor.cmd != nil {
			state = supervisor.cmd.ProcessState
		}
		t.Fatalf("backend drift child was not reaped: cmd=%v state=%v", supervisor.cmd, state)
	}
	if supervisor.stdin == nil || !supervisor.stdin.wasClosed() || supervisor.stdout == nil || !supervisor.stdout.wasClosed() || supervisor.stderr == nil || !supervisor.stderr.wasClosed() {
		t.Fatal("backend drift descriptors were not all closed")
	}
	if entries, readErr := os.ReadDir(root); readErr != nil {
		t.Fatalf("read disposable root after backend drift: %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("backend drift retained disposable state: %v", entries)
	}
}

func modelHostErrorLeaves(err error) []error {
	if err == nil {
		return nil
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		var leaves []error
		for _, child := range multi.Unwrap() {
			leaves = append(leaves, modelHostErrorLeaves(child)...)
		}
		return leaves
	}
	if single, ok := err.(interface{ Unwrap() error }); ok {
		if unwrapped := single.Unwrap(); unwrapped != nil {
			return modelHostErrorLeaves(unwrapped)
		}
	}
	return []error{err}
}

func assertModelHostOnlyErrorLeaves(t *testing.T, err error, allowed ...error) {
	t.Helper()
	leaves := modelHostErrorLeaves(err)
	if len(leaves) != len(allowed) {
		t.Fatalf("error leaves = %d (%v), want exactly %d allowed leaves", len(leaves), leaves, len(allowed))
	}
	matched := make([]bool, len(allowed))
	for _, leaf := range leaves {
		found := false
		for index, expected := range allowed {
			if !matched[index] && errors.Is(leaf, expected) {
				matched[index] = true
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("unexpected cleanup error leaf %T %v; all leaves=%v", leaf, leaf, leaves)
		}
	}
}

func TestCleanupStartedModelHostProcess_SuppressesBenignGroupKillEPERM(t *testing.T) {
	originalGroupKill := modelHostKillProcessGroup
	originalProcessKill := modelHostKillProcess
	originalGroupWait := modelHostWaitProcessGroupExit
	originalProcessWait := modelHostProcessWait
	t.Cleanup(func() {
		modelHostKillProcessGroup = originalGroupKill
		modelHostKillProcess = originalProcessKill
		modelHostWaitProcessGroupExit = originalGroupWait
		modelHostProcessWait = originalProcessWait
	})
	modelHostKillProcessGroup = func(pgid int, signal syscall.Signal) error {
		_ = originalGroupKill(pgid, signal)
		return syscall.EPERM
	}
	modelHostKillProcess = originalProcessKill
	modelHostProcessWait = originalProcessWait

	for _, verifyErr := range []error{nil, errors.New("injected post-reap group verification failure")} {
		verifyErr := verifyErr
		modelHostWaitProcessGroupExit = func(int, time.Duration) error { return verifyErr }
		cmd := exec.Command("/bin/sh", "-c", "sleep 30")
		configureProcessGroup(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start process-group test child: %v", err)
		}
		cleanupErr := cleanupStartedModelHostProcess(cmd, nil, nil, nil)
		if verifyErr == nil {
			if cleanupErr != nil {
				t.Fatalf("benign EPERM cleanup error = %v, want nil", cleanupErr)
			}
		} else {
			assertModelHostOnlyErrorLeaves(t, cleanupErr, syscall.EPERM, verifyErr)
		}
		if cmd.ProcessState == nil {
			t.Fatal("EPERM cleanup did not reap direct child")
		}
	}
}

func TestCleanupStartedModelHostProcess_NilAndPartialInputsAreSafe(t *testing.T) {
	closeErr := errors.New("sentinel closer failure")
	cases := []struct {
		name   string
		cmd    *exec.Cmd
		stdin  io.WriteCloser
		stdout io.ReadCloser
		stderr io.ReadCloser
	}{
		{name: "all nil"},
		{name: "stdin only", stdin: &modelHostTestWriteCloser{closeErr: closeErr}},
		{name: "stdout only", stdout: &modelHostTestReadCloser{closeErr: closeErr}},
		{name: "stderr only", stderr: &modelHostTestReadCloser{closeErr: closeErr}},
		{name: "empty command", cmd: &exec.Cmd{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := cleanupStartedModelHostProcess(testCase.cmd, testCase.stdin, testCase.stdout, testCase.stderr)
			if testCase.stdin != nil && !errors.Is(err, closeErr) {
				t.Fatalf("cleanup error = %v, want sentinel closer failure", err)
			}
			if testCase.stdout != nil && !errors.Is(err, closeErr) {
				t.Fatalf("cleanup error = %v, want sentinel closer failure", err)
			}
			if testCase.stderr != nil && !errors.Is(err, closeErr) {
				t.Fatalf("cleanup error = %v, want sentinel closer failure", err)
			}
		})
	}
}

func TestSpawnModelHost_SupervisorMemoryObservationFailureFailsClosed(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	supervisor := newRecordingModelHostSupervisor()
	supervisor.memoryErr = errors.New("injected physical memory observation failure")
	root := t.TempDir()
	host, err := SpawnModelHost(context.Background(), modelHostSupervisorTestConfig(root, supervisor))
	if host != nil {
		_ = host.Close()
		t.Fatal("memory observation failure returned a host")
	}
	if err == nil || !strings.Contains(err.Error(), "injected physical memory observation failure") {
		t.Fatalf("spawn error = %v, want injected observation failure", err)
	}
	if entries, readErr := os.ReadDir(root); readErr != nil {
		t.Fatalf("read disposable root: %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("disposable root retained memory-observation failure state: %v", entries)
	}
}

func TestSpawnModelHost_SupervisorBackendMismatchFailsBeforeStart(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	supervisor := newRecordingModelHostSupervisor()
	supervisor.backendSet = true
	supervisor.backend = "unavailable.test"
	root := t.TempDir()
	host, err := SpawnModelHost(context.Background(), modelHostSupervisorTestConfig(root, supervisor))
	if host != nil {
		_ = host.Close()
		t.Fatal("backend mismatch returned a host")
	}
	if err == nil || !strings.Contains(err.Error(), "resource limiter backend") {
		t.Fatalf("spawn error = %v, want backend mismatch", err)
	}
	if starts := supervisor.snapshotStarts(); len(starts) != 0 {
		t.Fatalf("backend mismatch started a process: %+v", starts)
	}
	if entries, readErr := os.ReadDir(root); readErr != nil {
		t.Fatalf("read disposable root: %v", readErr)
	} else if len(entries) != 0 {
		t.Fatalf("disposable root retained backend mismatch state: %v", entries)
	}
}

type modelHostConcurrentSupervisorResult struct {
	host     *ModelHost
	spawnErr error
	closeErr error
}

func TestSpawnModelHost_ConcurrentHostsKeepSupervisorInstancesIndependent(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	rootOne, rootTwo := t.TempDir(), t.TempDir()
	supervisorOne, supervisorTwo := newRecordingModelHostSupervisor(), newRecordingModelHostSupervisor()
	cfgOne := modelHostSupervisorTestConfig(rootOne, supervisorOne, "CODEFLOW_MODEL_HOST_MODE=resource-probe")
	cfgTwo := modelHostSupervisorTestConfig(rootTwo, supervisorTwo, "CODEFLOW_MODEL_HOST_MODE=resource-probe")
	results := make(chan modelHostConcurrentSupervisorResult, 2)
	var wg sync.WaitGroup
	for _, cfg := range []ModelHostConfig{cfgOne, cfgTwo} {
		wg.Add(1)
		go func(cfg ModelHostConfig) {
			defer wg.Done()
			host, spawnErr := SpawnModelHost(context.Background(), cfg)
			result := modelHostConcurrentSupervisorResult{host: host, spawnErr: spawnErr}
			if host != nil {
				result.closeErr = host.Close()
			}
			results <- result
		}(cfg)
	}
	wg.Wait()
	close(results)
	var got []modelHostConcurrentSupervisorResult
	for result := range results {
		got = append(got, result)
	}
	if len(got) != 2 {
		t.Fatalf("concurrent host results = %d, want 2", len(got))
	}
	for _, result := range got {
		if result.spawnErr != nil || result.host == nil {
			t.Fatalf("concurrent host spawn failed: %+v", result)
		}
		if result.closeErr != nil {
			t.Fatalf("concurrent host cleanup failed: %v", result.closeErr)
		}
	}
	if starts := supervisorOne.snapshotStarts(); len(starts) != 1 {
		t.Fatalf("first supervisor start calls = %d, want 1", len(starts))
	}
	if starts := supervisorTwo.snapshotStarts(); len(starts) != 1 {
		t.Fatalf("second supervisor start calls = %d, want 1", len(starts))
	}
	if got[0].host == got[1].host {
		t.Fatal("concurrent spawns returned the same host instance")
	}
	if got[0].host.supervisor == got[1].host.supervisor {
		t.Fatal("concurrent spawns unexpectedly shared supervisor instances")
	}
}
