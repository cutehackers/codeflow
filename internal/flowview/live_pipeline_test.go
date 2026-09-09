package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

func TestCanonicalPublicationArtifactsAreValidatedAndBound(t *testing.T) {
	basis := strings.Repeat("b", 64)
	snapshotID := "snapshot-live-artifacts"
	tree := strings.Repeat("t", 64)
	deps := strings.Repeat("d", 64)
	readSetID := "readset-live-artifacts"
	closureID := "closure-live-artifacts"
	mapIR := &semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: 2,
		MapID: "map-live-artifacts", GenerationID: "generation-live-artifacts",
		ComputedBasisID: basis, ValidatedAgainstSnapshotID: snapshotID,
		PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending",
		EnrichmentStatus: "not_requested", Authority: "candidate",
		Quality: semantic.MapQuality{Stage: "Q1", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0},
		Task:    semantic.MapTaskContext{TaskID: "task-live-artifacts", IntentRevision: 1, Mode: "feature"},
		Basis: semantic.MapBasisContext{
			RepositoryID: "repo-live-artifacts", WorktreeID: "worktree-live-artifacts", WorkspaceEpoch: 3,
			ComputedWorkspaceSnapshotID: snapshotID, ComputedBasisID: basis, SnapshotTreeID: tree,
			DependencyFingerprint: deps, AnalysisReadSetID: readSetID, CausalObservationClosureID: closureID,
		},
		Summary:  semantic.MapSummary{Requested: "live artifacts", Current: "candidate"},
		Steps:    []semantic.SemanticStep{},
		Edges:    []semantic.SemanticEdge{},
		Unknowns: []fusion.Unknown{},
		Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}},
	}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		t.Fatalf("fixture semantic map must be canonical: %v", err)
	}
	membership := rflscvs02.Observation{Kind: "membership", Path: ".", ValueHash: "membership-live-artifacts", Measured: true}
	readSet := rflscvs02.AnalysisReadSet{
		SchemaID: rflscvs02.ReadSetSchemaID, SchemaVersion: 2, ReadSetID: readSetID,
		ComputedBasisID: basis, WorkspaceEpoch: 3, Documents: []rflscvs02.ReadDocument{},
		NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{},
	}
	closure := rflscvs02.ObservationClosure{
		SchemaID: rflscvs02.ClosureSchemaID, SchemaVersion: 2, ClosureID: closureID, AnalysisReadSetID: readSetID,
		ComputedBasisID: basis, WorkspaceEpoch: 3, Status: "closed", NegativeObservations: []rflscvs02.Observation{},
		MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{},
		RequiredObservations: []string{"membership"}, MeasuredObservations: []string{"membership"}, ClosureDigest: strings.Repeat("c", 64),
	}
	result := &rflscvs02.Result{
		SchemaID: rflscvs02.AnalyzerResultSchemaID, SchemaVersion: 2, RequestID: "request-live-artifacts", Operation: "detect",
		AdapterVersion: "adapter-live-artifacts/1", AnalyzerRevision: "analyzer-live-artifacts/1", WorkspaceEpoch: 3,
		ComputedBasisID: basis, SnapshotID: snapshotID, SnapshotTreeDigest: tree, DependencyFingerprint: deps,
		ReadSet: readSet, Closure: closure,
		Capability:  rflscvs02.CapabilityProfile{Adapter: "live-artifacts", AdapterVersion: "adapter-live-artifacts/1", AnalyzerRevision: "analyzer-live-artifacts/1", Features: []string{"snapshot_bytes"}},
		Coverage:    rflscvs02.Coverage{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}, Measured: true},
		Diagnostics: []rflscvs02.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`),
	}
	projection := &semantic.FlowViewProjection{
		SchemaID: contractharness.FlowProjectionV2SchemaID, SchemaVersion: 2, ProjectionID: "projection-live-artifacts",
		GenerationID: mapIR.GenerationID, ComputedBasisID: basis, Mode: "feature",
		DisplayBudget:   semantic.DisplayBudget{TargetMin: 1, TargetMax: 15, Enforcement: "soft"},
		VisibleStepRefs: []string{}, PreservedStepRefs: []string{}, UnknownBoundaryRefs: []string{}, FoldedSubflows: []semantic.FoldedSubflow{},
	}
	refs, artifacts, digests, err := canonicalPublicationArtifacts(mapBytes, projection, result)
	if err != nil {
		t.Fatalf("canonical publication bundle rejected: %v", err)
	}
	for name, ref := range map[string]string{
		"semanticMap": refs.SemanticMap, "projection": refs.Projection, "analysisReadSet": refs.AnalysisReadSet,
		"observationClosure": refs.ObservationClosure, "analyzerResult": refs.AnalyzerResult,
	} {
		if ref == "" || artifacts[ref] == nil {
			t.Fatalf("canonical %s artifact is missing: ref=%q", name, ref)
		}
		if storage.ArtifactCASRef(artifacts[ref]) != ref {
			t.Fatalf("canonical %s artifact ref is not derived from staged bytes", name)
		}
		if digests[name] != strings.TrimPrefix(ref, "cas:sha256:") {
			t.Fatalf("canonical %s digest is not bound to its ref", name)
		}
	}
}

func TestLivePipelinePublishesAndStrictlyRereadsCompleteProofBundle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	query := &semantic.TaskViewQuery{
		SchemaID: semantic.FeatureQuerySchemaID, SchemaVersion: 2, Mode: "feature",
		Feature: &semantic.FeatureQueryParams{Request: "show main"},
	}
	if err := srv.RememberTaskQuery(query, query.Feature.Request); err != nil {
		t.Fatal(err)
	}
	srv.compileCandidate = func(ctx context.Context, snapshot protocol.Snapshot, _ *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		input, err := snapshot.AnalyzerInput()
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		doc := snapshot.Documents[0]
		content := []byte(snapshot.Files[doc.Path])
		readDocuments := make([]rflscvs02.ReadDocument, 0, len(input.Documents))
		for _, item := range input.Documents {
			readDocuments = append(readDocuments, rflscvs02.ReadDocument{Path: item.Path, DocumentRevisionID: item.RevisionID, ContentID: item.ContentID, ContentHash: item.ContentID, DocumentVersion: item.DocumentVersion, ByteLength: item.ByteLength})
		}
		membership := rflscvs02.Observation{Kind: "membership", Path: ".", ValueHash: "membership-live-pipeline", Measured: true}
		readSet := rflscvs02.AnalysisReadSet{SchemaID: rflscvs02.ReadSetSchemaID, SchemaVersion: 2, ReadSetID: "readset-live-pipeline", ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, Documents: readDocuments, NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{}}
		canonicalClosure := rflscvs02.ObservationClosure{SchemaID: rflscvs02.ClosureSchemaID, SchemaVersion: 2, ClosureID: "closure-live-pipeline", AnalysisReadSetID: readSet.ReadSetID, ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, Status: "closed", NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{}, RequiredObservations: []string{"membership"}, MeasuredObservations: []string{"membership"}, ClosureDigest: strings.Repeat("c", 64)}
		result := &rflscvs02.Result{SchemaID: rflscvs02.AnalyzerResultSchemaID, SchemaVersion: 2, RequestID: "request-live-pipeline", Operation: "detect", AdapterVersion: "adapter-live-pipeline/1", AnalyzerRevision: "analyzer-live-pipeline/1", WorkspaceEpoch: input.WorkspaceEpoch, ComputedBasisID: input.ComputedBasisID, SnapshotID: input.SnapshotID, SnapshotTreeDigest: input.RootTreeID, DependencyFingerprint: input.DependencyFingerprint, ReadSet: readSet, Closure: canonicalClosure, Capability: rflscvs02.CapabilityProfile{Adapter: "live-pipeline", AdapterVersion: "adapter-live-pipeline/1", AnalyzerRevision: "analyzer-live-pipeline/1", Features: []string{"snapshot_bytes", "relation:calls"}}, Coverage: rflscvs02.Coverage{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}, Measured: true}, Diagnostics: []rflscvs02.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`)}
		anchor := slicing.Anchor{RepoRelativePath: doc.Path, ByteRange: [2]int{0, len(content)}, FileHash: doc.ContentID, SpanHash: doc.ContentID, EnclosingSymbolPath: "main", CanonicalAstFingerprint: "ast-live-pipeline"}
		evidenceID := semantic.EvidenceIDForAnchor("flow-live-pipeline", anchor)
		intent, err := semantic.NormalizeTaskIntent("show main", semantic.IntentOptions{Mode: "feature"})
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		intent.TaskID, intent.Revision, intent.IntentStatus = "task-live-pipeline", 1, "parsed"
		mapIR := &semantic.SemanticMapIR{SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: 2, MapID: "map-live-pipeline", GenerationID: "generation-live-pipeline", ComputedBasisID: snapshot.ComputedBasisID, ValidatedAgainstSnapshotID: snapshot.SnapshotID, PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending", EnrichmentStatus: "not_requested", Authority: "candidate", Quality: semantic.MapQuality{Stage: "Q2", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0}, Task: semantic.MapTaskContext{TaskID: intent.TaskID, IntentRevision: intent.Revision, IntentStatus: intent.IntentStatus, Mode: intent.Mode}, Basis: semantic.MapBasisContext{RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID, DependencyFingerprint: snapshot.DependencyFingerprint, ConfigurationFingerprint: snapshot.ConfigurationFingerprint, AnalysisReadSetID: readSet.ReadSetID, CausalObservationClosureID: canonicalClosure.ClosureID}, Summary: semantic.MapSummary{Requested: "show main", Current: "candidate"}, Steps: []semantic.SemanticStep{{StepID: "step-live-pipeline", StructuralIdentity: "flow-live-pipeline|main.go|main|action", Ordinal: 1, Name: "main", TechnicalName: "main", Kind: "action", Anchor: anchor, CodeLens: &fusion.CodeLens{Path: doc.Path, StartLine: 1, EndLine: 1}, EvidenceRefs: []string{evidenceID}}}, Edges: []semantic.SemanticEdge{}, Unknowns: []fusion.Unknown{}, Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}}, Evidence: []semantic.SemanticEvidence{{EvidenceID: evidenceID, Kind: "source", SourceAuthority: "code", ComputedBasisID: snapshot.ComputedBasisID, DocumentRevisionID: doc.RevisionID, Anchor: anchor, Producer: &semantic.ProducerInfo{Name: "live-pipeline", Version: "1"}, ValidationStatus: "verified", RedactionStatus: "passed", SnapshotID: snapshot.SnapshotID, ByteRange: anchor.ByteRange, LineRange: [2]int{1, 1}}}}
		closure := semanticClosureFromVS02(result, snapshot.ConfigurationFingerprint)
		closure.TaskIntentRevision = intent.Revision
		closure.NormalizedQueryHash = queryIdentity(query)
		projection := semantic.BuildFlowViewProjection(mapIR)
		payload := &slicing.SlicedPayload{ValidatedResult: result}
		return mapIR, projection, payload, &semantic.ResolvedTarget{FlowID: "flow-live-pipeline", EntrySymbolPath: "main", Title: "show main"}, intent, &closure, nil
	}
	_, snap, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.processCheckpoint(context.Background(), snap); err != nil {
		t.Fatalf("live pipeline publication failed: %v", err)
	}
	manifest, pointer, err := srv.storage.ReadValidatedActiveProofManifest()
	if err != nil {
		t.Fatalf("published proof did not survive strict reread: %v", err)
	}
	if manifest == nil || pointer == nil || manifest.ArtifactRefs.SemanticMap == "" || manifest.ArtifactRefs.Projection == "" || manifest.ArtifactRefs.AnalysisReadSet == "" || manifest.ArtifactRefs.ObservationClosure == "" || manifest.ArtifactRefs.AnalyzerResult == "" {
		t.Fatalf("live pipeline published an incomplete proof bundle: manifest=%+v pointer=%+v", manifest, pointer)
	}
	impactURL := "http://127.0.0.1/api/task/impact?symbolId=main&computedBasisId=" + pointer.ComputedBasisID + "&generationId=" + pointer.GenerationID + "&freshness=current&maxDepth=3&maxNodes=50&relationKinds=calls&token=" + srv.AuthToken()
	request := httptest.NewRequest(http.MethodGet, impactURL, nil)
	recorder := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("strict current impact query failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var graph semantic.ChangeImpactGraph
	if err := json.Unmarshal(recorder.Body.Bytes(), &graph); err != nil {
		t.Fatalf("decode current impact graph: %v", err)
	}
	if graph.Freshness != "current" || !graph.IndirectImpact.CompletedWithinCoverage || graph.UnknownCount != 0 {
		t.Fatalf("current impact did not retain complete proof authority: %+v", graph)
	}
	_, _, err = srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n\nvar changed = true\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	staleRequest := httptest.NewRequest(http.MethodGet, impactURL, nil)
	staleRecorder := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(staleRecorder, staleRequest)
	if staleRecorder.Code != http.StatusConflict || !strings.Contains(staleRecorder.Body.String(), "current_proof_unavailable") {
		t.Fatalf("stale current impact must fail closed after edit: status=%d body=%s", staleRecorder.Code, staleRecorder.Body.String())
	}
}

