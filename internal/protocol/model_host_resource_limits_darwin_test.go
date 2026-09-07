//go:build darwin && cgo

package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const modelHostRLIMITNPROC = 7

// modelHostRLIMITOnlySupervisor deliberately does not implement the optional
// Core CPU observer. The acceptance test below must therefore exercise the
// inherited kernel RLIMIT_CPU termination path, not the 10 ms Core watchdog.
// It still delegates process start, RSS observation, and backend identity to
// the real Darwin supervisor.
type modelHostRLIMITOnlySupervisor struct{}

func (modelHostRLIMITOnlySupervisor) Start(command string, args, env []string, workDir string, limits ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	return defaultModelHostSupervisor{}.Start(command, args, env, workDir, limits)
}

func (modelHostRLIMITOnlySupervisor) PhysicalMemoryUsage(pid int) (uint64, error) {
	return defaultModelHostSupervisor{}.PhysicalMemoryUsage(pid)
}

func (modelHostRLIMITOnlySupervisor) ResourceLimitBackend() string {
	return defaultModelHostSupervisor{}.ResourceLimitBackend()
}

type modelHostObservedResourceLimit struct {
	Current uint64 `json:"current"`
	Maximum uint64 `json:"maximum"`
}

type modelHostObservedResourceLimits struct {
	CPU      modelHostObservedResourceLimit `json:"cpu"`
	Memory   modelHostObservedResourceLimit `json:"memory"`
	Process  modelHostObservedResourceLimit `json:"process"`
	PID      int                            `json:"pid"`
	CPUError string                         `json:"cpuError,omitempty"`
	MemError string                         `json:"memoryError,omitempty"`
	ProcErr  string                         `json:"processError,omitempty"`
	Fork     string                         `json:"fork"`
}

// TestSpawnModelHost_AdversarialResourceProbeRequiresOSLimits asks the direct
// child for the kernel's resource limits and memory observation, rather than
// trusting a capability field or a child-declared value.
func TestSpawnModelHost_AdversarialResourceProbeRequiresOSLimits(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	limits := DefaultModelHostResourceLimits()
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath:        os.Args[0],
		Args:           []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1"},
		ResourceLimits: limits,
		DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	defer host.Close()

	request := ModelHostRequest{
		SchemaID:         ModelHostRequestSchemaID,
		SchemaVersion:    ModelHostProtocolVersion,
		RequestID:        "resource-limit-probe",
		Operation:        ModelHostEnrichMethod,
		EvidencePack:     json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:resource-limit-probe"}`),
		PackDigest:       "sha256:resource-limit-probe",
		MaxResponseBytes: 1 << 20,
	}
	if _, err := host.Enrich(context.Background(), request); err != nil {
		t.Fatalf("resource probe enrichment: %v", err)
	}
	observedBytes, err := host.ReadDisposableFile("resource-limits.json")
	if err != nil {
		t.Fatalf("read child OS observation: %v", err)
	}
	var observed modelHostObservedResourceLimits
	if err := json.Unmarshal(observedBytes, &observed); err != nil {
		t.Fatalf("decode child OS observation: %v", err)
	}
	if observed.CPUError != "" || observed.MemError != "" || observed.ProcErr != "" {
		t.Fatalf("child could not observe all required OS limits: %+v", observed)
	}
	if observed.Fork != "blocked" {
		t.Fatalf("child process tree fork was not blocked by the sandbox: %+v", observed)
	}
	if !modelHostResourceLimitExact(observed, limits) {
		t.Fatalf("child observed resource limits different from the applied limits: observed=%+v limits=%+v", observed, limits)
	}
}

// TestSpawnModelHost_ResourceEvidenceUsesHostTreeBound ensures Core does not
// report Darwin's per-UID RLIMIT_NPROC value as this host's process-tree
// bound. The sandbox denies process-fork, so one supervised host process is
// the actual request-scoped tree limit even when the configured defense limit
// is larger.
func TestSpawnModelHost_ResourceEvidenceUsesHostTreeBound(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	limits := DefaultModelHostResourceLimits()
	limits.ProcessCount = 17
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath:        os.Args[0],
		Args:           []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1"},
		ResourceLimits: limits,
		DefaultTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	t.Cleanup(func() { _ = host.Close() })

	evidence := host.Capability().ResourceLimits
	if evidence == nil {
		t.Fatal("resource evidence is missing from the measured capability")
	}
	if evidence.Declared.ProcessCount != limits.ProcessCount {
		t.Fatalf("declared process-count defense changed: got %d want %d", evidence.Declared.ProcessCount, limits.ProcessCount)
	}
	if evidence.Applied.ProcessCount != 1 {
		t.Fatalf("applied process-count falsely claims a per-UID RLIMIT_NPROC value: got %d want host-tree bound 1", evidence.Applied.ProcessCount)
	}
	if evidence.Applied.CPUTimeSeconds != limits.CPUTimeSeconds || evidence.Applied.MemoryBytes != limits.MemoryBytes {
		t.Fatalf("applied CPU/memory limits changed: applied=%+v declared=%+v", evidence.Applied, limits)
	}
	if evidence.Backend != ModelHostResourceBackendDarwinHostTree || evidence.EnforcementStatus != ModelHostResourceEnforcementEnforced {
		t.Fatalf("resource evidence backend/enforcement is not truthful: %+v", evidence)
	}
	if err := ValidateModelHostResourceLimitEvidence(*evidence); err != nil {
		t.Fatalf("host-tree resource evidence rejected: %v", err)
	}
}

