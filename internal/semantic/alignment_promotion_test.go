package semantic

import (
	"encoding/json"
	"testing"
	"time"

	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

func TestPromoteRequirementAlignmentsWithCurrentProofRequiresExactProofAndEvidence(t *testing.T) {
	makeFixture := func() (*SemanticMapIR, []AcceptanceCriterion, *storage.GenerationProofManifest, *storage.ActivePointer) {
		m := &SemanticMapIR{
			SchemaID: SemanticMapSchemaID, SchemaVersion: SemanticSchemaVersion,
			MapID: "map-a15", GenerationID: "generation-a15", ComputedBasisID: "basis-a15",
			ValidatedAgainstSnapshotID: "snapshot-computed-a15", PublicationKind: "checkpoint", Freshness: "historical", Settlement: "passed", EnrichmentStatus: "not_requested", Authority: "candidate",
			Quality:  MapQuality{Stage: "Q3", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0},
			Task:     MapTaskContext{TaskID: "task-a15", IntentRevision: 4, Mode: "feature"},
			Basis:    MapBasisContext{RepositoryID: "repository-a15", WorktreeID: "worktree-a15", WorkspaceEpoch: 2, ComputedWorkspaceSnapshotID: "snapshot-computed-a15", ComputedBasisID: "basis-a15", AnalysisReadSetID: "readset-a15", CausalObservationClosureID: "closure-a15"},
			Summary:  MapSummary{Requested: "submit", Current: "submit is implemented"},
			Steps:    []SemanticStep{{StepID: "step-submit", StructuralIdentity: "service.go#Submit:call", Ordinal: 1, Name: "Submit", Rules: []string{"AC-submit"}, EvidenceRefs: []string{"evidence-source"}, Anchor: slicing.Anchor{RepoRelativePath: "service.go", EnclosingSymbolPath: "service.go#Submit", ByteRange: [2]int{0, 10}}}},
			Evidence: []SemanticEvidence{{EvidenceID: "evidence-source", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-a15", SnapshotID: "snapshot-computed-a15", ValidationStatus: "verified", Anchor: slicing.Anchor{RepoRelativePath: "service.go", EnclosingSymbolPath: "service.go#Submit", ByteRange: [2]int{0, 10}}}},
			Unknowns: []fusion.Unknown{},
		}
		criteria := []AcceptanceCriterion{{ID: "AC-submit", Text: "Submit", RequiredEvidenceKinds: []string{"source"}}}
		mapBytes, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		manifest := &storage.GenerationProofManifest{
			SchemaID: GenerationProofSchemaID, SchemaVersion: SemanticSchemaVersion, ProofID: "proof-a15", GenerationID: m.GenerationID,
			ComputedBasisID: m.ComputedBasisID, ComputedSnapshotID: m.Basis.ComputedWorkspaceSnapshotID, ValidatedAgainstSnapshotID: "snapshot-live-a15", TaskIntentRevision: m.Task.IntentRevision,
			NormalizedQueryHash: "query-a15", AnalysisReadSetID: "readset-a15", CausalObservationClosureID: "closure-a15", CausalObservationClosureDigest: "closure-digest-a15", WorkspaceEpoch: m.Basis.WorkspaceEpoch,
			CurrentPublication:   storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"},
			SettlementEvaluation: storage.SettlementEvaluation{Gate: "passed", BlockingObligationRefs: []string{}}, ArtifactRefs: storage.ArtifactRefs{SemanticMap: storage.ArtifactCASRef(mapBytes)}, ExpectedLiveHeadSnapshotID: "snapshot-live-a15", PublishedAt: time.Unix(1, 0).UTC(),
		}
		pointer := &storage.ActivePointer{SchemaID: ActivePointerSchemaID, SchemaVersion: SemanticSchemaVersion, GenerationID: m.GenerationID, ComputedBasisID: m.ComputedBasisID, ValidatedAgainstSnapshotID: "snapshot-live-a15", ExpectedLiveHeadSnapshotID: "snapshot-live-a15", WorkspaceEpoch: m.Basis.WorkspaceEpoch, TaskIntentRevision: m.Task.IntentRevision, NormalizedQueryHash: "query-a15", FlowCount: 1, RepositoryID: m.Basis.RepositoryID, WorktreeID: m.Basis.WorktreeID, TaskID: m.Task.TaskID, PublishedAt: time.Unix(1, 0).UTC()}
		return m, criteria, manifest, pointer
	}

	t.Run("valid current proof promotes", func(t *testing.T) {
		m, criteria, manifest, pointer := makeFixture()
		got := PromoteRequirementAlignmentsWithCurrentProof(criteria, m, manifest, pointer, AlignmentOptions{})
		if len(got) != 1 || got[0].Status != "confirmed" || got[0].Authority != "current_proof" || got[0].Reason != "" {
			t.Fatalf("valid current proof did not promote alignment: %+v", got)
		}
	})

	tests := map[string]func(*SemanticMapIR, *storage.GenerationProofManifest, *storage.ActivePointer){
		"basis mismatch": func(_ *SemanticMapIR, manifest *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			manifest.ComputedBasisID = "other-basis"
		},
		"generation mismatch": func(_ *SemanticMapIR, _ *storage.GenerationProofManifest, pointer *storage.ActivePointer) {
			pointer.GenerationID = "other-generation"
		},
		"query mismatch": func(_ *SemanticMapIR, _ *storage.GenerationProofManifest, pointer *storage.ActivePointer) {
			pointer.NormalizedQueryHash = "other-query"
		},
		"intent mismatch": func(_ *SemanticMapIR, manifest *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			manifest.TaskIntentRevision++
		},
		"artifact mismatch": func(_ *SemanticMapIR, manifest *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			manifest.ArtifactRefs.SemanticMap = "cas:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		},
		"missing evidence": func(m *SemanticMapIR, _ *storage.GenerationProofManifest, _ *storage.ActivePointer) { m.Evidence = nil },
		"stale evidence snapshot": func(m *SemanticMapIR, _ *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			m.Evidence[0].SnapshotID = "stale-snapshot"
		},
		"conflicting evidence": func(m *SemanticMapIR, _ *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			m.Evidence[0].ValidationStatus = "conflicting"
		},
		"untrusted evidence source": func(m *SemanticMapIR, _ *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			m.Evidence[0].SourceAuthority = "model"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			m, criteria, manifest, pointer := makeFixture()
			mutate(m, manifest, pointer)
			got := PromoteRequirementAlignmentsWithCurrentProof(criteria, m, manifest, pointer, AlignmentOptions{})
			if len(got) != 1 || got[0].Status == "confirmed" {
				t.Fatalf("invalid proof/evidence promoted alignment: %+v", got)
			}
		})
	}

	t.Run("missing proof", func(t *testing.T) {
		m, criteria, _, pointer := makeFixture()
		got := PromoteRequirementAlignmentsWithCurrentProof(criteria, m, nil, pointer, AlignmentOptions{})
		if len(got) != 1 || got[0].Status == "confirmed" {
			t.Fatalf("missing proof promoted alignment: %+v", got)
		}
	})
}
