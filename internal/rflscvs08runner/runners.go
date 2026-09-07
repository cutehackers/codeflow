// Package rflscvs08runner contains executable acceptance runners for the R2
// VS-08 semantic enrichment contract. Every runner calls a production public
// seam and returns immutable references that the contract harness records.
package rflscvs08runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

const (
	implementationPackage = "codeflow/internal/rflscvs08runner"
	basisID               = "basis-vs08"
	generationID          = "generation-vs08"

	// Acceptance hosts need enough bounded time for process startup and the
	// Core isolation probe under load. The timeout mode uses an explicit,
	// shorter request context below so this startup margin cannot weaken its
	// lifecycle assertion.
	modelHostAcceptanceDefaultTimeout = 5 * time.Second
	modelHostAcceptanceTimeout        = 120 * time.Millisecond
)

// Evidence is the immutable result returned by one acceptance runner.
type Evidence struct {
	Criterion                   string
	ImplementationTestID        string
	ImplementationPackage       string
	SnapshotID                  string
	ComputedBasisID             string
	GenerationID                string
	PackDigest                  string
	ModelHostAuditApplicability ModelHostAuditApplicability `json:"modelHostAuditApplicability"`
	ModelHostOutcome            string                      `json:"modelHostOutcome"`
	MountAudit                  protocol.ModelHostIsolationEvidence
	CapabilityAudit             protocol.ModelHostCapability
	RepositoryWriteAudit        []string
	ObjectRefs                  []string
	ModelHostLifecycle          []ModelHostObservation
	ConcurrentPackAudit         *ConcurrentPackEditAudit `json:"concurrentPackAudit,omitempty"`
	ConcurrentPackEditVerified  bool                     `json:"concurrentPackEditVerified,omitempty"`
}

// ModelHostAuditApplicability states whether a criterion's acceptance result
// includes a Core-observed supervised model-host execution. Criteria that do
// not exercise a host must carry no host audit, rather than inheriting an
// unrelated successful observation.
type ModelHostAuditApplicability string

const (
	ModelHostAuditApplicable    ModelHostAuditApplicability = "applicable"
	ModelHostAuditNotApplicable ModelHostAuditApplicability = "not_applicable"
)

type modelHostAuditExpectation struct {
	applicability ModelHostAuditApplicability
	outcome       string
}

var modelHostAuditExpectations = map[string]modelHostAuditExpectation{
	"VS08-A1":  {applicability: ModelHostAuditNotApplicable, outcome: "not_applicable"},
	"VS08-A2":  {applicability: ModelHostAuditNotApplicable, outcome: "not_applicable"},
	"VS08-A3":  {applicability: ModelHostAuditApplicable, outcome: "observed"},
	"VS08-A4":  {applicability: ModelHostAuditNotApplicable, outcome: "unavailable"},
	"VS08-A5":  {applicability: ModelHostAuditNotApplicable, outcome: "not_applicable"},
	"VS08-A6":  {applicability: ModelHostAuditNotApplicable, outcome: "not_applicable"},
	"VS08-A7":  {applicability: ModelHostAuditApplicable, outcome: "timed_out"},
	"VS08-A8":  {applicability: ModelHostAuditNotApplicable, outcome: "not_applicable"},
	"VS08-A9":  {applicability: ModelHostAuditNotApplicable, outcome: "not_applicable"},
	"VS08-A10": {applicability: ModelHostAuditApplicable, outcome: "observed"},
}

// ModelHostAuditExpectationForCriterion exposes the immutable acceptance
// applicability contract to registry consumers. The returned boolean is
// false for an unknown criterion.
func ModelHostAuditExpectationForCriterion(criterion string) (ModelHostAuditApplicability, string, bool) {
	expectation, ok := modelHostAuditExpectations[criterion]
	if !ok {
		return "", "", false
	}
	return expectation.applicability, expectation.outcome, true
}

// ModelHostObservation records what the production host boundary reported for
// one supervised terminal mode. Applicable criteria use these observations,
// rather than a caller-constructed isolation claim, for their lifecycle audit.
type ModelHostObservation struct {
	Applicability ModelHostAuditApplicability         `json:"applicability"`
	Mode          string                              `json:"mode"`
	RequestID     string                              `json:"requestId"`
	Error         string                              `json:"error,omitempty"`
	ErrorClass    string                              `json:"errorClass,omitempty"`
	Isolation     protocol.ModelHostIsolationEvidence `json:"isolation"`
	Capability    protocol.ModelHostCapability        `json:"capability"`
}

// ConcurrentPackExecutionEvidence records one independently supervised host
// execution used to prove that a live edit is reflected only in a later pack.
// HostIdentity is issued by protocol.Core, never reported by the child.
type ConcurrentPackExecutionEvidence struct {
	HostIdentity      string                              `json:"hostIdentity"`
	RequestID         string                              `json:"requestId"`
	SnapshotID        string                              `json:"snapshotId"`
	ComputedBasisID   string                              `json:"computedBasisId"`
	GenerationID      string                              `json:"generationId"`
	EvidencePackID    string                              `json:"evidencePackId"`
	PackDigest        string                              `json:"packDigest"`
	PackContentDigest string                              `json:"packContentDigest"`
	Isolation         protocol.ModelHostIsolationEvidence `json:"isolation"`
	Capability        protocol.ModelHostCapability        `json:"capability"`
	Error             string                              `json:"error,omitempty"`
	ErrorClass        string                              `json:"errorClass,omitempty"`
	// These authority-bearing bindings are intentionally private and therefore
	// absent from JSON. Validators require them to be present in the live Core
	// process, so persisted or fabricated executions fail closed.
	host        *protocol.ModelHost
	attestation *protocol.CoreHostAttestation
}

// ConcurrentPackEditAudit is the structured, Core-observed proof for the
// A10 live-edit check. The compatibility bool in Evidence is derived from
// this audit and is never sufficient on its own.
type ConcurrentPackEditAudit struct {
	First                        ConcurrentPackExecutionEvidence `json:"first"`
	Later                        ConcurrentPackExecutionEvidence `json:"later"`
	SnapshotIdentityDiffers      bool                            `json:"snapshotIdentityDiffers"`
	ComputedBasisIdentityDiffers bool                            `json:"computedBasisIdentityDiffers"`
	GenerationIdentityDiffers    bool                            `json:"generationIdentityDiffers"`
	PackIdentityDiffers          bool                            `json:"packIdentityDiffers"`
	PackDigestDiffers            bool                            `json:"packDigestDiffers"`
	PackContentDiffers           bool                            `json:"packContentDiffers"`
}

// Verified reports only the structural identity differences represented in
// the audit. Full host, receipt, policy, resource, and cleanup validation is
// performed by ValidateEvidence before the compatibility bool is accepted.
func (audit ConcurrentPackEditAudit) Verified() bool {
	return audit.First.HostIdentity != "" && audit.Later.HostIdentity != "" && audit.First.HostIdentity != audit.Later.HostIdentity && audit.SnapshotIdentityDiffers && audit.ComputedBasisIdentityDiffers && audit.GenerationIdentityDiffers && audit.PackIdentityDiffers && audit.PackDigestDiffers && audit.PackContentDiffers
}

