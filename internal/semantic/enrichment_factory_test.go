package semantic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"codeflow/internal/protocol"
)

func TestRunSemanticEnrichmentFactoryValidatesPackBeforeSpawn(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, _, request := enrichmentTestInput(t)
	request.ScopePaths = []string{"../outside"}
	var calls int
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request,
		ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
			calls++
			return nil, errors.New("factory must not run for an invalid pack")
		},
	})
	if calls != 0 {
		t.Fatalf("factory calls = %d, want 0 for invalid evidence pack", calls)
	}
	if result.State.Status != "unavailable" || result.Fallback == nil {
		t.Fatalf("invalid evidence pack result = %+v, want unavailable fallback", result)
	}
}

func TestRunSemanticEnrichmentFactorySpawnErrorReturnsFallback(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, _, request := enrichmentTestInput(t)
	spawnErr := errors.New("injected model host spawn failure")
	var calls int
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request,
		ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
			calls++
			return nil, spawnErr
		},
	})
	if calls != 1 {
		t.Fatalf("factory calls = %d, want 1", calls)
	}
	if result.State.Status != "unavailable" || result.Fallback == nil {
		t.Fatalf("spawn error result = %+v, want unavailable fallback", result)
	}
	if !strings.Contains(result.State.Reason, spawnErr.Error()) {
		t.Fatalf("spawn error reason = %q, want %q", result.State.Reason, spawnErr)
	}
	if result.coreHost != nil || result.coreTrust != nil {
		t.Fatal("spawn error synthesized model-host authority")
	}
}

func TestRunSemanticEnrichmentFactoryClosesHostReturnedWithSpawnError(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, mapIR, request := enrichmentTestInput(t)
	freshFactory, calls, hosts, disposableRoot := newActualSemanticModelHostFactory(t, mapIR, request)
	spawnErr := errors.New("injected spawn error after host creation")
	var returned *protocol.ModelHost
	factory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		returned, _ = freshFactory(ctx)
		return returned, spawnErr
	})
	defer func() {
		if returned != nil {
			_ = returned.Close()
		}
	}()

	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request, ModelHostFactory: factory, PromptRevision: "prompt-factory",
	})
	if *calls != 1 || len(*hosts) != 1 || returned == nil {
		t.Fatalf("factory calls/hosts = %d/%d, returned host = %p, want one actual host", *calls, len(*hosts), returned)
	}
	if result.State.Status != "unavailable" || !strings.Contains(result.State.Reason, spawnErr.Error()) {
		t.Fatalf("host plus spawn error result = %+v, want unavailable with spawn error", result)
	}
	if evidence := returned.IsolationEvidence(); !evidence.CleanupVerified {
		t.Fatalf("host plus spawn error cleanup evidence = %+v, want verified", evidence)
	}
	entries, err := os.ReadDir(disposableRoot)
	if err != nil {
		t.Fatalf("read disposable root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("disposable root entries = %v, want no leaked host directories", entries)
	}
}

