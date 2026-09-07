package flowview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func TestSemanticEnrichmentEndpoint_ReturnsExplicitUnavailableWithoutDeterministicMap(t *testing.T) {
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), Port: 0, AuthToken: "enrichment-token"})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/enrich?token="+srv.AuthToken(), bytes.NewBufferString(`{"targetStepId":"step-1"}`))
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var response struct {
		State    semanticEnrichmentEndpointState `json:"state"`
		Pack     json.RawMessage                 `json:"pack"`
		Proposal json.RawMessage                 `json:"proposal"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.State.Status != "unavailable" {
		t.Fatalf("status = %q, want unavailable", response.State.Status)
	}
	if response.State.CapabilityStatus != "unavailable" {
		t.Fatalf("capability status = %q, want unavailable", response.State.CapabilityStatus)
	}
	if string(response.Pack) != "null" || string(response.Proposal) != "null" {
		t.Fatalf("unavailable response exposed enrichment artifacts: pack=%s proposal=%s", response.Pack, response.Proposal)
	}
}

func TestSemanticEnrichmentEndpointUnavailableDoesNotPersistProposal(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "enrichment-token"})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/enrich?token="+srv.AuthToken(), bytes.NewBufferString(`{"generationId":"generation-unavailable","computedBasisId":"basis-unavailable","targetStepId":"step-unavailable"}`))
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		State semanticEnrichmentEndpointState `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.State.Status != "unavailable" {
		t.Fatalf("status = %q, want unavailable", response.State.Status)
	}
	_, err = semantic.LoadProposalForApproval(context.Background(), semantic.NewDurableProposalStore(root), srv.approvalWorkspaceID, "proposal-unavailable", "pack-unavailable")
	if !errors.Is(err, semantic.ErrProposalNotFound) {
		t.Fatalf("unavailable proposal lookup error = %v, want ErrProposalNotFound", err)
	}
}

func TestSemanticEnrichmentEndpointPersistenceFailureIsNotAvailable(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const generation = "generation-flowview-persist-failure"
	const stepID = "step-live-delta"
	disposableRoot := t.TempDir()
	var srv *Server
	var hostsMu sync.Mutex
	var hosts []*protocol.ModelHost
	factory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		mapIR := srv.cachedSemanticMap(generation, "")
		if mapIR == nil {
			return nil, errors.New("flowview persistence fixture map is unavailable")
		}
		digests, err := semantic.CanonicalQ3Digests(mapIR)
		if err != nil {
			return nil, err
		}
		host, err := protocol.SpawnModelHost(ctx, protocol.ModelHostConfig{
			BinPath: os.Args[0], Args: []string{"-test.run=TestFlowViewModelHostHelper"}, DisposableRoot: disposableRoot,
			Env: []string{
				"CODEFLOW_MODEL_HOST_HELPER=flowview",
				"CODEFLOW_VS08_MODEL_HOST_RELEASE=" + strings.Join([]string{digests.Fact, digests.Obligation, digests.Alignment, digests.Settlement}, "|"),
			},
			DefaultTimeout: 3 * time.Second,
		})
		if err == nil {
			hostsMu.Lock()
			hosts = append(hosts, host)
			hostsMu.Unlock()
		}
		return host, err
	})
	var err error
	srv, err = NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "flowview-persistence-token", ModelHostFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	query := &semantic.TaskViewQuery{SchemaID: semantic.FeatureQuerySchemaID, SchemaVersion: 2, Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: "show main"}}
	if err := srv.RememberTaskQuery(query, query.Feature.Request); err != nil {
		t.Fatal(err)
	}
	srv.compileCandidate = func(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
		mapIR, projection, payload, target, intent, closure, err := liveDeltaCandidate(ctx, snapshot, query, generation)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		mapIR.Freshness = "current"
		return mapIR, projection, payload, target, intent, closure, nil
	}
	_, snapshot, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.processCheckpoint(context.Background(), snapshot); err != nil {
		t.Fatalf("publish fixture: %v", err)
	}
	if srv.cachedSemanticMap(generation, "") == nil {
		t.Fatal("published fixture did not populate deterministic map cache")
	}
	failingStore := &flowViewFailingProposalStore{}
	srv.proposalStore = failingStore
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/enrich?token="+srv.AuthToken(), bytes.NewBufferString(`{"generationId":"generation-flowview-persist-failure","targetStepId":"step-live-delta","promptRevision":"prompt-flowview-persistence-error"}`))
	recorder := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s, want persistence failure", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"enrichment_persistence_failed"`) || !strings.Contains(recorder.Body.String(), "available semantic enrichment could not be persisted") {
		t.Fatalf("persistence failure response = %s, want stable generic error", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "flowview-persistence-canary") || strings.Contains(recorder.Body.String(), "HOME/private") || strings.Contains(recorder.Body.String(), `"status":"available"`) || strings.Contains(recorder.Body.String(), `"proposal"`) || strings.Contains(recorder.Body.String(), `"pack"`) {
		t.Fatalf("persistence failure response leaked success or storage details: %s", recorder.Body.String())
	}
	if got := failingStore.calls.Load(); got != 1 {
		t.Fatalf("proposal store calls = %d, want 1 after actual available result", got)
	}
	hostsMu.Lock()
	defer hostsMu.Unlock()
	if len(hosts) != 1 || hosts[0] == nil {
		t.Fatalf("supervised hosts = %d, want one actual host", len(hosts))
	}
	if !hosts[0].IsolationEvidence().CleanupVerified {
		t.Fatalf("actual host cleanup evidence = %+v", hosts[0].IsolationEvidence())
	}
	if entries, err := os.ReadDir(disposableRoot); err != nil {
		t.Fatalf("read disposable root: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("request host workdirs remain: %v", entries)
	}
}

