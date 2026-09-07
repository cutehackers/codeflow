package protocol

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSpawnModelHost_UsesMeasuredCapabilityAndDisposablePackBoundary(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"}, Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	pack := json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"digest-a03"}`)
	response, err := host.Enrich(context.Background(), ModelHostRequest{SchemaID: ModelHostRequestSchemaID, SchemaVersion: 2, RequestID: "request-a03", Operation: ModelHostEnrichMethod, EvidencePack: pack, PackDigest: "digest-a03", TargetStepID: "step-a03", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20})
	if err != nil {
		_ = host.Close()
		t.Fatalf("enrich: %v", err)
	}
	if response.Status != "accepted" || response.RequestID != "request-a03" {
		t.Fatalf("response = %+v", response)
	}
	evidence := host.IsolationEvidence()
	if evidence.SourceDelivery != "bounded_evidence_pack" || evidence.SourceMount != "not_mounted" || !evidence.Disposable || evidence.RepositoryPathExposed || evidence.RepositoryWriteCapability || len(evidence.RepositoryWriteAttempts) != 0 || evidence.RepositoryWriteAuditStatus != ModelHostRepositoryWriteAuditCapabilityEnforced || evidence.PackDigest != "digest-a03" {
		t.Fatalf("isolation evidence = %+v", evidence)
	}
	capability := host.Capability()
	if capability.RuntimePolicyBinding != ModelHostRuntimePolicyBindingExact || capability.ProbeScope != ModelHostProbeScopeSharedDenyBase || capability.RuntimePolicySharedBaseDigest != evidence.RuntimePolicySharedBaseDigest || capability.ProbePolicyDigest != evidence.ProbePolicyDigest || capability.ProbeSharedBaseDigest != evidence.ProbeSharedBaseDigest {
		t.Fatalf("capability policy evidence is not Core-bound: capability=%+v evidence=%+v", capability, evidence)
	}
	if err := host.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !host.IsolationEvidence().CleanupVerified {
		t.Fatalf("disposable working directory was not cleaned up")
	}
}

func TestSpawnModelHost_RejectsInvalidInitializeHandshake(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	tests := []struct {
		name        string
		mode        string
		wantCode    ErrorCode
		wantMessage string
	}{
		{name: "wrong schema id", mode: "initialize-wrong-schema-id", wantCode: EBadRequest, wantMessage: "/schemaId"},
		{name: "missing schema id", mode: "initialize-missing-schema-id", wantCode: EBadRequest, wantMessage: "schemaId"},
		{name: "wrong schema version", mode: "initialize-wrong-schema-version", wantCode: EBadRequest, wantMessage: "/schemaVersion"},
		{name: "missing schema version", mode: "initialize-missing-schema-version", wantCode: EBadRequest, wantMessage: "schemaVersion"},
		{name: "empty request id", mode: "initialize-empty-request-id", wantCode: EBadRequest, wantMessage: "/requestId"},
		{name: "mismatched request id", mode: "initialize-mismatched-request-id", wantCode: ECrashed, wantMessage: "request identity mismatch"},
		{name: "non-ok status", mode: "initialize-non-ok-status", wantCode: ECrashed, wantMessage: "status"},
		{name: "missing direct capability", mode: "initialize-missing-capability", wantCode: EBadRequest, wantMessage: "capability"},
		{name: "unknown top-level field", mode: "initialize-unknown-top-level", wantCode: EBadRequest, wantMessage: "unexpected"},
		{name: "unknown capability field", mode: "initialize-unknown-capability", wantCode: EBadRequest, wantMessage: "unexpected"},
		{name: "out-of-bound capability value", mode: "initialize-out-of-bound-capability", wantCode: EBadRequest, wantMessage: "maxRequestBytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			disposableRoot := t.TempDir()
			originalWait := modelHostProcessWait
			helperPID := 0
			modelHostProcessWait = func(process *os.Process) (*os.ProcessState, error) {
				if process != nil {
					helperPID = process.Pid
				}
				return originalWait(process)
			}
			t.Cleanup(func() { modelHostProcessWait = originalWait })
			host, err := SpawnModelHost(context.Background(), ModelHostConfig{
				BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
				Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=" + test.mode},
				DisposableRoot: disposableRoot, DefaultTimeout: time.Second,
			})
			if host != nil {
				_ = host.Close()
				t.Fatalf("invalid initialize response returned a host")
			}
			if err == nil {
				t.Fatal("invalid initialize response returned no error")
			}
			var protocolErr *Error
			if !errors.As(err, &protocolErr) {
				t.Fatalf("initialize error = %v, want protocol error class %s", err, test.wantCode)
			}
			if protocolErr.Code != test.wantCode {
				t.Fatalf("initialize error code = %s, want %s (%v)", protocolErr.Code, test.wantCode, err)
			}
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("initialize error = %v, want message containing %q", err, test.wantMessage)
			}
			entries, readErr := os.ReadDir(disposableRoot)
			if readErr != nil {
				t.Fatalf("read disposable root: %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("disposable root entries = %v, want no leaked host directories", entries)
			}
			if helperPID <= 0 {
				t.Fatal("Core-side process observer did not capture the helper PID")
			}
			if waitProcessExit(helperPID, 3*time.Second) {
				t.Fatalf("failed initialize helper process %d remained alive", helperPID)
			}
		})
	}
}