func TestValidateModelHostResourceLimitEvidenceRejectsPerUIDAppliedCount(t *testing.T) {
	limits := DefaultModelHostResourceLimits()
	evidence := ModelHostResourceLimitEvidence{
		Version:           ModelHostResourceLimitsVersion,
		Declared:          limits,
		Applied:           limits,
		EnforcementStatus: ModelHostResourceEnforcementEnforced,
		Backend:           ModelHostResourceBackendDarwinHostTree,
	}
	if err := ValidateModelHostResourceLimitEvidence(evidence); err == nil || !strings.Contains(err.Error(), "single-process sandbox tree bound") {
		t.Fatalf("per-UID process-count value was accepted as an applied host-tree bound: %v", err)
	}
}

func TestSpawnModelHost_EnforcesMemoryLimitAndCleansUp(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	for attempt := 0; attempt < 5; attempt++ {
		limits := DefaultModelHostResourceLimits()
		limits.MemoryBytes = 64 << 20
		disposableRoot := t.TempDir()
		host, err := SpawnModelHost(context.Background(), ModelHostConfig{
			BinPath:        os.Args[0],
			Args:           []string{"-test.run=TestModelHostResourceLimitHelper"},
			Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=memory-over"},
			DisposableRoot: disposableRoot,
			ResourceLimits: limits,
			DefaultTimeout: 2 * time.Second,
		})
		if err != nil {
			t.Fatalf("attempt %d spawn model host: %v", attempt, err)
		}
		request := ModelHostRequest{
			SchemaID:         ModelHostRequestSchemaID,
			SchemaVersion:    ModelHostProtocolVersion,
			RequestID:        fmt.Sprintf("resource-limit-memory-overrun-%d", attempt),
			Operation:        ModelHostEnrichMethod,
			EvidencePack:     json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:resource-limit-memory-overrun"}`),
			PackDigest:       "sha256:resource-limit-memory-overrun",
			MaxResponseBytes: 1 << 20,
		}
		response, enrichErr := host.Enrich(context.Background(), request)
		if enrichErr == nil {
			t.Fatalf("attempt %d memory over-allocation unexpectedly succeeded: response=%+v", attempt, response)
		}
		if len(response.Proposal) != 0 {
			t.Fatalf("attempt %d memory over-allocation exposed a proposal: %s", attempt, response.Proposal)
		}
		if errors.Is(enrichErr, ErrTimeout) || errors.Is(enrichErr, ErrCrashed) && !errors.Is(enrichErr, ErrModelHostResourceLimit) {
			t.Fatalf("attempt %d memory over-allocation was not classified by the Core watchdog: %v", attempt, enrichErr)
		}
		if !errors.Is(enrichErr, ErrModelHostResourceLimit) {
			t.Fatalf("attempt %d memory over-allocation error = %v, want resource-limit sentinel", attempt, enrichErr)
		}
		var resourceErr *ModelHostResourceLimitError
		if !errors.As(enrichErr, &resourceErr) || resourceErr.Kind != ModelHostResourceLimitMemory {
			t.Fatalf("attempt %d memory over-allocation cause = %#v, want memory", attempt, resourceErr)
		}
		if err := host.Close(); err != nil {
			t.Fatalf("attempt %d close after memory limit failure: %v", attempt, err)
		}
		evidence := host.IsolationEvidence()
		if evidence.TerminalStatus != "failure" {
			t.Fatalf("attempt %d memory limit terminal status = %q, want failure: %+v", attempt, evidence.TerminalStatus, evidence)
		}
		if evidence.ReceivedRequestID != request.RequestID || evidence.ReceivedPackDigest != request.PackDigest {
			t.Fatalf("attempt %d memory limit receipt evidence = request=%q pack=%q, want request=%q pack=%q", attempt, evidence.ReceivedRequestID, evidence.ReceivedPackDigest, request.RequestID, request.PackDigest)
		}
		if evidence.ResourceLimits == nil || evidence.ResourceLimits.EnforcementStatus != ModelHostResourceEnforcementEnforced {
			t.Fatalf("attempt %d memory limit resource evidence = %+v, want enforced evidence", attempt, evidence.ResourceLimits)
		}
		if capabilityLimits := host.Capability().ResourceLimits; capabilityLimits == nil || *capabilityLimits != *evidence.ResourceLimits {
			t.Fatalf("attempt %d memory limit capability/evidence diverged: capability=%+v evidence=%+v", attempt, capabilityLimits, evidence.ResourceLimits)
		}
		if err := ValidateModelHostResourceLimitEvidence(*evidence.ResourceLimits); err != nil {
			t.Fatalf("attempt %d memory limit resource evidence invalid: %v", attempt, err)
		}
		if !evidence.CleanupVerified {
			t.Fatalf("attempt %d memory limit failure did not verify disposable cleanup: %+v", attempt, evidence)
		}
		if host.cmd == nil || host.cmd.ProcessState == nil {
			t.Fatalf("attempt %d memory limit failure did not reap the model host process", attempt)
		}
		status, ok := host.cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !host.cmd.ProcessState.Exited() && (!ok || !status.Signaled()) {
			t.Fatalf("attempt %d memory limit failure did not terminate the model host process: state=%v", attempt, host.cmd.ProcessState)
		}
		if entries, readErr := os.ReadDir(disposableRoot); readErr != nil || len(entries) != 0 {
			t.Fatalf("attempt %d disposable root retained state: entries=%d readErr=%v", attempt, len(entries), readErr)
		}
	}
}

