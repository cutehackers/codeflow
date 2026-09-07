package semantic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/protocol"
)

func TestEnrichmentEgressIdentityBindsResourceLimits(t *testing.T) {
	capability := measuredFakeCapability()
	capabilityCopy := capability
	capabilityEvidence := *capability.ResourceLimits
	capabilityCopy.ResourceLimits = &capabilityEvidence
	capabilityCopy.ResourceLimits.Applied.MemoryBytes++
	if sameModelHostCapabilityIdentity(capability, capabilityCopy) {
		t.Fatal("capability egress identity ignored an applied memory-limit mutation")
	}

	isolation := completeFakeIsolation("sha256:"+strings.Repeat("a", 64), "success")
	isolationCopy := isolation
	isolationEvidence := *isolation.ResourceLimits
	isolationCopy.ResourceLimits = &isolationEvidence
	isolationCopy.ResourceLimits.Applied.ProcessCount++
	if sameIsolationIdentity(isolation, isolationCopy) {
		t.Fatal("isolation egress identity ignored an applied process-count mutation")
	}
	isolationCopy = isolation
	isolationCopy.RepositoryWriteAuditStatus = protocol.ModelHostRepositoryWriteAuditIndeterminate
	if sameIsolationIdentity(isolation, isolationCopy) {
		t.Fatal("isolation egress identity ignored a repository-write audit status mutation")
	}
}

func TestRunSemanticEnrichmentRejectsCompleteValueSpoofingHostBeforeEgress(t *testing.T) {
	result := completeSpoofedEnrichmentResult(t, true)
	if result.State.Status == "available" && result.Proposal != nil {
		if data, egressErr := MarshalEnrichmentResultEgress(&result); egressErr == nil && len(data) > 0 {
			t.Fatalf("complete value-spoofing host reached available public egress: %s", data)
		}
	}
	if result.State.Status != "unavailable" || result.Proposal != nil || result.View != nil || result.Fallback == nil {
		t.Fatalf("complete value-spoofing host was exposed: %+v", result)
	}
}

func TestValidateEnrichmentResultRejectsUnattestedAvailableValue(t *testing.T) {
	result := completeSpoofedEnrichmentResult(t, false)
	if result.State.Status != "available" || result.Proposal == nil {
		t.Fatalf("test-only pure seam did not produce the adversarial available value: %+v", result)
	}
	if err := ValidateEnrichmentState(result.State); err != nil {
		t.Fatalf("complete spoof state failed value validation before attestation: %v", err)
	}
	if err := ValidateEnrichmentResult(&result); err == nil {
		t.Fatal("typed validator accepted a complete value-spoofed available result")
	} else if !strings.Contains(err.Error(), "Core-supervised host authority") {
		t.Fatalf("typed validator failed for a reason other than missing Core authority: %v", err)
	}
	if _, err := MarshalEnrichmentResultEgress(&result); err == nil {
		t.Fatal("public egress accepted a complete value-spoofed available result")
	} else if !strings.Contains(err.Error(), "Core-supervised host authority") {
		t.Fatalf("public egress failed for a reason other than missing Core authority: %v", err)
	}
}

func completeSpoofedEnrichmentResult(t *testing.T, production bool) EnrichmentResult {
	t.Helper()
	snapshot, mapIR, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := json.Marshal(ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-f22",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "fake", ModelRevision: "r1", PromptRevision: "prompt-f22", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	})
	if err != nil {
		t.Fatal(err)
	}
	capability := measuredFakeCapability()
	probe := *capability.IsolationProbe
	probe.DisposableWriteAttempt = "blocked"
	capability.IsolationProbe = &probe
	host := &fakeModelHost{
		capability: capability,
		response: protocol.ModelHostResponse{
			SchemaID: ModelHostResponseV2SchemaID, SchemaVersion: 2,
			RequestID: "enrichment-" + pack.EvidencePackID, Status: "accepted", Proposal: proposal,
		},
		isolation: completeFakeIsolation(pack.PackDigest, "success"),
	}
	if production {
		return RunSemanticEnrichment(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host, PromptRevision: "prompt-f22"})
	}
	return runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host, PromptRevision: "prompt-f22"})
}