var implementationTestIDs = map[string]string{
	"VS08-A1":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A01",
	"VS08-A2":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A02",
	"VS08-A3":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A03",
	"VS08-A4":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A04",
	"VS08-A5":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A05",
	"VS08-A6":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A06",
	"VS08-A7":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A07",
	"VS08-A8":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A08",
	"VS08-A9":  "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A09",
	"VS08-A10": "codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A10",
}

type fixture struct {
	snapshot protocol.Snapshot
	mapIR    *semantic.SemanticMapIR
	anchor   slicing.Anchor
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	const source = "package checkout\n\nfunc Submit() {}\n"
	snapshot, err := protocol.NewSnapshot(8, map[string]string{"internal/checkout.go": source}, basisID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot.RepositoryID = "repo-vs08"
	snapshot.WorktreeID = "worktree-vs08"
	fileHash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "internal/checkout.go", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(fileHash[:]), EnclosingSymbolPath: "Submit"}
	mapIR := &semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		MapID: "map-vs08", GenerationID: generationID, ComputedBasisID: basisID,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID, PublicationKind: "checkpoint", Freshness: "current", Settlement: "pending", EnrichmentStatus: "not_requested", Authority: "candidate",
		Quality: semantic.MapQuality{Stage: "Q3", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0},
		Task:    semantic.MapTaskContext{TaskID: "task-vs08", IntentRevision: 1, Mode: "feature"},
		Basis:   semantic.MapBasisContext{RepositoryID: "repo-vs08", WorktreeID: "worktree-vs08", WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: basisID, SnapshotTreeID: snapshot.RootTreeID},
		Summary: semantic.MapSummary{Requested: "show submit flow", Current: "submit flow"},
		Steps:   []semantic.SemanticStep{{StepID: "step-submit", Name: "Submit", TechnicalName: "Submit", Kind: "entry", Anchor: anchor, EvidenceRefs: []string{"e-submit"}}},
		Edges:   []semantic.SemanticEdge{}, Evidence: []semantic.SemanticEvidence{{EvidenceID: "e-submit", Kind: "source", SourceAuthority: "code", ComputedBasisID: basisID, SnapshotID: snapshot.SnapshotID, Anchor: anchor, ValidationStatus: "verified", RedactionStatus: "clean"}},
		Unknowns: []fusion.Unknown{}, Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"internal"}, ExcludedReasons: []string{}},
	}
	return fixture{snapshot: snapshot, mapIR: mapIR, anchor: anchor}
}

func buildPack(t *testing.T, f fixture) *semantic.EvidencePack {
	t.Helper()
	pack, err := semantic.BuildEvidencePackV2(currentRequest(f, []string{"step-submit"}, []string{"internal"}))
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	return pack
}

func currentRequest(f fixture, targetStepIDs, scopePaths []string) semantic.EvidencePackRequest {
	proof := currentProof(f)
	mapBytes, _ := json.Marshal(f.mapIR)
	proof.ArtifactRefs.SemanticMap = storage.ArtifactCASRef(mapBytes)
	proofBytes, _ := json.Marshal(proof)
	pointer := currentPointer(f)
	pointer.ManifestObjectRef = storage.ArtifactCASRef(proofBytes)
	return semantic.EvidencePackRequest{
		Map: f.mapIR, Snapshot: f.snapshot, CurrentProof: proof, CurrentProofBytes: proofBytes, CurrentPointer: pointer, SemanticMapBytes: mapBytes, LiveHeadSnapshotID: f.snapshot.SnapshotID,
		TargetStepIDs: targetStepIDs, ScopePaths: scopePaths,
	}
}

func currentProof(f fixture) *storage.GenerationProofManifest {
	return &storage.GenerationProofManifest{
		SchemaID: semantic.GenerationProofSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		ProofID: "proof-" + f.mapIR.GenerationID, GenerationID: f.mapIR.GenerationID,
		ComputedBasisID: f.mapIR.ComputedBasisID, ComputedSnapshotID: f.snapshot.SnapshotID,
		ValidatedAgainstSnapshotID: f.snapshot.SnapshotID, ExpectedLiveHeadSnapshotID: f.snapshot.SnapshotID,
		TaskIntentRevision: f.mapIR.Task.IntentRevision, NormalizedQueryHash: "query-vs08",
		AnalysisReadSetID: "read-set-vs08", CausalObservationClosureID: "closure-vs08", CausalObservationClosureDigest: "closure-digest-vs08", CapabilityProfileDigest: "capability-vs08",
		WorkspaceEpoch:     f.snapshot.WorkspaceEpoch,
		CurrentPublication: storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"},
		ArtifactRefs:       storage.ArtifactRefs{SemanticMap: "cas:map", AnalysisReadSet: "cas:read-set", ObservationClosure: "cas:closure", AnalyzerResult: "cas:analyzer"},
	}
}

func currentPointer(f fixture) *storage.ActivePointer {
	return &storage.ActivePointer{
		SchemaID: semantic.ActivePointerSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		GenerationID: f.mapIR.GenerationID, ManifestObjectRef: "cas:proof", ComputedBasisID: f.mapIR.ComputedBasisID,
		ValidatedAgainstSnapshotID: f.snapshot.SnapshotID, ExpectedLiveHeadSnapshotID: f.snapshot.SnapshotID,
		WorkspaceEpoch: f.snapshot.WorkspaceEpoch, TaskIntentRevision: f.mapIR.Task.IntentRevision, NormalizedQueryHash: "query-vs08", FlowCount: 1,
		RepositoryID: f.mapIR.Basis.RepositoryID, WorktreeID: f.mapIR.Basis.WorktreeID, TaskID: f.mapIR.Task.TaskID,
	}
}

func refs(f fixture, pack *semantic.EvidencePack) []string {
	return []string{"snapshot:" + f.snapshot.SnapshotID, "basis:" + basisID, "generation:" + generationID, "pack:" + pack.PackDigest, "evidence:e-submit"}
}

func validateArtifactRefs(snapshotID, computedBasisID, generationID, packDigest string, refs []string) error {
	expected := []string{
		"snapshot:" + snapshotID,
		"basis:" + computedBasisID,
		"generation:" + generationID,
		"pack:" + packDigest,
		"evidence:e-submit",
	}
	if len(refs) != len(expected) {
		return fmt.Errorf("VS-08 artifact refs must contain exactly %d entries", len(expected))
	}
	allowed := make(map[string]struct{}, len(expected))
	for _, ref := range expected {
		allowed[ref] = struct{}{}
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, ok := allowed[ref]; !ok {
			return fmt.Errorf("VS-08 artifact ref %q is not bound to the evidence identity", ref)
		}
		if _, duplicate := seen[ref]; duplicate {
			return fmt.Errorf("VS-08 duplicate artifact ref %q", ref)
		}
		seen[ref] = struct{}{}
	}
	for _, ref := range expected {
		if _, ok := seen[ref]; !ok {
			return fmt.Errorf("VS-08 missing artifact ref %q", ref)
		}
	}
	return nil
}