func TestSpawnModelHost_EnforcesCPULimitWithTypedCause(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		limits := DefaultModelHostResourceLimits()
		limits.CPUTimeSeconds = 1
		disposableRoot := t.TempDir()
		host, err := SpawnModelHost(context.Background(), ModelHostConfig{
			BinPath:        os.Args[0],
			Args:           []string{"-test.run=TestModelHostResourceLimitHelper"},
			Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=cpu-over"},
			DisposableRoot: disposableRoot,
			ResourceLimits: limits,
			DefaultTimeout: 5 * time.Second,
			supervisor:     modelHostRLIMITOnlySupervisor{},
		})
		if err != nil {
			t.Fatalf("attempt %d spawn model host: %v", attempt, err)
		}
		request := ModelHostRequest{
			SchemaID:         ModelHostRequestSchemaID,
			SchemaVersion:    ModelHostProtocolVersion,
			RequestID:        fmt.Sprintf("resource-limit-cpu-overrun-%d", attempt),
			Operation:        ModelHostEnrichMethod,
			EvidencePack:     json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:resource-limit-cpu-overrun"}`),
			PackDigest:       "sha256:resource-limit-cpu-overrun",
			MaxResponseBytes: 1 << 20,
		}
		response, enrichErr := host.Enrich(context.Background(), request)
		if enrichErr == nil {
			t.Fatalf("attempt %d CPU exhaustion unexpectedly succeeded: response=%+v", attempt, response)
		}
		if len(response.Proposal) != 0 {
			t.Fatalf("attempt %d CPU exhaustion exposed a proposal: %s", attempt, response.Proposal)
		}
		if errors.Is(enrichErr, ErrTimeout) {
			t.Fatalf("attempt %d CPU exhaustion reached request timeout: %v", attempt, enrichErr)
		}
		if !errors.Is(enrichErr, ErrModelHostResourceLimit) {
			t.Fatalf("attempt %d CPU exhaustion error = %v, want resource-limit sentinel", attempt, enrichErr)
		}
		var resourceErr *ModelHostResourceLimitError
		if !errors.As(enrichErr, &resourceErr) || resourceErr.Kind != ModelHostResourceLimitCPU {
			t.Fatalf("attempt %d CPU exhaustion cause = %#v, want CPU", attempt, resourceErr)
		}
		if err := host.Close(); err != nil {
			t.Fatalf("attempt %d close after CPU limit failure: %v", attempt, err)
		}
		evidence := host.IsolationEvidence()
		if evidence.TerminalStatus != "failure" || evidence.ResourceLimits == nil || evidence.ResourceLimits.EnforcementStatus != ModelHostResourceEnforcementEnforced {
			t.Fatalf("attempt %d CPU limit evidence = %+v, want failure/enforced", attempt, evidence)
		}
		if evidence.ReceivedRequestID != request.RequestID || evidence.ReceivedPackDigest != request.PackDigest {
			t.Fatalf("attempt %d CPU limit receipt evidence = request=%q pack=%q, want request=%q pack=%q", attempt, evidence.ReceivedRequestID, evidence.ReceivedPackDigest, request.RequestID, request.PackDigest)
		}
		if capabilityLimits := host.Capability().ResourceLimits; capabilityLimits == nil || *capabilityLimits != *evidence.ResourceLimits {
			t.Fatalf("attempt %d CPU limit capability/evidence diverged: capability=%+v evidence=%+v", attempt, capabilityLimits, evidence.ResourceLimits)
		}
		if !evidence.CleanupVerified || host.cmd == nil || host.cmd.ProcessState == nil {
			var state any
			if host.cmd != nil {
				state = host.cmd.ProcessState
			}
			t.Fatalf("attempt %d CPU limit cleanup evidence = %+v state=%v", attempt, evidence, state)
		}
		status, ok := host.cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || (status.Signal() != syscall.SIGKILL && status.Signal() != syscall.SIGXCPU) {
			t.Fatalf("attempt %d CPU limit process state = %v, want an OS signal", attempt, host.cmd.ProcessState)
		}
		usage, ok := host.cmd.ProcessState.SysUsage().(*syscall.Rusage)
		observedCPU := time.Duration(0)
		if ok && usage != nil {
			observedCPU = time.Duration(usage.Utime.Sec)*time.Second + time.Duration(usage.Utime.Usec)*time.Microsecond + time.Duration(usage.Stime.Sec)*time.Second + time.Duration(usage.Stime.Usec)*time.Microsecond
		}
		minimumCPU := time.Duration(limits.CPUTimeSeconds)*time.Second - 250*time.Millisecond
		if !ok || usage == nil || observedCPU < minimumCPU {
			t.Fatalf("attempt %d CPU limit CPU accounting = %v, want at least %s", attempt, observedCPU, minimumCPU)
		}
		if entries, readErr := os.ReadDir(disposableRoot); readErr != nil || len(entries) != 0 {
			t.Fatalf("attempt %d disposable root retained state: entries=%d readErr=%v", attempt, len(entries), readErr)
		}
	}
}

func TestModelHostCPUAccountingIncludesUserAndSystemMicroseconds(t *testing.T) {
	if !modelHostRusageCPUAtLeast(&syscall.Rusage{
		Utime: syscall.Timeval{Sec: 0, Usec: 600_000},
		Stime: syscall.Timeval{Sec: 0, Usec: 400_000},
	}, 1) {
		t.Fatal("user and system CPU microseconds at the one-second boundary were not combined")
	}
	if modelHostRusageCPUAtLeast(&syscall.Rusage{
		Utime: syscall.Timeval{Sec: 0, Usec: 599_999},
		Stime: syscall.Timeval{Sec: 0, Usec: 400_000},
	}, 1) {
		t.Fatal("CPU accounting below the one-second boundary was classified as a limit hit")
	}
}

