package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/protocol"
)

func TestRunSemanticEnrichment_ValidatesProposalTargetEvidenceBasisAndAuthority(t *testing.T) {
	snapshot, mapIR, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	valid := ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-a05",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "fake", ModelRevision: "r1", PromptRevision: "prompt-a05", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{pack.Items[0].EvidenceID}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	}
	data, _ := json.Marshal(valid)
	host := &fakeModelHost{
		capability: measuredFakeCapability(),
		response:   protocol.ModelHostResponse{SchemaID: ModelHostResponseV2SchemaID, SchemaVersion: 2, RequestID: "enrichment-" + pack.EvidencePackID, Status: "accepted", Proposal: data},
	}
	result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host, PromptRevision: "prompt-a05", MaxAttempts: 1})
	if result.State.Status != "available" || result.Proposal == nil {
		t.Fatalf("valid proposal was not exposed: %+v", result)
	}

	invalid := valid
	invalid.TargetStepID = "missing-step"
	ctx := ProposalValidationContext{Map: mapIR, Pack: pack, TargetStepIDs: pack.TargetStepIDs, TargetSymbolPath: pack.TargetSymbolPath, ExpectedPackDigest: pack.PackDigest, ExpectedModelID: "fake", ExpectedModelRevision: "r1", ExpectedPromptRevision: "prompt-a05", ExpectedSchemaProfile: SemanticProposalSchemaProfile}
	if err := ValidateModelProposalV2(&invalid, ctx); err == nil {
		t.Fatal("nonexistent target was accepted")
	}
	invalid = valid
	invalid.EvidenceRefs = []string{"missing-evidence"}
	if err := ValidateModelProposalV2(&invalid, ctx); err == nil {
		t.Fatal("nonexistent evidence reference was accepted")
	}
	invalid = valid
	invalid.Authority = "verified"
	if err := ValidateModelProposalV2(&invalid, ctx); err == nil {
		t.Fatal("verified authority was accepted for a model proposal")
	}
	invalid = valid
	invalid.ComputedBasisID = "other-basis"
	if err := ValidateModelProposalV2(&invalid, ctx); err == nil {
		t.Fatal("mismatched basis was accepted")
	}
}

func TestRunSemanticEnrichment_InvalidProposalKeepsDeterministicResult(t *testing.T) {
	_, mapIR, request := enrichmentTestInput(t)
	before, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	host := &fakeModelHost{capability: measuredFakeCapability(), response: protocol.ModelHostResponse{SchemaID: ModelHostResponseV2SchemaID, SchemaVersion: 2, Status: "accepted", Proposal: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json","schemaVersion":2,"authority":"verified"}`)}}
	result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host, PromptRevision: "prompt-invalid"})
	if result.State.Status != "unavailable" || result.Proposal != nil || result.Fallback == nil || len(host.requests) != 2 {
		t.Fatalf("invalid proposal result = %+v, requests=%d", result, len(host.requests))
	}
	after, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || mapIR.EnrichmentStatus != "not_requested" {
		t.Fatalf("invalid proposal mutated deterministic map: before=%+v after=%+v map=%+v", before, after, mapIR)
	}
}

func TestRunSemanticEnrichment_InvalidProposalUsesCorrectionRetry(t *testing.T) {
	_, mapIR, request := enrichmentTestInput(t)
	host := &fakeModelHost{capability: measuredFakeCapability(), response: protocol.ModelHostResponse{
		SchemaID: ModelHostResponseV2SchemaID, SchemaVersion: 2, Status: "accepted",
		Proposal: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json","schemaVersion":2}`),
	}}
	request.PromptRevision = "prompt-f8"
	result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host, PromptRevision: "prompt-f8", MaxAttempts: 2})
	if result.State.Status != "unavailable" || result.Fallback == nil || len(host.requests) != 2 {
		t.Fatalf("invalid correction retry result = %+v, requests=%d", result, len(host.requests))
	}
	first, err := json.Marshal(host.requests[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(host.requests[1])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("invalid proposal retry repeated an identical request")
	}
	var wire map[string]any
	if err := json.Unmarshal(second, &wire); err != nil {
		t.Fatal(err)
	}
	if attempt, ok := wire["correctionAttempt"].(float64); !ok || attempt != 1 {
		t.Fatalf("correction attempt metadata = %#v", wire["correctionAttempt"])
	}
	if reason, ok := wire["correctionReason"].(string); !ok || reason == "" || len(reason) > 512 {
		t.Fatalf("correction reason metadata = %#v", wire["correctionReason"])
	}
	if target, ok := wire["correctionFor"].(string); !ok || target == "" {
		t.Fatalf("correction target metadata = %#v", wire["correctionFor"])
	}
	if _, ok := wire["repositoryPath"]; ok {
		t.Fatal("correction request exposed a repository path")
	}
	_ = mapIR
}