func cloneRunnerStrings(values []string) []string {
	if values == nil {
		return nil
	}
	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}

func cloneRunnerResourceLimitEvidence(value *protocol.ModelHostResourceLimitEvidence) *protocol.ModelHostResourceLimitEvidence {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneRunnerIsolationEvidence(value protocol.ModelHostIsolationEvidence) protocol.ModelHostIsolationEvidence {
	clone := value
	clone.RepositoryWriteAttempts = cloneRunnerStrings(value.RepositoryWriteAttempts)
	clone.ResourceLimits = cloneRunnerResourceLimitEvidence(value.ResourceLimits)
	return clone
}

func cloneRunnerCapability(value protocol.ModelHostCapability) protocol.ModelHostCapability {
	clone := value
	clone.Capabilities = cloneRunnerStrings(value.Capabilities)
	if value.IsolationProbe != nil {
		probe := *value.IsolationProbe
		clone.IsolationProbe = &probe
	}
	clone.ResourceLimits = cloneRunnerResourceLimitEvidence(value.ResourceLimits)
	return clone
}

func cloneRunnerObservation(value ModelHostObservation) ModelHostObservation {
	clone := value
	clone.Isolation = cloneRunnerIsolationEvidence(value.Isolation)
	clone.Capability = cloneRunnerCapability(value.Capability)
	return clone
}

func cloneRunnerObservations(values []ModelHostObservation) []ModelHostObservation {
	if values == nil {
		return nil
	}
	clone := make([]ModelHostObservation, len(values))
	for index := range values {
		clone[index] = cloneRunnerObservation(values[index])
	}
	return clone
}

func cloneRunnerConcurrentPackExecution(value ConcurrentPackExecutionEvidence) ConcurrentPackExecutionEvidence {
	clone := value
	clone.Isolation = cloneRunnerIsolationEvidence(value.Isolation)
	clone.Capability = cloneRunnerCapability(value.Capability)
	return clone
}

func cloneRunnerConcurrentPackAudit(value *ConcurrentPackEditAudit) *ConcurrentPackEditAudit {
	if value == nil {
		return nil
	}
	clone := *value
	clone.First = cloneRunnerConcurrentPackExecution(value.First)
	clone.Later = cloneRunnerConcurrentPackExecution(value.Later)
	return &clone
}

func cloneRunnerEvidence(value Evidence) Evidence {
	clone := value
	clone.MountAudit = cloneRunnerIsolationEvidence(value.MountAudit)
	clone.CapabilityAudit = cloneRunnerCapability(value.CapabilityAudit)
	clone.RepositoryWriteAudit = cloneRunnerStrings(value.RepositoryWriteAudit)
	clone.ObjectRefs = cloneRunnerStrings(value.ObjectRefs)
	clone.ModelHostLifecycle = cloneRunnerObservations(value.ModelHostLifecycle)
	clone.ConcurrentPackAudit = cloneRunnerConcurrentPackAudit(value.ConcurrentPackAudit)
	return clone
}

func evidence(t *testing.T, criterion string, f fixture, pack *semantic.EvidencePack, observed ...ModelHostObservation) Evidence {
	t.Helper()
	applicability, outcome, ok := ModelHostAuditExpectationForCriterion(criterion)
	if !ok {
		t.Fatalf("unknown VS-08 criterion %q", criterion)
	}
	record := Evidence{
		Criterion:                   criterion,
		ImplementationTestID:        implementationTestIDs[criterion],
		ImplementationPackage:       implementationPackage,
		SnapshotID:                  f.snapshot.SnapshotID,
		ComputedBasisID:             basisID,
		GenerationID:                generationID,
		PackDigest:                  pack.PackDigest,
		ModelHostAuditApplicability: applicability,
		ModelHostOutcome:            outcome,
		ObjectRefs:                  refs(f, pack),
	}
	if applicability == ModelHostAuditNotApplicable {
		if len(observed) != 0 {
			t.Fatalf("criterion %s is not model-host applicable but received %d observations", criterion, len(observed))
		}
		return cloneRunnerEvidence(record)
	}
	if len(observed) == 0 {
		t.Fatalf("criterion %s requires explicit model-host observations", criterion)
	}
	primaryMode := expectedModelHostPrimaryMode(criterion)
	var primary *ModelHostObservation
	for index := range observed {
		if observed[index].Mode == primaryMode {
			primary = &observed[index]
			break
		}
	}
	if primary == nil || primary.Applicability != ModelHostAuditApplicable {
		t.Fatalf("criterion %s is missing its %s primary model-host observation", criterion, primaryMode)
	}
	record.MountAudit = primary.Isolation
	record.CapabilityAudit = primary.Capability
	record.RepositoryWriteAudit = append([]string(nil), primary.Isolation.RepositoryWriteAttempts...)
	record.ModelHostLifecycle = append([]ModelHostObservation(nil), observed...)
	return cloneRunnerEvidence(record)
}

func runObservedModelHostMode(t *testing.T, pack *semantic.EvidencePack, mode string) ModelHostObservation {
	t.Helper()
	host, err := protocol.SpawnModelHost(context.Background(), protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestRFLSCR2VS08ModelHostHelper"},
		Env:            []string{"CODEFLOW_VS08_MODEL_HOST_HELPER=1", "CODEFLOW_VS08_MODEL_HOST_MODE=" + mode},
		DisposableRoot: t.TempDir(), DefaultTimeout: modelHostAcceptanceDefaultTimeout,
	})
	if err != nil {
		t.Fatalf("spawn observed model host %s: %v", mode, err)
	}
	request, err := protocol.NewModelHostRequest("request-vs08-"+mode, "step-submit", "Submit", "prompt-vs08", pack.PackDigest, pack, 1<<20)
	if err != nil {
		if closeErr := host.Close(); closeErr != nil {
			t.Fatalf("close observed model host %s after request construction failure: %v", mode, closeErr)
		}
		t.Fatalf("build observed model host request %s: %v", mode, err)
	}
	var callErr error
	if mode == "timeout" {
		ctx, cancel := context.WithTimeout(context.Background(), modelHostAcceptanceTimeout)
		_, callErr = host.Enrich(ctx, request)
		cancel()
	} else if mode == "cancel" {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, enrichErr := host.Enrich(ctx, request)
			result <- enrichErr
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		callErr = <-result
	} else {
		_, callErr = host.Enrich(context.Background(), request)
	}
	if closeErr := host.Close(); closeErr != nil {
		t.Fatalf("close observed model host %s: %v", mode, closeErr)
	}
	observation := ModelHostObservation{Applicability: ModelHostAuditApplicable, Mode: mode, RequestID: request.RequestID, Isolation: host.IsolationEvidence(), Capability: host.Capability()}
	if callErr != nil {
		observation.Error = callErr.Error()
		observation.ErrorClass = modelHostErrorClass(callErr)
	}
	return observation
}

func modelHostErrorClass(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, protocol.ErrTimeout):
		return "timeout"
	case errors.Is(err, protocol.ErrCancelled):
		return "cancel"
	case errors.Is(err, protocol.ErrCrashed):
		return "crash"
	default:
		return "failure"
	}
}