func TestSpawnModelHost_InitializeHandshakeErrorJoinsCleanupFailure(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	disposableRoot := t.TempDir()
	handshakeErr := "model host initialize response schema"
	injectedCleanupErr := errors.New("injected cleanup termination failure")
	previousTerminate := modelHostTerminateProcessGroup
	terminationCalls := 0
	modelHostTerminateProcessGroup = func(cmd *exec.Cmd) error {
		terminationCalls++
		originalErr := previousTerminate(cmd)
		if originalErr != nil {
			return errors.Join(originalErr, injectedCleanupErr)
		}
		return injectedCleanupErr
	}
	defer func() { modelHostTerminateProcessGroup = previousTerminate }()

	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=initialize-wrong-schema-id"},
		DisposableRoot: disposableRoot, DefaultTimeout: time.Second,
	})
	if host != nil {
		_ = host.Close()
		t.Fatal("invalid initialize response returned a host")
	}
	if err == nil {
		t.Fatal("invalid initialize response returned no error")
	}
	if !strings.Contains(err.Error(), handshakeErr) {
		t.Fatalf("initialize error = %v, want handshake schema error", err)
	}
	if !errors.Is(err, injectedCleanupErr) {
		t.Fatalf("initialize error = %v, want injected cleanup error", err)
	}
	if terminationCalls == 0 {
		t.Fatal("cleanup termination boundary was not called")
	}
	entries, readErr := os.ReadDir(disposableRoot)
	if readErr != nil {
		t.Fatalf("read disposable root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("disposable root entries = %v, want no leaked host directories", entries)
	}
}

func TestSpawnModelHost_TimeoutAndCancellationTerminateOnlyEnrichment(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	newHost := func(t *testing.T) *ModelHost {
		t.Helper()
		host, err := SpawnModelHost(context.Background(), ModelHostConfig{BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"}, Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_DELAY=200ms"}, DefaultTimeout: time.Second})
		if err != nil {
			t.Fatalf("spawn model host: %v", err)
		}
		return host
	}
	request := func() ModelHostRequest {
		return ModelHostRequest{SchemaID: ModelHostRequestSchemaID, SchemaVersion: 2, RequestID: "request-timeout", Operation: ModelHostEnrichMethod, EvidencePack: json.RawMessage(`{"schemaId":"pack"}`), PackDigest: "digest-timeout", MaxResponseBytes: 1 << 20}
	}
	t.Run("timeout", func(t *testing.T) {
		host := newHost(t)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err := host.Enrich(ctx, request())
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("error = %v, want timeout", err)
		}
		evidence := host.IsolationEvidence()
		if evidence.TerminalStatus != "timeout" || evidence.RepositoryWriteCapability || len(evidence.RepositoryWriteAttempts) != 0 {
			t.Fatalf("timeout evidence = %+v", evidence)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		host := newHost(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := host.Enrich(ctx, request())
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("error = %v, want cancellation", err)
		}
		evidence := host.IsolationEvidence()
		if evidence.TerminalStatus != "cancel" || evidence.RepositoryWriteCapability || len(evidence.RepositoryWriteAttempts) != 0 {
			t.Fatalf("cancel evidence = %+v", evidence)
		}
	})
}

func TestSpawnModelHost_NoReceiptTimeoutAndCancellationRemainLifecycleErrors(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	for _, test := range []struct {
		name          string
		mode          string
		wantErr       error
		wantTerminal  string
		cancelRequest bool
	}{
		{name: "timeout", mode: "no-receipt-timeout", wantErr: ErrTimeout, wantTerminal: "timeout"},
		{name: "cancel", mode: "no-receipt-cancel", wantErr: ErrCancelled, wantTerminal: "cancel", cancelRequest: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			host, err := SpawnModelHost(context.Background(), ModelHostConfig{
				BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
				Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=" + test.mode}, DefaultTimeout: time.Second,
			})
			if err != nil {
				t.Fatalf("spawn no-receipt host: %v", err)
			}
			request := ModelHostRequest{
				SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
				RequestID: "no-receipt-" + test.name, Operation: ModelHostEnrichMethod,
				EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json"}`),
				PackDigest:   "digest-no-receipt-" + test.name, MaxResponseBytes: 1 << 20,
			}
			ctx := context.Background()
			var cancel context.CancelFunc
			if test.cancelRequest {
				ctx, cancel = context.WithCancel(ctx)
				timer := time.AfterFunc(100*time.Millisecond, cancel)
				defer timer.Stop()
			} else {
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			_, enrichErr := host.Enrich(ctx, request)
			if !errors.Is(enrichErr, test.wantErr) {
				t.Fatalf("no-receipt %s error = %v, want %v", test.name, enrichErr, test.wantErr)
			}
			evidence := host.IsolationEvidence()
			if evidence.ReceivedRequestID != "" || evidence.ReceivedPackDigest != "" {
				t.Fatalf("no-receipt %s fabricated receipt evidence: %+v", test.name, evidence)
			}
			if evidence.TerminalStatus != test.wantTerminal || !evidence.CleanupVerified {
				t.Fatalf("no-receipt %s lifecycle evidence = %+v", test.name, evidence)
			}
			if err := ValidateModelHostIsolationEvidenceForLifecycle(evidence, host.Capability(), request.PackDigest, true); err != nil {
				t.Fatalf("no-receipt %s lifecycle evidence rejected: %v", test.name, err)
			}
			successEvidence := evidence
			successEvidence.TerminalStatus = "success"
			if err := ValidateModelHostIsolationEvidenceForLifecycle(successEvidence, host.Capability(), request.PackDigest, true); err == nil {
				t.Fatalf("no-receipt %s lifecycle validator accepted success evidence without a receipt", test.name)
			}
			if err := host.Close(); err != nil {
				t.Fatalf("close no-receipt %s host: %v", test.name, err)
			}
		})
	}
}