func TestSpawnModelHost_RLIMITEOFWaitRacePreservesTypedCause(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	originalWait := modelHostProcessWait
	modelHostProcessWait = func(process *os.Process) (*os.ProcessState, error) {
		state, err := originalWait(process)
		time.Sleep(200 * time.Millisecond)
		return state, err
	}
	t.Cleanup(func() { modelHostProcessWait = originalWait })

	limits := DefaultModelHostResourceLimits()
	limits.CPUTimeSeconds = 1
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=cpu-over"},
		DisposableRoot: t.TempDir(), ResourceLimits: limits, DefaultTimeout: 3 * time.Second,
		supervisor: modelHostRLIMITOnlySupervisor{},
	})
	if err != nil {
		t.Fatalf("spawn RLIMIT-only model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "resource-limit-eof-wait-race", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:resource-limit-eof-wait-race"}`),
		PackDigest:   "sha256:resource-limit-eof-wait-race", MaxResponseBytes: 1 << 20,
	}
	response, enrichErr := host.Enrich(context.Background(), request)
	if enrichErr == nil || !errors.Is(enrichErr, ErrModelHostResourceLimit) {
		t.Fatalf("EOF before delayed reaper lost CPU resource-limit cause: %v", enrichErr)
	}
	if len(response.Proposal) != 0 {
		t.Fatalf("EOF before delayed reaper exposed a proposal: %s", response.Proposal)
	}
	var resourceErr *ModelHostResourceLimitError
	if !errors.As(enrichErr, &resourceErr) || resourceErr.Kind != ModelHostResourceLimitCPU {
		t.Fatalf("delayed-reaper resource cause = %#v, want CPU", resourceErr)
	}
	if err := host.Close(); err != nil {
		t.Fatalf("close delayed-reaper host: %v", err)
	}
	if evidence := host.IsolationEvidence(); !evidence.CleanupVerified || evidence.TerminalStatus != "failure" {
		t.Fatalf("delayed-reaper cleanup evidence = %+v", evidence)
	}
}

func TestSpawnModelHost_ShortCrashIsNotClassifiedAsResourceLimit(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	for attempt := 0; attempt < 3; attempt++ {
		limits := DefaultModelHostResourceLimits()
		limits.CPUTimeSeconds = 1
		host, err := SpawnModelHost(context.Background(), ModelHostConfig{
			BinPath:        os.Args[0],
			Args:           []string{"-test.run=TestModelHostResourceLimitHelper"},
			Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=repository-write-attempt-crash"},
			DisposableRoot: t.TempDir(), ResourceLimits: limits, DefaultTimeout: 2 * time.Second,
		})
		if err != nil {
			t.Fatalf("attempt %d spawn crashing model host: %v", attempt, err)
		}
		request := ModelHostRequest{
			SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
			RequestID: fmt.Sprintf("resource-limit-negative-crash-%d", attempt), Operation: ModelHostEnrichMethod,
			EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:resource-limit-negative-crash"}`),
			PackDigest:   "sha256:resource-limit-negative-crash", MaxResponseBytes: 1 << 20,
		}
		_, enrichErr := host.Enrich(context.Background(), request)
		if enrichErr == nil || !errors.Is(enrichErr, ErrCrashed) || errors.Is(enrichErr, ErrModelHostResourceLimit) {
			t.Fatalf("attempt %d ordinary child crash was classified as resource limit: %v", attempt, enrichErr)
		}
		if err := host.Close(); err != nil {
			t.Fatalf("attempt %d close ordinary crashed host: %v", attempt, err)
		}
		if evidence := host.IsolationEvidence(); !evidence.CleanupVerified {
			t.Fatalf("attempt %d ordinary crash cleanup was not verified: %+v", attempt, evidence)
		}
	}
}

func TestSpawnModelHost_RecordsRuntimeRepositoryWriteAttempt(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	sentinelPath := modelHostProbeSentinelPath()
	before, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read repository sentinel before adversarial host: %v", err)
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=repository-write-attempt"},
		DisposableRoot: t.TempDir(), DefaultTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn adversarial model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "runtime-repository-write-attempt", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:runtime-repository-write-attempt"}`),
		PackDigest:   "sha256:runtime-repository-write-attempt", MaxResponseBytes: 1 << 20,
	}
	_, enrichErr := host.Enrich(context.Background(), request)
	if enrichErr == nil || !strings.Contains(enrichErr.Error(), "repository-write target") {
		t.Fatalf("runtime repository write attempt error = %v", enrichErr)
	}
	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("close adversarial host: %v", closeErr)
	}
	evidence := host.IsolationEvidence()
	if evidence.RepositoryWriteAuditStatus != ModelHostRepositoryWriteAuditAttributed || len(evidence.RepositoryWriteAttempts) != 1 || evidence.RepositoryWriteAttempts[0] != modelHostRepositoryWriteCapabilityViolation {
		t.Fatalf("runtime repository write audit = %+v", evidence)
	}
	if evidence.RepositoryWriteCapability {
		t.Fatalf("blocked runtime write was reported as capability: %+v", evidence)
	}
	if evidence.SentinelBeforeDigest == "" || evidence.SentinelBeforeDigest != evidence.SentinelAfterDigest || !evidence.SentinelUnchanged {
		t.Fatalf("runtime repository sentinel evidence = %+v", evidence)
	}
	after, err := os.ReadFile(sentinelPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("repository sentinel changed after adversarial attempt: readErr=%v before=%q after=%q", err, before, after)
	}
	if !evidence.CleanupVerified {
		t.Fatalf("runtime repository write audit did not verify cleanup: %+v", evidence)
	}
}

// TestSpawnModelHost_RuntimeRepositoryWriteAttemptIsAuditedOnTerminalCancel
// exercises the terminal paths that do not receive a model response. The
// Core-side audit must run before Close removes the FIFO and disposable cwd.
func TestSpawnModelHost_RuntimeRepositoryWriteAttemptIsAuditedOnTerminalCancel(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=repository-write-attempt-cancel"},
		DisposableRoot: t.TempDir(), DefaultTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn cancelling adversarial model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "runtime-repository-write-attempt-cancel", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:runtime-repository-write-attempt-cancel"}`),
		PackDigest:   "sha256:runtime-repository-write-attempt-cancel", MaxResponseBytes: 1 << 20,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, enrichErr := host.Enrich(ctx, request)
		done <- enrichErr
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case enrichErr := <-done:
		if !errors.Is(enrichErr, ErrCancelled) {
			t.Fatalf("cancelled runtime repository write error = %v", enrichErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled runtime repository write did not terminate")
	}
	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("close cancelled adversarial host: %v", closeErr)
	}
	evidence := host.IsolationEvidence()
	if evidence.TerminalStatus != "cancel" || evidence.RepositoryWriteAuditStatus != ModelHostRepositoryWriteAuditAttributed || len(evidence.RepositoryWriteAttempts) != 1 || !evidence.CleanupVerified {
		t.Fatalf("cancel terminal audit evidence = %+v", evidence)
	}
}