func TestLiveConsumerCancelsSupersededCheckpointAndRetainsNewest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte("export const value = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the production consumer and HTTP lifecycle, but shorten the
	// coalescing window so the test observes the real scheduler boundary.
	srv.scheduler = semantic.NewCoalescingScheduler(semantic.CoalescingConfig{
		QuietWindow:  5 * time.Millisecond,
		MaxWait:      20 * time.Millisecond,
		MaxQueueSize: 8,
	})
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	firstStarted := make(chan string, 1)
	firstCanceled := make(chan string, 1)
	secondStarted := make(chan string, 1)
	var calls atomic.Int32
	srv.compileCandidate = func(ctx context.Context, snapshot protocol.Snapshot, _ *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
		if calls.Add(1) == 1 {
			firstStarted <- snapshot.SnapshotID
			<-ctx.Done()
			firstCanceled <- snapshot.SnapshotID
			return nil, nil, nil, nil, nil, nil, ctx.Err()
		}
		secondStarted <- snapshot.SnapshotID
		return nil, nil, nil, nil, nil, nil, errors.New("test compiler stopped after supersession")
	}
	if err := srv.RememberTaskQuery(&semantic.TaskViewQuery{
		SchemaID: semantic.FeatureQuerySchemaID, SchemaVersion: 2, Mode: "feature",
		Feature: &semantic.FeatureQueryParams{Request: "show the value"},
	}, "show the value"); err != nil {
		t.Fatal(err)
	}
	srv.Start()

	_, first, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "main.ts", Content: []byte("export const value = 2;\n"), DocumentVersion: 1, Source: workspace.SourceAgentTransaction,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-firstStarted:
		if got != first.SnapshotID {
			t.Fatalf("first compile used %q, want %q", got, first.SnapshotID)
		}
	case <-time.After(time.Second):
		t.Fatal("first checkpoint did not enter the compiler")
	}

	_, second, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "main.ts", Content: []byte("export const value = 3;\n"), DocumentVersion: 2, Source: workspace.SourceAgentTransaction,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-firstCanceled:
		if got != first.SnapshotID {
			t.Fatalf("canceled checkpoint was %q, want stale %q", got, first.SnapshotID)
		}
	case <-time.After(time.Second):
		t.Fatal("new edit did not cancel the in-flight checkpoint")
	}
	select {
	case got := <-secondStarted:
		if got != second.SnapshotID {
			t.Fatalf("newest checkpoint used %q, want %q", got, second.SnapshotID)
		}
	case <-time.After(time.Second):
		t.Fatal("newest checkpoint was not retained after cancellation")
	}

	srv.hub.mu.Lock()
	defer srv.hub.mu.Unlock()
	for _, event := range srv.hub.ringBuffer {
		if (event.EventType == "generation.published" || event.EventType == "generation.gap") && event.ValidatedAgainstSnapshotID != nil && *event.ValidatedAgainstSnapshotID == first.SnapshotID {
			t.Fatalf("superseded checkpoint emitted terminal event: type=%s snapshot=%s", event.EventType, first.SnapshotID)
		}
	}
}