func TestModelHostCloseUnblocksReaderAfterResponseAndEOF(t *testing.T) {
	var framed bytes.Buffer
	if err := writeFrame(&framed, []byte(`{"jsonrpc":"2.0","id":"response"}`)); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan struct{})
	capReady := make(chan struct{})
	close(capReady)
	host := &ModelHost{
		cfg:        ModelHostConfig{MaxResponseBytes: 1 << 20},
		stdout:     io.NopCloser(bytes.NewReader(framed.Bytes())),
		frames:     make(chan modelHostFrame, 1),
		waitDone:   waitDone,
		capReady:   capReady,
		readerDone: make(chan struct{}),
		readerStop: make(chan struct{}),
		workDir:    t.TempDir(),
	}
	go host.readLoop()
	deadline := time.Now().Add(time.Second)
	for len(host.frames) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(host.frames) != 1 {
		t.Fatal("reader did not queue the prepared response frame")
	}

	finishResult := make(chan struct{})
	go func() {
		host.finishTerminal("cancel")
		close(finishResult)
	}()
	time.Sleep(20 * time.Millisecond)
	close(waitDone)
	select {
	case <-finishResult:
	case <-time.After(time.Second):
		t.Fatal("cancel terminal did not finish after process wait completed")
	}
	if evidence := host.IsolationEvidence(); evidence.TerminalStatus != "cancel" {
		t.Fatalf("terminal status = %q, want cancel", evidence.TerminalStatus)
	}
	select {
	case <-host.readerDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("reader remained blocked delivering EOF after host close")
	}
}

func TestSpawnModelHost_ObservedLifecycleModes(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	for _, tc := range []struct {
		mode   string
		status string
	}{
		{mode: "success", status: "success"},
		{mode: "failure", status: "failure"},
		{mode: "crash", status: "crash"},
		{mode: "timeout", status: "timeout"},
		{mode: "cancel", status: "cancel"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			host, err := SpawnModelHost(context.Background(), ModelHostConfig{
				BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
				Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=" + tc.mode},
				DisposableRoot: t.TempDir(), DefaultTimeout: 60 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("spawn model host: %v", err)
			}
			workDir := host.workDir
			request := ModelHostRequest{SchemaID: ModelHostRequestSchemaID, SchemaVersion: 2, RequestID: "request-lifecycle-" + tc.mode, Operation: ModelHostEnrichMethod, EvidencePack: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json","packDigest":"digest-lifecycle"}`), PackDigest: "digest-lifecycle", TargetStepID: "step-lifecycle", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20}
			var callErr error
			if tc.mode == "cancel" {
				ctx, cancel := context.WithCancel(context.Background())
				result := make(chan error, 1)
				go func() {
					_, err := host.Enrich(ctx, request)
					result <- err
				}()
				time.Sleep(20 * time.Millisecond)
				cancel()
				callErr = <-result
			} else {
				_, callErr = host.Enrich(context.Background(), request)
			}
			if tc.status == "success" && callErr != nil {
				t.Fatalf("success call failed: %v", callErr)
			}
			if tc.status != "success" && callErr == nil {
				t.Fatalf("%s call unexpectedly succeeded", tc.mode)
			}
			if err := host.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			evidence := host.IsolationEvidence()
			if evidence.SourceDelivery != "bounded_evidence_pack" || evidence.SourceMount != "not_mounted" || evidence.WorkingDirectoryMode != "process_private_disposable" || evidence.WorkingDirectoryPermission != "0700" || !evidence.Disposable || evidence.RepositoryPathExposed || evidence.RepositoryWriteCapability || len(evidence.RepositoryWriteAttempts) != 0 || evidence.DisposableWriteAttempt != "blocked" || evidence.PackDigest != request.PackDigest || evidence.ReceivedRequestID != request.RequestID || evidence.ReceivedPackDigest != request.PackDigest || evidence.TerminalStatus != tc.status || !evidence.CleanupVerified {
				t.Fatalf("observed lifecycle evidence = %+v", evidence)
			}
			if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
				t.Fatalf("disposable model host directory still exists after cleanup: %q err=%v", workDir, statErr)
			}
		})
	}
}

func TestSpawnModelHostKeepsInFlightPackStableAcrossConcurrentEdit(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=record", "CODEFLOW_MODEL_HOST_DELAY=50ms", "CODEFLOW_MODEL_HOST_RECORD=packs.log"},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	defer host.Close()
	source := "before"
	first := ModelHostRequest{SchemaID: ModelHostRequestSchemaID, SchemaVersion: 2, RequestID: "request-before", Operation: ModelHostEnrichMethod, EvidencePack: json.RawMessage(`{"content":"before"}`), PackDigest: "digest-before", TargetStepID: "step-live", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20}
	result := make(chan error, 1)
	go func() {
		_, err := host.Enrich(context.Background(), first)
		result <- err
	}()
	source = "after"
	if err := <-result; err != nil {
		t.Fatalf("first enrichment: %v", err)
	}
	second := first
	second.RequestID = "request-after"
	second.PackDigest = "digest-after"
	second.EvidencePack = json.RawMessage(`{"content":"` + source + `"}`)
	if _, err := host.Enrich(context.Background(), second); err != nil {
		t.Fatalf("second enrichment: %v", err)
	}
	data, err := host.ReadDisposableFile("packs.log")
	if err != nil {
		t.Fatalf("read recorded packs: %v", err)
	}
	if got := string(data); !strings.Contains(got, `{"content":"before"}`) || !strings.Contains(got, `{"content":"after"}`) || strings.Index(got, `{"content":"before"}`) > strings.Index(got, `{"content":"after"}`) {
		t.Fatalf("recorded packs did not preserve concurrent edit boundary: %q", got)
	}
}

