package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

func currentEvidenceProof(snapshot protocol.Snapshot, mapIR *SemanticMapIR) *storage.GenerationProofManifest {
	return &storage.GenerationProofManifest{
		SchemaID: GenerationProofSchemaID, SchemaVersion: SemanticSchemaVersion,
		ProofID: "proof-" + mapIR.GenerationID, GenerationID: mapIR.GenerationID,
		ComputedBasisID: mapIR.ComputedBasisID, ComputedSnapshotID: snapshot.SnapshotID,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID, ExpectedLiveHeadSnapshotID: snapshot.SnapshotID,
		WorkspaceEpoch:     snapshot.WorkspaceEpoch,
		CurrentPublication: storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"},
		ArtifactRefs:       storage.ArtifactRefs{SemanticMap: "cas:map", AnalysisReadSet: "cas:read-set", ObservationClosure: "cas:closure", AnalyzerResult: "cas:analyzer"},
	}
}

func bindCurrentEvidenceRequest(snapshot protocol.Snapshot, mapIR *SemanticMapIR, targetStepID string) EvidencePackRequest {
	if snapshot.RepositoryID == "" {
		snapshot.RepositoryID = mapIR.Basis.RepositoryID
	}
	if snapshot.WorktreeID == "" {
		snapshot.WorktreeID = mapIR.Basis.WorktreeID
	}
	proof := currentEvidenceProof(snapshot, mapIR)
	mapBytes, _ := json.Marshal(mapIR)
	proof.ArtifactRefs.SemanticMap = storage.ArtifactCASRef(mapBytes)
	proofBytes, _ := json.Marshal(proof)
	return EvidencePackRequest{
		Map: mapIR, Snapshot: snapshot, CurrentProof: proof, CurrentProofBytes: proofBytes, SemanticMapBytes: mapBytes,
		CurrentPointer:     &storage.ActivePointer{SchemaID: ActivePointerSchemaID, SchemaVersion: SemanticSchemaVersion, GenerationID: mapIR.GenerationID, ManifestObjectRef: storage.ArtifactCASRef(proofBytes), ComputedBasisID: mapIR.ComputedBasisID, ValidatedAgainstSnapshotID: snapshot.SnapshotID, ExpectedLiveHeadSnapshotID: snapshot.SnapshotID, WorkspaceEpoch: snapshot.WorkspaceEpoch, TaskIntentRevision: mapIR.Task.IntentRevision, RepositoryID: mapIR.Basis.RepositoryID, WorktreeID: mapIR.Basis.WorktreeID, TaskID: mapIR.Task.TaskID},
		LiveHeadSnapshotID: snapshot.SnapshotID,
		TargetStepIDs:      []string{targetStepID},
	}
}

func TestBuildEvidencePackV2_RequiresCurrentPublicationBinding(t *testing.T) {
	const source = "func Submit() {}"
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"checkout.go": source}, "basis-binding")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.RepositoryID = "repo-binding"
	snapshot.WorktreeID = "worktree-binding"
	hash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "checkout.go", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(hash[:]), EnclosingSymbolPath: "Submit"}
	mapIR := testEvidenceMap(snapshot, anchor, "e-binding", "step-a01")
	mapIR.Freshness = "current"
	mapIR.Basis = MapBasisContext{RepositoryID: "repo-binding", WorktreeID: "worktree-binding", WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID}
	mapIR.Task = MapTaskContext{TaskID: "task-binding", IntentRevision: 1, Mode: "feature"}

	tests := map[string]func(*EvidencePackRequest){
		"historical map": func(req *EvidencePackRequest) { req.Map.Freshness = "historical" },
		"missing proof":  func(req *EvidencePackRequest) { req.CurrentProof = nil },
		"mismatched active generation": func(req *EvidencePackRequest) {
			req.CurrentPointer.GenerationID = "generation-other"
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			req := bindCurrentEvidenceRequest(snapshot, mapIR, "step-a01")
			mutate(&req)
			if _, err := BuildEvidencePackV2(req); err == nil {
				t.Fatalf("unbound evidence pack was accepted for %s", name)
			}
			mapIR.Freshness = "current"
		})
	}
	if _, err := BuildEvidencePackV2(bindCurrentEvidenceRequest(snapshot, mapIR, "step-a01")); err != nil {
		t.Fatalf("valid current publication binding was rejected: %v", err)
	}
}