func TestValidateModelProposalV2_RequiresExactProvenance(t *testing.T) {
	snapshot, mapIR, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal := &ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-provenance",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		EvidenceRefs: []string{pack.Items[0].EvidenceID}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	}
	if err := ValidateModelProposalV2(proposal, ProposalValidationContext{Map: mapIR, Pack: pack, TargetStepIDs: pack.TargetStepIDs, TargetSymbolPath: pack.TargetSymbolPath}); err == nil {
		t.Fatal("proposal with empty provenance was accepted")
	}
	proposal.PackDigest = pack.PackDigest
	proposal.ModelID = "fake"
	proposal.ModelRevision = "r1"
	proposal.PromptRevision = "prompt-provenance"
	proposal.SchemaProfile = SemanticProposalSchemaProfile
	ctx := ProposalValidationContext{Map: mapIR, Pack: pack, TargetStepIDs: pack.TargetStepIDs, TargetSymbolPath: pack.TargetSymbolPath, ExpectedPackDigest: pack.PackDigest, ExpectedModelID: "fake", ExpectedModelRevision: "r1", ExpectedPromptRevision: "prompt-provenance", ExpectedSchemaProfile: SemanticProposalSchemaProfile}
	if err := ValidateModelProposalV2(proposal, ctx); err != nil {
		t.Fatalf("complete provenance rejected: %v", err)
	}
	for name, mutate := range map[string]func(*ModelProposal){
		"pack digest":     func(p *ModelProposal) { p.PackDigest = "0" + pack.PackDigest[1:] },
		"model id":        func(p *ModelProposal) { p.ModelID = "other-model" },
		"model revision":  func(p *ModelProposal) { p.ModelRevision = "other-revision" },
		"prompt revision": func(p *ModelProposal) { p.PromptRevision = "other-prompt" },
		"schema profile":  func(p *ModelProposal) { p.SchemaProfile = "other-profile" },
	} {
		mutated := *proposal
		mutate(&mutated)
		if err := ValidateModelProposalV2(&mutated, ctx); err == nil {
			t.Fatalf("%s provenance mismatch was accepted", name)
		}
	}
}

func TestValidateEvidencePackV2_RejectsTamperedContentRangeIdentityAndDigest(t *testing.T) {
	_, _, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*EvidencePack){
		"content digest":     func(value *EvidencePack) { value.Items[0].ContentDigest = "0" + value.Items[0].ContentDigest[1:] },
		"content bytes":      func(value *EvidencePack) { value.Items[0].Content += " changed" },
		"negative range":     func(value *EvidencePack) { value.Items[0].ByteRange = [2]int{-1, 2} },
		"reversed range":     func(value *EvidencePack) { value.Items[0].ByteRange = [2]int{4, 2} },
		"item snapshot":      func(value *EvidencePack) { value.Items[0].SnapshotID = "snapshot-other" },
		"item basis":         func(value *EvidencePack) { value.Items[0].ComputedBasisID = "basis-other" },
		"missing target":     func(value *EvidencePack) { value.TargetStepIDs = nil },
		"missing scope":      func(value *EvidencePack) { value.ScopePaths = nil },
		"missing repository": func(value *EvidencePack) { value.RepositoryID = "" },
		"missing worktree":   func(value *EvidencePack) { value.WorktreeID = "" },
		"negative epoch":     func(value *EvidencePack) { value.WorkspaceEpoch = -1 },
		"pack identity":      func(value *EvidencePack) { value.EvidencePackID = "pack-tampered" },
		"pack digest":        func(value *EvidencePack) { value.PackDigest = "0" + value.PackDigest[1:] },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			copy := *pack
			copy.Items = append([]EvidenceItem(nil), pack.Items...)
			mutate(&copy)
			if name != "pack identity" && name != "pack digest" {
				copy.EvidencePackID = deterministicPackID(&copy)
				copy.PackDigest = digestPack(&copy)
			}
			if err := ValidateEvidencePackV2(&copy); err == nil {
				t.Fatalf("tampered evidence pack was accepted: %s", name)
			}
		})
	}
}

func measuredFakeCapability() protocol.ModelHostCapability {
	probe := &protocol.ModelHostIsolationProbe{RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", SentinelBeforeDigest: "sha256:" + strings.Repeat("b", 64), SentinelAfterDigest: "sha256:" + strings.Repeat("b", 64), SentinelUnchanged: true, NetworkAttempt: "blocked"}
	return protocol.ModelHostCapability{Status: "measured", Measured: true, SchemaConstrained: true, Cancellation: true, MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, ModelID: "fake", Revision: "r1", License: "MIT", Checksum: "sha256:fake", Runtime: "test", DataBoundary: "local-only", IsolationBackend: "fake-sandbox", IsolationEnforced: true, IsolationProbe: probe, NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("a", 64), ResourceLimits: fakeResourceLimitEvidence()}
}

func fakeResourceLimitEvidence() *protocol.ModelHostResourceLimitEvidence {
	limits := protocol.DefaultModelHostResourceLimits()
	applied := limits
	applied.ProcessCount = 1
	return &protocol.ModelHostResourceLimitEvidence{
		Version: protocol.ModelHostResourceLimitsVersion, Declared: limits, Applied: applied,
		EnforcementStatus: protocol.ModelHostResourceEnforcementEnforced,
		Backend:           protocol.ModelHostResourceBackendDarwinHostTree,
	}
}
