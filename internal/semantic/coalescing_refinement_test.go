package semantic_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"codeflow/internal/evidence"
	"codeflow/internal/fusion"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

func TestLateRefinementV2PublishesThroughStrictAtomicPath(t *testing.T) {
	fixture := newLateRefinementFixture(t)
	commit, err := fixture.coordinator.PublishLateRefinementV2(fixture.input)
	if err != nil {
		t.Fatalf("strict late refinement should publish: %v", err)
	}
	if commit.ManifestRef == "" || commit.Pointer == nil || commit.Pointer.GenerationID != "gen-2" {
		t.Fatalf("missing committed refinement: %+v", commit)
	}
	manifest, pointer, err := fixture.storage.ReadValidatedActiveProofManifest()
	if err != nil {
		t.Fatalf("committed refinement is not restart-verifiable: %v", err)
	}
	if manifest.GenerationID != "gen-2" || pointer.GenerationID != "gen-2" || pointer.ExpectedPreviousGenerationID == nil || *pointer.ExpectedPreviousGenerationID != "gen-1" {
		t.Fatalf("unexpected active proof lineage: manifest=%+v pointer=%+v", manifest, pointer)
	}
	if manifest.CurrentPublication.Eligibility != "passed" || manifest.CurrentPublication.ClosureGate != "passed" {
		t.Fatalf("late refinement did not persist the evaluated gate result: %+v", manifest.CurrentPublication)
	}
	capabilityDigest, err := semantic.CanonicalCapabilityProfileDigest(fixture.input.AnalysisResult.Capability)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CapabilityProfileDigest != capabilityDigest {
		t.Fatalf("capability digest was not derived from the canonical result: got=%q want=%q", manifest.CapabilityProfileDigest, capabilityDigest)
	}
	refs := manifest.ArtifactRefs
	if refs.SemanticMap == "" || refs.Projection == "" || refs.AnalysisReadSet == "" || refs.ObservationClosure == "" || refs.AnalyzerResult == "" {
		t.Fatalf("late refinement omitted canonical artifact refs: %+v", refs)
	}
	if pointer.FlowCount != 1 {
		t.Fatalf("one semantic map must publish one flow, got %d", pointer.FlowCount)
	}
}

func TestLateRefinementV2RejectsIdentityAndQ3DriftWithoutPublication(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*semantic.LateRefinementInput)
		want   string
	}{
		{name: "request id", mutate: func(in *semantic.LateRefinementInput) { in.AnalysisResult.RequestID = "different-request" }, want: "analyzer result"},
		{name: "analyzer revision", mutate: func(in *semantic.LateRefinementInput) { in.AnalysisResult.AnalyzerRevision = "different-analyzer" }, want: "analyzer result"},
		{name: "basis", mutate: func(in *semantic.LateRefinementInput) { in.Map.ComputedBasisID = strings.Repeat("b", 64) }, want: "semantic-map"},
		{name: "closure read set", mutate: func(in *semantic.LateRefinementInput) { in.Closure.AnalysisReadSetID = "other-read-set" }, want: "closure"},
		{name: "q3 fact", mutate: func(in *semantic.LateRefinementInput) { in.Map.Steps[0].Name = "changed Q3 fact" }, want: "Q3"},
		{name: "open closure", mutate: func(in *semantic.LateRefinementInput) { in.Closure.ClosureStatus = "open" }, want: "closure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLateRefinementFixture(t)
			before, err := fixture.storage.ReadActivePointer()
			if err != nil || before == nil {
				t.Fatalf("read baseline pointer: %v", err)
			}
			beforeGen := before.GenerationID
			tc.mutate(&fixture.input)
			if _, err := fixture.coordinator.PublishLateRefinementV2(fixture.input); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("expected %q rejection, got %v", tc.want, err)
			}
			after, err := fixture.storage.ReadActivePointer()
			if err != nil || after == nil || after.GenerationID != beforeGen {
				t.Fatalf("rejected refinement changed active pointer: %+v err=%v", after, err)
			}
		})
	}
}

func TestLateRefinementV2PublisherCASFailureLeavesActiveProofUnchanged(t *testing.T) {
	fixture := newLateRefinementFixture(t)
	fixture.input.Publish = func(storage.PublicationTransaction) (storage.PublicationCommit, error) {
		return storage.PublicationCommit{}, storage.ErrCASConflict
	}
	if _, err := fixture.coordinator.PublishLateRefinementV2(fixture.input); !errors.Is(err, storage.ErrCASConflict) {
		t.Fatalf("expected publisher CAS failure, got %v", err)
	}
	ptr, err := fixture.storage.ReadActivePointer()
	if err != nil || ptr == nil || ptr.GenerationID != "gen-1" {
		t.Fatalf("CAS failure changed active proof: %+v err=%v", ptr, err)
	}
}