type concurrentPackEditEvidence struct {
	Audit     ConcurrentPackEditAudit
	Verified  bool
	FirstPack semantic.EvidencePack
	LaterPack semantic.EvidencePack
}

func verifyConcurrentPackEdit(t *testing.T, f fixture, pack *semantic.EvidencePack) bool {
	return verifyConcurrentPackEditEvidence(t, f, pack).Verified
}

func waitForConcurrentPackObservation(t *testing.T, host *protocol.ModelHost, request protocol.ModelHostRequest, requireInFlight bool) []byte {
	t.Helper()
	deadline := time.Now().Add(modelHostAcceptanceDefaultTimeout)
	var lastReadErr error
	for {
		evidence := host.IsolationEvidence()
		data, readErr := host.ReadDisposableFile("packs.log")
		lastReadErr = readErr
		// Receipt state is written by Core's protocol reader. The disposable
		// record is only useful as a bounded copy of the pack that was sent.
		// Require both observations before treating the request as in flight.
		if evidence.ReceivedRequestID == request.RequestID && evidence.ReceivedPackDigest == request.PackDigest && (!requireInFlight || evidence.TerminalStatus == "") && readErr == nil && len(data) > 0 {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("concurrent-edit request did not produce a Core receipt and pack observation: request=%s evidence=%+v readErr=%v", request.RequestID, evidence, lastReadErr)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func decodeConcurrentPackRecord(t *testing.T, data []byte) (string, semantic.EvidencePack) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("concurrent-edit host observation did not preserve one pack boundary: %q", data)
	}
	digest, body, ok := strings.Cut(lines[0], "|")
	if !ok || digest == "" || body == "" {
		t.Fatalf("concurrent-edit record has no pack boundary: %q", lines[0])
	}
	var recorded semantic.EvidencePack
	if err := json.Unmarshal([]byte(body), &recorded); err != nil {
		t.Fatalf("decode concurrent-edit record: %v", err)
	}
	return digest, recorded
}

func concurrentPackContentDigest(pack *semantic.EvidencePack) string {
	if pack == nil {
		return ""
	}
	hash := sha256.New()
	for _, item := range pack.Items {
		_, _ = hash.Write([]byte(item.EvidenceID))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(item.Content))
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func spawnConcurrentPackHost(t *testing.T, recordRelease bool) (*protocol.ModelHost, func() error) {
	t.Helper()
	env := []string{"CODEFLOW_VS08_MODEL_HOST_HELPER=1", "CODEFLOW_VS08_MODEL_HOST_MODE=record", "CODEFLOW_VS08_MODEL_HOST_DELAY=20ms", "CODEFLOW_VS08_MODEL_HOST_RECORD=packs.log"}
	if recordRelease {
		env = append(env, "CODEFLOW_VS08_MODEL_HOST_RELEASE=release")
	}
	host, err := protocol.SpawnModelHost(context.Background(), protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestRFLSCR2VS08ModelHostHelper"}, Env: env,
		DisposableRoot: t.TempDir(), DefaultTimeout: modelHostAcceptanceDefaultTimeout,
	})
	if err != nil {
		t.Fatalf("spawn concurrent-edit model host: %v", err)
	}
	closed := false
	closeHost := func() error {
		if closed {
			return nil
		}
		err := host.Close()
		if err == nil {
			closed = true
		}
		return err
	}
	t.Cleanup(func() {
		if !closed {
			if cleanupErr := host.Close(); cleanupErr != nil {
				t.Errorf("cleanup concurrent-edit model host: %v", cleanupErr)
			}
		}
	})
	return host, closeHost
}

func buildLaterConcurrentPack(t *testing.T, f fixture) *semantic.EvidencePack {
	t.Helper()
	laterFiles := make(map[string]string, len(f.snapshot.Files))
	for path, content := range f.snapshot.Files {
		laterFiles[path] = content
	}
	original, ok := laterFiles[f.anchor.RepoRelativePath]
	if !ok {
		t.Fatalf("later snapshot is missing anchored file %q", f.anchor.RepoRelativePath)
	}
	laterSource := original + "// later live edit\n"
	laterFiles[f.anchor.RepoRelativePath] = laterSource
	laterSnapshot, err := protocol.NewSnapshot(f.snapshot.WorkspaceEpoch, laterFiles, "")
	if err != nil {
		t.Fatalf("later snapshot: %v", err)
	}
	laterSnapshot.RepositoryID = f.snapshot.RepositoryID
	laterSnapshot.WorktreeID = f.snapshot.WorktreeID
	laterHash := sha256.Sum256([]byte(laterSource))
	laterAnchor := f.anchor
	laterAnchor.ByteRange = [2]int{0, len([]byte(laterSource))}
	laterAnchor.FileHash = hex.EncodeToString(laterHash[:])
	laterMap := *f.mapIR
	laterMap.MapID = f.mapIR.MapID + "-later"
	laterMap.GenerationID = f.mapIR.GenerationID + "-later"
	laterMap.ComputedBasisID = laterSnapshot.ComputedBasisID
	laterMap.ValidatedAgainstSnapshotID = laterSnapshot.SnapshotID
	laterMap.Basis = f.mapIR.Basis
	laterMap.Basis.ComputedWorkspaceSnapshotID = laterSnapshot.SnapshotID
	laterMap.Basis.SnapshotTreeID = laterSnapshot.RootTreeID
	laterMap.Basis.ComputedBasisID = laterSnapshot.ComputedBasisID
	laterMap.Steps = append([]semantic.SemanticStep(nil), f.mapIR.Steps...)
	for i := range laterMap.Steps {
		if laterMap.Steps[i].StepID == "step-submit" {
			laterMap.Steps[i].Anchor = laterAnchor
		}
	}
	laterMap.Evidence = append([]semantic.SemanticEvidence(nil), f.mapIR.Evidence...)
	for i := range laterMap.Evidence {
		if laterMap.Evidence[i].EvidenceID == "e-submit" {
			laterMap.Evidence[i].SnapshotID = laterSnapshot.SnapshotID
			laterMap.Evidence[i].ComputedBasisID = laterSnapshot.ComputedBasisID
			laterMap.Evidence[i].Anchor = laterAnchor
		}
	}
	laterFixture := fixture{snapshot: laterSnapshot, mapIR: &laterMap, anchor: laterAnchor}
	laterPack, err := semantic.BuildEvidencePackV2(currentRequest(laterFixture, []string{"step-submit"}, []string{"internal"}))
	if err != nil {
		t.Fatalf("later evidence pack: %v", err)
	}
	if err := semantic.ValidateEvidencePackV2(laterPack); err != nil {
		t.Fatalf("later concurrent-edit pack is invalid: %v", err)
	}
	return laterPack
}

func concurrentPackExecution(t *testing.T, label string, host *protocol.ModelHost, closeHost func() error, request protocol.ModelHostRequest, expected, recorded semantic.EvidencePack, recordedDigest string) ConcurrentPackExecutionEvidence {
	t.Helper()
	if recordedDigest != request.PackDigest || recorded.PackDigest != expected.PackDigest || recorded.EvidencePackID != expected.EvidencePackID || len(recorded.Items) == 0 || len(expected.Items) == 0 || recorded.Items[0].Content != expected.Items[0].Content {
		t.Fatalf("%s concurrent-edit host observation does not match the Core request pack: recordDigest=%q requestDigest=%q recorded=%+v expected=%+v", label, recordedDigest, request.PackDigest, recorded, expected)
	}
	if err := semantic.ValidateEvidencePackV2(&recorded); err != nil {
		t.Fatalf("%s recorded pack is invalid: %v", label, err)
	}
	attestation := protocol.AttestModelHost(host)
	if attestation == nil {
		t.Fatalf("%s concurrent-edit host has no Core attestation", label)
	}
	identity := protocol.ModelHostCoreDisplayID(host, attestation)
	if identity == "" {
		t.Fatalf("%s concurrent-edit host has no Core display identity", label)
	}
	if closeErr := closeHost(); closeErr != nil {
		t.Fatalf("close %s concurrent-edit model host: %v", label, closeErr)
	}
	if !protocol.VerifyCoreHostAttestation(host, attestation) {
		t.Fatalf("%s concurrent-edit host lost its exact Core attestation after close", label)
	}
	isolation := host.IsolationEvidence()
	capability := host.Capability()
	observation := ModelHostObservation{Applicability: ModelHostAuditApplicable, Mode: "success", RequestID: request.RequestID, Isolation: isolation, Capability: capability}
	if err := validateModelHostObservation(request.PackDigest, observation); err != nil {
		t.Fatalf("%s concurrent-edit host evidence is incomplete: %v", label, err)
	}
	return ConcurrentPackExecutionEvidence{
		HostIdentity: identity, RequestID: request.RequestID, SnapshotID: recorded.SnapshotID,
		ComputedBasisID: recorded.ComputedBasisID, GenerationID: recorded.GenerationID, EvidencePackID: recorded.EvidencePackID,
		PackDigest: recorded.PackDigest, PackContentDigest: concurrentPackContentDigest(&recorded),
		Isolation: isolation, Capability: capability,
		host: host, attestation: attestation,
	}
}

func verifyConcurrentPackEditEvidence(t *testing.T, f fixture, pack *semantic.EvidencePack) concurrentPackEditEvidence {
	t.Helper()
	if err := semantic.ValidateEvidencePackV2(pack); err != nil {
		t.Fatalf("first concurrent-edit pack is invalid: %v", err)
	}
	firstHost, firstClose := spawnConcurrentPackHost(t, true)
	first, err := protocol.NewModelHostRequest("request-vs08-before", "step-submit", "Submit", "prompt-vs08", pack.PackDigest, pack, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan error, 1)
	go func() {
		_, enrichErr := firstHost.Enrich(context.Background(), first)
		firstResult <- enrichErr
	}()
	firstData := waitForConcurrentPackObservation(t, firstHost, first, true)
	laterPack := buildLaterConcurrentPack(t, f)
	if err := firstHost.WriteDisposableFile("release", []byte("release")); err != nil {
		t.Fatalf("release first concurrent-edit request: %v", err)
	}
	if err := <-firstResult; err != nil {
		t.Fatalf("first observed enrichment: %v", err)
	}
	firstDigest, firstPack := decodeConcurrentPackRecord(t, firstData)
	firstExecution := concurrentPackExecution(t, "first", firstHost, firstClose, first, *pack, firstPack, firstDigest)

	laterHost, laterClose := spawnConcurrentPackHost(t, false)
	second, err := protocol.NewModelHostRequest("request-vs08-after", "step-submit", "Submit", "prompt-vs08", laterPack.PackDigest, laterPack, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := laterHost.Enrich(context.Background(), second); err != nil {
		t.Fatalf("second observed enrichment: %v", err)
	}
	laterData := waitForConcurrentPackObservation(t, laterHost, second, false)
	secondDigest, secondPack := decodeConcurrentPackRecord(t, laterData)
	laterExecution := concurrentPackExecution(t, "later", laterHost, laterClose, second, *laterPack, secondPack, secondDigest)
	audit := ConcurrentPackEditAudit{
		First: firstExecution, Later: laterExecution,
		SnapshotIdentityDiffers:      firstExecution.SnapshotID != laterExecution.SnapshotID,
		ComputedBasisIdentityDiffers: firstExecution.ComputedBasisID != laterExecution.ComputedBasisID,
		GenerationIdentityDiffers:    firstExecution.GenerationID != laterExecution.GenerationID,
		PackIdentityDiffers:          firstExecution.EvidencePackID != laterExecution.EvidencePackID,
		PackDigestDiffers:            firstExecution.PackDigest != laterExecution.PackDigest,
		PackContentDiffers:           firstExecution.PackContentDigest != laterExecution.PackContentDigest,
	}
	if !audit.Verified() {
		t.Fatalf("concurrent-edit structured audit is incomplete: %+v", audit)
	}
	return concurrentPackEditEvidence{Audit: audit, Verified: audit.Verified(), FirstPack: firstPack, LaterPack: secondPack}
}

func RunA01(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	if err := semantic.ValidateEvidencePackV2(pack); err != nil {
		t.Fatal(err)
	}
	return evidence(t, "VS08-A1", f, pack)
}

func RunA02(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	if !strings.Contains(pack.Items[0].Content, "func Submit") {
		t.Fatal("pack did not retain captured source")
	}
	unsafeRequest := currentRequest(f, []string{"step-submit"}, []string{"../internal"})
	if _, err := semantic.BuildEvidencePackV2(unsafeRequest); err == nil {
		t.Fatal("unsafe scope path was accepted")
	}
	return evidence(t, "VS08-A2", f, pack)
}

func RunA03(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	request, err := protocol.NewModelHostRequest("request-vs08", "step-submit", "Submit", "prompt-1", pack.PackDigest, pack, 1<<20)
	if err != nil || request.EvidencePack == nil || strings.Contains(string(request.EvidencePack), "repoRoot") {
		t.Fatalf("bounded model request invalid: %v", err)
	}
	observations := make([]ModelHostObservation, 0, 3)
	for _, mode := range []string{"success", "timeout", "cancel"} {
		observations = append(observations, runObservedModelHostMode(t, pack, mode))
	}
	return evidence(t, "VS08-A3", f, pack, observations...)
}

func RunA04(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	result := semantic.RunSemanticEnrichment(context.Background(), semantic.EnrichmentRequest{EvidencePack: currentRequest(f, []string{"step-submit"}, nil)})
	if result.State.Status != "unavailable" || result.Fallback == nil || result.State.Capability.Status != "unavailable" {
		t.Fatalf("absent host was not explicit: %+v", result)
	}
	return evidence(t, "VS08-A4", f, pack)
}

func validProposal(t *testing.T, f fixture, pack *semantic.EvidencePack) *semantic.ModelProposal {
	t.Helper()
	digests, err := semantic.CanonicalQ3Digests(f.mapIR)
	if err != nil {
		t.Fatal(err)
	}
	return &semantic.ModelProposal{SchemaID: semantic.SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-vs08", ComputedBasisID: basisID, GenerationID: generationID, SnapshotID: f.snapshot.SnapshotID, TargetStepID: "step-submit", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout", ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only", ModelID: "fake-model", ModelRevision: "r1", PromptRevision: "prompt-vs08", SchemaProfile: semantic.SemanticProposalSchemaProfile, PackDigest: pack.PackDigest, EvidenceRefs: []string{pack.Items[0].EvidenceID}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation, AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement}
}

func RunA05(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	proposal := validProposal(t, f, pack)
	if err := semantic.ValidateModelProposalV2(proposal, semantic.ProposalValidationContext{Map: f.mapIR, Pack: pack, TargetStepIDs: pack.TargetStepIDs, TargetSymbolPath: pack.TargetSymbolPath, ExpectedPackDigest: pack.PackDigest, ExpectedModelID: "fake-model", ExpectedModelRevision: "r1", ExpectedPromptRevision: "prompt-vs08", ExpectedSchemaProfile: semantic.SemanticProposalSchemaProfile}); err != nil {
		t.Fatal(err)
	}
	return evidence(t, "VS08-A5", f, pack)
}

func RunA06(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	proposal := validProposal(t, f, pack)
	proposal.TargetStepID = "missing-step"
	if err := semantic.ValidateModelProposalV2(proposal, semantic.ProposalValidationContext{Map: f.mapIR, Pack: pack, TargetStepIDs: pack.TargetStepIDs, TargetSymbolPath: pack.TargetSymbolPath, ExpectedPackDigest: pack.PackDigest, ExpectedModelID: "fake-model", ExpectedModelRevision: "r1", ExpectedPromptRevision: "prompt-vs08", ExpectedSchemaProfile: semantic.SemanticProposalSchemaProfile}); err == nil {
		t.Fatal("mismatched target accepted")
	}
	return evidence(t, "VS08-A6", f, pack)
}

func RunA07(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	before, err := semantic.CanonicalQ3Digests(f.mapIR)
	if err != nil {
		t.Fatal(err)
	}
	var capturedHost *protocol.ModelHost
	factory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		host, spawnErr := protocol.SpawnModelHost(ctx, protocol.ModelHostConfig{
			BinPath: os.Args[0], Args: []string{"-test.run=TestRFLSCR2VS08ModelHostHelper"},
			Env:            []string{"CODEFLOW_VS08_MODEL_HOST_HELPER=1", "CODEFLOW_VS08_MODEL_HOST_MODE=timeout"},
			DisposableRoot: t.TempDir(), DefaultTimeout: 120 * time.Millisecond,
		})
		capturedHost = host
		return host, spawnErr
	})
	result := semantic.RunSemanticEnrichment(context.Background(), semantic.EnrichmentRequest{EvidencePack: currentRequest(f, []string{"step-submit"}, nil), ModelHostFactory: factory})
	after, err := semantic.CanonicalQ3Digests(f.mapIR)
	if err != nil || result.State.Status != "timed_out" || result.Proposal != nil || result.View != nil || before != after {
		t.Fatalf("timeout mutation/state: result=%+v before=%+v after=%+v err=%v", result, before, after, err)
	}
	if capturedHost == nil || result.Pack == nil || result.Pack.PackDigest != pack.PackDigest {
		t.Fatalf("semantic timeout did not retain the actual supervised host/pack observation: result=%+v host=%p", result, capturedHost)
	}
	observation := ModelHostObservation{
		Applicability: ModelHostAuditApplicable,
		Mode:          "timeout",
		RequestID:     "enrichment-" + result.Pack.EvidencePackID,
		Error:         result.State.Reason,
		ErrorClass:    "timeout",
		Isolation:     capturedHost.IsolationEvidence(),
		Capability:    capturedHost.Capability(),
	}
	if !reflect.DeepEqual(observation.Isolation, result.State.Isolation) || !reflect.DeepEqual(observation.Capability, result.State.Capability) {
		t.Fatalf("semantic timeout state did not preserve the actual host evidence: state=%+v host=%+v", result.State, observation)
	}
	if observation.Isolation.TerminalStatus != "timeout" || observation.Isolation.ReceivedRequestID != observation.RequestID || observation.Isolation.ReceivedPackDigest != pack.PackDigest || !observation.Isolation.CleanupVerified {
		t.Fatalf("semantic timeout observation does not match actual supervised host: %+v", observation)
	}
	return evidence(t, "VS08-A7", f, pack, observation)
}

func RunA08(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	if _, err := semantic.AcceptSemanticProposal(f.mapIR, validProposal(t, f, pack), pack); err != nil {
		t.Fatal(err)
	}
	return evidence(t, "VS08-A8", f, pack)
}

func RunA09(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	disclosure, err := semantic.NewModelActivationDisclosure(semantic.ModelArtifact{ModelID: "local-slm", Revision: "r1", License: "Apache-2.0", Checksum: "sha256:vs08", Runtime: "sandbox", DataBoundary: "local-only", Capabilities: []string{"semantic_proposal"}}, semantic.ModelCapabilityState{Status: "unsupported"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := semantic.ResolveModelActivation(disclosure, "activate"); err != nil {
		t.Fatal(err)
	}
	return evidence(t, "VS08-A9", f, pack)
}

func RunA10(t *testing.T) Evidence {
	f := newFixture(t)
	pack := buildPack(t, f)
	observations := make([]ModelHostObservation, 0, 5)
	for _, mode := range []string{"success", "failure", "crash", "timeout", "cancel"} {
		observations = append(observations, runObservedModelHostMode(t, pack, mode))
	}
	record := evidence(t, "VS08-A10", f, pack, observations...)
	concurrent := verifyConcurrentPackEditEvidence(t, f, pack)
	record.ConcurrentPackAudit = cloneRunnerConcurrentPackAudit(&concurrent.Audit)
	record.ConcurrentPackEditVerified = concurrent.Audit.Verified()
	return cloneRunnerEvidence(record)
}

func ValidateEvidence(value Evidence) error {
	if value.Criterion == "" || value.ImplementationTestID != implementationTestIDs[value.Criterion] || value.ImplementationPackage != implementationPackage || value.SnapshotID == "" || value.ComputedBasisID == "" || value.GenerationID == "" || value.PackDigest == "" {
		return errors.New("VS-08 evidence identity is incomplete")
	}
	if len(value.RepositoryWriteAudit) != 0 {
		return errors.New("VS-08 repository write audit is non-empty")
	}
	if err := validateArtifactRefs(value.SnapshotID, value.ComputedBasisID, value.GenerationID, value.PackDigest, value.ObjectRefs); err != nil {
		return err
	}
	applicability, outcome, ok := ModelHostAuditExpectationForCriterion(value.Criterion)
	if !ok {
		return fmt.Errorf("VS-08 has no model-host applicability for criterion %q", value.Criterion)
	}
	if value.ModelHostAuditApplicability != applicability {
		return fmt.Errorf("VS-08 model-host applicability for %s = %q want %q", value.Criterion, value.ModelHostAuditApplicability, applicability)
	}
	if value.ModelHostOutcome != outcome {
		return fmt.Errorf("VS-08 model-host outcome for %s = %q want %q", value.Criterion, value.ModelHostOutcome, outcome)
	}
	if applicability == ModelHostAuditNotApplicable {
		if !reflect.DeepEqual(value.MountAudit, protocol.ModelHostIsolationEvidence{}) || !reflect.DeepEqual(value.CapabilityAudit, protocol.ModelHostCapability{}) || len(value.ModelHostLifecycle) != 0 || value.ConcurrentPackAudit != nil || value.ConcurrentPackEditVerified {
			return fmt.Errorf("VS-08 %s is not model-host applicable but contains measured host evidence", value.Criterion)
		}
		return nil
	}
	expectedModes := expectedModelHostModes(value.Criterion)
	if len(value.ModelHostLifecycle) != len(expectedModes) {
		return fmt.Errorf("VS-08 %s model-host lifecycle has %d observations want %d", value.Criterion, len(value.ModelHostLifecycle), len(expectedModes))
	}
	if value.Criterion == "VS08-A10" {
		if value.ConcurrentPackAudit == nil || !value.ConcurrentPackEditVerified || value.ConcurrentPackEditVerified != value.ConcurrentPackAudit.Verified() {
			return errors.New("VS08-A10 production concurrent-pack audit is missing or boolean-only")
		}
	} else if value.ConcurrentPackAudit != nil || value.ConcurrentPackEditVerified {
		return fmt.Errorf("VS-08 %s contains unrelated concurrent-pack host evidence", value.Criterion)
	}
	seenModes := make(map[string]bool, len(value.ModelHostLifecycle))
	for _, observation := range value.ModelHostLifecycle {
		if seenModes[observation.Mode] {
			return fmt.Errorf("VS-08 %s duplicated model-host lifecycle mode %q", value.Criterion, observation.Mode)
		}
		seenModes[observation.Mode] = true
		if err := validateModelHostObservation(value.PackDigest, observation); err != nil {
			return fmt.Errorf("VS-08 %s model-host lifecycle observation: %w", value.Criterion, err)
		}
	}
	for _, mode := range expectedModes {
		if !seenModes[mode] {
			return fmt.Errorf("VS-08 %s missing model-host lifecycle mode %q", value.Criterion, mode)
		}
	}
	primaryMode := expectedModelHostPrimaryMode(value.Criterion)
	var primary *ModelHostObservation
	for index := range value.ModelHostLifecycle {
		if value.ModelHostLifecycle[index].Mode == primaryMode {
			primary = &value.ModelHostLifecycle[index]
			break
		}
	}
	if primary == nil {
		return fmt.Errorf("VS-08 %s is missing its %s primary model-host lifecycle observation", value.Criterion, primaryMode)
	}
	if !reflect.DeepEqual(value.MountAudit, primary.Isolation) || !reflect.DeepEqual(value.CapabilityAudit, primary.Capability) || len(value.RepositoryWriteAudit) != len(primary.Isolation.RepositoryWriteAttempts) {
		return fmt.Errorf("VS-08 %s top-level host audit does not match its primary observation", value.Criterion)
	}
	for index := range value.RepositoryWriteAudit {
		if value.RepositoryWriteAudit[index] != primary.Isolation.RepositoryWriteAttempts[index] {
			return fmt.Errorf("VS-08 %s top-level repository audit does not match its primary observation", value.Criterion)
		}
	}
	if value.Criterion == "VS08-A10" {
		if err := validateConcurrentPackEditAudit(value.SnapshotID, value.ComputedBasisID, value.GenerationID, value.PackDigest, *value.ConcurrentPackAudit); err != nil {
			return fmt.Errorf("VS-08 A10 concurrent-pack audit: %w", err)
		}
		if value.ConcurrentPackAudit.First.SnapshotID != value.SnapshotID || value.ConcurrentPackAudit.First.ComputedBasisID != value.ComputedBasisID || value.ConcurrentPackAudit.First.GenerationID != value.GenerationID || value.ConcurrentPackAudit.First.PackDigest != value.PackDigest {
			return errors.New("VS-08 A10 concurrent-pack first execution does not match primary host evidence")
		}
	}
	return nil
}

func validConcurrentPackDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validConcurrentPackContentDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validateConcurrentPackExecution(packDigest string, execution ConcurrentPackExecutionEvidence) error {
	if execution.host == nil || execution.attestation == nil || !protocol.VerifyCoreHostAttestation(execution.host, execution.attestation) || protocol.ModelHostCoreDisplayID(execution.host, execution.attestation) != execution.HostIdentity {
		return errors.New("concurrent-pack execution has no exact Core host attestation")
	}
	if execution.RequestID == "" || execution.SnapshotID == "" || execution.ComputedBasisID == "" || execution.GenerationID == "" || execution.EvidencePackID == "" || execution.PackDigest != packDigest || !validConcurrentPackDigest(execution.PackDigest) || !validConcurrentPackContentDigest(execution.PackContentDigest) {
		return errors.New("concurrent-pack execution identity or digest is incomplete")
	}
	observation := ModelHostObservation{
		Applicability: ModelHostAuditApplicable, Mode: "success", RequestID: execution.RequestID,
		Error: execution.Error, ErrorClass: execution.ErrorClass,
		Isolation: execution.Isolation, Capability: execution.Capability,
	}
	if err := validateModelHostObservation(packDigest, observation); err != nil {
		return fmt.Errorf("concurrent-pack host execution: %w", err)
	}
	return nil
}

// ValidateConcurrentPackEditAudit is the Core-backed validator used by both
// the runner and the contract registry. Its unexported host and attestation
// fields make a JSON round-trip or fabricated audit fail closed.
func ValidateConcurrentPackEditAudit(snapshotID, computedBasisID, generationID, packDigest string, audit ConcurrentPackEditAudit) error {
	if !audit.Verified() {
		return errors.New("concurrent-pack audit identity-difference proof is incomplete")
	}
	if audit.First.host == audit.Later.host || audit.First.attestation == audit.Later.attestation || audit.First.HostIdentity == audit.Later.HostIdentity || audit.First.RequestID == audit.Later.RequestID || audit.First.SnapshotID == audit.Later.SnapshotID || audit.First.ComputedBasisID == audit.Later.ComputedBasisID || audit.First.GenerationID == audit.Later.GenerationID || audit.First.EvidencePackID == audit.Later.EvidencePackID || audit.First.PackDigest == audit.Later.PackDigest || audit.First.PackContentDigest == audit.Later.PackContentDigest {
		return errors.New("concurrent-pack audit reuses host/request or aliases immutable identities")
	}
	if audit.First.SnapshotID != snapshotID || audit.First.ComputedBasisID != computedBasisID || audit.First.GenerationID != generationID || audit.First.PackDigest != packDigest {
		return errors.New("concurrent-pack first execution is not bound to the recorded initial pack")
	}
	if !audit.SnapshotIdentityDiffers || !audit.ComputedBasisIdentityDiffers || !audit.GenerationIdentityDiffers || !audit.PackIdentityDiffers || !audit.PackDigestDiffers || !audit.PackContentDiffers {
		return errors.New("concurrent-pack audit does not prove every immutable identity changed")
	}
	if err := validateConcurrentPackExecution(audit.First.PackDigest, audit.First); err != nil {
		return fmt.Errorf("first execution: %w", err)
	}
	if err := validateConcurrentPackExecution(audit.Later.PackDigest, audit.Later); err != nil {
		return fmt.Errorf("later execution: %w", err)
	}
	return nil
}

func validateConcurrentPackEditAudit(snapshotID, computedBasisID, generationID, packDigest string, audit ConcurrentPackEditAudit) error {
	return ValidateConcurrentPackEditAudit(snapshotID, computedBasisID, generationID, packDigest, audit)
}

func expectedModelHostModes(criterion string) []string {
	switch criterion {
	case "VS08-A3":
		return []string{"success", "timeout", "cancel"}
	case "VS08-A7":
		return []string{"timeout"}
	case "VS08-A10":
		return []string{"success", "failure", "crash", "timeout", "cancel"}
	default:
		return nil
	}
}

func expectedModelHostPrimaryMode(criterion string) string {
	switch criterion {
	case "VS08-A7":
		return "timeout"
	case "VS08-A3", "VS08-A10":
		return "success"
	default:
		return ""
	}
}

func validateModelHostObservation(packDigest string, observation ModelHostObservation) error {
	if observation.Applicability != ModelHostAuditApplicable || observation.Mode == "" || observation.RequestID == "" {
		return errors.New("model-host observation applicability or identity is incomplete")
	}
	if observation.Isolation.PackDigest != packDigest || observation.Isolation.ReceivedRequestID != observation.RequestID || observation.Isolation.ReceivedPackDigest != packDigest || observation.Isolation.SourceDelivery != "bounded_evidence_pack" || observation.Isolation.SourceMount != "not_mounted" || observation.Isolation.WorkingDirectoryMode != "process_private_disposable" || observation.Isolation.WorkingDirectoryPermission != "0700" || !observation.Isolation.Disposable || observation.Isolation.RepositoryPathExposed || observation.Isolation.RepositoryWriteCapability || len(observation.Isolation.RepositoryWriteAttempts) != 0 || observation.Isolation.RepositoryWriteAuditStatus != protocol.ModelHostRepositoryWriteAuditCapabilityEnforced || observation.Isolation.DisposableWriteAttempt != "blocked" || !observation.Isolation.CoreTrustedProbe || !observation.Isolation.CleanupVerified || observation.Isolation.IsolationBackend != "sandbox-exec" || observation.Isolation.EnforcementStatus != "enforced" || observation.Isolation.NetworkPolicy != "deny_all" || observation.Isolation.RepositoryReadAttempt != "blocked" || observation.Isolation.RepositoryWriteAttempt != "blocked" || observation.Isolation.NetworkAttempt != "blocked" || observation.Isolation.SentinelBeforeDigest == "" || observation.Isolation.SentinelBeforeDigest != observation.Isolation.SentinelAfterDigest || !observation.Isolation.SentinelUnchanged || observation.Isolation.RuntimePolicyBinding != protocol.ModelHostRuntimePolicyBindingExact || observation.Isolation.ProbeScope != protocol.ModelHostProbeScopeSharedDenyBase || observation.Isolation.RuntimePolicySharedBaseDigest == "" || observation.Isolation.ProbePolicyDigest == "" || observation.Isolation.ProbeSharedBaseDigest == "" || observation.Isolation.ProbeSharedBaseDigest != observation.Isolation.RuntimePolicySharedBaseDigest {
		return errors.New("model-host isolation observation is incomplete")
	}
	if observation.Capability.Status != "measured" || !observation.Capability.Measured || !observation.Capability.SchemaConstrained || !observation.Capability.Cancellation || !observation.Capability.IsMeasured() {
		return errors.New("model-host capability observation is not measured")
	}
	if err := validateModelHostAuditBinding(observation.Capability, observation.Isolation); err != nil {
		return fmt.Errorf("model-host policy/resource observation: %w", err)
	}
	switch observation.Mode {
	case "success":
		if observation.Error != "" || observation.ErrorClass != "" || observation.Isolation.TerminalStatus != "success" {
			return errors.New("success model-host observation is invalid")
		}
	case "failure", "crash", "timeout", "cancel":
		if observation.Error == "" || observation.ErrorClass != observation.Mode || observation.Isolation.TerminalStatus != observation.Mode {
			return fmt.Errorf("%s model-host observation is invalid", observation.Mode)
		}
	default:
		return fmt.Errorf("unknown model-host lifecycle mode %q", observation.Mode)
	}
	return nil
}

func validateModelHostAuditBinding(capability protocol.ModelHostCapability, isolation protocol.ModelHostIsolationEvidence) error {
	if !capability.IsMeasured() {
		return errors.New("Core capability is not semantically measured")
	}
	if err := protocol.ValidateModelHostIsolationEvidence(isolation); err != nil {
		return fmt.Errorf("Core isolation evidence: %w", err)
	}
	if err := protocol.ValidateModelHostPolicyIdentityBinding(capability, isolation); err != nil {
		return fmt.Errorf("Core runtime and trusted-probe policy identity binding: %w", err)
	}
	if capability.ResourceLimits == nil || isolation.ResourceLimits == nil {
		return errors.New("Core resource-limit evidence is missing from capability or isolation evidence")
	}
	if err := protocol.ValidateModelHostResourceLimitEvidence(*capability.ResourceLimits); err != nil {
		return fmt.Errorf("capability resource limits: %w", err)
	}
	if err := protocol.ValidateModelHostResourceLimitEvidence(*isolation.ResourceLimits); err != nil {
		return fmt.Errorf("isolation resource limits: %w", err)
	}
	if capability.ResourceLimits.EnforcementStatus != protocol.ModelHostResourceEnforcementEnforced || capability.ResourceLimits.Backend != protocol.ModelHostResourceBackendDarwinHostTree || capability.ResourceLimits.Applied.ProcessCount != 1 || capability.ResourceLimits.Declared.CPUTimeSeconds != capability.ResourceLimits.Applied.CPUTimeSeconds || capability.ResourceLimits.Declared.MemoryBytes != capability.ResourceLimits.Applied.MemoryBytes {
		return errors.New("capability resource limits are not exactly applied and enforced")
	}
	if isolation.ResourceLimits.EnforcementStatus != protocol.ModelHostResourceEnforcementEnforced || isolation.ResourceLimits.Backend != protocol.ModelHostResourceBackendDarwinHostTree || isolation.ResourceLimits.Applied.ProcessCount != 1 || isolation.ResourceLimits.Declared.CPUTimeSeconds != isolation.ResourceLimits.Applied.CPUTimeSeconds || isolation.ResourceLimits.Declared.MemoryBytes != isolation.ResourceLimits.Applied.MemoryBytes {
		return errors.New("isolation resource limits are not exactly applied and enforced")
	}
	if *capability.ResourceLimits != *isolation.ResourceLimits {
		return errors.New("capability and isolation resource limits differ")
	}
	return nil
}
