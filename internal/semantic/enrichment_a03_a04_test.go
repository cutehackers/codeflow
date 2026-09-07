package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
)

type fakeModelHost struct {
	capability protocol.ModelHostCapability
	response   protocol.ModelHostResponse
	err        error
	requests   []protocol.ModelHostRequest
	closed     bool
	closeErr   error
	isolation  protocol.ModelHostIsolationEvidence
}

func (f *fakeModelHost) Enrich(_ context.Context, request protocol.ModelHostRequest) (protocol.ModelHostResponse, error) {
	f.requests = append(f.requests, request)
	if f.isolation.SourceDelivery == "" {
		f.isolation = completeFakeIsolation(request.PackDigest, "success")
	}
	if f.isolation.CoreTrustedProbe {
		f.isolation.PackDigest = request.PackDigest
		f.isolation.ReceivedRequestID = request.RequestID
		f.isolation.ReceivedPackDigest = request.PackDigest
	}
	if f.err != nil {
		switch {
		case errors.Is(f.err, protocol.ErrTimeout):
			f.isolation.TerminalStatus = "timeout"
		case errors.Is(f.err, protocol.ErrCancelled):
			f.isolation.TerminalStatus = "cancel"
		default:
			f.isolation.TerminalStatus = "failure"
		}
		return protocol.ModelHostResponse{}, f.err
	}
	return f.response, nil
}
func (f *fakeModelHost) Capability() protocol.ModelHostCapability { return f.capability }
func (f *fakeModelHost) IsolationEvidence() protocol.ModelHostIsolationEvidence {
	if f.isolation.SourceDelivery != "" {
		return f.isolation
	}
	return protocol.ModelHostIsolationEvidence{SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", Disposable: true, RepositoryPathExposed: false, RepositoryWriteCapability: false, RepositoryWriteAttempts: []string{}}
}
func (f *fakeModelHost) Close() error {
	f.closed = true
	if f.isolation.CoreTrustedProbe {
		f.isolation.CleanupVerified = true
	}
	return f.closeErr
}

func completeFakeIsolation(packDigest, terminal string) protocol.ModelHostIsolationEvidence {
	return protocol.ModelHostIsolationEvidence{
		SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable",
		WorkingDirectoryPermission: "0700", Disposable: true, RepositoryWriteAttempts: []string{}, RepositoryWriteAuditStatus: protocol.ModelHostRepositoryWriteAuditCapabilityEnforced, PackDigest: packDigest,
		CapabilityStatus: "measured", TerminalStatus: terminal, CleanupVerified: true, IsolationBackend: "fake-sandbox",
		EnforcementStatus: "enforced", RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked",
		SentinelBeforeDigest: "sha256:" + strings.Repeat("b", 64), SentinelAfterDigest: "sha256:" + strings.Repeat("b", 64), SentinelUnchanged: true,
		NetworkAttempt: "blocked", NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("a", 64), CoreTrustedProbe: true,
		ResourceLimits: fakeResourceLimitEvidence(),
	}
}

func TestRunSemanticEnrichment_UsesDeclaredCapabilityAndBoundedRequest(t *testing.T) {
	snapshot, mapIR, packRequest := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(packRequest)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal := ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-a03",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "fake", ModelRevision: "r1", PromptRevision: "prompt-1", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	}
	proposalBytes, _ := json.Marshal(proposal)
	host := &fakeModelHost{
		capability: measuredFakeCapability(),
		response:   protocol.ModelHostResponse{SchemaID: ModelHostResponseV2SchemaID, SchemaVersion: 2, RequestID: "enrichment-" + pack.EvidencePackID, Status: "accepted", Proposal: proposalBytes},
		isolation:  completeFakeIsolation(pack.PackDigest, "success"),
	}
	result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: packRequest, modelHost: host, PromptRevision: "prompt-1"})
	if result.State.Status != "available" || result.Proposal == nil || result.View == nil {
		t.Fatalf("enrichment result = %+v", result)
	}
	if len(host.requests) != 1 || strings.Contains(string(host.requests[0].EvidencePack), "repoRoot") || strings.Contains(string(host.requests[0].EvidencePack), "Submit checkout") {
		t.Fatalf("host received unbounded or proposal data: %+v", host.requests)
	}
	if host.requests[0].PackDigest == "" || host.requests[0].PackDigest != result.Pack.PackDigest {
		t.Fatalf("pack digest was not preserved: %+v", host.requests[0])
	}
}