func TestLateRefinementV2UsesCurrentPublicationGateBeforePublishing(t *testing.T) {
	fixture := newLateRefinementFixture(t)
	fixture.input.Delta.MembershipChanged = true
	publishCalls := 0
	fixture.input.Publish = func(storage.PublicationTransaction) (storage.PublicationCommit, error) {
		publishCalls++
		return storage.PublicationCommit{}, nil
	}
	if _, err := fixture.coordinator.PublishLateRefinementV2(fixture.input); err == nil || !strings.Contains(err.Error(), "current publication gate") {
		t.Fatalf("membership-changing late refinement must be rejected by the current gate, got err=%v", err)
	}
	if publishCalls != 0 {
		t.Fatalf("rejected late refinement must not invoke publisher, calls=%d", publishCalls)
	}
}

func TestLateRefinementV2BuildsCanonicalArtifactsInsteadOfCallerDigests(t *testing.T) {
	fixture := newLateRefinementFixture(t)
	fixture.input.ArtifactBytes = map[string][]byte{"caller-supplied": []byte("not a canonical artifact")}
	fixture.input.ArtifactDigests = map[string]string{"semanticMap": strings.Repeat("f", 64)}
	var committed storage.PublicationTransaction
	fixture.input.Publish = func(tx storage.PublicationTransaction) (storage.PublicationCommit, error) {
		committed = tx
		return storage.PublicationCommit{ManifestRef: "captured"}, nil
	}
	if _, err := fixture.coordinator.PublishLateRefinementV2(fixture.input); err != nil {
		t.Fatalf("canonical late refinement should reach publisher: %v", err)
	}
	refs := committed.Manifest.ArtifactRefs
	for name, ref := range map[string]string{
		"semanticMap":        refs.SemanticMap,
		"projection":         refs.Projection,
		"analysisReadSet":    refs.AnalysisReadSet,
		"observationClosure": refs.ObservationClosure,
		"analyzerResult":     refs.AnalyzerResult,
	} {
		data, ok := committed.Artifacts[ref]
		if !ok || storage.ArtifactCASRef(data) != ref {
			t.Fatalf("%s artifact is not content-addressed from canonical bytes: ref=%q found=%v", name, ref, ok)
		}
	}
	if _, ok := committed.Artifacts["caller-supplied"]; ok {
		t.Fatal("caller-supplied artifact bytes must not be promoted")
	}
	capabilityDigest, err := semantic.CanonicalCapabilityProfileDigest(fixture.input.AnalysisResult.Capability)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Manifest.CapabilityProfileDigest != capabilityDigest {
		t.Fatalf("manifest trusted caller digest instead of canonical capability profile: got=%q want=%q", committed.Manifest.CapabilityProfileDigest, capabilityDigest)
	}
}

type lateRefinementFixture struct {
	storage     *storage.Storage
	coordinator *semantic.RefinementCoordinator
	input       semantic.LateRefinementInput
}

