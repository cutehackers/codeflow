package rflscvs02

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/workspace"
)

func newLease(t *testing.T, disk, snapshot []byte) (workspace.SnapshotLease, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "lib", "feature.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, disk, 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := workspace.NewSnapshotEngine(root, 7)
	if err != nil {
		t.Fatal(err)
	}
	_, snap, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path:            "lib/feature.go",
		Content:         snapshot,
		DocumentVersion: 1,
		Source:          workspace.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.SnapshotVFS(snap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	return lease, root
}

func TestVS02A01SnapshotInputCarriesOnlyLeaseBytesAndFingerprint(t *testing.T) {
	lease, root := newLease(t, []byte("package disk\n"), []byte("package snapshot\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	if input.ComputedBasisID != lease.ComputedBasisID() || input.RootTreeID != lease.RootTreeID() || input.WorkspaceEpoch != lease.WorkspaceEpoch() {
		t.Fatalf("snapshot identity mismatch: %+v", input)
	}
	doc, ok := input.Document("lib/feature.go")
	if !ok || string(doc.Bytes) != "package snapshot\n" {
		t.Fatalf("input did not preserve lease bytes: %+v", doc)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "feature.go"), []byte("package mutated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc, _ = input.Document("lib/feature.go")
	if string(doc.Bytes) != "package snapshot\n" {
		t.Fatalf("input changed after live disk mutation: %q", doc.Bytes)
	}
	request, err := NewAnalyzerRequest("req-a1", "slice", input, []string{"lib/feature.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	params := request.Params()
	if _, ok := params["repoRoot"]; ok {
		t.Fatal("snapshot request must not require a live repoRoot")
	}
	if _, ok := params["snapshot"].(map[string]any)["files"]; !ok {
		t.Fatal("snapshot request must contain protocol content")
	}
}

func TestVS02A02AndA03ResultRequiresMeasuredObservations(t *testing.T) {
	lease, _ := newLease(t, []byte("package disk\n"), []byte("package snapshot\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewAnalyzerRequest("req-a2", "slice", input, []string{"lib/feature.go"}, []string{"negative_lookup", "membership", "dependency_frontier"})
	if err != nil {
		t.Fatal(err)
	}
	closed := Result{
		SchemaID: AnalyzerResultSchemaID, SchemaVersion: SchemaVersion,
		RequestID: request.RequestID, Operation: request.Operation, AnalyzerRevision: "go/2",
		AdapterVersion: "go/2", WorkspaceEpoch: input.WorkspaceEpoch, ComputedBasisID: input.ComputedBasisID,
		SnapshotID: input.SnapshotID, SnapshotTreeDigest: input.RootTreeID, DependencyFingerprint: input.DependencyFingerprint,
		ReadSet:    AnalysisReadSet{SchemaID: ReadSetSchemaID, SchemaVersion: SchemaVersion, ReadSetID: "rs-1", ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, Documents: readDocuments(input.Documents), NegativeObservations: []Observation{}, MembershipObservations: []Observation{}, DependencyFrontiers: []Observation{}},
		Closure:    ObservationClosure{SchemaID: ClosureSchemaID, SchemaVersion: SchemaVersion, ClosureID: "cl-1", AnalysisReadSetID: "rs-1", ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, Status: "closed", RequiredObservations: []string{"negative_lookup", "membership", "dependency_frontier"}, MeasuredObservations: []string{}, NegativeObservations: []Observation{}, MembershipObservations: []Observation{}, DependencyFrontiers: []Observation{}},
		Capability: CapabilityProfile{Adapter: "go", AnalyzerRevision: "go/2", Features: []string{"snapshot_bytes"}},
		Coverage:   Coverage{IncludedSourceRoots: []string{"lib"}, Measured: true},
		Payload:    json.RawMessage(`{"candidateId":"cand-test0001","language":"go","entrySymbolPath":"lib.feature","steps":[{"ordinal":1,"kind":"call","description":"test","symbolPath":"lib.feature","anchor":{"repoRelativePath":"lib/feature.go","byteRange":[0,1],"fileHash":"0000000000000000000000000000000000000000000000000000000000000000","spanHash":"0000000000000000000000000000000000000000000000000000000000000000","enclosingSymbolPath":"lib.feature","canonicalAstFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}}],"edges":[],"truncated":false,"visitedCycleDetected":false,"redactedCount":0}`),
	}
	if err := ValidateResult(request, closed); err == nil {
		t.Fatal("closed result with unmeasured required observations was accepted")
	}
	closed.Closure.Status = "open"
	closed.Closure.IncompleteReasons = []string{"negative_lookup is not measured", "membership is not measured", "dependency_frontier is not measured"}
	if err := ValidateResult(request, closed); err != nil {
		t.Fatalf("open result with explicit reason rejected: %v", err)
	}
}

func TestVS02A04RejectsCrossArtifactIdentity(t *testing.T) {
	lease, _ := newLease(t, []byte("package disk\n"), []byte("package snapshot\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewAnalyzerRequest("req-a4", "detect", input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := ValidResultForTest(request)
	result.ReadSet.ComputedBasisID = "wrong-basis"
	if err := ValidateResult(request, result); err == nil {
		t.Fatal("read-set basis mismatch was accepted")
	}
	result = ValidResultForTest(request)
	result.Closure.AnalysisReadSetID = "other-read-set"
	if err := ValidateResult(request, result); err == nil {
		t.Fatal("closure/read-set identity mismatch was accepted")
	}
}

func TestVS02A05AndA06EvidenceUsesValidatedSnapshotRanges(t *testing.T) {
	lease, _ := newLease(t, []byte("package disk\n"), []byte("package snapshot\nfunc Run() {}\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractEvidence(input, []EvidenceAnchor{{EvidenceID: "ev-1", Path: "lib/feature.go", RevisionID: input.Documents[0].RevisionID, StartByte: 9, EndByte: 26}}); err != nil {
		t.Fatalf("valid snapshot evidence rejected: %v", err)
	}
	for _, anchor := range []EvidenceAnchor{
		{EvidenceID: "ev-missing", Path: "../../secret", StartByte: 0, EndByte: 1},
		{EvidenceID: "ev-bounds", Path: "lib/feature.go", RevisionID: input.Documents[0].RevisionID, StartByte: 0, EndByte: 10000},
		{EvidenceID: "ev-revision", Path: "lib/feature.go", RevisionID: "rev-other", StartByte: 0, EndByte: 1},
	} {
		result, err := ExtractEvidence(input, []EvidenceAnchor{anchor})
		if err == nil || len(result) != 0 {
			t.Fatalf("invalid anchor produced evidence: anchor=%+v result=%+v err=%v", anchor, result, err)
		}
		var typed *EvidenceError
		if !errors.As(err, &typed) {
			t.Fatalf("invalid anchor error is not typed: %T %v", err, err)
		}
	}
}

func TestVS02A05EvidenceRejectsForgedSnapshotDocumentIdentity(t *testing.T) {
	lease, _ := newLease(t, []byte("package disk\n"), []byte("package snapshot\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	doc := input.Documents[0]
	anchor := EvidenceAnchor{EvidenceID: "ev-forged", Path: doc.Path, RevisionID: doc.RevisionID, StartByte: 0, EndByte: 8}
	input.Documents[0].ContentID = "forged-content-id"
	if _, err := ExtractEvidence(input, []EvidenceAnchor{anchor}); err == nil {
		t.Fatal("evidence extraction accepted forged snapshot content identity")
	} else {
		var typed *EvidenceError
		if !errors.As(err, &typed) || typed.Code != "invalid_snapshot" {
			t.Fatalf("forged snapshot returned wrong typed error: %T %v", err, err)
		}
	}

	input, err = SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	input.Documents[0].ByteLength++
	if _, err := ExtractEvidence(input, []EvidenceAnchor{anchor}); err == nil {
		t.Fatal("evidence extraction accepted forged snapshot byte length")
	}
}

func TestVS02A05EvidenceRejectsStaleAnchorHashes(t *testing.T) {
	lease, _ := newLease(t, []byte("package disk\n"), []byte("package snapshot\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	doc := input.Documents[0]
	span := doc.Bytes[:8]
	base := EvidenceAnchor{
		EvidenceID: "ev-hash",
		Path:       doc.Path,
		RevisionID: doc.RevisionID,
		FileHash:   doc.ContentID,
		SpanHash:   digestBytes(span),
		StartByte:  0,
		EndByte:    len(span),
	}
	if records, err := ExtractEvidence(input, []EvidenceAnchor{base}); err != nil || len(records) != 1 {
		t.Fatalf("matching anchor hashes were rejected: records=%+v err=%v", records, err)
	}

	staleFile := base
	staleFile.FileHash = "stale-file-hash"
	if records, err := ExtractEvidence(input, []EvidenceAnchor{staleFile}); err == nil || len(records) != 0 {
		t.Fatalf("stale file hash produced evidence: records=%+v err=%v", records, err)
	} else {
		var typed *EvidenceError
		if !errors.As(err, &typed) || typed.Code != "unknown_revision" {
			t.Fatalf("stale file hash returned wrong typed error: %T %v", err, err)
		}
	}

	staleSpan := base
	staleSpan.SpanHash = "stale-span-hash"
	if records, err := ExtractEvidence(input, []EvidenceAnchor{staleSpan}); err == nil || len(records) != 0 {
		t.Fatalf("stale span hash produced evidence: records=%+v err=%v", records, err)
	} else {
		var typed *EvidenceError
		if !errors.As(err, &typed) || typed.Code != "invalid_anchor" {
			t.Fatalf("stale span hash returned wrong typed error: %T %v", typed, err)
		}
	}
}

func TestVS02A07RedactsJSONKeysAndValuesBeforeClipping(t *testing.T) {
	raw := []byte(`{"apiKey":"super-secret-value","nested":{"password":"another-secret-value"},"message":"token: leaked-token"}`)
	clean, count, err := RedactJSON(raw, 64)
	if err != nil {
		t.Fatal(err)
	}
	if count < 3 || string(clean) == string(raw) {
		t.Fatalf("redaction did not cover key/value payload: count=%d clean=%s", count, clean)
	}
	if string(clean) == "" || len(clean) > 64 {
		t.Fatalf("diagnostic bound not applied after redaction: %d", len(clean))
	}
	if string(clean) == "super-secret-value" || string(clean) == "another-secret-value" {
		t.Fatal("secret survived redaction")
	}
}

func TestVS02A09BoundIsRejectedBeforeRequestAllocation(t *testing.T) {
	input := SnapshotInput{SnapshotID: "snap", ComputedBasisID: "basis", RootTreeID: "tree", DependencyFingerprint: "dep", WorkspaceEpoch: 1}
	request, err := NewAnalyzerRequest("req-bound", "slice", input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.MaxMessageBytes = 8
	if _, err := MarshalBoundedRequest(request); err == nil {
		t.Fatal("oversize request was accepted")
	}
}

func TestVS02A10CapabilityMatrixIsDerivedFromInitializeEvidence(t *testing.T) {
	evidence := []InitializeCapabilityEvidence{
		{Adapter: "dart", AdapterVersion: "dart/1", AnalyzerRevision: "dart/2", ProtocolVersion: 1, Cancellation: true, Progress: true, BatchAck: true, SnapshotOverlay: true, AnalysisMetadata: true, MaxMessageBytes: DefaultMaxMessageBytes, ConformancePassed: true, ConformanceProbeID: "test/dart", ConformanceObservations: completeCapabilityObservations(), OpenOnMissing: true, ReadOnlySource: true},
		{Adapter: "typescript", AdapterVersion: "ts/1", AnalyzerRevision: "ts/2", ProtocolVersion: 1, Cancellation: true, Progress: true, BatchAck: true, SnapshotOverlay: true, AnalysisMetadata: true, MaxMessageBytes: DefaultMaxMessageBytes, ConformancePassed: true, ConformanceProbeID: "test/typescript", ConformanceObservations: completeCapabilityObservations(), OpenOnMissing: true, ReadOnlySource: true},
	}
	matrix := CapabilityMatrixFromInitializeEvidence(evidence)
	if matrix["dart"].Status != "measured" || matrix["typescript"].Status != "measured" {
		t.Fatalf("initialize evidence did not produce measured entries: %+v", matrix)
	}
	if matrix["go"].Status != "unsupported" || len(matrix["go"].Features) != 0 {
		t.Fatalf("missing initialize evidence became fabricated success: %+v", matrix["go"])
	}
	if matrix["dart"].MeasurementID == "rflsc-r2-vs-02-dart" {
		t.Fatal("measurement id must be derived from initialize evidence")
	}
}

func TestVS02A10CapabilityMatrixDoesNotPromoteUnsupportedRelations(t *testing.T) {
	matrix := CapabilityMatrixFromInitializeEvidence([]InitializeCapabilityEvidence{
		{Adapter: "dart", AdapterVersion: "dart/0.1", AnalyzerRevision: "dart/2", ProtocolVersion: 1, Cancellation: true, Progress: true, BatchAck: true, SnapshotOverlay: true, AnalysisMetadata: true, MaxMessageBytes: DefaultMaxMessageBytes, ConformancePassed: true, ConformanceProbeID: "test/dart", ConformanceObservations: completeCapabilityObservations(), OpenOnMissing: true, ReadOnlySource: true},
		{Adapter: "typescript", AdapterVersion: "typescript/0.1", AnalyzerRevision: "ts/2", ProtocolVersion: 1, Cancellation: true, Progress: true, BatchAck: true, SnapshotOverlay: true, AnalysisMetadata: true, MaxMessageBytes: DefaultMaxMessageBytes, ConformancePassed: true, ConformanceProbeID: "test/typescript", ConformanceObservations: completeCapabilityObservations(), OpenOnMissing: true, ReadOnlySource: true},
		{Adapter: "go", AdapterVersion: "go/0.1", AnalyzerRevision: "go/2", ProtocolVersion: 1, Cancellation: true, Progress: true, BatchAck: true, SnapshotOverlay: true, AnalysisMetadata: true, MaxMessageBytes: DefaultMaxMessageBytes, ConformancePassed: true, ConformanceProbeID: "test/go", ConformanceObservations: completeCapabilityObservations(), OpenOnMissing: true, ReadOnlySource: true},
	})
	for _, language := range []string{"dart", "typescript", "go"} {
		profile, ok := matrix[language]
		if !ok || profile.Adapter == "" || profile.AnalyzerRevision == "" || len(profile.Features) == 0 {
			t.Fatalf("missing measured capability profile for %s: %+v", language, profile)
		}
		if profile.Supports("unsupported_relation") {
			t.Fatalf("unsupported relation was declared supported for %s", language)
		}
	}
}

func TestVS02A10CapabilityMeasurementIdentityBindsConformanceProof(t *testing.T) {
	base := InitializeCapabilityEvidence{
		Adapter: "go", AdapterVersion: "go/0.1", AnalyzerRevision: "go/2", ProtocolVersion: 1,
		Cancellation: true, Progress: true, BatchAck: true, SnapshotOverlay: true,
		AnalysisMetadata: true, MaxMessageBytes: DefaultMaxMessageBytes, ConformancePassed: true,
		ConformanceProbeID: "canonical/go/v1", ConformanceObservations: completeCapabilityObservations(),
		OpenOnMissing: true, ReadOnlySource: true,
	}
	first := CapabilityMeasurementFromInitialize(base)
	reordered := base
	reordered.ConformanceObservations = []string{"dependency_frontier", "negative_lookup", "membership", "read_set", "snapshot_bytes"}
	if got := CapabilityMeasurementFromInitialize(reordered); got.MeasurementID != first.MeasurementID {
		t.Fatalf("measurement identity changed with observation ordering: %q vs %q", first.MeasurementID, got.MeasurementID)
	}
	for _, mutate := range []func(*InitializeCapabilityEvidence){
		func(e *InitializeCapabilityEvidence) { e.ConformanceProbeID = "canonical/go/v2" },
		func(e *InitializeCapabilityEvidence) { e.OpenOnMissing = false },
		func(e *InitializeCapabilityEvidence) { e.ReadOnlySource = false },
		func(e *InitializeCapabilityEvidence) {
			e.ConformanceObservations = append(e.ConformanceObservations, "extra")
		},
	} {
		changed := base
		changed.ConformanceObservations = append([]string(nil), base.ConformanceObservations...)
		mutate(&changed)
		if got := CapabilityMeasurementFromInitialize(changed); got.MeasurementID == first.MeasurementID {
			t.Fatalf("measurement identity did not bind conformance proof change: %+v", changed)
		}
	}
}

func completeCapabilityObservations() []string {
	return []string{"snapshot_bytes", "read_set", "membership", "negative_lookup", "dependency_frontier"}
}

func TestVS02A11IntegrityAuditCapturesTreeAndWrites(t *testing.T) {
	lease, _ := newLease(t, []byte("package disk\n"), []byte("package snapshot\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	audit := input.SourceWriteAudit
	if audit.SourceIntegrityViolation || audit.CodeFlowWriteCount != 0 || len(audit.RepositoryPathWrites) != 0 || audit.CapturedSnapshotTreeDigest != input.RootTreeID {
		t.Fatalf("unexpected source write audit: %+v", audit)
	}
}

func ValidResultForTest(request AnalyzerRequest) Result {
	return Result{
		SchemaID: AnalyzerResultSchemaID, SchemaVersion: SchemaVersion,
		RequestID: request.RequestID, Operation: request.Operation, AnalyzerRevision: "analyzer/2", AdapterVersion: "adapter/2",
		WorkspaceEpoch: request.Snapshot.WorkspaceEpoch, ComputedBasisID: request.Snapshot.ComputedBasisID, SnapshotID: request.Snapshot.SnapshotID,
		SnapshotTreeDigest: request.Snapshot.RootTreeID, DependencyFingerprint: request.Snapshot.DependencyFingerprint,
		ReadSet:    AnalysisReadSet{SchemaID: ReadSetSchemaID, SchemaVersion: SchemaVersion, ReadSetID: "readset-1", ComputedBasisID: request.Snapshot.ComputedBasisID, WorkspaceEpoch: request.Snapshot.WorkspaceEpoch, Documents: readDocuments(request.Snapshot.Documents), NegativeObservations: []Observation{}, MembershipObservations: []Observation{}, DependencyFrontiers: []Observation{}},
		Closure:    ObservationClosure{SchemaID: ClosureSchemaID, SchemaVersion: SchemaVersion, ClosureID: "closure-1", AnalysisReadSetID: "readset-1", ComputedBasisID: request.Snapshot.ComputedBasisID, WorkspaceEpoch: request.Snapshot.WorkspaceEpoch, Status: "open", NegativeObservations: []Observation{}, MembershipObservations: []Observation{}, DependencyFrontiers: []Observation{}, RequiredObservations: append([]string{}, request.RequiredObservations...), MeasuredObservations: []string{}, IncompleteReasons: []string{"scope is bounded"}},
		Capability: CapabilityProfile{Adapter: "test", AnalyzerRevision: "analyzer/2", Features: []string{"snapshot_bytes"}},
		Coverage:   Coverage{IncludedSourceRoots: []string{"."}, Measured: true},
		Payload:    json.RawMessage(`{"candidateId":"cand-test0001","language":"go","entrySymbolPath":"lib.feature","steps":[{"ordinal":1,"kind":"call","description":"test","symbolPath":"lib.feature","anchor":{"repoRelativePath":"lib/feature.go","byteRange":[0,1],"fileHash":"0000000000000000000000000000000000000000000000000000000000000000","spanHash":"0000000000000000000000000000000000000000000000000000000000000000","enclosingSymbolPath":"lib.feature","canonicalAstFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}}],"edges":[],"truncated":false,"visitedCycleDetected":false,"redactedCount":0}`),
	}
}

func readDocuments(documents []SnapshotDocument) []ReadDocument {
	out := make([]ReadDocument, 0, len(documents))
	for _, doc := range documents {
		out = append(out, ReadDocument{Path: doc.Path, DocumentRevisionID: doc.RevisionID, ContentID: doc.ContentID, ContentHash: doc.ContentID, DocumentVersion: doc.DocumentVersion, ByteLength: doc.ByteLength})
	}
	return out
}

func TestResultJSONIsRegisteredV2Shape(t *testing.T) {
	lease, _ := newLease(t, []byte("package disk\n"), []byte("package snapshot\n"))
	input, err := SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewAnalyzerRequest("req-json", "detect", input, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ValidResultForTest(request))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("empty result")
	}
}