func TestRunSemanticEnrichment_AbsentOrUnmeasuredHostIsUnavailable(t *testing.T) {
	_, _, packRequest := enrichmentTestInput(t)
	withoutHost := RunSemanticEnrichment(context.Background(), EnrichmentRequest{EvidencePack: packRequest})
	if withoutHost.State.Status != "unavailable" || withoutHost.Fallback == nil {
		t.Fatalf("absent host result = %+v", withoutHost)
	}
	unmeasured := &fakeModelHost{capability: protocol.ModelHostCapability{Status: "unsupported", ModelID: "named-only"}}
	result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: packRequest, modelHost: unmeasured})
	if result.State.Status != "unavailable" || result.State.Capability.Status != "unsupported" {
		t.Fatalf("unmeasured host result = %+v", result)
	}
	if !unmeasured.closed {
		t.Fatal("unmeasured host was not closed by the enrichment owner")
	}
}

func TestRunSemanticEnrichmentRejectsIncompleteIsolationEvidence(t *testing.T) {
	snapshot, mapIR, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal := ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-isolation",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "fake", ModelRevision: "r1", PromptRevision: "prompt-isolation", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	}
	proposalBytes, _ := json.Marshal(proposal)
	host := &fakeModelHost{
		capability: measuredFakeCapability(),
		response:   protocol.ModelHostResponse{SchemaID: ModelHostResponseV2SchemaID, SchemaVersion: 2, RequestID: "enrichment-" + pack.EvidencePackID, Status: "accepted", Proposal: proposalBytes},
		isolation:  protocol.ModelHostIsolationEvidence{SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", Disposable: true, RepositoryWriteAttempts: []string{}},
	}
	result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host, PromptRevision: "prompt-isolation", MaxAttempts: 1})
	if result.State.Status == "available" || result.Proposal != nil || result.View != nil {
		t.Fatalf("incomplete isolation evidence was exposed: %+v", result)
	}
	if result.State.Status != "unavailable" || result.Fallback == nil {
		t.Fatalf("incomplete isolation evidence did not produce deterministic fallback: %+v", result)
	}
}

func TestRunSemanticEnrichmentReportsCleanupFailureOnInvalidIsolation(t *testing.T) {
	_, _, request := enrichmentTestInput(t)
	cleanupErr := errors.New("injected cleanup failure")
	host := &fakeModelHost{
		capability: measuredFakeCapability(),
		response:   protocol.ModelHostResponse{SchemaID: ModelHostResponseV2SchemaID, SchemaVersion: 2, Status: "accepted"},
		closeErr:   cleanupErr,
		isolation:  protocol.ModelHostIsolationEvidence{SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", Disposable: true, RepositoryWriteAttempts: []string{}},
	}
	result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host, MaxAttempts: 1})
	if result.State.Status != "unavailable" || result.Proposal != nil || result.View != nil {
		t.Fatalf("invalid isolation cleanup result = %+v, want unavailable", result)
	}
	if !strings.Contains(result.State.Reason, "source_integrity_violation") || !strings.Contains(result.State.Reason, cleanupErr.Error()) {
		t.Fatalf("cleanup failure was not reported with source-integrity reason: %q", result.State.Reason)
	}
	if !reflect.DeepEqual(result.State.Isolation, protocol.ModelHostIsolationEvidence{}) {
		t.Fatalf("invalid isolation evidence escaped result: %+v", result.State.Isolation)
	}
}

func enrichmentTestInput(t *testing.T) (protocol.Snapshot, *SemanticMapIR, EvidencePackRequest) {
	t.Helper()
	const source = "func Submit() {}"
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"checkout.go": source}, "basis-a03")
	if err != nil {
		t.Fatal(err)
	}
	fileHash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "checkout.go", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(fileHash[:]), EnclosingSymbolPath: "Submit"}
	mapIR := testEvidenceMap(snapshot, anchor, "e-a03", "step-a03")
	mapIR.GenerationID = "generation-a03"
	mapIR.Freshness = "current"
	mapIR.Basis = MapBasisContext{RepositoryID: "repo-a03", WorktreeID: "worktree-a03", WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID}
	return snapshot, mapIR, bindCurrentEvidenceRequest(snapshot, mapIR, "step-a03")
}