func TestRunSemanticEnrichmentFactoryFailedClaimLeavesPreclaimedHostLive(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, mapIR, request := enrichmentTestInput(t)
	freshFactory, spawnCalls, hosts, disposableRoot := newActualSemanticModelHostFactory(t, mapIR, request)
	ownedHost, err := freshFactory(context.Background())
	if err != nil {
		t.Fatalf("spawn actual semantic model host: %v", err)
	}
	if ownedHost == nil || len(*hosts) != 1 || *spawnCalls != 1 {
		t.Fatalf("preclaimed host setup = host %p, hosts %d, spawn calls %d, want one actual host", ownedHost, len(*hosts), *spawnCalls)
	}
	if !ownedHost.ClaimOperation() {
		t.Fatal("original owner could not claim actual model host")
	}
	defer func() { _ = ownedHost.Close() }()

	spawnErr := errors.New("injected factory error for preclaimed host")
	var factoryCalls int
	factory := protocol.ModelHostFactory(func(context.Context) (*protocol.ModelHost, error) {
		factoryCalls++
		return ownedHost, spawnErr
	})
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request, ModelHostFactory: factory, PromptRevision: "prompt-factory",
	})
	if factoryCalls != 1 {
		t.Fatalf("losing factory calls = %d, want 1", factoryCalls)
	}
	if result.State.Status != "unavailable" || !strings.Contains(result.State.Reason, spawnErr.Error()) {
		t.Fatalf("preclaimed host plus factory error result = %+v, want unavailable with injected error", result)
	}
	if evidence := ownedHost.IsolationEvidence(); evidence.CleanupVerified || evidence.TerminalStatus != "" {
		t.Fatalf("losing caller changed preclaimed host lifecycle = %+v, want live and uncleaned", evidence)
	}
	entries, err := os.ReadDir(disposableRoot)
	if err != nil {
		t.Fatalf("read live disposable root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("live disposable root entries = %v, want one host directory", entries)
	}

	if err := ownedHost.Close(); err != nil {
		t.Fatalf("original owner close: %v", err)
	}
	evidence := ownedHost.IsolationEvidence()
	if !evidence.CleanupVerified {
		t.Fatalf("original owner cleanup evidence = %+v, want verified", evidence)
	}
	entries, err = os.ReadDir(disposableRoot)
	if err != nil {
		t.Fatalf("read cleaned disposable root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("cleaned disposable root entries = %v, want no host directories", entries)
	}
}

func TestRunSemanticEnrichmentFactoryRejectsAmbiguousDirectHost(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, _, request := enrichmentTestInput(t)
	direct := &fakeModelHost{}
	var calls int
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request,
		modelHost:    direct,
		ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
			calls++
			return nil, errors.New("ambiguous factory must not run")
		},
	})
	if calls != 0 {
		t.Fatalf("factory calls = %d, want 0 for ambiguous host sources", calls)
	}
	if result.State.Status != "unavailable" || result.Fallback == nil {
		t.Fatalf("ambiguous host result = %+v, want unavailable fallback", result)
	}
	if !strings.Contains(result.State.Reason, "both") {
		t.Fatalf("ambiguous host reason = %q, want explicit ambiguity", result.State.Reason)
	}
	if !direct.closed {
		t.Fatal("ambiguous direct host was not closed")
	}
}

func TestRunSemanticEnrichmentFactorySequentialFreshHostsCleanup(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, mapIR, request := enrichmentTestInput(t)
	factory, calls, hosts, _ := newActualSemanticModelHostFactory(t, mapIR, request)
	for index := 0; index < 2; index++ {
		result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
			EvidencePack: request, ModelHostFactory: factory, PromptRevision: "prompt-factory",
		})
		if result.State.Status != "available" || result.Proposal == nil {
			t.Fatalf("sequential result %d = %+v, want available proposal", index, result)
		}
		if !result.State.Isolation.CleanupVerified {
			t.Fatalf("sequential result %d cleanup evidence = %+v, want verified", index, result.State.Isolation)
		}
	}
	if got := *calls; got != 2 {
		t.Fatalf("factory calls = %d, want 2", got)
	}
	if len(*hosts) != 2 || (*hosts)[0] == (*hosts)[1] {
		t.Fatalf("factory hosts = %p, %p, want two fresh actual hosts", (*hosts)[0], (*hosts)[1])
	}
}

func TestRunSemanticEnrichmentFactoryConcurrentFreshHostsCleanup(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, mapIR, request := enrichmentTestInput(t)
	factory, calls, hosts, _ := newActualSemanticModelHostFactory(t, mapIR, request)
	results := make([]EnrichmentResult, 2)
	var wait sync.WaitGroup
	wait.Add(len(results))
	for index := range results {
		go func(index int) {
			defer wait.Done()
			results[index] = RunSemanticEnrichment(context.Background(), EnrichmentRequest{
				EvidencePack: request, ModelHostFactory: factory, PromptRevision: "prompt-factory",
			})
		}(index)
	}
	wait.Wait()
	for index, result := range results {
		if result.State.Status != "available" || result.Proposal == nil {
			t.Fatalf("concurrent result %d = %+v, want available proposal", index, result)
		}
		if !result.State.Isolation.CleanupVerified {
			t.Fatalf("concurrent result %d cleanup evidence = %+v, want verified", index, result.State.Isolation)
		}
	}
	if got := *calls; got != len(results) {
		t.Fatalf("factory calls = %d, want %d", got, len(results))
	}
	if len(*hosts) != len(results) || (*hosts)[0] == (*hosts)[1] {
		t.Fatalf("factory hosts = %p, %p, want distinct actual hosts", (*hosts)[0], (*hosts)[1])
	}
}

