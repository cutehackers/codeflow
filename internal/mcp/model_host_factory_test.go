package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/flowview"
	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

func TestMCPEnrichmentRequestScopedFactory(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	if _, ok := fixture.server.semanticMaps.Load(semanticMapCacheKey(fixture.root, fixture.generation)); ok {
		t.Fatal("restart fixture unexpectedly retained a process-local semantic map")
	}

	call := func() (*semantic.EnrichmentResult, error) {
		value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
			"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-factory",
		})
		if err != nil {
			return nil, fmt.Errorf("MCP enrichment failed: %w", err)
		}
		result, ok := value.(*semantic.EnrichmentResult)
		if !ok {
			return nil, fmt.Errorf("MCP enrichment type = %T", value)
		}
		if result.State.Status != "available" || result.Proposal == nil || !result.State.Isolation.CleanupVerified {
			return result, fmt.Errorf("MCP enrichment result = %+v", result.State)
		}
		return result, nil
	}

	if _, err := call(); err != nil {
		t.Fatal(err)
	}
	if _, err := call(); err != nil {
		t.Fatal(err)
	}
	if fixture.calls.Load() != 2 {
		t.Fatalf("sequential factory calls = %d, want 2", fixture.calls.Load())
	}
	fixture.hostsMu.Lock()
	if len(fixture.hosts) != 2 || fixture.hosts[0] == fixture.hosts[1] {
		fixture.hostsMu.Unlock()
		t.Fatal("sequential MCP requests reused a host identity")
	}
	fixture.hostsMu.Unlock()

	var group sync.WaitGroup
	type mcpCall struct {
		result *semantic.EnrichmentResult
		err    error
	}
	concurrent := make(chan mcpCall, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := call()
			concurrent <- mcpCall{result: result, err: err}
		}()
	}
	group.Wait()
	close(concurrent)
	for outcome := range concurrent {
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result == nil || outcome.result.State.Status != "available" {
			t.Fatalf("concurrent MCP enrichment result = %+v", outcome.result)
		}
	}
	if fixture.calls.Load() != 4 {
		t.Fatalf("concurrent factory calls = %d, want 4", fixture.calls.Load())
	}
	for name, args := range map[string]map[string]any{
		"unknown generation":     {"target": fixture.root, "generationId": "generation-unknown", "computedBasisId": fixture.basis, "targetStepId": fixture.stepID},
		"conflicting identities": {"target": fixture.root, "generationId": fixture.generation, "computedBasisId": "basis-conflicting", "targetStepId": fixture.stepID},
	} {
		value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", args)
		if err != nil {
			t.Fatalf("%s request failed: %v", name, err)
		}
		result, ok := value.(*semantic.EnrichmentResult)
		if !ok || result.State.Status != "unavailable" {
			t.Fatalf("%s result = %#v, want unavailable", name, value)
		}
	}
	if fixture.calls.Load() != 4 {
		t.Fatalf("identity-miss factory calls = %d, want 4", fixture.calls.Load())
	}
	fixture.hostsMu.Lock()
	defer fixture.hostsMu.Unlock()
	if len(fixture.hosts) != 4 {
		t.Fatalf("recorded MCP hosts = %d, want 4", len(fixture.hosts))
	}
	for i, host := range fixture.hosts {
		if host == nil || !host.IsolationEvidence().CleanupVerified {
			t.Fatalf("MCP host %d cleanup evidence = %+v", i, host.IsolationEvidence())
		}
		for j := i + 1; j < len(fixture.hosts); j++ {
			if host == fixture.hosts[j] {
				t.Fatalf("MCP requests %d and %d reused a host identity", i, j)
			}
		}
	}
	if entries, err := os.ReadDir(fixture.disposableRoot); err != nil {
		t.Fatalf("read MCP disposable root: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("MCP request host workdirs remain: %v", entries)
	}
}

