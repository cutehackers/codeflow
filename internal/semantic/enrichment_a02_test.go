package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
)

func TestBuildEvidencePackV2_RedactsJSONSecretsBeforeEgress(t *testing.T) {
	const source = `{"apiKey":"super-secret-value","nested":{"password":"another-secret"}}`
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"config.json": source}, "basis-a02")
	if err != nil {
		t.Fatal(err)
	}
	fileHash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "config.json", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(fileHash[:]), EnclosingSymbolPath: "config"}
	mapIR := testEvidenceMap(snapshot, anchor, "e-secret", "secret-step")
	pack, err := BuildEvidencePackV2(bindCurrentEvidenceRequest(snapshot, mapIR, "secret-step"))
	if err != nil {
		t.Fatal(err)
	}
	item := pack.Items[0]
	if strings.Contains(item.Content, "super-secret-value") || strings.Contains(item.Content, "another-secret") {
		t.Fatalf("secret leaked into pack: %q", item.Content)
	}
	if pack.RedactionStatus != "redacted" {
		t.Fatalf("redaction status = %q, want redacted", pack.RedactionStatus)
	}
	if err := ValidateEvidencePackV2(pack); err != nil {
		t.Fatalf("redacted pack failed validation: %v", err)
	}
}

func TestBuildEvidencePackV2_RedactsAndValidatesPlaintextSource(t *testing.T) {
	const source = `const token = "super-secret-value";`
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"checkout.go": source}, "basis-a02-plaintext")
	if err != nil {
		t.Fatal(err)
	}
	fileHash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "checkout.go", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(fileHash[:]), EnclosingSymbolPath: "checkout"}
	mapIR := testEvidenceMap(snapshot, anchor, "e-plaintext-secret", "plaintext-secret-step")
	pack, err := BuildEvidencePackV2(bindCurrentEvidenceRequest(snapshot, mapIR, "plaintext-secret-step"))
	if err != nil {
		t.Fatal(err)
	}
	if pack.RedactionStatus != "redacted" || strings.Contains(pack.Items[0].Content, "super-secret-value") {
		t.Fatalf("plaintext secret was not safely redacted: status=%q content=%q", pack.RedactionStatus, pack.Items[0].Content)
	}
	if err := ValidateEvidencePackV2(pack); err != nil {
		t.Fatalf("redacted plaintext pack failed validation: %v", err)
	}
	pack.RedactionStatus = "invalid"
	if err := ValidateEvidencePackV2(pack); err == nil {
		t.Fatal("invalid redaction status was accepted")
	}
}

func TestBuildEvidencePackV2_RejectsUnsafePathBeforeEgress(t *testing.T) {
	const source = "func unsafe() {}"
	snapshot := protocol.Snapshot{SnapshotID: "snapshot-a02-unsafe", ComputedBasisID: "basis-a02-unsafe", Files: map[string]string{"../escape.go": source}}
	fileHash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "../escape.go", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(fileHash[:]), EnclosingSymbolPath: "unsafe"}
	mapIR := testEvidenceMap(snapshot, anchor, "e-unsafe", "unsafe-step")
	if _, err := BuildEvidencePackV2(bindCurrentEvidenceRequest(snapshot, mapIR, "unsafe-step")); err == nil {
		t.Fatal("unsafe repository path was accepted")
	}
}

func testEvidenceMap(snapshot protocol.Snapshot, anchor slicing.Anchor, evidenceID, stepID string) *SemanticMapIR {
	return &SemanticMapIR{
		SchemaID: SemanticMapSchemaID, SchemaVersion: SemanticSchemaVersion,
		MapID: "map-a02", GenerationID: "generation-a02", ComputedBasisID: snapshot.ComputedBasisID,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID, Freshness: "current", EnrichmentStatus: "not_requested",
		Task:     MapTaskContext{TaskID: "task-a02", IntentRevision: 1, Mode: "feature"},
		Basis:    MapBasisContext{RepositoryID: "repo-a02", WorktreeID: "worktree-a02", WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID},
		Steps:    []SemanticStep{{StepID: stepID, Name: anchor.EnclosingSymbolPath, Anchor: anchor, EvidenceRefs: []string{evidenceID}}},
		Evidence: []SemanticEvidence{{EvidenceID: evidenceID, Kind: "source", SourceAuthority: "code", ComputedBasisID: snapshot.ComputedBasisID, SnapshotID: snapshot.SnapshotID, Anchor: anchor, ValidationStatus: "verified"}},
	}
}