func TestBuildEvidencePackV2_RejectsTamperedMapBytesAndProofReference(t *testing.T) {
	const source = "func Submit() {}"
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"checkout.go": source}, "basis-binding-cas")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.RepositoryID = "repo-binding-cas"
	snapshot.WorktreeID = "worktree-binding-cas"
	hash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "checkout.go", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(hash[:]), EnclosingSymbolPath: "Submit"}
	mapIR := testEvidenceMap(snapshot, anchor, "e-binding-cas", "step-cas")
	mapIR.Basis = MapBasisContext{RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID}
	mapIR.Task = MapTaskContext{TaskID: "task-binding-cas", IntentRevision: 1, Mode: "feature"}

	validRequest := func() EvidencePackRequest {
		req := bindCurrentEvidenceRequest(snapshot, mapIR, "step-cas")
		mapBytes, marshalErr := json.Marshal(mapIR)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req.SemanticMapBytes = mapBytes
		req.CurrentProof.ArtifactRefs.SemanticMap = storage.ArtifactCASRef(mapBytes)
		proofBytes, marshalErr := json.Marshal(req.CurrentProof)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req.CurrentProofBytes = proofBytes
		req.CurrentPointer.ManifestObjectRef = storage.ArtifactCASRef(proofBytes)
		return req
	}

	if _, err := BuildEvidencePackV2(validRequest()); err != nil {
		t.Fatalf("valid exact artifact binding was rejected: %v", err)
	}
	t.Run("same identity but tampered map bytes", func(t *testing.T) {
		req := validRequest()
		var tampered SemanticMapIR
		if err := json.Unmarshal(req.SemanticMapBytes, &tampered); err != nil {
			t.Fatal(err)
		}
		tampered.Summary.Current = "tampered explanation"
		req.SemanticMapBytes, err = json.Marshal(tampered)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BuildEvidencePackV2(req); err == nil {
			t.Fatal("tampered semantic-map bytes were accepted")
		}
	})
	t.Run("different proof object reference", func(t *testing.T) {
		req := validRequest()
		req.CurrentPointer.ManifestObjectRef = storage.ArtifactCASRef([]byte(`{"proof":"different"}`))
		if _, err := BuildEvidencePackV2(req); err == nil {
			t.Fatal("pointer reference to a different proof object was accepted")
		}
	})
}

func TestBuildEvidencePackV2_RejectsSuppliedMapOrProofStructDrift(t *testing.T) {
	const source = "func Submit() {}"
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"checkout.go": source}, "basis-binding-struct")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.RepositoryID = "repo-binding-struct"
	snapshot.WorktreeID = "worktree-binding-struct"
	hash := sha256.Sum256([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "checkout.go", ByteRange: [2]int{0, len(source)}, FileHash: hex.EncodeToString(hash[:]), EnclosingSymbolPath: "Submit"}
	mapIR := testEvidenceMap(snapshot, anchor, "e-binding-struct", "step-struct")
	mapIR.Basis = MapBasisContext{RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID}
	mapIR.Task = MapTaskContext{TaskID: "task-binding-struct", IntentRevision: 1, Mode: "feature"}

	t.Run("supplied map content differs from exact artifact bytes", func(t *testing.T) {
		req := bindCurrentEvidenceRequest(snapshot, mapIR, "step-struct")
		req.Map.Summary.Current = "caller-only mutation"
		if _, err := BuildEvidencePackV2(req); err == nil {
			t.Fatal("supplied semantic-map struct drift was accepted")
		}
	})
	t.Run("supplied proof non-identity fields differ from exact artifact bytes", func(t *testing.T) {
		req := bindCurrentEvidenceRequest(snapshot, mapIR, "step-struct")
		req.CurrentProof.ArtifactRefs.AnalysisReadSet = "cas:caller-only-mutation"
		if _, err := BuildEvidencePackV2(req); err == nil {
			t.Fatal("supplied proof struct drift was accepted")
		}
	})
}