func TestSpawnModelHost_UsesExplicitMinimalEnvironment(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	t.Setenv("CODEFLOW_PARENT_SECRET", "parent-secret-must-not-leak")
	t.Setenv("CODEFLOW_PARENT_REPO_ROOT", "/repository/root-must-not-leak")
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{
			"CODEFLOW_MODEL_HOST_HELPER=1",
			"CODEFLOW_MODEL_HOST_ENV_RECORD=environment.txt",
			"PATH=/usr/bin:/bin",
		},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	environment, err := host.ReadDisposableFile("environment.txt")
	if err != nil {
		t.Fatalf("read child environment: %v", err)
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	got := string(environment)
	for _, forbidden := range []string{"CODEFLOW_PARENT_SECRET", "parent-secret-must-not-leak", "CODEFLOW_PARENT_REPO_ROOT", "/repository/root-must-not-leak"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("child environment leaked %q: %q", forbidden, got)
		}
	}
	for _, key := range []string{"HOME", "PWD", "TMPDIR", "TMP", "TEMP", "CODEFLOW_ADAPTER_WORKDIR"} {
		value := environmentValue(got, key)
		if value == "" || !strings.Contains(value, "codeflow-model-host-") {
			t.Fatalf("child %s was not disposable: %q", key, value)
		}
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	unsafeRecord := t.TempDir() + "/should-not-exist"
	if _, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "HOME=" + repoRoot, "CODEFLOW_MODEL_HOST_ENV_RECORD=" + unsafeRecord},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	}); err == nil {
		t.Fatal("unsafe repository HOME was accepted")
	}
	if _, err := os.Stat(unsafeRecord); !os.IsNotExist(err) {
		t.Fatalf("unsafe environment spawned helper: stat error=%v", err)
	}
	if _, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "PATH=" + repoRoot + "/bin"},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	}); err == nil {
		t.Fatal("repository-root PATH was accepted")
	}
}

func TestSpawnModelHost_RejectsSecretBearingAllowlistedEnvironmentValue(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	recordPath := t.TempDir() + "/should-not-exist"
	_, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{
			"CODEFLOW_MODEL_HOST_HELPER=1",
			"CODEFLOW_MODEL_HOST_MODE=token=parent-secret",
			"CODEFLOW_MODEL_HOST_ENV_RECORD=" + recordPath,
		},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("secret-bearing allowlisted environment value was forwarded")
	}
	if _, statErr := os.Stat(recordPath); !os.IsNotExist(statErr) {
		t.Fatalf("secret-bearing environment reached child: stat error=%v", statErr)
	}
}

func TestSpawnModelHostRejectsCompositeRepositoryAndCredentialArgs(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(modelHostProbeSentinelPath()), "../.."))
	secretPath := filepath.Join(t.TempDir(), ".netrc")
	if err := os.WriteFile(secretPath, []byte("machine example login user password secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	credentialCases := []string{
		secretPath,
		filepath.Join(filepath.Dir(secretPath), ".env.production"),
		filepath.Join(filepath.Dir(secretPath), ".npmrc"),
		filepath.Join(filepath.Dir(secretPath), ".pypirc"),
		filepath.Join(filepath.Dir(secretPath), ".git-credentials"),
		filepath.Join(filepath.Dir(secretPath), "credentials.json"),
		filepath.Join(filepath.Dir(secretPath), ".docker", "config.json"),
		filepath.Join(filepath.Dir(secretPath), ".config", "gcloud", "application_default_credentials.json"),
		filepath.Join(filepath.Dir(secretPath), ".ssh", "id_rsa"),
		filepath.Join(filepath.Dir(secretPath), ".kube", "config"),
		filepath.Join(filepath.Dir(secretPath), "Library", "Keychains", "login.keychain-db"),
		filepath.Join(filepath.Dir(secretPath), ".gnupg", "private-keys-v1.d", "key"),
	}
	for _, path := range credentialCases {
		if _, err := SpawnModelHost(context.Background(), ModelHostConfig{
			BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper", "--config=" + path},
			Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
		}); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("credential-bearing composite path error = %v, want bad request: %q", err, path)
		}
	}
	if _, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper", "--api-token=top-secret"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("secret-bearing argument error = %v, want bad request", err)
	}
	repositoryArg := filepath.Join(repositoryRoot, "internal", "protocol", "testdata", "model-host-sentinel.txt")
	if _, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper", "--config=" + repositoryArg},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("repository-bearing composite argument error = %v, want bad request", err)
	}
	alias := filepath.Join(t.TempDir(), "repository-alias")
	if err := os.Symlink(repositoryRoot, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper", "--config=" + filepath.Join(alias, "internal", "protocol", "testdata", "model-host-sentinel.txt")},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("repository symlink composite argument error = %v, want bad request", err)
	}
}

func TestSpawnModelHostAllowsTokenizerModelPath(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	modelDir := t.TempDir()
	tokenizer := filepath.Join(modelDir, "tokenizer")
	if err := os.WriteFile(tokenizer, []byte("model tokenizer"), 0o600); err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(modelHostProbeSentinelPath()), "../.."))
	if err := validateModelHostArguments([]string{"--tokenizer=" + tokenizer}, repositoryRoot); err != nil {
		t.Fatalf("normal tokenizer argument was rejected: %v", err)
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, AllowedReadPaths: []string{tokenizer},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("normal tokenizer model path was rejected: %v", err)
	}
	_ = host.Close()
}

func TestSpawnModelHostRejectsCredentialStoreReadAllowlist(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	credentialDir := t.TempDir() + "/.aws"
	if err := os.Mkdir(credentialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(credentialDir, "credentials")
	if err := os.WriteFile(credentialPath, []byte("must-not-be-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, AllowedReadPaths: []string{credentialPath},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("credential-store read path was accepted")
	}
}

func TestSpawnModelHostRejectsRepositoryDisposableRoot(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	repositoryRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(repositoryRoot, ".git")); statErr == nil {
			break
		}
		parent := filepath.Dir(repositoryRoot)
		if parent == repositoryRoot {
			t.Skip("repository root is unavailable")
		}
		repositoryRoot = parent
	}
	_, err = SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: repositoryRoot,
		DefaultTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("repository disposable root was accepted")
	}
}