func TestSpawnModelHost_RuntimeRepositoryWriteAttemptIsAuditedOnTerminalTimeout(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=repository-write-attempt-timeout"},
		DisposableRoot: t.TempDir(), DefaultTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn timeout adversarial model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "runtime-repository-write-attempt-timeout", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:runtime-repository-write-attempt-timeout"}`),
		PackDigest:   "sha256:runtime-repository-write-attempt-timeout", MaxResponseBytes: 1 << 20,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, enrichErr := host.Enrich(ctx, request)
	if !errors.Is(enrichErr, ErrTimeout) {
		t.Fatalf("timed-out runtime repository write error = %v", enrichErr)
	}
	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("close timed-out adversarial host: %v", closeErr)
	}
	evidence := host.IsolationEvidence()
	if evidence.TerminalStatus != "timeout" || evidence.RepositoryWriteAuditStatus != ModelHostRepositoryWriteAuditAttributed || len(evidence.RepositoryWriteAttempts) != 1 || !evidence.CleanupVerified {
		t.Fatalf("timeout terminal audit evidence = %+v", evidence)
	}
}

func TestSpawnModelHost_RuntimeRepositoryWriteAttemptIsAuditedOnFailure(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=repository-write-attempt-failure"},
		DisposableRoot: t.TempDir(), DefaultTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn failing adversarial model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "runtime-repository-write-attempt-failure", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"sha256:runtime-repository-write-attempt-failure"}`),
		PackDigest:   "sha256:runtime-repository-write-attempt-failure", MaxResponseBytes: 1 << 20,
	}
	_, enrichErr := host.Enrich(context.Background(), request)
	if enrichErr == nil || !strings.Contains(enrichErr.Error(), "repository-write target") {
		t.Fatalf("failing runtime repository write error = %v", enrichErr)
	}
	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("close failing adversarial host: %v", closeErr)
	}
	evidence := host.IsolationEvidence()
	if evidence.TerminalStatus != "failure" || evidence.RepositoryWriteAuditStatus != ModelHostRepositoryWriteAuditAttributed || len(evidence.RepositoryWriteAttempts) != 1 || !evidence.CleanupVerified {
		t.Fatalf("failure terminal audit evidence = %+v", evidence)
	}
}

func TestSpawnModelHost_ReapsChildWhenPreExecLimitApplicationFails(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_INHERITED_HARD_LIMIT") == "1" {
		return
	}
	disposableRoot := t.TempDir()
	reportPath := filepath.Join(disposableRoot, "hard-limit-report.json")
	command := exec.Command(os.Args[0], "-test.run=^TestModelHostInheritedHardLimitHelper$")
	command.Env = append(os.Environ(),
		"CODEFLOW_MODEL_HOST_INHERITED_HARD_LIMIT=1",
		"CODEFLOW_MODEL_HOST_RESULT="+reportPath,
		"CODEFLOW_MODEL_HOST_DISPOSABLE_ROOT="+disposableRoot,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inherited hard-limit helper: %v\n%s", err, output)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read inherited hard-limit report: %v", err)
	}
	var report struct {
		SpawnError        string `json:"spawnError"`
		IsECHILD          bool   `json:"isECHILD"`
		WaitResult        string `json:"waitResult"`
		DisposableEntries int    `json:"disposableEntries"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode inherited hard-limit report: %v", err)
	}
	if report.SpawnError == "" {
		t.Fatal("pre-exec resource-limit failure unexpectedly spawned a host")
	}
	if !report.IsECHILD {
		t.Fatalf("pre-exec resource-limit failure did not preserve ECHILD: report=%s", reportBytes)
	}
	if report.WaitResult != "ECHILD" {
		t.Fatalf("pre-exec resource-limit failure left a child behind: wait=%q report=%s", report.WaitResult, reportBytes)
	}
	if report.DisposableEntries != 0 {
		t.Fatalf("unexpected disposable-root contents after failed spawn: entries=%d report=%s", report.DisposableEntries, reportBytes)
	}
}

func TestSpawnModelHost_RepeatedPostIsolationCancellationDoesNotLeakAuditReaders(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	baseline, err := modelHostOpenFileDescriptorCount()
	if err != nil {
		t.Fatalf("count baseline file descriptors: %v", err)
	}
	maxOpen := baseline
	for attempt := 0; attempt < 12; attempt++ {
		ctx, cancel := context.WithCancel(context.Background())
		previousHook := modelHostPostIsolationHook.Load()
		modelHostPostIsolationHook.Store(func() { cancel() })
		var spawnErr error
		func() {
			defer modelHostPostIsolationHook.Store(previousHook)
			_, spawnErr = SpawnModelHost(ctx, ModelHostConfig{
				BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
				Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
			})
		}()
		cancel()
		if !errors.Is(spawnErr, context.Canceled) {
			t.Fatalf("post-isolation cancellation attempt %d error = %v, want context.Canceled", attempt, spawnErr)
		}
		open, countErr := modelHostOpenFileDescriptorCount()
		if countErr != nil {
			t.Fatalf("count file descriptors after cancellation attempt %d: %v", attempt, countErr)
		}
		if open > maxOpen {
			maxOpen = open
		}
	}
	after, err := modelHostOpenFileDescriptorCount()
	if err != nil {
		t.Fatalf("count final file descriptors: %v", err)
	}
	if maxOpen > baseline+3 || after > baseline+3 {
		t.Fatalf("post-isolation cancellation leaked audit readers: baseline=%d max=%d after=%d", baseline, maxOpen, after)
	}
}

