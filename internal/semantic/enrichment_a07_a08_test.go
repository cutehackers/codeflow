package semantic

import (
	"context"
	"reflect"
	"testing"

	"codeflow/internal/protocol"
)

func TestRunSemanticEnrichment_FailureUpdatesOnlyEnrichmentState(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		err        error
		wantStatus string
	}{
		{name: "timeout", err: protocol.TimeoutError("test timeout"), wantStatus: "timed_out"},
		{name: "crash", err: protocol.CrashedError("test crash"), wantStatus: "unavailable"},
		{name: "cancel", err: protocol.CancelledError("test cancel"), wantStatus: "unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, mapIR, request := enrichmentTestInput(t)
			before, err := CanonicalQ3Digests(mapIR)
			if err != nil {
				t.Fatal(err)
			}
			host := &fakeModelHost{capability: measuredFakeCapability(), err: testCase.err}
			result := runSemanticEnrichmentForTest(context.Background(), EnrichmentRequest{EvidencePack: request, modelHost: host})
			if result.State.Status != testCase.wantStatus || result.Fallback == nil {
				t.Fatalf("failure result = %+v", result)
			}
			after, err := CanonicalQ3Digests(mapIR)
			if err != nil {
				t.Fatal(err)
			}
			if before != after || mapIR.EnrichmentStatus != "not_requested" {
				t.Fatalf("failure changed deterministic map: before=%+v after=%+v status=%q", before, after, mapIR.EnrichmentStatus)
			}
		})
	}
}

func TestAcceptSemanticProposal_PreservesQ3CanonicalFields(t *testing.T) {
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
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-a08",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "fake", ModelRevision: "r1", PromptRevision: "prompt-a08", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{pack.Items[0].EvidenceID}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	}
	before := struct {
		Steps      []SemanticStep
		Edges      []SemanticEdge
		Evidence   []SemanticEvidence
		Quality    MapQuality
		Alignment  []RequirementAlignment
		Settlement string
	}{append([]SemanticStep(nil), mapIR.Steps...), append([]SemanticEdge(nil), mapIR.Edges...), append([]SemanticEvidence(nil), mapIR.Evidence...), mapIR.Quality, append([]RequirementAlignment(nil), mapIR.RequirementAlignment...), mapIR.Settlement}
	view, err := AcceptSemanticProposal(mapIR, proposal, pack)
	if err != nil {
		t.Fatal(err)
	}
	if view.Map.EnrichmentStatus != "available" || mapIR.EnrichmentStatus != "not_requested" {
		t.Fatalf("proposal acceptance statuses: view=%q source=%q", view.Map.EnrichmentStatus, mapIR.EnrichmentStatus)
	}
	if !reflect.DeepEqual(before.Steps, view.Map.Steps) || !reflect.DeepEqual(before.Edges, view.Map.Edges) || !reflect.DeepEqual(before.Evidence, view.Map.Evidence) || !reflect.DeepEqual(before.Quality, view.Map.Quality) || !reflect.DeepEqual(before.Alignment, view.Map.RequirementAlignment) || before.Settlement != view.Map.Settlement {
		t.Fatalf("proposal acceptance changed Q3 fields")
	}
	if view.Digests != digests {
		t.Fatalf("view digests = %+v, want %+v", view.Digests, digests)
	}
}