func TestMCPLazyFlowViewCoordinatorUsesModelHostFactory(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	coordinator, err := fixture.server.getLiveCoordinator(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("http://%s/api/semantic/enrich?token=%s", coordinator.Addr(), coordinator.AuthToken())
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(fmt.Sprintf(`{"generationId":%q,"targetStepId":%q,"promptRevision":"prompt-mcp-lazy"}`, fixture.generation, fixture.stepID)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lazy FlowView enrichment status = %d", resp.StatusCode)
	}
	var result semantic.EnrichmentResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.State.Status != "available" || result.Proposal == nil || !result.State.Isolation.CleanupVerified {
		t.Fatalf("lazy FlowView enrichment result = %+v", result.State)
	}
	if fixture.calls.Load() != 1 {
		t.Fatalf("lazy FlowView factory calls = %d, want 1", fixture.calls.Load())
	}
	fixture.hostsMu.Lock()
	defer fixture.hostsMu.Unlock()
	if len(fixture.hosts) != 1 || fixture.hosts[0] == nil || !fixture.hosts[0].IsolationEvidence().CleanupVerified {
		t.Fatalf("lazy FlowView host cleanup evidence = %+v", fixture.hosts)
	}
	if entries, err := os.ReadDir(fixture.disposableRoot); err != nil {
		t.Fatalf("read lazy FlowView disposable root: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("lazy FlowView request host workdirs remain: %v", entries)
	}
}

func TestMCPShutdownRejectsLateLazyFlowViewRESTFactory(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	coordinator, err := fixture.server.getLiveCoordinator(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("http://%s/api/semantic/enrich?token=%s", coordinator.Addr(), coordinator.AuthToken())
	fixture.server.modelHostSpawnMu.Lock()
	type httpOutcome struct {
		response *http.Response
		err      error
	}
	requestDone := make(chan httpOutcome, 1)
	go func() {
		req, requestErr := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(fmt.Sprintf(`{"generationId":%q,"targetStepId":%q}`, fixture.generation, fixture.stepID)))
		if requestErr != nil {
			requestDone <- httpOutcome{err: requestErr}
			return
		}
		response, requestErr := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		requestDone <- httpOutcome{response: response, err: requestErr}
	}()
	closeDone := make(chan error, 1)
	go func() { closeDone <- fixture.server.Close() }()
	deadline := time.NewTimer(time.Second)
	for !fixture.server.modelHostClosing.Load() {
		select {
		case <-deadline.C:
			fixture.server.modelHostSpawnMu.Unlock()
			t.Fatal("MCP close did not close lazy REST admission")
		default:
			runtime.Gosched()
		}
	}
	deadline.Stop()
	fixture.server.modelHostSpawnMu.Unlock()
	if closeErr := <-closeDone; closeErr != nil {
		t.Fatalf("MCP close error = %v", closeErr)
	}
	outcome := <-requestDone
	if outcome.response != nil {
		_ = outcome.response.Body.Close()
	}
	if fixture.calls.Load() != 0 {
		t.Fatalf("late lazy REST factory calls = %d, want 0", fixture.calls.Load())
	}
}

func TestMCPEnrichmentFactoryShutdownAdmission(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	var calls atomic.Int32
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
		calls.Add(1)
		return nil, errors.New("shutdown admission test factory must not be called")
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if calls.Load() != 0 {
		t.Fatalf("unused MCP factory spawned %d hosts", calls.Load())
	}
	_, _, release, err := srv.beginModelHostRequest(context.Background())
	release()
	if !errors.Is(err, errMCPModelHostServerClosed) {
		t.Fatalf("post-shutdown admission error = %v, want %v", err, errMCPModelHostServerClosed)
	}
	if calls.Load() != 0 {
		t.Fatalf("post-shutdown admission spawned %d hosts", calls.Load())
	}
}

func TestMCPShutdownRejectsLazyCoordinatorCreation(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	var calls atomic.Int32
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root, ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
		calls.Add(1)
		return nil, errors.New("lazy coordinator factory must not run after close")
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv.modelHostMu.Lock()
	coordinatorDone := make(chan error, 1)
	go func() {
		_, coordinatorErr := srv.getLiveCoordinator(root)
		coordinatorDone <- coordinatorErr
	}()
	closeDone := make(chan error, 1)
	go func() { closeDone <- srv.Close() }()
	deadline := time.NewTimer(time.Second)
	for !srv.modelHostClosing.Load() {
		select {
		case <-deadline.C:
			srv.modelHostMu.Unlock()
			t.Fatal("MCP close did not close admission")
		default:
			runtime.Gosched()
		}
	}
	deadline.Stop()
	srv.modelHostMu.Unlock()
	if coordinatorErr := <-coordinatorDone; !errors.Is(coordinatorErr, errMCPModelHostServerClosed) {
		t.Fatalf("lazy coordinator error = %v, want closed admission", coordinatorErr)
	}
	if closeErr := <-closeDone; closeErr != nil {
		t.Fatalf("MCP close error = %v", closeErr)
	}
	if calls.Load() != 0 {
		t.Fatalf("lazy coordinator factory calls after close = %d, want 0", calls.Load())
	}
}

func TestMCPEnrichmentShutdownCancelsAdmittedRequest(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	started := make(chan struct{})
	var calls atomic.Int32
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), ModelHostFactory: func(ctx context.Context) (*protocol.ModelHost, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, factory, release, err := srv.beginModelHostRequest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	spawnDone := make(chan error, 1)
	go func() {
		_, err := factory(requestCtx)
		spawnDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("admitted factory did not start")
	}
	released := make(chan struct{})
	go func() {
		<-spawnDone
		release()
		close(released)
	}()
	srv.Close()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel admitted factory")
	}
	if calls.Load() != 1 {
		t.Fatalf("admitted factory calls = %d, want 1", calls.Load())
	}
}

func TestMCPModelHostFactoryAdmissionClosesBeforeWaitingCall(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	started := make(chan struct{})
	var calls atomic.Int32
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), ModelHostFactory: func(ctx context.Context) (*protocol.ModelHost, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, factory, release, err := srv.beginModelHostRequest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	srv.modelHostSpawnMu.Lock()
	invokeDone := make(chan error, 1)
	go func() {
		_, err := factory(requestCtx)
		invokeDone <- err
	}()
	stopDone := make(chan error, 1)
	go func() { stopDone <- srv.stopModelHostRequests(context.Background()) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !srv.modelHostClosing.Load() {
		select {
		case <-deadline.C:
			srv.modelHostSpawnMu.Unlock()
			release()
			t.Fatal("MCP shutdown did not close admission")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-started:
		srv.modelHostSpawnMu.Unlock()
		release()
		t.Fatal("MCP factory started while spawn admission was held")
	default:
	}
	srv.modelHostSpawnMu.Unlock()
	if err := <-invokeDone; !errors.Is(err, errMCPModelHostServerClosed) {
		t.Fatalf("waiting MCP factory error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("stop MCP model-host requests: %v", err)
	}
	release()
	if calls.Load() != 0 {
		t.Fatalf("MCP factory calls after shutdown admission closed = %d, want 0", calls.Load())
	}
}

type mcpEnrichmentFactoryFixture struct {
	server         *Server
	root           string
	disposableRoot string
	generation     string
	stepID         string
	basis          string
	calls          atomic.Int32
	hostsMu        sync.Mutex
	hosts          []*protocol.ModelHost
}

func newMCPEnrichmentFactoryFixture(t *testing.T) *mcpEnrichmentFactoryFixture {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := &mcpEnrichmentFactoryFixture{root: root, generation: "generation-mcp-factory", stepID: "step-mcp-enrichment"}
	coordServer, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	coord, err := coordServer.getLiveCoordinator(root)
	if err != nil {
		coordServer.Close()
		t.Fatal(err)
	}
	_, snapshot, err := coord.SnapshotEngine().ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		coordServer.Close()
		t.Fatal(err)
	}
	protocolSnapshot, release, err := coordServer.captureAnalysisSnapshot(context.Background(), root)
	if err != nil {
		coordServer.Close()
		t.Fatal(err)
	}
	defer release()
	if protocolSnapshot.SnapshotID != snapshot.SnapshotID {
		coordServer.Close()
		t.Fatalf("fixture capture snapshot = %q, want %q", protocolSnapshot.SnapshotID, snapshot.SnapshotID)
	}
	mapIR, readSet, closure, analyzerResult := mcpEnrichmentPublicationFixture(t, protocolSnapshot, fixture.generation, fixture.stepID)
	fixture.basis = mapIR.ComputedBasisID
	st, err := coordServer.getStorage(root)
	if err != nil {
		coordServer.Close()
		t.Fatal(err)
	}
	if err := publishMCPEnrichmentFixture(t, coord, st, mapIR, readSet, closure, analyzerResult, protocolSnapshot); err != nil {
		coordServer.Close()
		t.Fatal(err)
	}
	// Reuse the prepared coordinator and storage state in the factory-backed
	// server. The public MCP path still resolves both through its own server.
	var digestMu sync.RWMutex
	digests, err := semantic.CanonicalQ3Digests(mapIR)
	if err != nil {
		coordServer.Close()
		t.Fatal(err)
	}
	disposableRoot := t.TempDir()
	fixture.disposableRoot = disposableRoot
	factory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		fixture.calls.Add(1)
		digestMu.RLock()
		current := digests
		digestMu.RUnlock()
		host, err := protocol.SpawnModelHost(ctx, protocol.ModelHostConfig{
			BinPath: os.Args[0], Args: []string{"-test.run=TestMCPModelHostHelper"}, DisposableRoot: disposableRoot,
			Env: []string{"CODEFLOW_MODEL_HOST_HELPER=mcp", "CODEFLOW_VS08_MODEL_HOST_RELEASE=" + strings.Join([]string{current.Fact, current.Obligation, current.Alignment, current.Settlement}, "|")}, DefaultTimeout: 3 * time.Second,
		})
		if err == nil {
			fixture.hostsMu.Lock()
			fixture.hosts = append(fixture.hosts, host)
			fixture.hostsMu.Unlock()
		}
		return host, err
	})
	// The coordinator was built only to establish a real snapshot/publication.
	// Close it before creating the server under test; the server creates no
	// second coordinator until its request captures the same durable state.
	coordServer.Close()
	server, err := NewServer(Config{RepoRoot: root, ModelHostFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	fixture.server = server
	return fixture
}

func mcpEnrichmentPublicationFixture(t *testing.T, snapshot protocol.Snapshot, generation, stepID string) (*semantic.SemanticMapIR, rflscvs02.AnalysisReadSet, rflscvs02.ObservationClosure, *rflscvs02.Result) {
	t.Helper()
	if len(snapshot.Documents) == 0 {
		t.Fatal("fixture snapshot has no documents")
	}
	doc := snapshot.Documents[0]
	content := snapshot.Files[doc.Path]
	input, err := snapshot.AnalyzerInput()
	if err != nil {
		t.Fatal(err)
	}
	readDocuments := make([]rflscvs02.ReadDocument, 0, len(input.Documents))
	for _, item := range input.Documents {
		readDocuments = append(readDocuments, rflscvs02.ReadDocument{Path: item.Path, DocumentRevisionID: item.RevisionID, ContentID: item.ContentID, ContentHash: item.ContentID, DocumentVersion: item.DocumentVersion, ByteLength: item.ByteLength})
	}
	readSetID := "readset-mcp-factory"
	closureID := "closure-mcp-factory"
	membership := rflscvs02.Observation{Kind: "membership", Path: ".", ValueHash: "membership-mcp-factory", Measured: true}
	readSet := rflscvs02.AnalysisReadSet{SchemaID: rflscvs02.ReadSetSchemaID, SchemaVersion: 2, ReadSetID: readSetID, ComputedBasisID: snapshot.ComputedBasisID, WorkspaceEpoch: snapshot.WorkspaceEpoch, Documents: readDocuments, NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{}}
	closure := rflscvs02.ObservationClosure{SchemaID: rflscvs02.ClosureSchemaID, SchemaVersion: 2, ClosureID: closureID, AnalysisReadSetID: readSetID, ComputedBasisID: snapshot.ComputedBasisID, WorkspaceEpoch: snapshot.WorkspaceEpoch, Status: "closed", NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{}, RequiredObservations: []string{"membership"}, MeasuredObservations: []string{"membership"}, ClosureDigest: strings.Repeat("d", 64)}
	result := &rflscvs02.Result{SchemaID: rflscvs02.AnalyzerResultSchemaID, SchemaVersion: 2, RequestID: "request-mcp-factory", Operation: "detect", AdapterVersion: "adapter-mcp-factory/1", AnalyzerRevision: "analyzer-mcp-factory/1", WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedBasisID: snapshot.ComputedBasisID, SnapshotID: snapshot.SnapshotID, SnapshotTreeDigest: snapshot.RootTreeID, DependencyFingerprint: snapshot.DependencyFingerprint, ReadSet: readSet, Closure: closure, Capability: rflscvs02.CapabilityProfile{Adapter: "mcp-factory", AdapterVersion: "adapter-mcp-factory/1", AnalyzerRevision: "analyzer-mcp-factory/1", Features: []string{"snapshot_bytes"}}, Coverage: rflscvs02.Coverage{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}, Measured: true}, Diagnostics: []rflscvs02.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`)}
	anchor := slicing.Anchor{RepoRelativePath: doc.Path, ByteRange: [2]int{0, len([]byte(content))}, FileHash: doc.ContentID, SpanHash: doc.ContentID, EnclosingSymbolPath: "main", CanonicalAstFingerprint: "ast-mcp-factory"}
	evidenceID := semantic.EvidenceIDForAnchor("flow-mcp-factory", anchor)
	mapIR := &semantic.SemanticMapIR{SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: 2, MapID: "map-" + generation, GenerationID: generation, ComputedBasisID: snapshot.ComputedBasisID, ValidatedAgainstSnapshotID: snapshot.SnapshotID, PublicationKind: "checkpoint", Freshness: "current", Settlement: "pending", EnrichmentStatus: "not_requested", Authority: "candidate", Quality: semantic.MapQuality{Stage: "Q3", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0}, Task: semantic.MapTaskContext{TaskID: "task-mcp-factory", IntentRevision: 1, IntentStatus: "parsed", Mode: "feature"}, Basis: semantic.MapBasisContext{RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, SnapshotTreeID: snapshot.RootTreeID, DependencyFingerprint: snapshot.DependencyFingerprint, ConfigurationFingerprint: snapshot.ConfigurationFingerprint, AnalysisReadSetID: readSetID, CausalObservationClosureID: closureID}, Summary: semantic.MapSummary{Requested: "show main", Current: "candidate"}, Steps: []semantic.SemanticStep{{StepID: stepID, StructuralIdentity: "flow-mcp-factory|main.go|main|action", Ordinal: 1, Name: "main", TechnicalName: "main", Kind: "action", Anchor: anchor, EvidenceRefs: []string{evidenceID}}}, Edges: []semantic.SemanticEdge{}, Unknowns: []fusion.Unknown{}, Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}}, Evidence: []semantic.SemanticEvidence{{EvidenceID: evidenceID, Kind: "source", SourceAuthority: "code", ComputedBasisID: snapshot.ComputedBasisID, DocumentRevisionID: doc.RevisionID, Anchor: anchor, Producer: &semantic.ProducerInfo{Name: "mcp-factory", Version: "1"}, ValidationStatus: "verified", RedactionStatus: "passed", SnapshotID: snapshot.SnapshotID, ByteRange: anchor.ByteRange, LineRange: [2]int{1, 1}}}}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		t.Fatalf("MCP fixture map: %v", err)
	}
	return mapIR, readSet, closure, result
}

func publishMCPEnrichmentFixture(t *testing.T, coord *flowview.Server, st *storage.Storage, mapIR *semantic.SemanticMapIR, readSet rflscvs02.AnalysisReadSet, closure rflscvs02.ObservationClosure, result *rflscvs02.Result, snapshot protocol.Snapshot) error {
	t.Helper()
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		return err
	}
	projection := semantic.BuildFlowViewProjection(mapIR)
	projectionBytes, err := json.Marshal(projection)
	if err != nil {
		return err
	}
	readSetBytes, err := json.Marshal(readSet)
	if err != nil {
		return err
	}
	closureBytes, err := json.Marshal(closure)
	if err != nil {
		return err
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return err
	}
	for schemaID, data := range map[string][]byte{semantic.SemanticMapSchemaID: mapBytes, contractharness.FlowProjectionV2SchemaID: projectionBytes, rflscvs02.ReadSetSchemaID: readSetBytes, rflscvs02.ClosureSchemaID: closureBytes, rflscvs02.AnalyzerResultSchemaID: resultBytes} {
		if err := contractharness.Validate(schemaID, data); err != nil {
			return fmt.Errorf("fixture %s: %w", schemaID, err)
		}
	}
	refs := storage.ArtifactRefs{SemanticMap: storage.ArtifactCASRef(mapBytes), Projection: storage.ArtifactCASRef(projectionBytes), AnalysisReadSet: storage.ArtifactCASRef(readSetBytes), ObservationClosure: storage.ArtifactCASRef(closureBytes), AnalyzerResult: storage.ArtifactCASRef(resultBytes)}
	capabilityDigest, err := semantic.CanonicalCapabilityProfileDigest(result.Capability)
	if err != nil {
		return err
	}
	queryHashBytes := sha256.Sum256([]byte("mcp-factory-query"))
	queryHash := hex.EncodeToString(queryHashBytes[:])
	manifest := &storage.GenerationProofManifest{SchemaID: semantic.GenerationProofSchemaID, SchemaVersion: 2, ProofID: "proof-" + mapIR.GenerationID, GenerationID: mapIR.GenerationID, ComputedBasisID: mapIR.ComputedBasisID, ComputedSnapshotID: snapshot.SnapshotID, ValidatedAgainstSnapshotID: snapshot.SnapshotID, TaskIntentRevision: mapIR.Task.IntentRevision, NormalizedQueryHash: queryHash, AnalysisReadSetID: closure.AnalysisReadSetID, CausalObservationClosureID: closure.ClosureID, CausalObservationClosureDigest: closure.ClosureDigest, CapabilityProfileDigest: capabilityDigest, WorkspaceEpoch: snapshot.WorkspaceEpoch, CurrentPublication: storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"}, SettlementEvaluation: storage.SettlementEvaluation{Gate: mapIR.Settlement, BlockingObligationRefs: []string{}}, ArtifactRefs: refs, ExpectedLiveHeadSnapshotID: snapshot.SnapshotID, PublishedAt: time.Now().UTC()}
	pointer := &storage.ActivePointer{SchemaID: semantic.ActivePointerSchemaID, SchemaVersion: 2, GenerationID: mapIR.GenerationID, ComputedBasisID: mapIR.ComputedBasisID, ValidatedAgainstSnapshotID: snapshot.SnapshotID, ExpectedLiveHeadSnapshotID: snapshot.SnapshotID, WorkspaceEpoch: snapshot.WorkspaceEpoch, TaskIntentRevision: mapIR.Task.IntentRevision, NormalizedQueryHash: manifest.NormalizedQueryHash, FlowCount: 1, RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, TaskID: mapIR.Task.TaskID, PublishedAt: manifest.PublishedAt}
	basisID, snapshotID, generationID := mapIR.ComputedBasisID, snapshot.SnapshotID, mapIR.GenerationID
	eventBytes, err := json.Marshal(semantic.EventEnvelope{SchemaID: semantic.EventEnvelopeSchemaID, SchemaVersion: 2, StreamID: "flowview-live-stream", Sequence: 1, EventID: "mcp-factory-event-1", EventType: "generation.published", OccurredAt: time.Now().UTC(), ComputedBasisID: &basisID, ValidatedAgainstSnapshotID: &snapshotID, GenerationID: &generationID, Data: map[string]any{}})
	if err != nil {
		return err
	}
	artifacts := map[string][]byte{refs.SemanticMap: mapBytes, refs.Projection: projectionBytes, refs.AnalysisReadSet: readSetBytes, refs.ObservationClosure: closureBytes, refs.AnalyzerResult: resultBytes}
	_, err = st.PublishGeneration(storage.PublicationTransaction{Manifest: manifest, Pointer: pointer, Event: eventBytes, Artifacts: artifacts, ExpectedLiveHeadSnapshotID: snapshot.SnapshotID, ActualLiveHeadSnapshotID: snapshot.SnapshotID, LiveHeadCommit: func(expected string, commit func() error) error {
		return coord.SnapshotEngine().WithLiveHead(expected, commit)
	}})
	return err
}

func TestMCPModelHostHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") != "mcp" {
		return
	}
	reader := bufio.NewReaderSize(os.Stdin, 64<<10)
	for {
		body, err := readMCPModelHostFrame(reader)
		if err != nil {
			return
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &request) != nil || request.JSONRPC != "2.0" {
			return
		}
		switch request.Method {
		case "initialize":
			probe := &protocol.ModelHostIsolationProbe{RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", DisposableWriteAttempt: "blocked", SentinelBeforeDigest: "sha256:" + strings.Repeat("a", 64), SentinelAfterDigest: "sha256:" + strings.Repeat("a", 64), SentinelUnchanged: true, NetworkAttempt: "blocked"}
			capability := protocol.ModelHostCapability{Status: "measured", ModelID: "mcp-test-model", Revision: "r1", License: "MIT", Checksum: "sha256:mcp", Runtime: "test", DataBoundary: "local-only", Measured: true, SchemaConstrained: true, Cancellation: true, MaxRequestBytes: protocol.DefaultMaxMessageSizeBytes, MaxResponseBytes: protocol.DefaultMaxMessageSizeBytes, IsolationBackend: "sandbox-exec", IsolationEnforced: true, IsolationProbe: probe, NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("c", 64)}
			if writeMCPModelHostResponse(request.ID, protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: request.ID, Status: "ok", Capability: capability}) != nil {
				return
			}
		case protocol.ModelHostEnrichMethod:
			var params protocol.ModelHostRequest
			if json.Unmarshal(request.Params, &params) != nil {
				return
			}
			var pack semantic.EvidencePack
			if json.Unmarshal(params.EvidencePack, &pack) != nil || len(pack.TargetStepIDs) == 0 || len(pack.Items) == 0 {
				return
			}
			if writeMCPModelHostReceipt(params.RequestID, params.PackDigest) != nil {
				return
			}
			digestParts := strings.Split(os.Getenv("CODEFLOW_VS08_MODEL_HOST_RELEASE"), "|")
			if len(digestParts) != 4 {
				return
			}
			proposal, err := json.Marshal(semantic.ModelProposal{SchemaID: semantic.SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-mcp-" + params.PackDigest[:12], ComputedBasisID: pack.ComputedBasisID, GenerationID: pack.GenerationID, SnapshotID: pack.SnapshotID, TargetStepID: pack.TargetStepIDs[0], TargetSymbolPath: pack.TargetSymbolPath, ProposedTitle: "MCP enrichment", ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only", ModelID: "mcp-test-model", ModelRevision: "r1", PromptRevision: params.PromptRevision, SchemaProfile: semantic.SemanticProposalSchemaProfile, PackDigest: pack.PackDigest, EvidenceRefs: []string{pack.Items[0].EvidenceID}, FactDigest: digestParts[0], ObligationDigest: digestParts[1], AlignmentDigest: digestParts[2], SettlementDigest: digestParts[3]})
			if err != nil || writeMCPModelHostResponse(request.ID, protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: params.RequestID, Status: "accepted", Proposal: proposal}) != nil {
				return
			}
		default:
			return
		}
	}
}

func readMCPModelHostFrame(reader *bufio.Reader) ([]byte, error) {
	contentLength := int64(-1)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		if contentLength >= 0 {
			return nil, fmt.Errorf("duplicate content length")
		}
		contentLength, err = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || contentLength < 0 || contentLength > protocol.DefaultMaxMessageSizeBytes {
			return nil, fmt.Errorf("invalid content length")
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func writeMCPModelHostFrame(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func writeMCPModelHostResponse(id string, response protocol.ModelHostResponse) error {
	return writeMCPModelHostFrame(map[string]any{"jsonrpc": "2.0", "id": id, "result": response})
}

func writeMCPModelHostReceipt(requestID, packDigest string) error {
	return writeMCPModelHostFrame(map[string]any{"jsonrpc": "2.0", "method": "request_received", "params": map[string]any{"requestId": requestID, "packDigest": packDigest}})
}
