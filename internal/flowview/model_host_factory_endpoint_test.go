package flowview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func TestFlowViewSemanticApprovalUsesDurableExecutionService(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var digestMu sync.RWMutex
	var q3 semantic.Q3CanonicalDigests
	var compileCalls atomic.Int32
	var factoryCalls atomic.Int32
	var hostsMu sync.Mutex
	var hosts []*protocol.ModelHost
	disposableRoot := t.TempDir()
	factory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		digestMu.RLock()
		current := q3
		digestMu.RUnlock()
		factoryCalls.Add(1)
		host, err := protocol.SpawnModelHost(ctx, protocol.ModelHostConfig{
			BinPath: os.Args[0], Args: []string{"-test.run=TestFlowViewModelHostHelper"}, DisposableRoot: disposableRoot,
			Env: []string{
				"CODEFLOW_MODEL_HOST_HELPER=flowview",
				"CODEFLOW_VS08_MODEL_HOST_RELEASE=" + strings.Join([]string{current.Fact, current.Obligation, current.Alignment, current.Settlement}, "|"),
			},
			DefaultTimeout: 3 * time.Second,
		})
		if err == nil {
			hostsMu.Lock()
			hosts = append(hosts, host)
			hostsMu.Unlock()
		}
		return host, err
	})
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "flowview-factory-token", ModelHostFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	query := &semantic.TaskViewQuery{SchemaID: semantic.FeatureQuerySchemaID, SchemaVersion: 2, Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: "show main"}}
	if err := srv.RememberTaskQuery(query, query.Feature.Request); err != nil {
		t.Fatal(err)
	}
	srv.compileCandidate = func(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
		compileCalls.Add(1)
		mapIR, projection, payload, target, intent, closure, err := liveDeltaCandidate(ctx, snapshot, query, "generation-flowview-factory")
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		// Current enrichment requires the exact map artifact to carry current
		// freshness. The publication proof still binds every identity below.
		mapIR.Freshness = "current"
		mapIR.Quality.Stage = "Q3"
		digests, err := semantic.CanonicalQ3Digests(mapIR)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		digestMu.Lock()
		q3 = digests
		digestMu.Unlock()
		return mapIR, projection, payload, target, intent, closure, nil
	}
	_, snapshot, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.processCheckpoint(context.Background(), snapshot); err != nil {
		t.Fatalf("publish fixture: %v", err)
	}
	// Use the strict reread path that backs the public enrichment endpoint,
	// rather than trusting the in-memory candidate as the approval fixture.
	bundle, err := srv.storage.ReadValidatedActiveProofBundle()
	if err != nil || bundle == nil || len(bundle.SemanticMap) == 0 || bundle.Manifest == nil || bundle.Pointer == nil {
		t.Fatalf("strict current Q3 proof reread failed: bundle=%+v err=%v", bundle, err)
	}
	var proofMap semantic.SemanticMapIR
	if err := json.Unmarshal(bundle.SemanticMap, &proofMap); err != nil {
		t.Fatalf("decode strict current Q3 map: %v", err)
	}
	if proofMap.GenerationID != "generation-flowview-factory" || proofMap.Freshness != "current" || proofMap.Quality.Stage != "Q3" || proofMap.MapID == "" || proofMap.ComputedBasisID == "" || proofMap.ValidatedAgainstSnapshotID == "" {
		t.Fatalf("strict proof map is not current Q3: %+v", proofMap)
	}
	if srv.cachedSemanticMap("generation-flowview-factory", "") == nil {
		srv.mu.Lock()
		cached := make([]string, 0, len(srv.mapCache))
		for id := range srv.mapCache {
			cached = append(cached, id)
		}
		srv.mu.Unlock()
		srv.hub.mu.Lock()
		events := make([]string, 0, len(srv.hub.ringBuffer))
		for _, event := range srv.hub.ringBuffer {
			data, _ := json.Marshal(event.Data)
			events = append(events, event.EventType+":"+string(data))
		}
		srv.hub.mu.Unlock()
		t.Fatalf("published fixture did not populate deterministic map cache: pipeline=%v compile=%d cache=%v events=%v", srv.LastPipelineError(), compileCalls.Load(), cached, events)
	}
	// Publication evaluates settlement and therefore finalizes the bytes whose
	// Q3 digests the supervised host must echo. Refresh the factory's expected
	// digests from that published, authoritative map.
	publishedMap := srv.cachedSemanticMap("generation-flowview-factory", "")
	publishedDigests, err := semantic.CanonicalQ3Digests(publishedMap)
	if err != nil {
		t.Fatalf("published Q3 digests: %v", err)
	}
	digestMu.Lock()
	q3 = publishedDigests
	digestMu.Unlock()

	call := func() (semantic.EnrichmentResult, error) {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/enrich?token="+srv.AuthToken(), bytes.NewBufferString(`{"generationId":"generation-flowview-factory","targetStepId":"step-live-delta","promptRevision":"prompt-flowview-factory"}`))
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return semantic.EnrichmentResult{}, fmt.Errorf("enrichment status = %d body=%s", rec.Code, rec.Body.String())
		}
		var result semantic.EnrichmentResult
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			return semantic.EnrichmentResult{}, fmt.Errorf("decode enrichment result: %v body=%s", err, rec.Body.String())
		}
		if result.State.Status != "available" || result.Proposal == nil {
			return result, fmt.Errorf("enrichment result = %+v", result.State)
		}
		if !result.State.Isolation.CleanupVerified {
			return result, fmt.Errorf("request host cleanup was not verified: %+v", result.State.Isolation)
		}
		return result, nil
	}

	firstResult, err := call()
	if err != nil {
		t.Fatal(err)
	}
	store := semantic.NewDurableProposalStore(root)
	loaded, err := semantic.LoadProposalForApproval(context.Background(), store, srv.approvalWorkspaceID, firstResult.Proposal.ProposalID, firstResult.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("restart proposal lookup: %v", err)
	}
	if loaded == nil || loaded.WorkspaceID != srv.approvalWorkspaceID || !reflect.DeepEqual(loaded.Proposal, firstResult.Proposal) || !reflect.DeepEqual(loaded.Pack, firstResult.Pack) {
		t.Fatalf("restart proposal pair = %#v, want exact available result", loaded)
	}
	// Exercise rejected commands against a clean approval store before the
	// first commit. This keeps the no-row assertion independent of the later
	// successful event and proves the reopened durable pair is the only input.
	cleanServer, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "flowview-clean-approval-token"})
	if err != nil {
		t.Fatalf("new clean approval server: %v", err)
	}
	cleanServer.proposalStore = store
	postCleanApproval := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+cleanServer.AuthToken(), bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		cleanServer.httpServer.Handler.ServeHTTP(rec, req)
		return rec
	}
	cleanTransactions := semantic.NewApprovalTransactionStore(root, cleanServer.engine)
	assertCleanApprovalRows := func(name string) {
		snapshot, err := cleanTransactions.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("%s clean approval snapshot: %v", name, err)
		}
		if len(snapshot.Events) != 0 || len(snapshot.Aggregates) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 {
			t.Fatalf("%s published approval rows: events=%d aggregates=%d idempotency=%d outbox=%d", name, len(snapshot.Events), len(snapshot.Aggregates), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
		}
	}
	validApprovalBody := flowViewApprovalBody(firstResult, "flowview-strict-draft", "approve", false, "")
	malformedApprovalBodies := map[string]string{
		"duplicate decision": strings.Replace(validApprovalBody, `"decision":"approve"`, `"decision":"approve","decision":"approve"`, 1),
		"unknown property":   strings.TrimSuffix(validApprovalBody, "}") + `,"unexpected":true}`,
		"trailing document":  validApprovalBody + `{}`,
	}
	for name, body := range malformedApprovalBodies {
		response := postCleanApproval(body)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"missing_precondition"`) {
			t.Fatalf("%s response=%d body=%s, want strict draft rejection", name, response.Code, response.Body.String())
		}
		assertCleanApprovalRows(name)
	}
	legacyResponse := postCleanApproval(flowViewApprovalBodyValues("command-clean-legacy", firstResult.Proposal.ProposalID, firstResult.Pack.EvidencePackID, firstResult.Proposal.ComputedBasisID, firstResult.Proposal.GenerationID, 1, "approved", "key-clean-legacy", 0, "none", nil))
	if legacyResponse.Code != http.StatusBadRequest || !strings.Contains(legacyResponse.Body.String(), `"code":"approval_invalid"`) {
		_ = cleanServer.Shutdown(context.Background())
		t.Fatalf("legacy decision on clean server status=%d body=%s, want approval_invalid", legacyResponse.Code, legacyResponse.Body.String())
	}
	staleResponse := postCleanApproval(flowViewApprovalBodyValues("command-clean-stale", firstResult.Proposal.ProposalID, firstResult.Pack.EvidencePackID, firstResult.Proposal.ComputedBasisID, firstResult.Proposal.GenerationID+"-stale", 1, "approve", "key-clean-stale", 0, "none", nil))
	if staleResponse.Code != http.StatusConflict || !strings.Contains(staleResponse.Body.String(), `"code":"approval_conflict"`) {
		_ = cleanServer.Shutdown(context.Background())
		t.Fatalf("stale generation on clean server status=%d body=%s, want approval_conflict", staleResponse.Code, staleResponse.Body.String())
	}
	cleanSnapshot, err := cleanTransactions.Snapshot(context.Background())
	if err != nil {
		_ = cleanServer.Shutdown(context.Background())
		t.Fatalf("clean approval snapshot: %v", err)
	}
	if len(cleanSnapshot.Events) != 0 || len(cleanSnapshot.Aggregates) != 0 || len(cleanSnapshot.IdempotencyResults) != 0 || len(cleanSnapshot.Outbox) != 0 {
		_ = cleanServer.Shutdown(context.Background())
		t.Fatalf("clean rejected approval published rows: events=%d aggregates=%d idempotency=%d outbox=%d", len(cleanSnapshot.Events), len(cleanSnapshot.Aggregates), len(cleanSnapshot.IdempotencyResults), len(cleanSnapshot.Outbox))
	}
	if err := cleanServer.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown clean approval server: %v", err)
	}

	// Approval lookup must use the exact pair from the reopened durable store.
	srv.proposalStore = store
	approvalBody := flowViewApprovalBody(firstResult, "flowview-restart", "approve", false, "")
	approvalRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+srv.AuthToken(), bytes.NewBufferString(approvalBody))
	approvalResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(approvalResponse, approvalRequest)
	if approvalResponse.Code != http.StatusOK {
		t.Fatalf("approval from reopened durable store status=%d body=%s", approvalResponse.Code, approvalResponse.Body.String())
	}
	var firstApproval semantic.ApprovalExecutionResult
	if err := json.Unmarshal(approvalResponse.Body.Bytes(), &firstApproval); err != nil || firstApproval.Receipt.Replayed || firstApproval.Receipt.Event.EventID == "" || firstApproval.Receipt.Aggregate.Version != 1 {
		t.Fatalf("first approval response=%s err=%v, want non-replayed version-one commit", approvalResponse.Body.String(), err)
	}
	access, err := srv.approvalGate.AuthenticateAndAuthorize(context.Background(), srv.approvalWorkspaceID, root)
	if err != nil {
		t.Fatalf("derive approval authority: %v", err)
	}
	if event := firstApproval.Receipt.Event; event.ActorID != access.Actor().ActorID || event.SessionID != access.Actor().SessionID || event.WorkspaceID != access.Workspace().WorkspaceID() {
		t.Fatalf("approval event authority = actor %q session %q workspace %q, want gate-derived actor/session/workspace", event.ActorID, event.SessionID, event.WorkspaceID)
	}
	var wireObject map[string]any
	if err := json.Unmarshal(approvalResponse.Body.Bytes(), &wireObject); err != nil {
		t.Fatalf("decode approval result: %v", err)
	}
	for _, key := range []string{"receipt", "generationId", "computedBasisId", "validatedSnapshotId", "intentRevision", "freshness"} {
		if _, ok := wireObject[key]; !ok {
			t.Fatalf("approval result missing lower-camel key %q: %s", key, approvalResponse.Body.Bytes())
		}
	}
	if _, ok := wireObject["Receipt"]; ok {
		t.Fatalf("approval result exposed upper-case field: %s", approvalResponse.Body.Bytes())
	}
	transactionStore := semantic.NewApprovalTransactionStore(root, srv.engine)
	beforeApprovalSnapshot, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval snapshot: %v", err)
	}
	if len(beforeApprovalSnapshot.Events) != 1 || len(beforeApprovalSnapshot.Aggregates) != 1 || len(beforeApprovalSnapshot.IdempotencyResults) != 1 || len(beforeApprovalSnapshot.Outbox) != 1 {
		t.Fatalf("approval snapshot counts = events %d aggregates %d idempotency %d outbox %d, want 1 each", len(beforeApprovalSnapshot.Events), len(beforeApprovalSnapshot.Aggregates), len(beforeApprovalSnapshot.IdempotencyResults), len(beforeApprovalSnapshot.Outbox))
	}
	approvalRequest = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+srv.AuthToken(), bytes.NewBufferString(approvalBody))
	approvalResponse = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(approvalResponse, approvalRequest)
	if approvalResponse.Code != http.StatusOK {
		t.Fatalf("exact approval retry status=%d body=%s", approvalResponse.Code, approvalResponse.Body.String())
	}
	var retryApproval semantic.ApprovalExecutionResult
	firstReplayComparable := firstApproval
	firstReplayComparable.Receipt.Replayed = true
	if err := json.Unmarshal(approvalResponse.Body.Bytes(), &retryApproval); err != nil || !retryApproval.Receipt.Replayed || !reflect.DeepEqual(firstReplayComparable, retryApproval) || !reflect.DeepEqual(firstApproval.Receipt.EventPayload, retryApproval.Receipt.EventPayload) {
		t.Fatalf("exact approval retry response=%s err=%v, want same result with Replayed=true", approvalResponse.Body.String(), err)
	}
	afterApprovalSnapshot, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval retry snapshot: %v", err)
	}
	if len(afterApprovalSnapshot.Events) != 1 || len(afterApprovalSnapshot.Aggregates) != 1 || len(afterApprovalSnapshot.IdempotencyResults) != 1 || len(afterApprovalSnapshot.Outbox) != 1 {
		t.Fatalf("approval retry changed durable counts: events %d aggregates %d idempotency %d outbox %d", len(afterApprovalSnapshot.Events), len(afterApprovalSnapshot.Aggregates), len(afterApprovalSnapshot.IdempotencyResults), len(afterApprovalSnapshot.Outbox))
	}
	unchangedSnapshot, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after rejected commands: %v", err)
	}
	if len(unchangedSnapshot.Events) != 1 || len(unchangedSnapshot.Aggregates) != 1 || len(unchangedSnapshot.IdempotencyResults) != 1 || len(unchangedSnapshot.Outbox) != 1 {
		t.Fatalf("rejected commands changed durable counts: events %d aggregates %d idempotency %d outbox %d", len(unchangedSnapshot.Events), len(unchangedSnapshot.Aggregates), len(unchangedSnapshot.IdempotencyResults), len(unchangedSnapshot.Outbox))
	}
	approvalRequest = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+srv.AuthToken(), bytes.NewBufferString(flowViewApprovalBodyValues("command-mismatch", firstResult.Proposal.ProposalID+"-wrong", firstResult.Pack.EvidencePackID, firstResult.Proposal.ComputedBasisID, firstResult.Proposal.GenerationID, 1, "approve", "key-mismatch", 0, "none", nil)))
	approvalResponse = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(approvalResponse, approvalRequest)
	if approvalResponse.Code != http.StatusNotFound || !strings.Contains(approvalResponse.Body.String(), `"code":"approval_unavailable"`) {
		t.Fatalf("mismatched approval status=%d body=%s, want safe unavailable", approvalResponse.Code, approvalResponse.Body.String())
	}
	records, err := filepath.Glob(filepath.Join(root, ".codeflow", "semantic-proposals", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("durable proposal records = %v, err=%v, want one record", records, err)
	}
	originalRecord, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatalf("read durable proposal record: %v", err)
	}
	if err := os.WriteFile(records[0], []byte(`{"storeVersion":1,"workspaceId":"corrupt"}`), 0o600); err != nil {
		t.Fatalf("corrupt durable proposal record: %v", err)
	}
	approvalRequest = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+srv.AuthToken(), bytes.NewBufferString(flowViewApprovalBodyValues("command-corrupt", firstResult.Proposal.ProposalID, firstResult.Pack.EvidencePackID, firstResult.Proposal.ComputedBasisID, firstResult.Proposal.GenerationID, 1, "approve", "key-corrupt", 0, "none", nil)))
	approvalResponse = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(approvalResponse, approvalRequest)
	if approvalResponse.Code != http.StatusBadRequest || !strings.Contains(approvalResponse.Body.String(), `"code":"approval_invalid"`) {
		t.Fatalf("corrupt approval status=%d body=%s, want bounded invalid Evidence", approvalResponse.Code, approvalResponse.Body.String())
	}
	if err := os.WriteFile(records[0], originalRecord, 0o600); err != nil {
		t.Fatalf("restore durable proposal record: %v", err)
	}
	if _, err := call(); err != nil {
		t.Fatal(err)
	}
	if factoryCalls.Load() != 2 {
		t.Fatalf("sequential factory calls = %d, want 2", factoryCalls.Load())
	}
	hostsMu.Lock()
	if len(hosts) != 2 || hosts[0] == hosts[1] {
		hostsMu.Unlock()
		t.Fatal("sequential requests reused a host identity")
	}
	hostsMu.Unlock()
	type flowViewCall struct {
		result semantic.EnrichmentResult
		err    error
	}
	concurrent := make(chan flowViewCall, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := call()
			concurrent <- flowViewCall{result: result, err: err}
		}()
	}
	group.Wait()
	close(concurrent)
	if factoryCalls.Load() != 4 {
		t.Fatalf("concurrent factory calls = %d, want 4", factoryCalls.Load())
	}
	for outcome := range concurrent {
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.result.State.Status != "available" {
			t.Fatalf("concurrent enrichment result = %+v", outcome.result.State)
		}
	}
	hostsMu.Lock()
	defer hostsMu.Unlock()
	if len(hosts) != 4 {
		t.Fatalf("recorded hosts = %d, want 4", len(hosts))
	}
	for i := range hosts {
		for j := i + 1; j < len(hosts); j++ {
			if hosts[i] == hosts[j] {
				t.Fatalf("requests %d and %d reused a host identity", i, j)
			}
		}
	}
	for i, host := range hosts {
		if host == nil || !host.IsolationEvidence().CleanupVerified {
			t.Fatalf("host %d cleanup evidence = %+v", i, host.IsolationEvidence())
		}
	}
	if entries, err := os.ReadDir(disposableRoot); err != nil {
		t.Fatalf("read disposable root: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("request host workdirs remain: %v", entries)
	}
}

func TestFlowViewSemanticEnrichmentFactoryShutdownAdmission(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	var calls atomic.Int32
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), Port: 0, ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
		calls.Add(1)
		return nil, errors.New("test factory must not be called")
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("unused factory spawned %d hosts", calls.Load())
	}
	// No map is needed to exercise the post-shutdown request admission path.
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/enrich?token="+srv.AuthToken(), bytes.NewBufferString(`{"targetStepId":"step-1"}`))
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if calls.Load() != 0 {
		t.Fatalf("post-shutdown request spawned %d hosts", calls.Load())
	}
	_, factory, release, err := srv.beginModelHostRequest(context.Background())
	release()
	if !errors.Is(err, errFlowViewModelHostServerClosed) {
		t.Fatalf("post-shutdown admission error = %v, want %v", err, errFlowViewModelHostServerClosed)
	}
	if factory != nil {
		t.Fatal("post-shutdown admission returned a factory")
	}
}

func TestFlowViewSemanticEnrichmentShutdownCancelsAdmittedRequest(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
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
		t.Fatal("admitted FlowView factory did not start")
	}
	released := make(chan struct{})
	go func() {
		<-spawnDone
		release()
		close(released)
	}()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(context.Background()) }()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("FlowView shutdown did not cancel admitted factory")
	}
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("FlowView shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FlowView shutdown did not join admitted factory")
	}
	if calls.Load() != 1 {
		t.Fatalf("admitted FlowView factory calls = %d, want 1", calls.Load())
	}
}

func TestFlowViewModelHostFactoryAdmissionClosesBeforeWaitingCall(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
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
	// Hold the spawn-admission mutex to force the request factory to wait
	// before invoking the underlying factory.
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
			t.Fatal("FlowView shutdown did not close admission")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-started:
		srv.modelHostSpawnMu.Unlock()
		release()
		t.Fatal("factory started while spawn admission was held")
	default:
	}
	srv.modelHostSpawnMu.Unlock()
	if err := <-invokeDone; !errors.Is(err, errFlowViewModelHostServerClosed) {
		t.Fatalf("waiting FlowView factory error = %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("stop FlowView model-host requests: %v", err)
	}
	release()
	if calls.Load() != 0 {
		t.Fatalf("factory calls after shutdown admission closed = %d, want 0", calls.Load())
	}
}

func TestFlowViewShutdownPreservesFirstErrorAndRetriesDrain(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), Port: 0, ModelHostFactory: func(ctx context.Context) (*protocol.ModelHost, error) {
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
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		_, callErr := factory(requestCtx)
		finished <- callErr
	}()
	<-started
	firstCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	firstErr := srv.Shutdown(firstCtx)
	cancel()
	if firstErr == nil || !errors.Is(firstErr, context.DeadlineExceeded) {
		t.Fatalf("first Shutdown error = %v, want preserved deadline", firstErr)
	}
	release()
	if callErr := <-finished; callErr == nil {
		t.Fatal("blocked factory unexpectedly succeeded")
	}
	secondErr := srv.Shutdown(context.Background())
	if secondErr == nil || !errors.Is(secondErr, context.DeadlineExceeded) {
		t.Fatalf("retry Shutdown error = %v, want original deadline", secondErr)
	}
	if thirdErr := srv.Shutdown(context.Background()); thirdErr == nil || !errors.Is(thirdErr, context.DeadlineExceeded) {
		t.Fatalf("idempotent Shutdown error = %v, want original deadline", thirdErr)
	}
}

func TestFlowViewModelHostHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") != "flowview" {
		return
	}
	reader := bufio.NewReaderSize(os.Stdin, 64<<10)
	for {
		body, err := readFlowViewModelHostFrame(reader)
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
			capability := protocol.ModelHostCapability{Status: "measured", ModelID: "flowview-test-model", Revision: "r1", License: "MIT", Checksum: "sha256:flowview", Runtime: "test", DataBoundary: "local-only", Measured: true, SchemaConstrained: true, Cancellation: true, MaxRequestBytes: protocol.DefaultMaxMessageSizeBytes, MaxResponseBytes: protocol.DefaultMaxMessageSizeBytes, IsolationBackend: "sandbox-exec", IsolationEnforced: true, IsolationProbe: probe, NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("c", 64)}
			if writeFlowViewModelHostResponse(request.ID, protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: request.ID, Status: "ok", Capability: capability}) != nil {
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
			if writeFlowViewModelHostReceipt(params.RequestID, params.PackDigest) != nil {
				return
			}
			digestParts := strings.Split(os.Getenv("CODEFLOW_VS08_MODEL_HOST_RELEASE"), "|")
			if len(digestParts) != 4 {
				return
			}
			digests := semantic.Q3CanonicalDigests{Fact: digestParts[0], Obligation: digestParts[1], Alignment: digestParts[2], Settlement: digestParts[3]}
			proposal, err := json.Marshal(semantic.ModelProposal{SchemaID: semantic.SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-flowview-" + params.PackDigest[:12], ComputedBasisID: pack.ComputedBasisID, GenerationID: pack.GenerationID, SnapshotID: pack.SnapshotID, TargetStepID: pack.TargetStepIDs[0], TargetSymbolPath: pack.TargetSymbolPath, ProposedTitle: "FlowView enrichment", ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only", ModelID: "flowview-test-model", ModelRevision: "r1", PromptRevision: params.PromptRevision, SchemaProfile: semantic.SemanticProposalSchemaProfile, PackDigest: pack.PackDigest, EvidenceRefs: []string{pack.Items[0].EvidenceID}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation, AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement})
			if err != nil || writeFlowViewModelHostResponse(request.ID, protocol.ModelHostResponse{SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: 2, RequestID: params.RequestID, Status: "accepted", Proposal: proposal}) != nil {
				return
			}
		default:
			return
		}
	}
}

func readFlowViewModelHostFrame(reader *bufio.Reader) ([]byte, error) {
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

func writeFlowViewModelHostFrame(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func writeFlowViewModelHostResponse(id string, response protocol.ModelHostResponse) error {
	return writeFlowViewModelHostFrame(map[string]any{"jsonrpc": "2.0", "id": id, "result": response})
}

func writeFlowViewModelHostReceipt(requestID, packDigest string) error {
	return writeFlowViewModelHostFrame(map[string]any{"jsonrpc": "2.0", "method": "request_received", "params": map[string]any{"requestId": requestID, "packDigest": packDigest}})
}