func modelHostOpenFileDescriptorCount() (int, error) {
	directory, err := os.Open("/dev/fd")
	if err != nil {
		return 0, err
	}
	defer directory.Close()
	names, err := directory.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	return len(names), nil
}

func TestSpawnModelHost_RepeatedPreExecLimitFailuresDoNotLeakAuditReaders(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_INHERITED_HARD_LIMIT_REPEAT") == "1" {
		return
	}
	disposableRoot := t.TempDir()
	reportPath := filepath.Join(disposableRoot, "hard-limit-repeat-report.json")
	command := exec.Command(os.Args[0], "-test.run=^TestModelHostRepeatedInheritedHardLimitHelper$")
	command.Env = append(os.Environ(),
		"CODEFLOW_MODEL_HOST_INHERITED_HARD_LIMIT_REPEAT=1",
		"CODEFLOW_MODEL_HOST_RESULT="+reportPath,
		"CODEFLOW_MODEL_HOST_DISPOSABLE_ROOT="+disposableRoot,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("repeated inherited hard-limit helper: %v\n%s", err, output)
	}
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read repeated inherited hard-limit report: %v", err)
	}
	var report struct {
		Attempts          int `json:"attempts"`
		Failures          int `json:"failures"`
		FDStart           int `json:"fdStart"`
		FDMax             int `json:"fdMax"`
		FDAfter           int `json:"fdAfter"`
		DisposableEntries int `json:"disposableEntries"`
	}
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode repeated inherited hard-limit report: %v", err)
	}
	if report.Attempts < 4 || report.Failures != report.Attempts {
		t.Fatalf("pre-exec failure attempts were not all rejected: report=%s", reportBytes)
	}
	if report.FDMax > report.FDStart+3 || report.FDAfter > report.FDStart+3 {
		t.Fatalf("pre-exec failures leaked audit readers: report=%s", reportBytes)
	}
	if report.DisposableEntries != 0 {
		t.Fatalf("unexpected disposable-root contents after repeated failed spawns: report=%s", reportBytes)
	}
}

func TestModelHostRuntimeRepositoryWriteAuditOwnershipIsIdempotent(t *testing.T) {
	var nilAudit *modelHostRuntimeRepositoryWriteAudit
	if err := nilAudit.Close(); err != nil {
		t.Fatalf("nil audit close = %v, want nil", err)
	}

	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open disposable audit file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close disposable audit file: %v", err)
	}
	audit := &modelHostRuntimeRepositoryWriteAudit{targetReader: file}
	firstErr := audit.Close()
	if firstErr == nil {
		t.Fatal("closing an already-closed audit reader unexpectedly succeeded")
	}
	if secondErr := audit.Close(); !errors.Is(secondErr, firstErr) {
		t.Fatalf("second audit close = %v, want original close error %v", secondErr, firstErr)
	}
	setup := modelHostIsolationSetup{runtimeRepositoryWriteAudit: audit}
	if closeErr := setup.closeRuntimeRepositoryWriteAudit(); !errors.Is(closeErr, firstErr) {
		t.Fatalf("setup close = %v, want original close error %v", closeErr, firstErr)
	}
	if setup.runtimeRepositoryWriteAudit != nil {
		t.Fatal("setup retained runtime audit after ownership release")
	}
	if closeErr := setup.closeRuntimeRepositoryWriteAudit(); closeErr != nil {
		t.Fatalf("repeated empty setup close = %v, want nil", closeErr)
	}
}

func TestSpawnModelHostTransfersRuntimeRepositoryWriteAuditToHost(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	baseline, err := modelHostOpenFileDescriptorCount()
	if err != nil {
		t.Fatalf("count baseline file descriptors: %v", err)
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	audit := host.runtimeRepositoryWriteAudit
	if audit == nil || audit.targetReader == nil {
		_ = host.Close()
		t.Fatal("successful host did not receive the runtime audit reader")
	}
	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("close model host: %v", closeErr)
	}
	if closeErr := audit.Close(); closeErr != nil {
		t.Fatalf("repeated transferred-audit close: %v", closeErr)
	}
	if closeErr := audit.Close(); closeErr != nil {
		t.Fatalf("third transferred-audit close: %v", closeErr)
	}
	after, err := modelHostOpenFileDescriptorCount()
	if err != nil {
		t.Fatalf("count final file descriptors: %v", err)
	}
	if after > baseline+3 {
		t.Fatalf("successful host audit reader remained open after Close: baseline=%d after=%d", baseline, after)
	}
}

func TestTerminateProcessGroupReportsInjectedGroupWaitFailure(t *testing.T) {
	originalGroupKill := modelHostKillProcessGroup
	originalProcessKill := modelHostKillProcess
	originalGroupWait := modelHostWaitProcessGroupExit
	t.Cleanup(func() {
		modelHostKillProcessGroup = originalGroupKill
		modelHostKillProcess = originalProcessKill
		modelHostWaitProcessGroupExit = originalGroupWait
	})
	modelHostKillProcessGroup = func(int, syscall.Signal) error { return nil }
	modelHostKillProcess = func(*os.Process) error { return nil }
	modelHostWaitProcessGroupExit = func(int, time.Duration) error {
		return errors.New("injected process-group wait timeout")
	}
	err := terminateProcessGroup(&exec.Cmd{Process: &os.Process{Pid: 424242}})
	if err == nil || !strings.Contains(err.Error(), "injected process-group wait timeout") {
		t.Fatalf("terminateProcessGroup error = %v, want injected group wait failure", err)
	}
}

func TestModelHostInheritedHardLimitHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_INHERITED_HARD_LIMIT") != "1" {
		return
	}
	reportPath := os.Getenv("CODEFLOW_MODEL_HOST_RESULT")
	disposableRoot := os.Getenv("CODEFLOW_MODEL_HOST_DISPOSABLE_ROOT")
	report := struct {
		SpawnError        string `json:"spawnError"`
		IsECHILD          bool   `json:"isECHILD"`
		WaitResult        string `json:"waitResult"`
		DisposableEntries int    `json:"disposableEntries"`
	}{}
	writeReport := func() {
		data, _ := json.Marshal(report)
		_ = os.WriteFile(reportPath, data, 0o600)
	}
	var cpuLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CPU, &cpuLimit); err != nil {
		report.SpawnError = "getrlimit: " + err.Error()
		writeReport()
		return
	}
	cpuLimit.Cur = 1
	cpuLimit.Max = 1
	if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &cpuLimit); err != nil {
		report.SpawnError = "setrlimit: " + err.Error()
		writeReport()
		return
	}
	limits := DefaultModelHostResourceLimits()
	_, spawnErr := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath:        os.Args[0],
		Args:           []string{"-test.run=TestModelHostResourceLimitHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1"},
		DisposableRoot: disposableRoot,
		ResourceLimits: limits,
		DefaultTimeout: time.Second,
	})
	report.SpawnError = fmt.Sprint(spawnErr)
	report.IsECHILD = errors.Is(spawnErr, syscall.ECHILD)
	var waitStatus syscall.WaitStatus
	pid, waitErr := syscall.Wait4(-1, &waitStatus, syscall.WNOHANG, nil)
	switch {
	case errors.Is(waitErr, syscall.ECHILD):
		report.WaitResult = "ECHILD"
	case waitErr != nil:
		report.WaitResult = "error: " + waitErr.Error()
	case pid > 0:
		report.WaitResult = fmt.Sprintf("leftover:%d", pid)
	default:
		report.WaitResult = "running"
	}
	if entries, err := os.ReadDir(disposableRoot); err == nil {
		report.DisposableEntries = len(entries)
	} else {
		report.DisposableEntries = -1
	}
	writeReport()
}

func TestModelHostRepeatedInheritedHardLimitHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_INHERITED_HARD_LIMIT_REPEAT") != "1" {
		return
	}
	reportPath := os.Getenv("CODEFLOW_MODEL_HOST_RESULT")
	disposableRoot := os.Getenv("CODEFLOW_MODEL_HOST_DISPOSABLE_ROOT")
	report := struct {
		Attempts          int `json:"attempts"`
		Failures          int `json:"failures"`
		FDStart           int `json:"fdStart"`
		FDMax             int `json:"fdMax"`
		FDAfter           int `json:"fdAfter"`
		DisposableEntries int `json:"disposableEntries"`
	}{}
	writeReport := func() {
		data, _ := json.Marshal(report)
		_ = os.WriteFile(reportPath, data, 0o600)
	}
	var cpuLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CPU, &cpuLimit); err != nil {
		writeReport()
		return
	}
	cpuLimit.Cur = 1
	cpuLimit.Max = 1
	if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &cpuLimit); err != nil {
		writeReport()
		return
	}
	limits := DefaultModelHostResourceLimits()
	report.FDStart, _ = modelHostOpenFileDescriptorCount()
	report.FDMax = report.FDStart
	for report.Attempts = 0; report.Attempts < 8; report.Attempts++ {
		_, spawnErr := SpawnModelHost(context.Background(), ModelHostConfig{
			BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostResourceLimitHelper"},
			Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: disposableRoot,
			ResourceLimits: limits, DefaultTimeout: time.Second,
		})
		if spawnErr != nil {
			report.Failures++
		}
		if open, countErr := modelHostOpenFileDescriptorCount(); countErr == nil && open > report.FDMax {
			report.FDMax = open
		}
	}
	report.FDAfter, _ = modelHostOpenFileDescriptorCount()
	if entries, err := os.ReadDir(disposableRoot); err == nil {
		report.DisposableEntries = len(entries)
	} else {
		report.DisposableEntries = -1
	}
	writeReport()
}

func modelHostResourceLimitExact(observed modelHostObservedResourceLimits, limits ModelHostResourceLimits) bool {
	return observed.CPU.Current == limits.CPUTimeSeconds && observed.CPU.Maximum == limits.CPUTimeSeconds &&
		observed.Memory.Current > 0 && observed.Memory.Current < limits.MemoryBytes &&
		observed.Process.Current == limits.ProcessCount && observed.Process.Maximum == limits.ProcessCount &&
		observed.PID > 0
}