type flowViewFailingProposalStore struct {
	calls atomic.Int32
}

func (s *flowViewFailingProposalStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	s.calls.Add(1)
	return errors.New("flowview-persistence-canary HOME/private")
}

func (*flowViewFailingProposalStore) Load(context.Context, string, string, string) (*semantic.StoredProposal, error) {
	return nil, &semantic.ProposalNotFoundError{}
}

func TestSemanticEnrichmentEndpoint_RejectsUnknownRequestFields(t *testing.T) {
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), Port: 0, AuthToken: "enrichment-token"})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/enrich?token="+srv.AuthToken(), bytes.NewBufferString(`{"repositoryPath":"/tmp/escape"}`))
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestWriteSemanticEnrichmentRejectsRedactionOfRequiredIdentity(t *testing.T) {
	result := semantic.EnrichmentResult{State: semantic.EnrichmentState{
		SchemaID: semantic.EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "unavailable",
		ProposalID: "token:required-identity", Capability: protocol.ModelHostCapability{Status: "unavailable"},
		UpdatedAt: "2026-09-05T00:00:00Z",
	}}
	recorder := httptest.NewRecorder()
	writeSemanticEnrichment(recorder, result)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s, want redaction validation failure", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("token:required-identity")) {
		t.Fatalf("pre-redaction identity leaked in error body: %s", recorder.Body.String())
	}
}

func TestWriteSemanticEnrichmentRejectsCompleteUnattestedAvailableValue(t *testing.T) {
	result := completeUnattestedAvailableResult(t)
	if err := semantic.ValidateEnrichmentState(result.State); err != nil {
		t.Fatalf("complete spoof state failed value validation before attestation: %v", err)
	}
	recorder := httptest.NewRecorder()
	writeSemanticEnrichment(recorder, result)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s, want trust-boundary failure", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Core-supervised host authority") {
		t.Fatalf("egress failed for a reason other than missing Core authority: %s", recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte(`"status":"available"`)) {
		t.Fatalf("unattested available state reached FlowView egress: %s", recorder.Body.String())
	}
}