func TestSpawnModelHost_RejectsRequestAboveMeasuredCapabilityBeforeWrite(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	recordPath := t.TempDir() + "/request.log"
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:             []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MAX_REQUEST=256", "CODEFLOW_MODEL_HOST_RECORD=" + recordPath},
		MaxRequestBytes: 1 << 20, DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	defer host.Close()
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "request-measured-bound", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"content":"` + strings.Repeat("x", 220) + `"}`),
		PackDigest:   "digest-measured-bound", TargetStepID: "step-bound", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20,
	}
	if _, err := host.Enrich(context.Background(), request); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("oversized request error = %v, want measured-bound backpressure", err)
	}
	if _, err := os.Stat(recordPath); !os.IsNotExist(err) {
		t.Fatalf("oversized request reached host: stat error=%v", err)
	}
}

func TestSpawnModelHost_ReportsActualFilesystemIsolation(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec runtime evidence is Darwin-specific")
	}
	sentinelPath := modelHostProbeSentinelPath()
	sentinelBefore, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read isolation sentinel: %v", err)
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "request-isolation-probe", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"content":"probe"}`), PackDigest: "digest-isolation-probe",
		TargetStepID: "step-isolation-probe", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20,
	}
	if _, err := host.Enrich(context.Background(), request); err != nil {
		_ = host.Close()
		t.Fatalf("enrichment: %v", err)
	}
	if err := host.Close(); err != nil {
		t.Fatalf("close model host: %v", err)
	}
	evidence := host.IsolationEvidence()
	sentinelAfter, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read isolation sentinel after probe: %v", err)
	}
	if !bytes.Equal(sentinelBefore, sentinelAfter) {
		t.Fatalf("repository isolation sentinel changed during probe")
	}
	if evidence.IsolationBackend != "sandbox-exec" || evidence.EnforcementStatus != "enforced" || evidence.NetworkPolicy != "deny_all" || evidence.RepositoryReadAttempt != "blocked" || evidence.RepositoryWriteAttempt != "blocked" || evidence.DisposableWriteAttempt != "blocked" || evidence.NetworkAttempt != "blocked" || evidence.SentinelBeforeDigest == "" || evidence.SentinelBeforeDigest != evidence.SentinelAfterDigest || !evidence.SentinelUnchanged || !evidence.CoreTrustedProbe || evidence.RuntimePolicyBinding != ModelHostRuntimePolicyBindingExact || evidence.ProbeScope != ModelHostProbeScopeSharedDenyBase || !validModelHostPolicyDigest(evidence.ProbePolicyDigest) || !validModelHostPolicyDigest(evidence.ProbeSharedBaseDigest) {
		t.Fatalf("filesystem isolation was not actually observed: %+v", evidence)
	}
}

func TestSpawnModelHostRejectsRuntimeProfileMutationAfterIsolation(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec runtime profile binding is Darwin-specific")
	}
	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{
			name: "profile bytes",
			mutate: func(profilePath string) error {
				file, err := os.OpenFile(profilePath, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					return err
				}
				defer file.Close()
				_, err = file.WriteString("\n")
				return err
			},
		},
		{
			name: "profile identity",
			mutate: func(profilePath string) error {
				data, err := os.ReadFile(profilePath)
				if err != nil {
					return err
				}
				replacement := profilePath + ".replacement"
				if err := os.WriteFile(replacement, data, 0o600); err != nil {
					return err
				}
				if err := os.Remove(profilePath); err != nil {
					return err
				}
				return os.Rename(replacement, profilePath)
			},
		},
		{
			name: "profile symlink",
			mutate: func(profilePath string) error {
				data, err := os.ReadFile(profilePath)
				if err != nil {
					return err
				}
				target := profilePath + ".target"
				if err := os.WriteFile(target, data, 0o600); err != nil {
					return err
				}
				if err := os.Remove(profilePath); err != nil {
					return err
				}
				return os.Symlink(target, profilePath)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			disposableRoot := t.TempDir()
			originalHook := modelHostPostIsolationHook.Load().(func())
			originalWait := modelHostProcessWait
			helperPID := 0
			modelHostProcessWait = func(process *os.Process) (*os.ProcessState, error) {
				if process != nil {
					helperPID = process.Pid
				}
				return originalWait(process)
			}
			modelHostPostIsolationHook.Store(func() {
				entries, err := os.ReadDir(disposableRoot)
				if err != nil || len(entries) != 1 {
					return
				}
				profilePath := filepath.Join(disposableRoot, entries[0].Name(), "model-host.sb")
				_ = test.mutate(profilePath)
			})
			t.Cleanup(func() {
				modelHostPostIsolationHook.Store(originalHook)
				modelHostProcessWait = originalWait
			})

			host, err := SpawnModelHost(context.Background(), ModelHostConfig{
				BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
				Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DisposableRoot: disposableRoot, DefaultTimeout: time.Second,
			})
			if host != nil {
				_ = host.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "runtime sandbox profile") {
				t.Fatalf("runtime profile mutation result = host=%v err=%v, want fail-closed profile binding error", host != nil, err)
			}
			if helperPID != 0 {
				t.Fatalf("runtime profile mutation started a child process with pid %d", helperPID)
			}
			entries, readErr := os.ReadDir(disposableRoot)
			if readErr != nil {
				t.Fatalf("read disposable root: %v", readErr)
			}
			if len(entries) != 0 {
				t.Fatalf("disposable root entries = %v, want no leaked host directory", entries)
			}
		})
	}
}