// TestModelHostResourceLimitHelper is a test-only adversarial child. It
// records getrlimit(2) values from inside the sandbox and does not make those
// values authoritative for the production capability or evidence.
func TestModelHostResourceLimitHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		body, err := readNextFrame(reader, DefaultMaxMessageSizeBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &request) != nil {
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			probe := modelHostFilesystemProbe()
			result = ModelHostResponse{
				SchemaID: ModelHostResponseSchemaID, SchemaVersion: ModelHostProtocolVersion,
				RequestID: request.ID, Status: "ok",
				Capability: ModelHostCapability{
					Status: "measured", ModelID: "resource-probe", Revision: "r1",
					License: "MIT", Checksum: "sha256:resource-probe", Runtime: "test", DataBoundary: "local",
					SchemaConstrained: true, Cancellation: true, Measured: true,
					MaxRequestBytes: DefaultMaxMessageSizeBytes, MaxResponseBytes: DefaultMaxMessageSizeBytes,
					IsolationBackend: "sandbox-exec", IsolationEnforced: true, IsolationProbe: &probe,
					NetworkPolicy: "deny_all",
				},
			}
		case ModelHostEnrichMethod:
			var params ModelHostRequest
			if json.Unmarshal(request.Params, &params) != nil {
				return
			}
			receipt, _ := json.Marshal(struct {
				JSONRPC string `json:"jsonrpc"`
				Method  string `json:"method"`
				Params  any    `json:"params"`
			}{JSONRPCVersion, modelHostReceiptMethod, struct {
				RequestID  string `json:"requestId"`
				PackDigest string `json:"packDigest"`
			}{params.RequestID, params.PackDigest}})
			if writeFrameBounded(os.Stdout, receipt, DefaultMaxMessageSizeBytes) != nil {
				return
			}
			mode := os.Getenv("CODEFLOW_MODEL_HOST_MODE")
			if strings.HasPrefix(mode, "repository-write-attempt") && !modelHostRecordRuntimeRepositoryWriteAttempt(params.RequestID) {
				return
			}
			if mode == "repository-write-attempt-failure" {
				response, _ := json.Marshal(struct {
					JSONRPC string `json:"jsonrpc"`
					ID      string `json:"id"`
					Error   any    `json:"error"`
				}{JSONRPCVersion, request.ID, map[string]any{"code": -32001, "message": "adversarial failure"}})
				if writeFrameBounded(os.Stdout, response, DefaultMaxMessageSizeBytes) != nil {
					return
				}
				continue
			}
			if mode == "repository-write-attempt-crash" {
				os.Exit(17)
			}
			if mode == "repository-write-attempt-cancel" || mode == "repository-write-attempt-timeout" {
				for {
					time.Sleep(time.Second)
				}
			}
			if mode == "cpu-over" {
				// The Go runtime installs a notifier for SIGXCPU. Restore the
				// kernel's default action so RLIMIT_CPU, rather than a runtime
				// observer or child self-report, terminates this adversarial host.
				modelHostResetCPUTimeLimitSignal()
				for {
					// Keep the child on-CPU after sending its receipt. The
					// inherited RLIMIT_CPU, not this declaration, must terminate it.
				}
			}
			if os.Getenv("CODEFLOW_MODEL_HOST_MODE") == "memory-over" {
				allocation := make([]byte, 256<<20)
				for index := 0; index < len(allocation); index += os.Getpagesize() {
					allocation[index] = byte(index)
				}
				runtime.KeepAlive(allocation)
				for {
					runtime.KeepAlive(allocation)
					time.Sleep(time.Second)
				}
			}
			observed := modelHostObservedResourceLimits{PID: os.Getpid()}
			forkProbe := exec.Command(os.Args[0], "-test.run=^$")
			if err := forkProbe.Run(); err == nil {
				observed.Fork = "allowed"
			} else {
				observed.Fork = "blocked"
			}
			var cpuLimit syscall.Rlimit
			if err := syscall.Getrlimit(syscall.RLIMIT_CPU, &cpuLimit); err != nil {
				observed.CPUError = err.Error()
			} else {
				observed.CPU = modelHostObservedResourceLimit{Current: cpuLimit.Cur, Maximum: cpuLimit.Max}
			}
			memoryUsage, err := currentModelHostPhysicalMemoryUsage()
			if err != nil {
				observed.MemError = err.Error()
			} else {
				observed.Memory = modelHostObservedResourceLimit{Current: memoryUsage}
			}
			var processLimit syscall.Rlimit
			if err := syscall.Getrlimit(modelHostRLIMITNPROC, &processLimit); err != nil {
				observed.ProcErr = err.Error()
			} else {
				observed.Process = modelHostObservedResourceLimit{Current: processLimit.Cur, Maximum: processLimit.Max}
			}
			observedBytes, _ := json.Marshal(observed)
			if err := os.WriteFile("resource-limits.json", observedBytes, 0o600); err != nil {
				return
			}
			result = ModelHostResponse{
				SchemaID: ModelHostResponseSchemaID, SchemaVersion: ModelHostProtocolVersion,
				RequestID: request.ID, Status: "accepted",
				Proposal: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json","schemaVersion":2}`),
			}
		default:
			return
		}
		response, _ := json.Marshal(struct {
			JSONRPC string `json:"jsonrpc"`
			ID      string `json:"id"`
			Result  any    `json:"result"`
		}{JSONRPCVersion, request.ID, result})
		if writeFrameBounded(os.Stdout, response, DefaultMaxMessageSizeBytes) != nil {
			return
		}
	}
}

func modelHostRecordRuntimeRepositoryWriteAttempt(requestID string) bool {
	// The helper derives the repository sentinel from its compiled test
	// location, not from the model-host request, argv, or environment. This is
	// an adversarial real O_WRONLY source-open attempt. The sandbox remains the
	// authority for whether the open is permitted.
	if sentinelPath := modelHostAdversarialRepositorySentinelPath(); sentinelPath != "" {
		if file, err := os.OpenFile(sentinelPath, os.O_WRONLY, 0); err == nil {
			_ = file.Close()
		}
	}
	challengeBytes, err := os.ReadFile(modelHostRuntimeRepositoryWriteChallengeName)
	if err != nil {
		return false
	}
	file, openErr := os.OpenFile(modelHostRuntimeRepositoryWriteTargetName, os.O_WRONLY, 0)
	if openErr != nil {
		return false
	}
	defer file.Close()
	event := strings.TrimSpace(string(challengeBytes)) + "\x00" + requestID
	_, err = file.Write([]byte(event))
	return err == nil
}

func modelHostAdversarialRepositorySentinelPath() string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	sentinelPath := filepath.Join(repositoryRoot, ".git", "HEAD")
	if info, err := os.Stat(filepath.Join(repositoryRoot, ".git")); err == nil && !info.IsDir() {
		sentinelPath = filepath.Join(repositoryRoot, ".git")
	}
	return sentinelPath
}
