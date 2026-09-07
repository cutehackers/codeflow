package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
)

func TestBuildEvidencePackV2_CurrentVerifiedScopedEvidence(t *testing.T) {
	const source = "package checkout\n\nfunc Submit() { return }\n"
	fileHash := sha256.Sum256([]byte(source))
	snapshot, err := protocol.NewSnapshot(7, map[string]string{"internal/checkout.go": source}, "basis-a01")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot.RepositoryID = "repo-a01"
	snapshot.WorktreeID = "worktree-a01"
	anchor := slicing.Anchor{
		RepoRelativePath:    "internal/checkout.go",
		ByteRange:           [2]int{0, len([]byte(source))},
		FileHash:            hex.EncodeToString(fileHash[:]),
		EnclosingSymbolPath: "Submit",
	}
	mapIR := &SemanticMapIR{
		SchemaID:                   SemanticMapSchemaID,
		SchemaVersion:              SemanticSchemaVersion,
		MapID:                      "map-a01",
		GenerationID:               "generation-a01",
		ComputedBasisID:            snapshot.ComputedBasisID,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID,
		PublicationKind:            "checkpoint",
		Freshness:                  "current",
		Settlement:                 "passed",
		EnrichmentStatus:           "not_requested",
		Authority:                  "candidate",
		Task:                       MapTaskContext{TaskID: "task-a01", IntentRevision: 1, Mode: "feature"},
		Basis:                      MapBasisContext{RepositoryID: "repo-a01", WorktreeID: "worktree-a01", WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID},
		Steps: []SemanticStep{{
			StepID: "submit", Name: "Submit", Anchor: anchor, EvidenceRefs: []string{"e-submit"},
		}},
		Evidence: []SemanticEvidence{{
			EvidenceID: "e-submit", Kind: "source", SourceAuthority: "code",
			ComputedBasisID: snapshot.ComputedBasisID, SnapshotID: snapshot.SnapshotID,
			Anchor: anchor, ValidationStatus: "verified", RedactionStatus: "clean",
		}},
	}

	request := bindCurrentEvidenceRequest(snapshot, mapIR, "submit")
	request.MaxItems = 4
	request.MaxBytes = 4096
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatalf("build bounded pack: %v", err)
	}
	if pack.SchemaID != EvidencePackV2SchemaID || pack.SchemaVersion != 2 {
		t.Fatalf("pack schema = %s/%d, want v2", pack.SchemaID, pack.SchemaVersion)
	}
	if len(pack.Items) != 1 || pack.Items[0].EvidenceID != "e-submit" {
		t.Fatalf("pack items = %+v, want only current target evidence", pack.Items)
	}
	if !strings.Contains(pack.Items[0].Content, "func Submit") {
		t.Fatalf("pack content was not sourced from the captured snapshot: %q", pack.Items[0].Content)
	}
	if pack.Items[0].Content == "verified code anchor" || pack.Items[0].Source != "internal/checkout.go" {
		t.Fatalf("pack contains synthesized or unscoped content: %+v", pack.Items[0])
	}
}