func TestSpawnModelHost_RejectsNonAllowlistedExecutable(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=exec-probe"},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "request-exec-probe", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"content":"exec-probe"}`), PackDigest: "digest-exec-probe",
		TargetStepID: "step-exec-probe", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20,
	}
	if _, err := host.Enrich(context.Background(), request); err != nil {
		_ = host.Close()
		t.Fatalf("enrichment: %v", err)
	}
	record, err := host.ReadDisposableFile("exec-probe.txt")
	_ = host.Close()
	if err != nil {
		t.Fatalf("read executable probe: %v", err)
	}
	for _, executable := range []string{"/bin/sh", "/usr/bin/head", "/usr/bin/nc", "/usr/bin/true"} {
		if !strings.Contains(string(record), executable+"=blocked") {
			t.Fatalf("runtime sandbox allowed non-allowlisted executable %q: %q", executable, record)
		}
	}
}

func TestSpawnModelHost_RejectsResponseAboveMeasuredCapabilityBeforeDecode(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:              []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=oversized-response", "CODEFLOW_MODEL_HOST_MAX_RESPONSE=256"},
		MaxResponseBytes: 1 << 20, DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	defer host.Close()
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "request-response-bound", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"content":"bounded"}`), PackDigest: "digest-response-bound",
		TargetStepID: "step-response-bound", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20,
	}
	if _, err := host.Enrich(context.Background(), request); !errors.Is(err, ErrCrashed) {
		t.Fatalf("oversized response error = %v, want crash before decoding", err)
	}
}

func TestSpawnModelHostRejectsResponseWithoutChildReceipt(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=no-receipt"},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	request := ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: "request-no-receipt", Operation: ModelHostEnrichMethod,
		EvidencePack: json.RawMessage(`{"content":"no-receipt"}`), PackDigest: "digest-no-receipt",
		TargetStepID: "step-no-receipt", TargetSymbolPath: "Submit", MaxResponseBytes: 1 << 20,
	}
	_, err = host.Enrich(context.Background(), request)
	_ = host.Close()
	if !errors.Is(err, ErrCrashed) {
		t.Fatalf("response without child receipt error = %v, want crash", err)
	}
	if evidence := host.IsolationEvidence(); evidence.ReceivedRequestID != "" || evidence.ReceivedPackDigest != "" {
		t.Fatalf("missing receipt produced received evidence: %+v", evidence)
	}
}

func TestSpawnModelHost_FailsClosedWithoutIsolationProbe(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	_, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=no-isolation-probe"},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("missing isolation probe error = %v, want unsupported", err)
	}
}

func TestSpawnModelHostUsesCoreTrustedProbeWhenChildProbeIsSpoofed(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=1", "CODEFLOW_MODEL_HOST_MODE=spoof-isolation-probe"},
		DisposableRoot: t.TempDir(), DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	defer host.Close()
	evidence := host.IsolationEvidence()
	if evidence.SentinelBeforeDigest == "sha256:spoofed" || evidence.SentinelAfterDigest == "sha256:spoofed" || evidence.SentinelBeforeDigest == "" || evidence.SentinelBeforeDigest != evidence.SentinelAfterDigest || !evidence.SentinelUnchanged {
		t.Fatalf("child probe replaced Core trusted observation: %+v", evidence)
	}
}

func environmentValue(environment, key string) string {
	for _, entry := range strings.Split(environment, "\n") {
		name, value, ok := strings.Cut(entry, "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

const modelHostProbeSentinelContents = "codeflow-model-host-sentinel-v1\n"

func modelHostProbeSentinelPath() string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	return filepath.Join(repoRoot, "internal", "protocol", "testdata", "model-host-sentinel.txt")
}

func modelHostFilesystemProbe() ModelHostIsolationProbe {
	path := modelHostProbeSentinelPath()
	expected := []byte(modelHostProbeSentinelContents)
	beforeBytes := expected
	readAttempt := "blocked"
	if data, err := os.ReadFile(path); err == nil {
		beforeBytes = data
		readAttempt = "allowed"
	}
	beforeSum := sha256.Sum256(beforeBytes)
	probe := ModelHostIsolationProbe{RepositoryReadAttempt: readAttempt, SentinelBeforeDigest: "sha256:" + hex.EncodeToString(beforeSum[:])}
	writeAttempt := "blocked"
	writePath := filepath.Join("/tmp", "codeflow-model-host-probe-write-"+strconv.Itoa(os.Getpid()))
	if file, err := os.OpenFile(writePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		writeAttempt = "allowed"
		_ = file.Close()
		_ = os.Remove(writePath)
	}
	probe.RepositoryWriteAttempt = writeAttempt
	probe.NetworkAttempt = "blocked"
	listener, listenErr := net.Listen("tcp4", "127.0.0.1:0")
	if listenErr == nil {
		defer listener.Close()
		connection, dialErr := net.DialTimeout("tcp4", listener.Addr().String(), 50*time.Millisecond)
		if dialErr == nil {
			probe.NetworkAttempt = "allowed"
			_ = connection.Close()
		}
	}
	afterBytes := beforeBytes
	if data, err := os.ReadFile(path); err == nil {
		afterBytes = data
	}
	afterSum := sha256.Sum256(afterBytes)
	probe.SentinelAfterDigest = "sha256:" + hex.EncodeToString(afterSum[:])
	probe.SentinelUnchanged = probe.SentinelBeforeDigest == probe.SentinelAfterDigest
	return probe
}

func TestValidateModelHostIsolationEvidence_RejectsRepositoryCapability(t *testing.T) {
	valid := ModelHostIsolationEvidence{SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable", Disposable: true, PackDigest: "digest"}
	if err := ValidateModelHostIsolationEvidence(valid); err != nil {
		t.Fatal(err)
	}
	valid.RepositoryWriteCapability = true
	if err := ValidateModelHostIsolationEvidence(valid); err == nil {
		t.Fatal("repository write capability was accepted")
	}
}

func TestValidateModelHostIsolationEvidenceRequiresRuntimeRepositoryWriteAuditStatus(t *testing.T) {
	evidence := ModelHostIsolationEvidence{
		SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable",
		Disposable: true, PackDigest: "digest", IsolationBackend: "sandbox-exec", CoreTrustedProbe: true,
	}
	if err := ValidateModelHostIsolationEvidence(evidence); err == nil || !strings.Contains(err.Error(), "repository-write audit status") {
		t.Fatalf("missing runtime repository-write audit status was accepted: %v", err)
	}
	evidence.RepositoryWriteAuditStatus = ModelHostRepositoryWriteAuditIndeterminate
	if err := ValidateModelHostIsolationEvidence(evidence); err == nil || !strings.Contains(err.Error(), "indeterminate") {
		t.Fatalf("indeterminate runtime repository-write audit was accepted: %v", err)
	}
}

func TestModelHostRepositoryWriteAttemptsIdentifyRepositoryProbe(t *testing.T) {
	attempts := modelHostRepositoryWriteAttempts(ModelHostIsolationProbe{RepositoryWriteAttempt: "allowed"})
	if len(attempts) != 1 || attempts[0] != modelHostRepositoryWriteCapabilityViolation {
		t.Fatalf("repository write audit = %v, want %q", attempts, modelHostRepositoryWriteCapabilityViolation)
	}
}

func TestModelHostEvidenceAccessorsDeepCopyResourceLimits(t *testing.T) {
	limits := DefaultModelHostResourceLimits()
	applied := limits
	applied.ProcessCount = 1
	resourceEvidence := &ModelHostResourceLimitEvidence{
		Version: ModelHostResourceLimitsVersion, Declared: limits, Applied: applied,
		EnforcementStatus: ModelHostResourceEnforcementEnforced,
		Backend:           ModelHostResourceBackendDarwinHostTree,
	}
	probe := &ModelHostIsolationProbe{RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", SentinelBeforeDigest: "sha256:" + strings.Repeat("a", 64), SentinelAfterDigest: "sha256:" + strings.Repeat("a", 64), SentinelUnchanged: true, NetworkAttempt: "blocked"}
	host := &ModelHost{
		cap:      ModelHostCapability{Capabilities: []string{"semantic_proposal"}, IsolationProbe: probe, ResourceLimits: resourceEvidence},
		evidence: ModelHostIsolationEvidence{ResourceLimits: resourceEvidence},
	}
	capability := host.Capability()
	capability.Capabilities[0] = "tampered"
	capability.IsolationProbe.RepositoryReadAttempt = "allowed"
	capability.ResourceLimits.Applied.MemoryBytes++
	isolation := host.IsolationEvidence()
	isolation.ResourceLimits.Applied.ProcessCount++
	if host.cap.Capabilities[0] != "semantic_proposal" || host.cap.IsolationProbe.RepositoryReadAttempt != "blocked" || host.cap.ResourceLimits.Applied != applied || host.evidence.ResourceLimits.Applied != applied {
		t.Fatalf("evidence accessor exposed mutable nested state: capability=%+v evidence=%+v", host.cap, host.evidence)
	}
}

func TestModelHostCloseReportsInjectedTerminationAndWaitFailures(t *testing.T) {
	originalTerminate := modelHostTerminateProcessGroup
	originalWait := modelHostProcessWait
	originalTimeout := modelHostLifecycleWaitTimeout
	t.Cleanup(func() {
		modelHostTerminateProcessGroup = originalTerminate
		modelHostProcessWait = originalWait
		modelHostLifecycleWaitTimeout = originalTimeout
	})
	modelHostTerminateProcessGroup = func(*exec.Cmd) error {
		return errors.New("injected process-group termination failure")
	}
	modelHostProcessWait = func(*os.Process) (*os.ProcessState, error) {
		return nil, errors.New("injected Process.Wait failure")
	}
	modelHostLifecycleWaitTimeout = time.Millisecond
	host := &ModelHost{
		cmd:        &exec.Cmd{Process: &os.Process{Pid: 424242}},
		waitDone:   make(chan struct{}),
		readerDone: make(chan struct{}),
		readerStop: make(chan struct{}),
		memoryDone: make(chan struct{}),
		memoryStop: make(chan struct{}),
		workDir:    t.TempDir(),
	}
	close(host.readerDone)
	close(host.memoryDone)
	go host.reapProcess()
	err := host.Close()
	if err == nil {
		t.Fatal("Close unexpectedly hid injected termination and wait failures")
	}
	if !strings.Contains(err.Error(), "injected process-group termination failure") || !strings.Contains(err.Error(), "injected Process.Wait failure") {
		t.Fatalf("Close error = %v, want termination and Process.Wait failures", err)
	}
	if evidence := host.IsolationEvidence(); evidence.CleanupVerified {
		t.Fatalf("cleanup was reported verified after lifecycle failures: %+v", evidence)
	}
}

func TestModelHostHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") != "1" {
		return
	}
	if recordPath := os.Getenv("CODEFLOW_MODEL_HOST_ENV_RECORD"); recordPath != "" {
		if err := os.WriteFile(recordPath, []byte(strings.Join(os.Environ(), "\n")), 0o600); err != nil {
			return
		}
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		body, err := readNextFrame(reader, DefaultMaxMessageSizeBytes)
		if err != nil {
			if err == io.EOF {
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
			maxRequestBytes := int64(DefaultMaxMessageSizeBytes)
			if raw := os.Getenv("CODEFLOW_MODEL_HOST_MAX_REQUEST"); raw != "" {
				if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
					maxRequestBytes = parsed
				}
			}
			maxResponseBytes := int64(DefaultMaxMessageSizeBytes)
			if raw := os.Getenv("CODEFLOW_MODEL_HOST_MAX_RESPONSE"); raw != "" {
				if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
					maxResponseBytes = parsed
				}
			}
			capability := ModelHostCapability{Status: "measured", ModelID: "fake-model", Revision: "r1", License: "MIT", Checksum: "sha256:fake", Runtime: "test", DataBoundary: "local", Measured: true, SchemaConstrained: true, Cancellation: true, MaxRequestBytes: maxRequestBytes, MaxResponseBytes: maxResponseBytes}
			mode := os.Getenv("CODEFLOW_MODEL_HOST_MODE")
			if mode != "no-isolation-probe" {
				probe := modelHostFilesystemProbe()
				if mode == "spoof-isolation-probe" {
					probe = ModelHostIsolationProbe{
						RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked",
						SentinelBeforeDigest: "sha256:spoofed", SentinelAfterDigest: "sha256:spoofed",
						SentinelUnchanged: true, NetworkAttempt: "blocked",
					}
				}
				capability.IsolationBackend = "sandbox-exec"
				capability.IsolationEnforced = true
				capability.IsolationProbe = &probe
				capability.NetworkPolicy = "deny_all"
			}
			response := ModelHostResponse{SchemaID: ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: request.ID, Status: "ok", Capability: capability}
			if strings.HasPrefix(mode, "initialize-") {
				rawResponse, err := json.Marshal(response)
				if err != nil {
					return
				}
				var object map[string]any
				if json.Unmarshal(rawResponse, &object) != nil {
					return
				}
				switch mode {
				case "initialize-wrong-schema-id":
					object["schemaId"] = "https://codeflow.local/schemas/unknown.schema.json"
				case "initialize-missing-schema-id":
					delete(object, "schemaId")
				case "initialize-wrong-schema-version":
					object["schemaVersion"] = 1
				case "initialize-missing-schema-version":
					delete(object, "schemaVersion")
				case "initialize-empty-request-id":
					object["requestId"] = ""
				case "initialize-mismatched-request-id":
					object["requestId"] = "different-request-id"
				case "initialize-non-ok-status":
					object["status"] = "rejected"
				case "initialize-missing-capability":
					delete(object, "capability")
				case "initialize-unknown-top-level":
					object["unexpected"] = "unexpected"
				case "initialize-unknown-capability":
					if capabilityObject, ok := object["capability"].(map[string]any); ok {
						capabilityObject["unexpected"] = "unexpected"
					}
				case "initialize-out-of-bound-capability":
					if capabilityObject, ok := object["capability"].(map[string]any); ok {
						capabilityObject["maxRequestBytes"] = 10485761
					}
				}
				mutatedResponse, err := json.Marshal(object)
				if err != nil {
					return
				}
				result = json.RawMessage(mutatedResponse)
			} else {
				result = response
			}
		case ModelHostEnrichMethod:
			var params ModelHostRequest
			if json.Unmarshal(request.Params, &params) != nil || strings.Contains(string(params.EvidencePack), "repoRoot") {
				return
			}
			mode := os.Getenv("CODEFLOW_MODEL_HOST_MODE")
			if mode != "no-receipt" && mode != "no-receipt-timeout" && mode != "no-receipt-cancel" {
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
			}
			if recordPath := os.Getenv("CODEFLOW_MODEL_HOST_RECORD"); recordPath != "" {
				file, err := os.OpenFile(recordPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					return
				}
				_, _ = file.Write(append(append([]byte(nil), params.EvidencePack...), '\n'))
				_ = file.Close()
			}
			if mode == "exec-probe" {
				probes := []struct {
					path string
					args []string
				}{
					{path: "/bin/sh", args: []string{"-c", "exit 0"}},
					{path: "/usr/bin/head", args: []string{"-c", "0", "/dev/null"}},
					{path: "/usr/bin/nc", args: []string{"-h"}},
					{path: "/usr/bin/true"},
				}
				var outcomes []string
				for _, probe := range probes {
					command := exec.Command(probe.path, probe.args...)
					command.Stdout = io.Discard
					command.Stderr = io.Discard
					if err := command.Start(); err != nil {
						outcomes = append(outcomes, probe.path+"=blocked")
						continue
					}
					_ = command.Wait()
					outcomes = append(outcomes, probe.path+"=allowed")
				}
				_ = os.WriteFile("exec-probe.txt", []byte(strings.Join(outcomes, "\n")), 0o600)
			}
			if mode == "crash" {
				os.Exit(17)
			}
			if delay := os.Getenv("CODEFLOW_MODEL_HOST_DELAY"); delay != "" {
				if duration, err := time.ParseDuration(delay); err == nil {
					time.Sleep(duration)
				}
			}
			if mode == "timeout" || mode == "cancel" || mode == "no-receipt-timeout" || mode == "no-receipt-cancel" {
				continue
			}
			if mode == "failure" {
				response, _ := json.Marshal(struct {
					JSONRPC string `json:"jsonrpc"`
					ID      string `json:"id"`
					Error   any    `json:"error"`
				}{JSONRPCVersion, request.ID, map[string]any{"code": -32001, "message": "fake failure"}})
				if writeFrameBounded(os.Stdout, response, DefaultMaxMessageSizeBytes) != nil {
					return
				}
				continue
			}
			if mode == "oversized-response" {
				result = ModelHostResponse{SchemaID: ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: request.ID, Status: "accepted", Proposal: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json","schemaVersion":2,"payload":"` + strings.Repeat("x", 512) + `"}`)}
			} else {
				result = ModelHostResponse{SchemaID: ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: request.ID, Status: "accepted", Proposal: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json","schemaVersion":2}`)}
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