func completeUnattestedAvailableResult(t *testing.T) semantic.EnrichmentResult {
	t.Helper()
	const (
		content    = "func Submit() {}"
		packDigest = ""
	)
	contentSum := sha256.Sum256([]byte(content))
	pack := &semantic.EvidencePack{
		SchemaID: semantic.EvidencePackV2SchemaID, SchemaVersion: 2,
		TargetSymbolPath: "Submit", ComputedBasisID: "basis-f22", GenerationID: "generation-f22",
		RepositoryID: "repo-f22", WorktreeID: "worktree-f22", SnapshotID: "snapshot-f22", WorkspaceEpoch: 1,
		TargetStepIDs: []string{"step-submit"}, ScopePaths: []string{"internal"}, RedactionStatus: "clean",
		Items:      []semantic.EvidenceItem{{EvidenceID: "e-submit", Kind: "source", Source: "internal/checkout.go", Content: content, Verified: true, SnapshotID: "snapshot-f22", ComputedBasisID: "basis-f22", ContentDigest: hex.EncodeToString(contentSum[:]), ByteRange: [2]int{0, len(content)}}},
		PackDigest: packDigest,
	}
	pack.EvidencePackID = testDeterministicPackID(pack)
	pack.PackDigest = testPackDigest(pack)
	if err := semantic.ValidateEvidencePackV2(pack); err != nil {
		t.Fatalf("test evidence pack is not complete: %v", err)
	}
	sentinel := "sha256:" + strings.Repeat("b", 64)
	capability := protocol.ModelHostCapability{
		Status: "measured", Measured: true, SchemaConstrained: true, Cancellation: true,
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, ModelID: "spoof", Revision: "r1", License: "MIT", Checksum: "sha256:spoof", Runtime: "test", DataBoundary: "bounded-pack",
		IsolationBackend: "fake-sandbox", IsolationEnforced: true, NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("a", 64),
		ResourceLimits: testModelHostResourceLimitEvidence(),
		IsolationProbe: &protocol.ModelHostIsolationProbe{
			RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", DisposableWriteAttempt: "blocked",
			SentinelBeforeDigest: sentinel, SentinelAfterDigest: sentinel, SentinelUnchanged: true, NetworkAttempt: "blocked",
		},
	}
	proposal := &semantic.ModelProposal{
		SchemaID: semantic.SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-f22",
		ComputedBasisID: pack.ComputedBasisID, GenerationID: pack.GenerationID, SnapshotID: pack.SnapshotID,
		TargetStepID: "step-submit", TargetSymbolPath: pack.TargetSymbolPath, ProposedTitle: "Submit checkout", ProposedCategory: "entry",
		EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only", ModelID: capability.ModelID, ModelRevision: capability.Revision,
		PromptRevision: "prompt-f22", SchemaProfile: semantic.SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-submit"}, FactDigest: strings.Repeat("0", 64), ObligationDigest: strings.Repeat("0", 64), AlignmentDigest: strings.Repeat("0", 64), SettlementDigest: strings.Repeat("0", 64),
	}
	requestID := "request-f22"
	return semantic.EnrichmentResult{
		State: semantic.EnrichmentState{
			SchemaID: semantic.EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "available",
			ProposalID: proposal.ProposalID, PackDigest: pack.PackDigest, UpdatedAt: "2026-09-05T00:00:00Z",
			Capability: capability,
			Isolation: protocol.ModelHostIsolationEvidence{
				SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable", WorkingDirectoryPermission: "0700", Disposable: true,
				RepositoryPathExposed: false, RepositoryWriteCapability: false, RepositoryWriteAttempts: []string{}, RepositoryWriteAuditStatus: protocol.ModelHostRepositoryWriteAuditCapabilityEnforced, PackDigest: pack.PackDigest, ReceivedRequestID: requestID, ReceivedPackDigest: pack.PackDigest, CapabilityStatus: "measured", TerminalStatus: "success", CleanupVerified: true,
				IsolationBackend: "fake-sandbox", EnforcementStatus: "enforced", RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", DisposableWriteAttempt: "blocked",
				SentinelBeforeDigest: sentinel, SentinelAfterDigest: sentinel, SentinelUnchanged: true, NetworkAttempt: "blocked", NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("a", 64), CoreTrustedProbe: true,
				ResourceLimits: testModelHostResourceLimitEvidence(),
			},
		},
		Pack: pack, Proposal: proposal,
	}
}

func testModelHostResourceLimitEvidence() *protocol.ModelHostResourceLimitEvidence {
	limits := protocol.DefaultModelHostResourceLimits()
	applied := limits
	applied.ProcessCount = 1
	return &protocol.ModelHostResourceLimitEvidence{
		Version: protocol.ModelHostResourceLimitsVersion, Declared: limits, Applied: applied,
		EnforcementStatus: protocol.ModelHostResourceEnforcementEnforced,
		Backend:           protocol.ModelHostResourceBackendDarwinHostTree,
	}
}

func testDeterministicPackID(pack *semantic.EvidencePack) string {
	copy := *pack
	copy.EvidencePackID = ""
	copy.PackDigest = ""
	sum := sha256.Sum256(mustMarshalTestJSON(&copy))
	return "pack-v2-" + hex.EncodeToString(sum[:])[:24]
}

func testPackDigest(pack *semantic.EvidencePack) string {
	copy := *pack
	copy.PackDigest = ""
	sum := sha256.Sum256(mustMarshalTestJSON(&copy))
	return hex.EncodeToString(sum[:])
}

func mustMarshalTestJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

type semanticEnrichmentEndpointState struct {
	Status           string `json:"status"`
	CapabilityStatus string `json:"-"`
}

func (s *semanticEnrichmentEndpointState) UnmarshalJSON(data []byte) error {
	var raw struct {
		Status     string `json:"status"`
		Capability struct {
			Status string `json:"status"`
		} `json:"capability"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Status = raw.Status
	s.CapabilityStatus = raw.Capability.Status
	return nil
}