func TestLivePipelinePublishesSemanticDeltaForCommittedBatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nvar value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	query := &semantic.TaskViewQuery{SchemaID: semantic.FeatureQuerySchemaID, SchemaVersion: 2, Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: "show main"}}
	if err := srv.RememberTaskQuery(query, query.Feature.Request); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	srv.compileCandidate = func(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
		generation := fmt.Sprintf("generation-live-delta-%d", calls.Add(1))
		return liveDeltaCandidate(ctx, snapshot, query, generation)
	}
	_, firstSnapshot, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\nvar value = 1\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.processCheckpoint(context.Background(), firstSnapshot); err != nil {
		t.Fatalf("first publication failed: %v", err)
	}

	tx, err := srv.engine.BeginTransaction(workspace.SourceAgentTransaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.StageEdit(workspace.EditRequest{Path: "main.go", Content: []byte("package main\nvar value = 2\n"), DocumentVersion: 2}); err != nil {
		t.Fatal(err)
	}
	batch, secondSnapshot, err := srv.engine.CommitTransaction(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.processCheckpoint(context.Background(), secondSnapshot); err != nil {
		t.Fatalf("second publication failed: %v", err)
	}

	manifest, pointer, err := srv.storage.ReadValidatedActiveProofManifest()
	if err != nil {
		t.Fatalf("strict proof reread failed: %v", err)
	}
	if manifest == nil || pointer == nil || manifest.ArtifactRefs.SemanticDelta == "" {
		t.Fatalf("committed successor did not publish a semantic delta: manifest=%+v pointer=%+v", manifest, pointer)
	}
	bundle, err := srv.storage.ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatal(err)
	}
	var delta semantic.SemanticDeltaIR
	if err := json.Unmarshal(bundle.SemanticDelta, &delta); err != nil {
		t.Fatal(err)
	}
	if delta.Status != "comparable" || delta.FromGeneration != "generation-live-delta-1" || delta.ToGeneration != "generation-live-delta-2" || delta.BaselineComputedBasisID != firstSnapshot.ComputedBasisID || delta.CurrentComputedBasisID != secondSnapshot.ComputedBasisID || delta.CurrentValidatedAgainstSnapshotID != secondSnapshot.SnapshotID || delta.TaskIntentRevision != 1 {
		t.Fatalf("semantic delta identity is not bound to the two publications: %+v", delta)
	}
	if manifest.WorkspaceEpoch != batch.WorkspaceEpoch || len(batch.Revisions) != 1 {
		t.Fatalf("committed batch identity is not bound to publication: manifest epoch=%d batch=%+v", manifest.WorkspaceEpoch, batch)
	}

	liveURL := fmt.Sprintf("http://127.0.0.1/api/live/generation?generationId=%s&computedBasisId=%s&snapshotId=%s&token=%s", pointer.GenerationID, pointer.ComputedBasisID, pointer.ValidatedAgainstSnapshotID, srv.AuthToken())
	liveRecorder := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(liveRecorder, httptest.NewRequest(http.MethodGet, liveURL, nil))
	if liveRecorder.Code != http.StatusOK || !strings.Contains(liveRecorder.Body.String(), `"semanticDelta"`) || !strings.Contains(liveRecorder.Body.String(), `"proofManifest"`) {
		t.Fatalf("generation-bound Live view did not return persisted proof artifacts: status=%d body=%s", liveRecorder.Code, liveRecorder.Body.String())
	}
	mismatchRecorder := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(mismatchRecorder, httptest.NewRequest(http.MethodGet, strings.Replace(liveURL, "snapshotId="+pointer.ValidatedAgainstSnapshotID, "snapshotId=snapshot-mismatch", 1), nil))
	if mismatchRecorder.Code != http.StatusConflict || !strings.Contains(mismatchRecorder.Body.String(), "generation_unavailable") {
		t.Fatalf("cross-proof Live view request did not fail closed: status=%d body=%s", mismatchRecorder.Code, mismatchRecorder.Body.String())
	}

	impactURL := fmt.Sprintf("http://127.0.0.1/api/task/impact?changeBatchId=%s&computedBasisId=%s&generationId=%s&freshness=current&maxDepth=3&maxNodes=50&relationKinds=calls&token=%s", batch.BatchID, pointer.ComputedBasisID, pointer.GenerationID, srv.AuthToken())
	recorder := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, impactURL, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("committed batch impact query failed: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var graph semantic.ChangeImpactGraph
	if err := json.Unmarshal(recorder.Body.Bytes(), &graph); err != nil {
		t.Fatal(err)
	}
	if graph.Freshness != "current" || graph.Target.ChangeBatchID != batch.BatchID {
		t.Fatalf("committed batch impact did not retain exact current identity: %+v", graph)
	}
}

func liveDeltaCandidate(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery, generation string) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	input, err := snapshot.AnalyzerInput()
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if len(snapshot.Documents) == 0 {
		return nil, nil, nil, nil, nil, nil, errors.New("test snapshot has no documents")
	}
	doc := snapshot.Documents[0]
	suffix := strings.TrimPrefix(generation, "generation-")
	readSetID := "readset-" + suffix
	closureID := "closure-" + suffix
	membership := rflscvs02.Observation{Kind: "membership", Path: ".", ValueHash: "membership-live-delta", Measured: true}
	readDocuments := make([]rflscvs02.ReadDocument, 0, len(input.Documents))
	for _, item := range input.Documents {
		readDocuments = append(readDocuments, rflscvs02.ReadDocument{Path: item.Path, DocumentRevisionID: item.RevisionID, ContentID: item.ContentID, ContentHash: item.ContentID, DocumentVersion: item.DocumentVersion, ByteLength: item.ByteLength})
	}
	readSet := rflscvs02.AnalysisReadSet{SchemaID: rflscvs02.ReadSetSchemaID, SchemaVersion: 2, ReadSetID: readSetID, ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, Documents: readDocuments, NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{}}
	canonicalClosure := rflscvs02.ObservationClosure{SchemaID: rflscvs02.ClosureSchemaID, SchemaVersion: 2, ClosureID: closureID, AnalysisReadSetID: readSetID, ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, Status: "closed", NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{}, RequiredObservations: []string{"membership"}, MeasuredObservations: []string{"membership"}, ClosureDigest: strings.Repeat("d", 64)}
	result := &rflscvs02.Result{SchemaID: rflscvs02.AnalyzerResultSchemaID, SchemaVersion: 2, RequestID: "request-" + suffix, Operation: "detect", AdapterVersion: "adapter-live-delta/1", AnalyzerRevision: "analyzer-live-delta/1", WorkspaceEpoch: input.WorkspaceEpoch, ComputedBasisID: input.ComputedBasisID, SnapshotID: input.SnapshotID, SnapshotTreeDigest: input.RootTreeID, DependencyFingerprint: input.DependencyFingerprint, ReadSet: readSet, Closure: canonicalClosure, Capability: rflscvs02.CapabilityProfile{Adapter: "live-delta", AdapterVersion: "adapter-live-delta/1", AnalyzerRevision: "analyzer-live-delta/1", Features: []string{"snapshot_bytes", "relation:calls"}}, Coverage: rflscvs02.Coverage{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}, Measured: true}, Diagnostics: []rflscvs02.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`)}
	intent, err := semantic.NormalizeTaskIntent(query.Feature.Request, semantic.IntentOptions{Mode: query.Mode})
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	intent.TaskID, intent.Revision, intent.IntentStatus = "task-live-delta", 1, "parsed"
	anchor := slicing.Anchor{RepoRelativePath: doc.Path, ByteRange: [2]int{0, doc.ByteLength}, FileHash: doc.ContentID, SpanHash: doc.ContentID, EnclosingSymbolPath: "main", CanonicalAstFingerprint: "ast-live-delta"}
	evidenceID := semantic.EvidenceIDForAnchor("flow-live-delta-"+generation, anchor)
	mapIR := &semantic.SemanticMapIR{SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: 2, MapID: "map-" + generation, GenerationID: generation, ComputedBasisID: snapshot.ComputedBasisID, ValidatedAgainstSnapshotID: snapshot.SnapshotID, PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending", EnrichmentStatus: "not_requested", Authority: "candidate", Quality: semantic.MapQuality{Stage: "Q2", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0}, Task: semantic.MapTaskContext{TaskID: intent.TaskID, IntentRevision: intent.Revision, IntentStatus: intent.IntentStatus, Mode: intent.Mode}, Basis: semantic.MapBasisContext{RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID, DependencyFingerprint: snapshot.DependencyFingerprint, ConfigurationFingerprint: snapshot.ConfigurationFingerprint, AnalysisReadSetID: readSetID, CausalObservationClosureID: closureID}, Summary: semantic.MapSummary{Requested: query.Feature.Request, Current: "candidate"}, Steps: []semantic.SemanticStep{{StepID: "step-live-delta", StructuralIdentity: "flow-live-delta|main.go|main|action", Ordinal: 1, Name: "main", TechnicalName: "main", Kind: "action", Anchor: anchor, EvidenceRefs: []string{evidenceID}}}, Edges: []semantic.SemanticEdge{}, Unknowns: []fusion.Unknown{}, Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}}, Evidence: []semantic.SemanticEvidence{{EvidenceID: evidenceID, Kind: "source", SourceAuthority: "code", ComputedBasisID: snapshot.ComputedBasisID, DocumentRevisionID: doc.RevisionID, Anchor: anchor, Producer: &semantic.ProducerInfo{Name: "live-delta", Version: "1"}, ValidationStatus: "verified", RedactionStatus: "passed", SnapshotID: snapshot.SnapshotID, ByteRange: anchor.ByteRange, LineRange: [2]int{1, 1}}}}
	closure := semanticClosureFromVS02(result, snapshot.ConfigurationFingerprint)
	closure.TaskIntentRevision = intent.Revision
	closure.NormalizedQueryHash = queryIdentity(query)
	projection := semantic.BuildFlowViewProjection(mapIR)
	return mapIR, projection, &slicing.SlicedPayload{ValidatedResult: result}, &semantic.ResolvedTarget{FlowID: "flow-live-delta", EntrySymbolPath: "main", Title: query.Feature.Request}, intent, &closure, nil
}