func newLateRefinementFixture(t *testing.T) lateRefinementFixture {
	t.Helper()
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	basis := strings.Repeat("a", 64)
	rootTree := strings.Repeat("t", 64)
	dep := strings.Repeat("d", 64)
	query := strings.Repeat("1", 64)
	snapshotID := "snap-refinement"
	repoID, worktreeID := "repo-refinement", "worktree-refinement"
	content := []byte("package main\nfunc main() {}\n")
	contentSum := sha256.Sum256(content)
	contentID := hex.EncodeToString(contentSum[:])
	snap := &workspace.WorkspaceSnapshot{
		SchemaID:                 workspaceSnapshotSchemaID,
		SchemaVersion:            2,
		SnapshotID:               snapshotID,
		WorkspaceEpoch:           1,
		Sequence:                 1,
		ComputedBasisID:          basis,
		RootTreeID:               rootTree,
		ConfigurationFingerprint: "config-refinement",
		DependencyFingerprint:    dep,
		RepositoryID:             repoID,
		WorktreeID:               worktreeID,
		Entries:                  map[string]workspace.SnapshotEntry{"main.go": {RevisionID: "rev-1", ContentID: contentID, DocumentVersion: 1, ByteLength: len(content)}},
		RepositoryPathWriteAudit: workspace.SourceWriteAudit{CapturedSnapshotTreeDigest: rootTree},
	}
	inputSnapshot := evidence.SnapshotInput{
		SnapshotID:               snapshotID,
		WorkspaceEpoch:           1,
		ComputedBasisID:          basis,
		RootTreeID:               rootTree,
		ConfigurationFingerprint: "config-refinement",
		DependencyFingerprint:    dep,
		Documents:                []evidence.SnapshotDocument{{Path: "main.go", RevisionID: "rev-1", ContentID: contentID, DocumentVersion: 1, ByteLength: len(content), Bytes: append([]byte(nil), content...)}},
		SourceWriteAudit:         workspace.SourceWriteAudit{CapturedSnapshotTreeDigest: rootTree},
	}
	request, err := evidence.NewAnalyzerRequest("request-refinement", "detect", inputSnapshot, nil, []string{"membership"})
	if err != nil {
		t.Fatal(err)
	}
	membership := evidence.Observation{Kind: "membership", Path: ".", ValueHash: "membership-refinement", Measured: true}
	result := evidence.Result{
		SchemaID:              evidence.AnalyzerResultSchemaID,
		SchemaVersion:         evidence.SchemaVersion,
		RequestID:             request.RequestID,
		Operation:             request.Operation,
		AdapterVersion:        "adapter-refinement/1",
		AnalyzerRevision:      "analyzer-refinement/1",
		WorkspaceEpoch:        1,
		ComputedBasisID:       basis,
		SnapshotID:            snapshotID,
		SnapshotTreeDigest:    rootTree,
		DependencyFingerprint: dep,
		ReadSet:               evidence.AnalysisReadSet{SchemaID: evidence.ReadSetSchemaID, SchemaVersion: 2, ReadSetID: "read-refinement", ComputedBasisID: basis, WorkspaceEpoch: 1, Documents: []evidence.ReadDocument{{Path: "main.go", DocumentRevisionID: "rev-1", ContentID: contentID, ContentHash: contentID, DocumentVersion: 1, ByteLength: len(content)}}, NegativeObservations: []evidence.Observation{}, MembershipObservations: []evidence.Observation{membership}, DependencyFrontiers: []evidence.Observation{}},
		Closure:               evidence.ObservationClosure{SchemaID: evidence.ClosureSchemaID, SchemaVersion: 2, ClosureID: "closure-refinement", AnalysisReadSetID: "read-refinement", ComputedBasisID: basis, WorkspaceEpoch: 1, Status: "closed", NegativeObservations: []evidence.Observation{}, MembershipObservations: []evidence.Observation{membership}, DependencyFrontiers: []evidence.Observation{}, RequiredObservations: []string{"membership"}, MeasuredObservations: []string{"membership"}, ClosureDigest: strings.Repeat("c", 64)},
		Capability:            evidence.CapabilityProfile{Adapter: "go", AdapterVersion: "adapter-refinement/1", AnalyzerRevision: "analyzer-refinement/1", Features: []string{"membership"}},
		Coverage:              evidence.Coverage{IncludedSourceRoots: []string{"."}, Measured: true},
		Diagnostics:           []evidence.Diagnostic{},
		Payload:               json.RawMessage(`{"language":"go","confident":true}`),
	}
	mapIR := createTestMapIR("Q2", nil, 0, 0)
	mapIR.SchemaID = semantic.SemanticMapSchemaID
	mapIR.SchemaVersion = semantic.SemanticSchemaVersion
	mapIR.MapID = "map-refinement"
	mapIR.GenerationID = "gen-1"
	mapIR.ComputedBasisID = basis
	mapIR.ValidatedAgainstSnapshotID = snapshotID
	mapIR.PublicationKind = "checkpoint"
	mapIR.Freshness = "historical"
	mapIR.Settlement = "pending"
	mapIR.EnrichmentStatus = "available"
	mapIR.Authority = "candidate"
	mapIR.Task = semantic.MapTaskContext{TaskID: "task-1", IntentRevision: 1, IntentStatus: "user_confirmed", Mode: "feature"}
	mapIR.Basis = semantic.MapBasisContext{RepositoryID: repoID, WorktreeID: worktreeID, WorkspaceEpoch: 1, ComputedWorkspaceSnapshotID: snapshotID, ComputedBasisID: basis, SnapshotTreeID: rootTree, DependencyFingerprint: dep, ConfigurationFingerprint: "config-refinement", AnalysisReadSetID: "read-refinement", CausalObservationClosureID: "closure-refinement"}
	mapIR.Coverage = &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}}
	mapIR.Unknowns = []fusion.Unknown{}
	mapIR.Edges = []semantic.SemanticEdge{}
	mapIR.Steps[0].StructuralIdentity = "task-1|main.go|main|action"
	mapIR.Steps[0].Kind = "action"
	mapIR.Steps[0].Anchor = slicing.Anchor{RepoRelativePath: "main.go", ByteRange: [2]int{0, len(content)}, FileHash: contentID, SpanHash: contentID, EnclosingSymbolPath: "main", CanonicalAstFingerprint: contentID}
	mapIR.Steps[0].EvidenceRefs = []string{"ev-refinement"}
	mapIR.Evidence = []semantic.SemanticEvidence{{EvidenceID: "ev-refinement", Kind: "source", SourceAuthority: "code", ComputedBasisID: basis, DocumentRevisionID: "rev-1", Anchor: mapIR.Steps[0].Anchor, ValidationStatus: "verified", RedactionStatus: "passed", SnapshotID: snapshotID, ByteRange: [2]int{0, len(content)}, LineRange: [2]int{1, 2}}}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	mapRef := storage.ArtifactCASRef(mapBytes)
	if _, err := st.WriteArtifactCAS(mapBytes); err != nil {
		t.Fatal(err)
	}
	readSetBytes, err := json.Marshal(result.ReadSet)
	if err != nil {
		t.Fatal(err)
	}
	closureBytes, err := json.Marshal(result.Closure)
	if err != nil {
		t.Fatal(err)
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	priorProjection := &semantic.FlowViewProjection{
		SchemaID: semantic.FlowViewProjectionSchemaID, SchemaVersion: 2,
		ProjectionID: "projection-refinement-1", GenerationID: "gen-1", ComputedBasisID: basis,
		Mode: "feature", DisplayBudget: semantic.DisplayBudget{TargetMin: 1, TargetMax: 15, Enforcement: "soft"},
		VisibleStepRefs: []string{"step-1"}, PreservedStepRefs: []string{"step-1"},
		UnknownBoundaryRefs: []string{}, FoldedSubflows: []semantic.FoldedSubflow{},
	}
	priorProjectionBytes, err := json.Marshal(priorProjection)
	if err != nil {
		t.Fatal(err)
	}
	readSetRef := storage.ArtifactCASRef(readSetBytes)
	closureRef := storage.ArtifactCASRef(closureBytes)
	resultRef := storage.ArtifactCASRef(resultBytes)
	priorProjectionRef := storage.ArtifactCASRef(priorProjectionBytes)
	for _, artifact := range [][]byte{readSetBytes, closureBytes, resultBytes, priorProjectionBytes} {
		if _, err := st.WriteArtifactCAS(artifact); err != nil {
			t.Fatal(err)
		}
	}
	capabilityDigest, err := semantic.CanonicalCapabilityProfileDigest(result.Capability)
	if err != nil {
		t.Fatal(err)
	}
	prevManifest := &storage.GenerationProofManifest{SchemaID: semantic.GenerationProofSchemaID, SchemaVersion: 2, ProofID: "proof-gen-1", GenerationID: "gen-1", ComputedBasisID: basis, ComputedSnapshotID: snapshotID, ValidatedAgainstSnapshotID: snapshotID, TaskIntentRevision: 1, NormalizedQueryHash: query, AnalysisReadSetID: "read-refinement", CausalObservationClosureID: "closure-refinement", CausalObservationClosureDigest: strings.Repeat("c", 64), CapabilityProfileDigest: capabilityDigest, WorkspaceEpoch: 1, CurrentPublication: storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"}, SettlementEvaluation: storage.SettlementEvaluation{Gate: "pending", BlockingObligationRefs: []string{}}, ArtifactRefs: storage.ArtifactRefs{SemanticMap: mapRef, Projection: priorProjectionRef, AnalysisReadSet: readSetRef, ObservationClosure: closureRef, AnalyzerResult: resultRef}, ExpectedLiveHeadSnapshotID: snapshotID, ExpectedPreviousGenerationID: nil, PublishedAt: time.Unix(1, 0).UTC()}
	manifestRef, err := st.WriteManifestCAS(prevManifest)
	if err != nil {
		t.Fatal(err)
	}
	ptr := &storage.ActivePointer{SchemaID: semantic.ActivePointerSchemaID, SchemaVersion: 2, GenerationID: "gen-1", ManifestObjectRef: manifestRef, PublishedAt: time.Unix(1, 0).UTC(), ComputedBasisID: basis, ValidatedAgainstSnapshotID: snapshotID, ExpectedLiveHeadSnapshotID: snapshotID, WorkspaceEpoch: 1, TaskIntentRevision: 1, NormalizedQueryHash: query, FlowCount: 1, RepositoryID: repoID, WorktreeID: worktreeID, TaskID: "task-1"}
	if err := st.CompareAndSwapActivePointer(snapshotID, "", ptr); err != nil {
		t.Fatal(err)
	}
	refinementMap := *mapIR
	refinementMap.GenerationID = "gen-2"
	refinementMap.MapID = "map-refinement-2"
	refinementMap.PublicationKind = "refinement"
	refinementMap.Supersedes = "gen-1"
	closure := semantic.CausalObservationClosure{SchemaID: semantic.ObservationClosureSchemaID, SchemaVersion: 2, ClosureID: result.Closure.ClosureID, ComputedBasisID: basis, WorkspaceEpoch: 1, TaskIntentRevision: 1, NormalizedQueryHash: query, RequiredObservations: []string{"membership"}, MeasuredObservations: []string{"membership"}, AnalysisReadSetID: result.ReadSet.ReadSetID, PositiveDependencies: semantic.PositiveDependencies{DocumentRevisionRefs: []string{"main.go@rev-1"}, ConfigurationFingerprint: "config-refinement"}, NegativeObservations: []semantic.NegativeObservation{}, MembershipObservations: []semantic.MembershipObservation{{Kind: "membership", ContainerRef: ".", MembershipDigest: "membership-refinement"}}, DependencyFrontiers: []semantic.DependencyFrontier{}, CapabilityProfile: &semantic.CapabilityProfile{Adapter: result.Capability.Adapter, Features: append([]string(nil), result.Capability.Features...)}, CoverageBoundary: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}}, ClosureStatus: "closed", ClosureDigest: result.Closure.ClosureDigest}
	closure.CanonicalResult = &result
	projection := &semantic.FlowViewProjection{SchemaID: semantic.FlowViewProjectionSchemaID, SchemaVersion: 2, ProjectionID: "projection-refinement", GenerationID: "gen-2", ComputedBasisID: basis, Mode: "feature", DisplayBudget: semantic.DisplayBudget{TargetMin: 1, TargetMax: 15, Enforcement: "soft"}, VisibleStepRefs: []string{"step-1"}, PreservedStepRefs: []string{"step-1"}, UnknownBoundaryRefs: []string{}, FoldedSubflows: []semantic.FoldedSubflow{}}
	intent, err := semantic.NormalizeTaskIntent("show main", semantic.IntentOptions{Mode: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	intent.TaskID = "task-1"
	intent.Revision = 1
	intent.IntentStatus = "user_confirmed"
	delta := &workspace.WorkspaceDelta{FromSnapshotID: snapshotID, ToSnapshotID: snapshotID, AddedPaths: []string{}, ModifiedPaths: []string{}, DeletedPaths: []string{}, ChangedPaths: []string{}}
	eventBasis, eventSnapshot, eventGeneration := basis, snapshotID, refinementMap.GenerationID
	eventBytes, err := json.Marshal(semantic.EventEnvelope{SchemaID: semantic.EventEnvelopeSchemaID, SchemaVersion: 2, StreamID: "stream-refinement", Sequence: 2, EventID: "event-refinement-2", EventType: "generation.published", OccurredAt: time.Unix(2, 0).UTC(), ComputedBasisID: &eventBasis, ValidatedAgainstSnapshotID: &eventSnapshot, GenerationID: &eventGeneration})
	if err != nil {
		t.Fatal(err)
	}
	input := semantic.LateRefinementInput{Map: &refinementMap, Projection: projection, Closure: &closure, Delta: delta, CapturedSnapshot: snap, LiveHeadSnapshot: snap, Intent: intent, AnalysisRequest: &request, AnalysisResult: &result, PreviousMapBytes: mapBytes, ArtifactBytes: map[string][]byte{}, ArtifactDigests: map[string]string{}, Event: eventBytes, RepositoryID: repoID, WorktreeID: worktreeID, QueryHash: query, ExpectedPreviousID: "gen-1", Metrics: semantic.PublicationMetrics{LagMs: 4, PendingRevisions: 0, MeasuredAt: time.Unix(3, 0).UTC(), Activity: "analyzing", TraceID: "trace-refinement"}, LiveHeadCommit: func(expected string, commit func() error) error {
		if expected != snapshotID {
			return storage.ErrCASConflict
		}
		return commit()
	}}
	return lateRefinementFixture{storage: st, coordinator: semantic.NewRefinementCoordinator(st), input: input}
}

const workspaceSnapshotSchemaID = "https://codeflow.local/schemas/rflsc.workspace-snapshot.v2.schema.json"
