package storage_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/evidence"
	"codeflow/internal/storage"
)

func TestPublicationTransactionNoGhostEventOnBoundaryFailure(t *testing.T) {
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	prev := ""
	tx := publicationTx("gen-1", "snap-1", &prev)
	liveHead := "snap-1"
	tx.LiveHeadCommit = publicationAuthority(&liveHead)
	st.SetPublicationFault("pointer")
	if _, err := st.PublishGeneration(tx); err == nil {
		t.Fatal("pointer failure must reject publication")
	}
	st.SetPublicationFault("")
	if ptr, err := st.ReadActivePointer(); err != nil || ptr != nil {
		t.Fatalf("failed publication exposed pointer=%+v err=%v", ptr, err)
	}
	ledgerPath := filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl")
	if data, err := os.ReadFile(ledgerPath); err == nil && len(data) != 0 {
		t.Fatalf("failed publication exposed ghost events: %s", data)
	}
	if _, err := st.ReadManifestCAS("cas:sha256:" + strings.Repeat("a", 64)); err == nil {
		t.Fatal("failed publication exposed a manifest")
	}

	commit, err := st.PublishGeneration(tx)
	if err != nil {
		t.Fatalf("retry should succeed: %v", err)
	}
	if commit.ManifestRef == "" || commit.Pointer == nil {
		t.Fatalf("missing commit refs: %+v", commit)
	}
	if ptr, err := st.ReadActivePointer(); err != nil || ptr == nil || ptr.GenerationID != "gen-1" {
		t.Fatalf("committed pointer missing: %+v err=%v", ptr, err)
	}
	if _, err := st.ReadActiveProofManifest(); err != nil {
		t.Fatalf("committed manifest missing: %v", err)
	}
	if manifest, ptr, err := st.ReadValidatedActiveProofManifest(); err != nil || manifest == nil || ptr == nil {
		t.Fatalf("committed proof bundle is not restart-valid: manifest=%+v pointer=%+v err=%v", manifest, ptr, err)
	}
	data, err := os.ReadFile(ledgerPath)
	if err != nil || !strings.Contains(string(data), "event-1") {
		t.Fatalf("expected one committed event, got %q err=%v", data, err)
	}
}

func TestPublicationTransactionRejectsStaleActualLiveHead(t *testing.T) {
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	prev := ""
	tx := publicationTx("gen-1", "snap-expected", &prev)
	liveHead := "snap-new"
	tx.LiveHeadCommit = publicationAuthority(&liveHead)
	tx.ActualLiveHeadSnapshotID = liveHead
	if _, err := st.PublishGeneration(tx); !errors.Is(err, storage.ErrCASConflict) {
		t.Fatalf("expected stale head conflict, got %v", err)
	}
	if ptr, err := st.ReadActivePointer(); err != nil || ptr != nil {
		t.Fatalf("stale head exposed pointer=%+v err=%v", ptr, err)
	}
	ledgerPath := filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl")
	if data, err := os.ReadFile(ledgerPath); err == nil && len(data) != 0 {
		t.Fatalf("stale head exposed ghost event: %q", data)
	}
}

func publicationTx(gen, snap string, prev *string) storage.PublicationTransaction {
	return publicationTxWithSequence(gen, snap, prev, 1)
}

