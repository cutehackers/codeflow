package flowview

import (
	"context"
	"strings"
	"testing"

	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func TestChangeImpactBatchResolvesOnlyCommittedRevisionBackedSteps(t *testing.T) {
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	tx, err := srv.engine.BeginTransaction(workspace.SourceAgentTransaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.StageEdit(workspace.EditRequest{Path: "src/order.go", Content: []byte("package order\n"), DocumentVersion: 1}); err != nil {
		t.Fatal(err)
	}
	batch, snapshot, err := srv.engine.CommitTransaction(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	basis := strings.Repeat("b", 64)
	mapIR := &semantic.SemanticMapIR{
		SchemaID:                   semantic.SemanticMapSchemaID,
		SchemaVersion:              2,
		MapID:                      "map-batch",
		GenerationID:               "generation-batch",
		ComputedBasisID:            basis,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID,
		Basis:                      semantic.MapBasisContext{WorkspaceEpoch: batch.WorkspaceEpoch},
		Steps: []semantic.SemanticStep{{
			StepID:        "step-order",
			TechnicalName: "Order.Update",
			Anchor:        slicing.Anchor{RepoRelativePath: "src/order.go"},
		}},
	}
	delta := &semantic.SemanticDeltaIR{
		ComparisonID:                      "comparison-batch",
		CurrentComputedBasisID:            basis,
		CurrentValidatedAgainstSnapshotID: snapshot.SnapshotID,
		ToGeneration:                      mapIR.GenerationID,
		Status:                            "comparable",
		Changes: []semantic.DeltaChange{{
			DeltaID:          "delta-order",
			TargetStepID:     "step-order",
			ToStepID:         "step-order",
			EpistemicStatus:  "observed",
			ValidationStatus: "verified",
		}},
	}
	refs, err := srv.impactBatchStepRefs(semantic.ImpactTarget{ChangeBatchID: batch.BatchID}, delta, mapIR)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0] != "step-order" {
		t.Fatalf("unexpected change batch step refs: %v", refs)
	}

	mapIR.Steps[0].Anchor.RepoRelativePath = "src/unrelated.go"
	if _, err := srv.impactBatchStepRefs(semantic.ImpactTarget{ChangeBatchID: batch.BatchID}, delta, mapIR); err == nil || !strings.Contains(err.Error(), "not_observed") {
		t.Fatalf("unrelated delta step must not inherit change batch identity: %v", err)
	}
}