func TestRunSemanticEnrichmentFactoryRejectsSequentialReusedHost(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, mapIR, request := enrichmentTestInput(t)
	freshFactory, _, hosts, _ := newActualSemanticModelHostFactory(t, mapIR, request)
	var mu sync.Mutex
	var shared *protocol.ModelHost
	reusedFactory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		mu.Lock()
		defer mu.Unlock()
		if shared != nil {
			return shared, nil
		}
		host, err := freshFactory(ctx)
		if err == nil {
			shared = host
		}
		return host, err
	})
	first := RunSemanticEnrichment(context.Background(), EnrichmentRequest{EvidencePack: request, ModelHostFactory: reusedFactory, PromptRevision: "prompt-factory"})
	second := RunSemanticEnrichment(context.Background(), EnrichmentRequest{EvidencePack: request, ModelHostFactory: reusedFactory, PromptRevision: "prompt-factory"})
	if first.State.Status != "available" || first.Proposal == nil || !first.State.Isolation.CleanupVerified {
		t.Fatalf("first reused-host result = %+v, want available and cleaned", first)
	}
	if second.State.Status != "unavailable" || second.Proposal != nil || !strings.Contains(second.State.Reason, "already claimed") {
		t.Fatalf("second reused-host result = %+v, want claim failure", second)
	}
	if len(*hosts) != 1 || shared != (*hosts)[0] || !shared.IsolationEvidence().CleanupVerified {
		t.Fatalf("reused host lifecycle = hosts=%d shared=%p evidence=%+v", len(*hosts), shared, shared.IsolationEvidence())
	}
}

func TestRunSemanticEnrichmentFactoryRejectsConcurrentReusedHost(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	_, mapIR, request := enrichmentTestInput(t)
	freshFactory, _, hosts, _ := newActualSemanticModelHostFactory(t, mapIR, request)
	var mu sync.Mutex
	var shared *protocol.ModelHost
	reusedFactory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		mu.Lock()
		defer mu.Unlock()
		if shared != nil {
			return shared, nil
		}
		host, err := freshFactory(ctx)
		if err == nil {
			shared = host
		}
		return host, err
	})
	type outcome struct{ result EnrichmentResult }
	outcomes := make(chan outcome, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			outcomes <- outcome{result: RunSemanticEnrichment(context.Background(), EnrichmentRequest{EvidencePack: request, ModelHostFactory: reusedFactory, PromptRevision: "prompt-factory"})}
		}()
	}
	wait.Wait()
	close(outcomes)
	available := 0
	unavailableClaim := 0
	for item := range outcomes {
		if item.result.State.Status == "available" && item.result.Proposal != nil {
			available++
		} else if item.result.State.Status == "unavailable" && strings.Contains(item.result.State.Reason, "already claimed") {
			unavailableClaim++
		}
	}
	if available != 1 || unavailableClaim != 1 {
		t.Fatalf("concurrent reused-host outcomes = available %d, claim failures %d", available, unavailableClaim)
	}
	if len(*hosts) != 1 || shared == nil || !shared.IsolationEvidence().CleanupVerified {
		t.Fatalf("concurrent reused host lifecycle = hosts=%d shared=%p evidence=%+v", len(*hosts), shared, shared.IsolationEvidence())
	}
}

func newActualSemanticModelHostFactory(t *testing.T, mapIR *SemanticMapIR, request EvidencePackRequest) (protocol.ModelHostFactory, *int, *[]*protocol.ModelHost, string) {
	t.Helper()
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := json.Marshal(ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-factory",
		ComputedBasisID: request.Snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: request.Snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout", ProposedCategory: "entry",
		EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only", ModelID: "actual-test-model", ModelRevision: "r1",
		PromptRevision: "prompt-factory", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	encoded := base64.RawStdEncoding.EncodeToString(proposal)
	config := protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestSemanticModelHostHelper"},
		Env:            []string{"CODEFLOW_MODEL_HOST_HELPER=semantic", "CODEFLOW_VS08_MODEL_HOST_RELEASE=" + encoded},
		DisposableRoot: root, DefaultTimeout: 3 * time.Second,
	}
	var mu sync.Mutex
	calls := 0
	hosts := make([]*protocol.ModelHost, 0, 2)
	factory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		host, err := protocol.SpawnModelHost(ctx, config)
		if err == nil {
			mu.Lock()
			hosts = append(hosts, host)
			mu.Unlock()
		}
		return host, err
	})
	return factory, &calls, &hosts, root
}