func publicationTxWithSequence(gen, snap string, prev *string, sequence int) storage.PublicationTransaction {
	basis := strings.Repeat("a", 64)
	queryHash := strings.Repeat("b", 64)
	const (
		manifestSchema = "https://codeflow.local/schemas/rflsc.generation-proof-manifest.v2.schema.json"
		pointerSchema  = "https://codeflow.local/schemas/rflsc.active-pointer.v2.schema.json"
		mapSchema      = "https://codeflow.local/schemas/rflsc.semantic-map-ir.v2.schema.json"
	)
	readSetID := "read-1"
	closureID := "closure-1"
	closureDigest := strings.Repeat("c", 64)
	capability := evidence.CapabilityProfile{Adapter: "test-adapter", AdapterVersion: "adapter-1", AnalyzerRevision: "analyzer-1", Features: []string{"snapshot_bytes"}}
	capabilityBytes := mustMarshalFixture(capability)
	capabilityHash := sha256.Sum256(capabilityBytes)
	capabilityDigest := hex.EncodeToString(capabilityHash[:])
	observations := map[string][]evidence.Observation{
		"negativeObservations":   {{Kind: "negative_lookup", Path: "test/missing.go", Measured: true}},
		"membershipObservations": {{Kind: "membership", Path: "test", Measured: true}},
		"dependencyFrontiers":    {{Kind: "dependency_frontier", Path: "test/go.mod", Measured: true}},
	}
	readSet := evidence.AnalysisReadSet{
		SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion,
		ReadSetID: readSetID, ComputedBasisID: basis, WorkspaceEpoch: 1,
		Documents: []evidence.ReadDocument{}, NegativeObservations: observations["negativeObservations"],
		MembershipObservations: observations["membershipObservations"], DependencyFrontiers: observations["dependencyFrontiers"],
	}
	closure := evidence.ObservationClosure{
		SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion,
		ClosureID: closureID, AnalysisReadSetID: readSetID, ComputedBasisID: basis, WorkspaceEpoch: 1,
		Status: "closed", NegativeObservations: observations["negativeObservations"],
		MembershipObservations: observations["membershipObservations"], DependencyFrontiers: observations["dependencyFrontiers"],
		RequiredObservations: []string{"negative_lookup", "membership", "dependency_frontier"},
		MeasuredObservations: []string{"negative_lookup", "membership", "dependency_frontier"}, ClosureDigest: closureDigest,
	}
	readSetBytes := mustMarshalFixture(readSet)
	closureBytes := mustMarshalFixture(closure)
	analyzerResult := evidence.Result{
		SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion,
		RequestID: "request-1", Operation: "detect", AdapterVersion: capability.AdapterVersion,
		AnalyzerRevision: capability.AnalyzerRevision, WorkspaceEpoch: 1, ComputedBasisID: basis,
		SnapshotID: snap, SnapshotTreeDigest: "tree-test", DependencyFingerprint: "deps-test",
		ReadSet: readSet, Closure: closure, Capability: capability,
		Coverage:    evidence.Coverage{IncludedSourceRoots: []string{"."}, Measured: true},
		Diagnostics: []evidence.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`),
	}
	analyzerResultBytes := mustMarshalFixture(analyzerResult)
	mapData := mustMarshalFixture(map[string]any{
		"schemaId": mapSchema, "schemaVersion": 2, "mapId": "map-" + gen,
		"generationId": gen, "computedBasisId": basis, "validatedAgainstSnapshotId": snap,
		"publicationKind": "initial", "freshness": "historical", "settlement": "pending",
		"enrichmentStatus": "not_requested", "authority": "candidate",
		"quality": map[string]any{"stage": "Q1", "unresolvedCriticalCount": 0, "conflictingCriticalCount": 0},
		"task":    map[string]any{"taskId": "task-test", "intentRevision": 1, "mode": "feature"},
		"basis": map[string]any{
			"repositoryId": "repo-test", "worktreeId": "worktree-test", "workspaceEpoch": 1,
			"computedWorkspaceSnapshotId": snap, "computedBasisId": basis, "snapshotTreeId": "tree-test",
			"dependencyFingerprint": "deps-test", "analysisReadSetId": readSetID, "causalObservationClosureId": closureID,
		},
		"summary": map[string]any{"requested": "test", "current": "candidate"},
		"steps":   []any{}, "edges": []any{}, "unknowns": []any{},
		"coverage": map[string]any{"includedSourceRoots": []string{"."}, "excludedReasons": []string{}},
	})
	projectionBytes := mustMarshalFixture(map[string]any{
		"schemaId": "https://codeflow.local/schemas/rflsc.flowview-projection.v2.schema.json", "schemaVersion": 2,
		"projectionId": "projection-" + gen, "generationId": gen, "computedBasisId": basis, "mode": "feature",
		"displayBudget":   map[string]any{"targetMin": 1, "targetMax": 1, "enforcement": "soft"},
		"visibleStepRefs": []any{}, "preservedStepRefs": []any{}, "foldedSubflows": []any{}, "unknownBoundaryRefs": []any{},
	})
	mapRef := storage.ArtifactCASRef(mapData)
	projectionRef := storage.ArtifactCASRef(projectionBytes)
	readSetRef := storage.ArtifactCASRef(readSetBytes)
	closureRef := storage.ArtifactCASRef(closureBytes)
	analyzerResultRef := storage.ArtifactCASRef(analyzerResultBytes)
	ptr := &storage.ActivePointer{SchemaID: pointerSchema, SchemaVersion: 2, GenerationID: gen, ComputedBasisID: basis, ValidatedAgainstSnapshotID: snap, ExpectedLiveHeadSnapshotID: snap, ExpectedPreviousGenerationID: prev, WorkspaceEpoch: 1, TaskIntentRevision: 1, NormalizedQueryHash: queryHash, FlowCount: 1, RepositoryID: "repo-test", WorktreeID: "worktree-test", TaskID: "task-test", PublishedAt: time.Unix(1, 0).UTC()}
	manifest := &storage.GenerationProofManifest{SchemaID: manifestSchema, SchemaVersion: 2, ProofID: "proof-" + gen, GenerationID: gen, ComputedBasisID: basis, ComputedSnapshotID: snap, ValidatedAgainstSnapshotID: snap, TaskIntentRevision: 1, NormalizedQueryHash: queryHash, AnalysisReadSetID: readSetID, CausalObservationClosureID: closureID, CausalObservationClosureDigest: closureDigest, CapabilityProfileDigest: capabilityDigest, WorkspaceEpoch: 1, CurrentPublication: storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"}, SettlementEvaluation: storage.SettlementEvaluation{Gate: "pending", BlockingObligationRefs: []string{}}, ArtifactRefs: storage.ArtifactRefs{SemanticMap: mapRef, Projection: projectionRef, AnalysisReadSet: readSetRef, ObservationClosure: closureRef, AnalyzerResult: analyzerResultRef}, ExpectedLiveHeadSnapshotID: snap, ExpectedPreviousGenerationID: prev, PublishedAt: time.Unix(1, 0).UTC()}
	snapshotID := snap
	generationID := gen
	event := map[string]any{"schemaId": "https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json", "schemaVersion": 2, "streamId": "stream-1", "sequence": sequence, "eventId": fmt.Sprintf("event-%d", sequence), "eventType": "generation.published", "occurredAt": time.Unix(1, 0).UTC(), "computedBasisId": basis, "validatedAgainstSnapshotId": snapshotID, "generationId": generationID}
	eventBytes, _ := json.Marshal(event)
	return storage.PublicationTransaction{Manifest: manifest, Pointer: ptr, Event: eventBytes, Artifacts: map[string][]byte{mapRef: mapData, projectionRef: projectionBytes, readSetRef: readSetBytes, closureRef: closureBytes, analyzerResultRef: analyzerResultBytes}, ExpectedLiveHeadSnapshotID: snap, ActualLiveHeadSnapshotID: snap, ExpectedPreviousGenerationID: ""}
}

func mustMarshalFixture(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func publicationAuthority(liveHead *string) func(string, func() error) error {
	return func(expectedID string, commit func() error) error {
		if liveHead == nil || expectedID == "" || *liveHead != expectedID {
			return storage.ErrCASConflict
		}
		return commit()
	}
}

func TestPublicationTransactionAppendsLargeLedgerInPlace(t *testing.T) {
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl")
	basis := strings.Repeat("a", 64)
	largePayload := strings.Repeat("x", 8*1024)
	const existingEvents = 1024
	ledger := make([]byte, 0, existingEvents*(len(largePayload)+256))
	for sequence := 1; sequence <= existingEvents; sequence++ {
		event := map[string]any{
			"schemaId": "https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json", "schemaVersion": 2,
			"streamId": "stream-1", "sequence": sequence, "eventId": fmt.Sprintf("event-%d", sequence),
			"eventType": "activity.updated", "occurredAt": time.Unix(int64(sequence), 0).UTC(),
			"computedBasisId": basis, "validatedAgainstSnapshotId": "snap-ledger", "data": map[string]any{"activity": "idle", "payload": largePayload},
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		ledger = append(ledger, encoded...)
		ledger = append(ledger, '\n')
	}
	if err := os.WriteFile(ledgerPath, ledger, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	prev := ""
	tx := publicationTxWithSequence("gen-large", "snap-large", &prev, existingEvents+1)
	tx.LiveHeadCommit = publicationAuthority(stringPtr("snap-large"))
	commit, err := st.PublishGeneration(tx)
	if err != nil {
		t.Fatalf("large-ledger publication failed: %v", err)
	}
	after, err := os.Stat(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("publication replaced the event ledger instead of appending in place")
	}
	if after.Size() <= before.Size() || commit.ManifestRef == "" {
		t.Fatalf("large-ledger append did not add one durable event: before=%d after=%d commit=%+v", before.Size(), after.Size(), commit)
	}
	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < len(ledger) || string(data[:len(ledger)]) != string(ledger) || strings.Count(string(data[len(ledger):]), "event-1025") != 1 {
		t.Fatalf("large-ledger append changed prior bytes or appended the wrong event")
	}
}

func TestPublicationTransactionRollbackTruncatesExistingLedger(t *testing.T) {
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}

	first := publicationTx("gen-1", "snap-1", stringPtr(""))
	liveHead := "snap-1"
	first.LiveHeadCommit = publicationAuthority(&liveHead)
	if _, err := st.PublishGeneration(first); err != nil {
		t.Fatalf("initial publication failed: %v", err)
	}
	pointerPath := filepath.Join(st.BaseDir(), "active-pointer.json")
	ledgerPath := filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl")
	priorPointer, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}
	priorLedger, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}

	second := publicationTxWithSequence("gen-2", "snap-2", stringPtr("gen-1"), 2)
	second.ExpectedPreviousGenerationID = "gen-1"
	liveHead = "snap-2"
	second.LiveHeadCommit = publicationAuthority(&liveHead)
	st.SetPublicationFault("pointer")
	if _, err := st.PublishGeneration(second); err == nil {
		t.Fatal("pointer fault must reject the second publication")
	}
	st.SetPublicationFault("")

	gotPointer, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}
	gotLedger, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPointer, priorPointer) {
		t.Fatalf("rollback changed the prior active pointer: before=%q after=%q", priorPointer, gotPointer)
	}
	if !bytes.Equal(gotLedger, priorLedger) {
		t.Fatalf("rollback did not truncate the appended event: before=%q after=%q", priorLedger, gotLedger)
	}

	restarted := storage.New(root)
	if err := restarted.RecoverPendingPublication(); err != nil {
		t.Fatalf("restart recovery failed: %v", err)
	}
	if gotLedger, err := os.ReadFile(ledgerPath); err != nil || !bytes.Equal(gotLedger, priorLedger) {
		t.Fatalf("restart changed recovered event ledger: before=%q after=%q err=%v", priorLedger, gotLedger, err)
	}
}

func stringPtr(value string) *string { return &value }
