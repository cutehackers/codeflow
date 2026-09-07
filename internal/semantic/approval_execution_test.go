package semantic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/protocol"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

type approvalExecutionFixture struct {
	root          string
	engine        *workspace.SnapshotEngine
	access        ApprovalAccess
	before        *SemanticMapIR
	proposalStore ProposalStore
	proposalID    string
	packID        string
}

func newApprovalExecutionFixture(t *testing.T) approvalExecutionFixture {
	t.Helper()
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	_, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "execution-fixture-key", "execution-fixture-command", "execution-fixture-event", "execution-fixture-approval", "execution-fixture-aggregate")
	request := bindCurrentEvidenceRequest(snapshot, before, "step-authority")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)
	factory := newActualApprovalExecutionHostFactory(t, before, request)
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{EvidencePack: request, ModelHostFactory: factory, PromptRevision: "prompt-execution"})
	if result.State.Status != "available" || result.Proposal == nil || result.Pack == nil || result.View == nil || result.View.Map == nil {
		t.Fatalf("actual persisted execution fixture enrichment = %+v", result)
	}
	proposalStore := NewDurableProposalStore(root)
	workspaceID := access.Workspace().WorkspaceID()
	if err := PersistAvailableEnrichment(context.Background(), proposalStore, workspaceID, &result); err != nil {
		t.Fatalf("persist actual execution fixture: %v", err)
	}
	reopened := NewDurableProposalStore(root)
	loaded, err := LoadProposalForApproval(context.Background(), reopened, workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("reload actual execution fixture: %v", err)
	}
	if loaded == nil || loaded.Proposal == nil || loaded.Pack == nil || loaded.Proposal.ProposalID != result.Proposal.ProposalID || loaded.Pack.EvidencePackID != result.Pack.EvidencePackID {
		t.Fatalf("reloaded execution fixture identity = %+v", loaded)
	}
	if !reflect.DeepEqual(loaded.Proposal, result.Proposal) || !reflect.DeepEqual(loaded.Pack, result.Pack) {
		t.Fatalf("reloaded execution fixture changed persisted pair")
	}
	if err := storage.New(root).WithValidatedActiveApprovalIdentity(func(storage.ValidatedActiveApprovalIdentity) error { return nil }); err != nil {
		t.Fatalf("actual execution fixture active proof: %v", err)
	}
	return approvalExecutionFixture{root: root, engine: engine, access: access, before: before, proposalStore: reopened, proposalID: result.Proposal.ProposalID, packID: result.Pack.EvidencePackID}
}

func newActualApprovalExecutionHostFactory(t *testing.T, mapIR *SemanticMapIR, request EvidencePackRequest) protocol.ModelHostFactory {
	t.Helper()
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatalf("execution fixture pack: %v", err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatalf("execution fixture digests: %v", err)
	}
	targetStepID := request.TargetStepIDs[0]
	proposal, err := json.Marshal(ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-execution",
		ComputedBasisID: request.Snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: request.Snapshot.SnapshotID,
		TargetStepID: targetStepID, TargetSymbolPath: pack.TargetSymbolPath, ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "actual-test-model", ModelRevision: "r1", PromptRevision: "prompt-execution", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: append([]string(nil), packTargetEvidenceRefs(mapIR, targetStepID)...), FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	})
	if err != nil {
		t.Fatalf("execution fixture proposal: %v", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(proposal)
	root := t.TempDir()
	config := protocol.ModelHostConfig{BinPath: os.Args[0], Args: []string{"-test.run=TestSemanticModelHostHelper"}, Env: []string{"CODEFLOW_MODEL_HOST_HELPER=semantic", "CODEFLOW_VS08_MODEL_HOST_RELEASE=" + encoded}, DisposableRoot: root, DefaultTimeout: 3 * time.Second}
	return func(ctx context.Context) (*protocol.ModelHost, error) { return protocol.SpawnModelHost(ctx, config) }
}

func packTargetEvidenceRefs(mapIR *SemanticMapIR, targetStepID string) []string {
	for _, step := range mapIR.Steps {
		if step.StepID == targetStepID {
			return append([]string(nil), step.EvidenceRefs...)
		}
	}
	return nil
}

func TestApprovalExecutionServiceActualPublishedProofAndStoredPair(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service, err := NewApprovalExecutionService(fixture.root, fixture.engine, fixture.proposalStore)
	if err != nil {
		t.Fatalf("new approval execution service: %v", err)
	}
	draft := ApprovalCommandDraft{
		CommandID: "execution-command-approve", ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
		ComputedBasisID: fixture.before.ComputedBasisID, GenerationID: fixture.before.GenerationID,
		IntentRevision: int64(fixture.before.Task.IntentRevision), Decision: "approve", IdempotencyKey: "execution-key-approve",
		ExpectedApprovalVersion: 0, ExpectedState: "none",
	}
	receipt, err := service.Execute(context.Background(), fixture.access, draft)
	if err != nil {
		t.Fatalf("execute approval against actual proof/pair: %v", err)
	}
	if receipt.Receipt.Replayed || receipt.Receipt.Event.EventID == "" || receipt.Receipt.Aggregate.State != "active" || receipt.Receipt.Aggregate.Version != 1 {
		t.Fatalf("actual execution receipt = %+v", receipt)
	}
	if receipt.Receipt.Event.ApprovedText == "" || receipt.GenerationID != fixture.before.GenerationID || receipt.ComputedBasisID != fixture.before.ComputedBasisID || receipt.Freshness != "current" {
		t.Fatalf("actual execution projection = %+v", receipt)
	}
	snapshot, err := NewApprovalTransactionStore(fixture.root, fixture.engine).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("execution transaction snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 || len(snapshot.Aggregates) != 1 {
		t.Fatalf("actual execution durable rows = %+v", snapshot)
	}
}

func TestNewApprovalExecutionServiceRequiresExactCoreDependencies(t *testing.T) {
	root := t.TempDir()
	engine, _ := approvalAuthorityWorkspaceEngine(t, root)
	proposalStore := NewDurableProposalStore(root)
	if service, err := NewApprovalExecutionService(root, nil, proposalStore); service != nil || !errors.Is(err, ErrApprovalExecutionInvalid) {
		t.Fatalf("nil engine constructor result = service=%p err=%v", service, err)
	}
	if service, err := NewApprovalExecutionService(root, engine, nil); service != nil || !errors.Is(err, ErrApprovalExecutionInvalid) {
		t.Fatalf("nil proposal store constructor result = service=%p err=%v", service, err)
	}
	otherRoot := t.TempDir()
	otherEngine, err := workspace.NewSnapshotEngine(otherRoot, 0)
	if err != nil {
		t.Fatalf("other engine: %v", err)
	}
	if service, err := NewApprovalExecutionService(root, otherEngine, proposalStore); service != nil || !errors.Is(err, ErrApprovalExecutionInvalid) {
		t.Fatalf("mismatched engine constructor result = service=%p err=%v", service, err)
	}
}

func TestApprovalExecutionServiceRejectsMissingStoredPairWithoutApprovalArtifacts(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service, err := NewApprovalExecutionService(fixture.root, fixture.engine, fixture.proposalStore)
	if err != nil {
		t.Fatalf("new approval execution service: %v", err)
	}
	draft := ApprovalCommandDraft{
		CommandID: "execution-missing-command", ProposalID: "missing-proposal", EvidencePackID: fixture.packID,
		ComputedBasisID: fixture.before.ComputedBasisID, GenerationID: fixture.before.GenerationID,
		IntentRevision: int64(fixture.before.Task.IntentRevision), Decision: "approve", IdempotencyKey: "execution-key-missing",
		ExpectedApprovalVersion: 0, ExpectedState: "none",
	}
	receipt, err := service.Execute(context.Background(), fixture.access, draft)
	if err == nil || !errors.Is(err, ErrApprovalExecutionUnavailable) || !reflect.DeepEqual(receipt.Receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("missing stored pair result = %+v err=%v", receipt, err)
	}
	snapshot, snapshotErr := NewApprovalTransactionStore(fixture.root, fixture.engine).Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatalf("missing pair transaction snapshot: %v", snapshotErr)
	}
	if len(snapshot.Events) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 || len(snapshot.Aggregates) != 0 {
		t.Fatalf("missing pair created approval artifacts = %+v", snapshot)
	}
}

func TestApprovalExecutionServiceRejectsCorruptStoredPairAndProofWithoutArtifacts(t *testing.T) {
	t.Run("corrupt stored pair", func(t *testing.T) {
		fixture := newApprovalExecutionFixture(t)
		store, ok := fixture.proposalStore.(*DurableProposalStore)
		if !ok || store == nil {
			t.Fatal("execution fixture did not use a durable proposal store")
		}
		path := store.recordPath(fixture.access.Workspace().WorkspaceID(), fixture.proposalID, fixture.packID)
		if err := os.WriteFile(path, []byte(`{"schemaId":"corrupt-execution-pair"`), 0o600); err != nil {
			t.Fatalf("corrupt stored pair: %v", err)
		}
		service := mustNewApprovalExecutionService(t, fixture)
		receipt, err := service.Execute(context.Background(), fixture.access, approvalExecutionDraft(fixture, "corrupt-pair-command", "corrupt-pair-key", "approve", "none", 0, nil, nil))
		if err == nil || !errors.Is(err, ErrApprovalExecutionInvalid) || !errors.Is(err, ErrProposalInvalid) || !reflect.DeepEqual(receipt.Receipt, ApprovalCommitReceipt{}) || strings.Contains(err.Error(), "corrupt-execution-pair") {
			t.Fatalf("corrupt stored pair result = %+v err=%v", receipt, err)
		}
		assertApprovalExecutionStoreHasNoRows(t, service)
	})

	t.Run("corrupt active proof", func(t *testing.T) {
		fixture := newApprovalExecutionFixture(t)
		corruptApprovalAuthorityPointer(t, fixture.root)
		service := mustNewApprovalExecutionService(t, fixture)
		receipt, err := service.Execute(context.Background(), fixture.access, approvalExecutionDraft(fixture, "corrupt-proof-command", "corrupt-proof-key", "approve", "none", 0, nil, nil))
		if err == nil || !errors.Is(err, ErrApprovalExecutionUnavailable) || !reflect.DeepEqual(receipt.Receipt, ApprovalCommitReceipt{}) {
			t.Fatalf("corrupt active proof result = %+v err=%v", receipt, err)
		}
		assertApprovalExecutionStoreHasNoRows(t, service)
	})
}

func TestApprovalExecutionServiceRunsLifecycleDecisionsAgainstOneStoredPair(t *testing.T) {
	t.Run("reject from genesis", func(t *testing.T) {
		fixture := newApprovalExecutionFixture(t)
		service := mustNewApprovalExecutionService(t, fixture)
		result := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "reject-command", "reject-key", "reject", "none", 0, nil, nil))
		if result.Receipt.Aggregate.State != "rejected" || result.Receipt.Aggregate.Version != 1 || result.Receipt.Event.ApprovedText != "" {
			t.Fatalf("reject from genesis receipt = %+v", result.Receipt)
		}
	})

	for _, decision := range []string{"edit_then_approve", "reject", "revoke", "supersede"} {
		decision := decision
		t.Run("active to "+decision, func(t *testing.T) {
			fixture := newApprovalExecutionFixture(t)
			service := mustNewApprovalExecutionService(t, fixture)
			initial := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "initial-command", "initial-key", "approve", "none", 0, nil, nil))
			predecessor := initial.Receipt.Event.ApprovalID
			var edited *string
			if decision == "edit_then_approve" {
				value := "Edited approved title"
				edited = &value
			}
			result := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, decision+"-command", decision+"-key", decision, "active", 1, &predecessor, edited))
			wantState := "active"
			if decision == "reject" {
				wantState = "rejected"
			} else if decision == "revoke" {
				wantState = "revoked"
			} else if decision == "supersede" {
				wantState = "superseded"
			}
			if result.Receipt.Aggregate.State != wantState || result.Receipt.Aggregate.Version != 2 {
				t.Fatalf("%s result = %+v", decision, result.Receipt)
			}
			if decision == "edit_then_approve" && result.Receipt.Event.ApprovedText != *edited {
				t.Fatalf("edited approval text = %q, want %q", result.Receipt.Event.ApprovedText, *edited)
			}
			if decision != "edit_then_approve" && result.Receipt.Event.ApprovedText != "" {
				t.Fatalf("%s approval text = %q, want empty", decision, result.Receipt.Event.ApprovedText)
			}
		})
	}
}

func TestApprovalExecutionServiceExactRetryAndRestartReturnOriginalReceipt(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "retry-command", "retry-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, draft)
	advanced := cloneSemanticMap(fixture.before)
	advanced.GenerationID = "generation-execution-advanced"
	advanced.MapID = "map-execution-advanced"
	publishApprovalAuthorityProofAtWithEngine(t, fixture.root, advanced, fixture.before.GenerationID, 2, fixture.engine)
	retry := mustExecuteApproval(t, service, fixture, draft)
	assertApprovalExecutionReplayResult(t, retry, first)
	if retry.Freshness != "historical" {
		t.Fatalf("retry freshness = %q, want historical after proof advance", retry.Freshness)
	}
	snapshot, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("retry snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 || len(snapshot.Aggregates) != 1 {
		t.Fatalf("same-key retry duplicated rows: %+v", snapshot)
	}

	restartedEngine, err := workspace.NewSnapshotEngine(fixture.root, 0)
	if err != nil {
		t.Fatalf("reopen workspace engine: %v", err)
	}
	restartedService, err := NewApprovalExecutionService(fixture.root, restartedEngine, NewDurableProposalStore(fixture.root))
	if err != nil {
		t.Fatalf("reopen approval execution service: %v", err)
	}
	restarted := mustExecuteApproval(t, restartedService, fixture, draft)
	assertApprovalExecutionReplayResult(t, restarted, first)
	if restarted.Freshness != "historical" {
		t.Fatalf("restarted retry freshness = %q, want historical after proof advance", restarted.Freshness)
	}
}

func TestApprovalExecutionServiceReplaySkipsProposalLookupAfterProofAdvance(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "replay-before-load-command", "replay-before-load-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, draft)
	advanced := cloneSemanticMap(fixture.before)
	advanced.GenerationID = "generation-execution-replay-before-load"
	advanced.MapID = "map-execution-replay-before-load"
	publishApprovalAuthorityProofAtWithEngine(t, fixture.root, advanced, fixture.before.GenerationID, 2, fixture.engine)

	failingStore := &approvalExecutionFailingLoadStore{}
	service.proposalStore = failingStore
	replayed, err := service.Execute(context.Background(), fixture.access, draft)
	if err != nil {
		t.Fatalf("replay with failing proposal lookup: %v", err)
	}
	assertApprovalExecutionReplayResult(t, replayed, first)
	if replayed.Freshness != "historical" {
		t.Fatalf("replay freshness = %q, want historical after proof advance", replayed.Freshness)
	}
	if calls := failingStore.loadCalls.Load(); calls != 0 {
		t.Fatalf("replay proposal lookup calls = %d, want zero", calls)
	}
}

func TestApprovalExecutionFreshnessAfterDurableCommitNeverTrustsMissingOrCorruptProof(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "missing", mutate: func(t *testing.T, root string) {
			path := filepath.Join(storage.New(root).BaseDir(), "active-pointer.json")
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove active proof pointer: %v", err)
			}
		}},
		{name: "corrupt", mutate: func(t *testing.T, root string) { corruptApprovalAuthorityPointer(t, root) }},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newApprovalExecutionFixture(t)
			service := mustNewApprovalExecutionService(t, fixture)
			draft := approvalExecutionDraft(fixture, "freshness-proof-"+testCase.name+"-command", "freshness-proof-"+testCase.name+"-key", "approve", "none", 0, nil, nil)
			first := mustExecuteApproval(t, service, fixture, draft)
			testCase.mutate(t, fixture.root)
			replayed, err := service.Execute(context.Background(), fixture.access, draft)
			if err != nil {
				t.Fatalf("replay after %s proof: %v", testCase.name, err)
			}
			assertApprovalExecutionReplayResult(t, replayed, first)
			if replayed.Freshness != approvalExecutionFreshnessHistorical {
				t.Fatalf("replay after %s proof freshness = %q, want historical", testCase.name, replayed.Freshness)
			}
		})
	}
}

func TestApprovalExecutionFreshnessProjectorRejectsTargetIdentityMismatches(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "freshness-target-command", "freshness-target-key", "approve", "none", 0, nil, nil)
	command, err := BindApprovalCommandV2(fixture.access, draft)
	if err != nil {
		t.Fatalf("bind freshness target command: %v", err)
	}
	base := approvalExecutionTargetForMap(command.Command(), fixture.before)
	for _, testCase := range []struct {
		name   string
		mutate func(*approvalTransactionTarget)
	}{
		{name: "workspace", mutate: func(target *approvalTransactionTarget) { target.WorkspaceID = "workspace-freshness-other" }},
		{name: "computed basis", mutate: func(target *approvalTransactionTarget) { target.ComputedBasisID = "basis-freshness-other" }},
		{name: "generation", mutate: func(target *approvalTransactionTarget) { target.GenerationID = "generation-freshness-other" }},
		{name: "validated snapshot", mutate: func(target *approvalTransactionTarget) { target.ValidatedSnapshotID = "snapshot-freshness-other" }},
		{name: "map", mutate: func(target *approvalTransactionTarget) { target.MapID = "map-freshness-other" }},
		{name: "task", mutate: func(target *approvalTransactionTarget) { target.TaskID = "task-freshness-other" }},
		{name: "intent revision", mutate: func(target *approvalTransactionTarget) { target.IntentRevision++ }},
		{name: "workspace epoch", mutate: func(target *approvalTransactionTarget) { target.WorkspaceEpoch++ }},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			target := base
			testCase.mutate(&target)
			freshness, err := service.projectApprovalFreshness(context.Background(), target)
			if err != nil || freshness != approvalExecutionFreshnessHistorical {
				t.Fatalf("target %s freshness = %q err=%v, want historical and nil error", testCase.name, freshness, err)
			}
		})
	}
}

func TestApprovalExecutionReplayFreshnessProjectionIsReadOnly(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "freshness-read-only-command", "freshness-read-only-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, draft)
	advanced := cloneSemanticMap(fixture.before)
	advanced.GenerationID = "generation-freshness-read-only-advanced"
	advanced.MapID = "map-freshness-read-only-advanced"
	publishApprovalAuthorityProofAtWithEngine(t, fixture.root, advanced, fixture.before.GenerationID, 2, fixture.engine)

	bundleBefore, err := storage.New(fixture.root).ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read active proof before replay projection: %v", err)
	}
	txPath := filepath.Join(fixture.root, ".codeflow", "approval-transactions", approvalTransactionDatabaseName)
	databaseBefore, err := os.ReadFile(txPath)
	if err != nil {
		t.Fatalf("read transaction database before replay projection: %v", err)
	}
	snapshotBefore, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read transaction snapshot before replay projection: %v", err)
	}

	replayed, err := service.Execute(context.Background(), fixture.access, draft)
	if err != nil {
		t.Fatalf("replay after proof advance: %v", err)
	}
	assertApprovalExecutionReplayResult(t, replayed, first)
	if replayed.Freshness != approvalExecutionFreshnessHistorical {
		t.Fatalf("replay freshness = %q, want historical", replayed.Freshness)
	}

	bundleAfter, err := storage.New(fixture.root).ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read active proof after replay projection: %v", err)
	}
	databaseAfter, err := os.ReadFile(txPath)
	if err != nil {
		t.Fatalf("read transaction database after replay projection: %v", err)
	}
	snapshotAfter, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read transaction snapshot after replay projection: %v", err)
	}
	if !bytes.Equal(bundleBefore.SemanticMap, bundleAfter.SemanticMap) {
		t.Fatal("replay freshness projection changed semantic-map bytes")
	}
	if !bytes.Equal(databaseBefore, databaseAfter) {
		t.Fatal("replay freshness projection changed transaction database bytes")
	}
	if !reflect.DeepEqual(snapshotBefore, snapshotAfter) {
		t.Fatal("replay freshness projection changed transaction snapshot")
	}
}

func TestApprovalExecutionFreshnessProjectorConcurrentProofAdvance(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "freshness-concurrent-command", "freshness-concurrent-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, draft)
	if first.Freshness != approvalExecutionFreshnessCurrent {
		t.Fatalf("initial freshness = %q, want current", first.Freshness)
	}
	advanced := cloneSemanticMap(fixture.before)
	advanced.GenerationID = "generation-freshness-concurrent-advanced"
	advanced.MapID = "map-freshness-concurrent-advanced"

	start := make(chan struct{})
	publicationErr := make(chan error, 1)
	go func() {
		<-start
		publicationErr <- publishApprovalAuthorityProofAtWithEngineResult(fixture.root, advanced, fixture.before.GenerationID, 2, fixture.engine)
	}()
	const readers = 8
	results := make(chan error, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := service.Execute(context.Background(), fixture.access, draft)
			if err != nil {
				results <- err
				return
			}
			if !result.Receipt.Replayed {
				results <- errors.New("concurrent freshness read did not replay")
				return
			}
			results <- nil
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent freshness reads did not complete")
	}
	for i := 0; i < readers; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent freshness read: %v", err)
		}
	}
	if err := <-publicationErr; err != nil {
		t.Fatalf("concurrent proof advance: %v", err)
	}
	final, err := service.Execute(context.Background(), fixture.access, draft)
	if err != nil {
		t.Fatalf("freshness read after proof advance: %v", err)
	}
	if final.Freshness != approvalExecutionFreshnessHistorical {
		t.Fatalf("freshness after proof advance = %q, want historical", final.Freshness)
	}
}

func TestApprovalExecutionReplayFreshnessAfterRealLiveHeadAdvanceIsHistoricalAndReadOnly(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "freshness-live-head-command", "freshness-live-head-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, draft)
	if first.Freshness != approvalExecutionFreshnessCurrent {
		t.Fatalf("initial freshness = %q, want current", first.Freshness)
	}

	proofBefore, err := storage.New(fixture.root).ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read active proof before live-head advance: %v", err)
	}
	pointerPath := filepath.Join(storage.New(fixture.root).BaseDir(), "active-pointer.json")
	pointerBefore, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read active pointer before live-head advance: %v", err)
	}
	transactionPath := filepath.Join(fixture.root, ".codeflow", "approval-transactions", approvalTransactionDatabaseName)
	databaseBefore, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database before live-head advance: %v", err)
	}
	transactionBefore, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read transaction snapshot before live-head advance: %v", err)
	}
	liveHeadBefore := fixture.engine.LiveHeadID()
	if liveHeadBefore == "" || liveHeadBefore != fixture.before.ValidatedAgainstSnapshotID {
		t.Fatalf("initial live head = %q, want proof snapshot %q", liveHeadBefore, fixture.before.ValidatedAgainstSnapshotID)
	}

	if _, _, err := fixture.engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "checkout.go", Content: []byte("func Submit() { println(\"live head advanced\") }"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
	}); err != nil {
		t.Fatalf("advance durable workspace live head: %v", err)
	}
	liveHeadAfter := fixture.engine.LiveHeadID()
	if liveHeadAfter == "" || liveHeadAfter == liveHeadBefore {
		t.Fatalf("live head after edit = %q, want a new durable head", liveHeadAfter)
	}

	proofAfterEdit, err := storage.New(fixture.root).ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read active proof after live-head advance: %v", err)
	}
	pointerAfterEdit, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read active pointer after live-head advance: %v", err)
	}
	assertApprovalExecutionProofBytesEqual(t, proofBefore, proofAfterEdit)
	if !bytes.Equal(pointerBefore, pointerAfterEdit) {
		t.Fatal("real workspace edit changed active pointer bytes")
	}

	var liveHeadValidationAttempted atomic.Bool
	fixture.engine.SetLiveHeadAttemptHook(func() { liveHeadValidationAttempted.Store(true) })
	defer fixture.engine.SetLiveHeadAttemptHook(nil)
	failingStore := &approvalExecutionFailingLoadStore{}
	service.proposalStore = failingStore
	replayed, err := service.Execute(context.Background(), fixture.access, draft)
	if err != nil {
		t.Fatalf("exact retry after real live-head advance: %v", err)
	}
	assertApprovalExecutionReplayResult(t, replayed, first)
	if replayed.Freshness != approvalExecutionFreshnessHistorical {
		t.Fatalf("exact retry freshness = %q, want historical", replayed.Freshness)
	}
	if !liveHeadValidationAttempted.Load() {
		t.Fatal("exact retry freshness projection bypassed SnapshotEngine live-head validation")
	}
	if calls := failingStore.loadCalls.Load(); calls != 0 {
		t.Fatalf("exact retry proposal lookup calls = %d, want zero", calls)
	}

	transactionAfter, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read transaction snapshot after exact retry: %v", err)
	}
	if !reflect.DeepEqual(transactionBefore.Events, transactionAfter.Events) || !reflect.DeepEqual(transactionBefore.Aggregates, transactionAfter.Aggregates) || !reflect.DeepEqual(transactionBefore.IdempotencyResults, transactionAfter.IdempotencyResults) || !reflect.DeepEqual(transactionBefore.Outbox, transactionAfter.Outbox) || !reflect.DeepEqual(transactionBefore.targets, transactionAfter.targets) {
		t.Fatal("exact retry changed durable approval rows or target metadata")
	}
	if !reflect.DeepEqual(transactionBefore, transactionAfter) {
		t.Fatal("exact retry changed the complete transaction snapshot")
	}
	databaseAfter, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database after exact retry: %v", err)
	}
	if !bytes.Equal(databaseBefore, databaseAfter) {
		t.Fatal("exact retry changed transaction database bytes")
	}
	proofAfterRetry, err := storage.New(fixture.root).ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read active proof after exact retry: %v", err)
	}
	assertApprovalExecutionProofBytesEqual(t, proofBefore, proofAfterRetry)
	pointerAfterRetry, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read active pointer after exact retry: %v", err)
	}
	if !bytes.Equal(pointerBefore, pointerAfterRetry) {
		t.Fatal("exact retry changed active pointer bytes")
	}
}

func assertApprovalExecutionProofBytesEqual(t *testing.T, before, after *storage.ValidatedActiveProofBundle) {
	t.Helper()
	if before == nil || after == nil || !bytes.Equal(before.ManifestBytes, after.ManifestBytes) || !bytes.Equal(before.SemanticMap, after.SemanticMap) || !bytes.Equal(before.AnalyzerResult, after.AnalyzerResult) || !bytes.Equal(before.SemanticDelta, after.SemanticDelta) {
		t.Fatal("active proof bytes changed")
	}
	if !reflect.DeepEqual(before.Manifest, after.Manifest) || !reflect.DeepEqual(before.Pointer, after.Pointer) {
		t.Fatal("active proof identity changed")
	}
}

func TestApprovalExecutionServiceReplaysLifecycleVersionsAfterRestart(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	approveDraft := approvalExecutionDraft(fixture, "restart-lifecycle-v1-command", "restart-lifecycle-v1-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, approveDraft)
	predecessor := first.Receipt.Event.ApprovalID
	editedText := "Restart lifecycle edited title"
	editDraft := approvalExecutionDraft(fixture, "restart-lifecycle-v2-command", "restart-lifecycle-v2-key", "edit_then_approve", "active", 1, &predecessor, &editedText)
	second := mustExecuteApproval(t, service, fixture, editDraft)
	if first.Receipt.Aggregate.Version != 1 || len(first.Receipt.Aggregate.History) != 2 || second.Receipt.Aggregate.Version != 2 || len(second.Receipt.Aggregate.History) != 3 || second.Receipt.Aggregate.ActiveApprovalID != second.Receipt.Event.ApprovalID {
		t.Fatalf("initial lifecycle versions = first=%+v second=%+v", first, second)
	}

	authorizer := NewApprovalWorkspaceAuthorizer(fixture.root)
	rotatedAccess, err := NewApprovalAccessGate(NewLocalProcessApprovalAuthenticator(), authorizer).AuthenticateAndAuthorize(context.Background(), authorizer.WorkspaceID(), fixture.root)
	if err != nil {
		t.Fatalf("new restart lifecycle access: %v", err)
	}
	if rotatedAccess.Actor().ActorID != fixture.access.Actor().ActorID || rotatedAccess.Actor().SessionID == fixture.access.Actor().SessionID {
		t.Fatalf("restart lifecycle access = %+v, want stable actor and rotated session", rotatedAccess.Actor())
	}
	restartedEngine, err := workspace.NewSnapshotEngine(fixture.root, 0)
	if err != nil {
		t.Fatalf("reopen restart lifecycle engine: %v", err)
	}
	restartedService, err := NewApprovalExecutionService(fixture.root, restartedEngine, NewDurableProposalStore(fixture.root))
	if err != nil {
		t.Fatalf("reopen restart lifecycle service: %v", err)
	}
	replayedFirst, err := restartedService.Execute(context.Background(), rotatedAccess, approveDraft)
	if err != nil {
		t.Fatalf("replay lifecycle version 1: %v", err)
	}
	assertApprovalExecutionReplayResult(t, replayedFirst, first)
	replayedSecond, err := restartedService.Execute(context.Background(), rotatedAccess, editDraft)
	if err != nil {
		t.Fatalf("replay lifecycle version 2: %v", err)
	}
	assertApprovalExecutionReplayResult(t, replayedSecond, second)
	if replayedFirst.Receipt.Aggregate.Version != 1 || len(replayedFirst.Receipt.Aggregate.History) != 2 || replayedSecond.Receipt.Aggregate.Version != 2 || len(replayedSecond.Receipt.Aggregate.History) != 3 || replayedSecond.Receipt.Aggregate.ActiveApprovalID != replayedSecond.Receipt.Event.ApprovalID {
		t.Fatalf("replayed lifecycle versions = first=%+v second=%+v", replayedFirst, replayedSecond)
	}
	snapshot, err := restartedService.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("replay lifecycle snapshot: %v", err)
	}
	if len(snapshot.Events) != 2 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 2 || len(snapshot.Outbox) != 2 {
		t.Fatalf("replay lifecycle duplicated durable rows: %+v", snapshot)
	}
}

type approvalExecutionFailingLoadStore struct {
	loadCalls atomic.Int32
}

func (*approvalExecutionFailingLoadStore) SaveEnrichmentResult(context.Context, string, *EnrichmentResult) error {
	return errors.New("unexpected proposal persistence")
}

func (s *approvalExecutionFailingLoadStore) Load(context.Context, string, string, string) (*StoredProposal, error) {
	s.loadCalls.Add(1)
	return nil, errors.New("unexpected proposal lookup")
}

func TestApprovalExecutionReplayRequiresDurableTargetMetadata(t *testing.T) {
	mutation := approvalTransactionTestMutation(t, "replay-target-key", "replay-target-command", "replay-target-event", "replay-target-approval", "replay-target-aggregate")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), mutation); err != nil {
		t.Fatalf("commit replay target fixture: %v", err)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot replay target fixture: %v", err)
	}
	command := mutation.Command()
	cases := []struct {
		name   string
		mutate func(*ApprovalTransactionSnapshot)
	}{
		{name: "missing target", mutate: func(candidate *ApprovalTransactionSnapshot) { candidate.targets = nil }},
		{name: "mismatched target", mutate: func(candidate *ApprovalTransactionSnapshot) {
			candidate.targets[0].Target.ProposalID = "replay-target-other-proposal"
		}},
		{name: "corrupt target", mutate: func(candidate *ApprovalTransactionSnapshot) {
			candidate.targets[0].Target.MapID = ""
		}},
		{name: "substituted validated snapshot", mutate: func(candidate *ApprovalTransactionSnapshot) {
			candidate.targets[0].Target.ValidatedSnapshotID = "replay-target-other-snapshot"
		}},
		{name: "substituted workspace epoch", mutate: func(candidate *ApprovalTransactionSnapshot) {
			candidate.targets[0].Target.WorkspaceEpoch++
		}},
		{name: "substituted map", mutate: func(candidate *ApprovalTransactionSnapshot) {
			candidate.targets[0].Target.MapID = "replay-target-other-map"
		}},
		{name: "substituted task", mutate: func(candidate *ApprovalTransactionSnapshot) {
			candidate.targets[0].Target.TaskID = "replay-target-other-task"
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := snapshot
			candidate.targets = make([]approvalTransactionTargetRecord, len(snapshot.targets))
			for index := range snapshot.targets {
				candidate.targets[index] = snapshot.targets[index]
				candidate.targets[index].Aggregate = cloneApprovalAggregateV2(snapshot.targets[index].Aggregate)
			}
			testCase.mutate(&candidate)
			result, found, replayErr := approvalExecutionReplay(candidate, command)
			if !found || replayErr == nil || !errors.Is(replayErr, ErrApprovalExecutionUnavailable) || !reflect.DeepEqual(result, ApprovalExecutionResult{}) {
				t.Fatalf("replay %s = result=%+v found=%v err=%v, want found unavailable and zero result", testCase.name, result, found, replayErr)
			}
		})
	}
}

func assertApprovalExecutionReplayResult(t *testing.T, got, original ApprovalExecutionResult) {
	t.Helper()
	if !got.Receipt.Replayed {
		t.Fatalf("replay result = %+v, want replayed receipt", got)
	}
	gotReceipt := got.Receipt
	gotReceipt.Replayed = false
	if !reflect.DeepEqual(gotReceipt, original.Receipt) || got.GenerationID != original.GenerationID || got.ComputedBasisID != original.ComputedBasisID || got.ValidatedSnapshotID != original.ValidatedSnapshotID || got.IntentRevision != original.IntentRevision {
		t.Fatalf("replay result = %+v, want original result except Replayed", got)
	}
}

func TestApprovalExecutionServiceRestartRotatesCoreSessionForExactReplay(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "rotated-session-command", "rotated-session-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, draft)
	advanced := cloneSemanticMap(fixture.before)
	advanced.GenerationID = "generation-execution-rotated-advanced"
	advanced.MapID = "map-execution-rotated-advanced"
	publishApprovalAuthorityProofAtWithEngine(t, fixture.root, advanced, fixture.before.GenerationID, 2, fixture.engine)

	// A new authenticator models a process restart. The OS principal and
	// configured workspace remain stable, while the Core-issued session must
	// rotate. The command draft and idempotency key are otherwise identical.
	authorizer := NewApprovalWorkspaceAuthorizer(fixture.root)
	rotatedAccess, err := NewApprovalAccessGate(NewLocalProcessApprovalAuthenticator(), authorizer).AuthenticateAndAuthorize(context.Background(), authorizer.WorkspaceID(), fixture.root)
	if err != nil {
		t.Fatalf("new Core session access: %v", err)
	}
	if rotatedAccess.Actor().ActorID != fixture.access.Actor().ActorID {
		t.Fatalf("rotated Core actor = %q, want stable actor %q", rotatedAccess.Actor().ActorID, fixture.access.Actor().ActorID)
	}
	if rotatedAccess.Actor().SessionID == fixture.access.Actor().SessionID {
		t.Fatalf("new Core authenticator reused session %q", rotatedAccess.Actor().SessionID)
	}

	restartedEngine, err := workspace.NewSnapshotEngine(fixture.root, 0)
	if err != nil {
		t.Fatalf("reopen workspace engine: %v", err)
	}
	restartedService, err := NewApprovalExecutionService(fixture.root, restartedEngine, NewDurableProposalStore(fixture.root))
	if err != nil {
		t.Fatalf("reopen approval execution service: %v", err)
	}
	replayed, err := restartedService.Execute(context.Background(), rotatedAccess, draft)
	if err != nil {
		t.Fatalf("rotated-session exact retry: %v", err)
	}
	assertApprovalExecutionReplayResult(t, replayed, first)
	snapshot, err := restartedService.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("rotated-session snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 || len(snapshot.Aggregates) != 1 {
		t.Fatalf("rotated-session replay duplicated rows: %+v", snapshot)
	}
}

func TestApprovalExecutionServiceSessionRotationDoesNotRelaxIdempotency(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "rotation-boundary-command", "rotation-boundary-key", "approve", "none", 0, nil, nil)
	if _, err := service.Execute(context.Background(), fixture.access, draft); err != nil {
		t.Fatalf("initial rotation-boundary commit: %v", err)
	}
	authorizer := NewApprovalWorkspaceAuthorizer(fixture.root)
	rotatedAccess, err := NewApprovalAccessGate(NewLocalProcessApprovalAuthenticator(), authorizer).AuthenticateAndAuthorize(context.Background(), authorizer.WorkspaceID(), fixture.root)
	if err != nil {
		t.Fatalf("rotated Core access: %v", err)
	}
	if rotatedAccess.Actor().ActorID != fixture.access.Actor().ActorID || rotatedAccess.Actor().SessionID == fixture.access.Actor().SessionID {
		t.Fatalf("rotated Core authority = %+v, want stable actor and fresh session", rotatedAccess.Actor())
	}

	changedDraft := draft
	changedDraft.CommandID = "rotation-boundary-different-command"
	if result, err := service.Execute(context.Background(), rotatedAccess, changedDraft); err == nil || !errors.Is(err, ErrApprovalExecutionConflict) || !reflect.DeepEqual(result.Receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("same-key semantic mutation = result=%+v err=%v, want conflict and zero receipt", result, err)
	}

	otherAuthenticator := newLocalProcessApprovalAuthenticator(
		func() (string, error) { return "uid:rotation-boundary-other-principal", nil },
		func() (string, error) { return "session-rotation-boundary-other", nil },
	)
	otherAuthorizer := NewApprovalWorkspaceAuthorizer(fixture.root)
	otherAccess, err := NewApprovalAccessGate(otherAuthenticator, otherAuthorizer).AuthenticateAndAuthorize(context.Background(), otherAuthorizer.WorkspaceID(), fixture.root)
	if err != nil {
		t.Fatalf("other Core actor access: %v", err)
	}
	if otherAccess.Actor().ActorID == fixture.access.Actor().ActorID {
		t.Fatalf("test actors unexpectedly share actor identity %q", otherAccess.Actor().ActorID)
	}
	if result, err := service.Execute(context.Background(), otherAccess, draft); err == nil || !errors.Is(err, ErrApprovalExecutionConflict) || !reflect.DeepEqual(result.Receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("same-key actor mutation = result=%+v err=%v, want conflict and zero receipt", result, err)
	}

	snapshot, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("rotation-boundary snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 || len(snapshot.Aggregates) != 1 {
		t.Fatalf("same-key mutations changed durable rows: %+v", snapshot)
	}
}

func TestApprovalExecutionServiceStaleProofOrStateDoesNotPublish(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ApprovalCommandDraft, approvalExecutionFixture)
	}{
		{name: "generation", mutate: func(draft *ApprovalCommandDraft, _ approvalExecutionFixture) { draft.GenerationID = "generation-stale" }},
		{name: "intent", mutate: func(draft *ApprovalCommandDraft, _ approvalExecutionFixture) { draft.IntentRevision++ }},
		{name: "expected state", mutate: func(draft *ApprovalCommandDraft, _ approvalExecutionFixture) {
			draft.Decision = "reject"
			draft.ExpectedState = "active"
			draft.ExpectedApprovalVersion = 1
		}},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newApprovalExecutionFixture(t)
			service := mustNewApprovalExecutionService(t, fixture)
			draft := approvalExecutionDraft(fixture, "stale-"+testCase.name, "stale-"+testCase.name, "approve", "none", 0, nil, nil)
			testCase.mutate(&draft, fixture)
			receipt, err := service.Execute(context.Background(), fixture.access, draft)
			if err == nil || !errors.Is(err, ErrApprovalExecutionConflict) || !reflect.DeepEqual(receipt.Receipt, ApprovalCommitReceipt{}) {
				t.Fatalf("stale %s result = %+v err=%v", testCase.name, receipt, err)
			}
			snapshot, snapshotErr := service.transactions.Snapshot(context.Background())
			if snapshotErr != nil {
				t.Fatalf("stale %s snapshot: %v", testCase.name, snapshotErr)
			}
			if len(snapshot.Events) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 || len(snapshot.Aggregates) != 0 {
				t.Fatalf("stale %s published artifacts: %+v", testCase.name, snapshot)
			}
		})
	}
}

func TestApprovalExecutionServiceConcurrentKeysCommitOneVersion(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	firstDraft := approvalExecutionDraft(fixture, "concurrent-command-a", "concurrent-key-a", "approve", "none", 0, nil, nil)
	secondDraft := approvalExecutionDraft(fixture, "concurrent-command-b", "concurrent-key-b", "approve", "none", 0, nil, nil)
	results := make(chan struct {
		result ApprovalExecutionResult
		err    error
	}, 2)
	start := make(chan struct{})
	for _, draft := range []ApprovalCommandDraft{firstDraft, secondDraft} {
		draft := draft
		go func() {
			<-start
			result, err := service.Execute(context.Background(), fixture.access, draft)
			results <- struct {
				result ApprovalExecutionResult
				err    error
			}{result: result, err: err}
		}()
	}
	close(start)
	var committed, conflicted int
	for range 2 {
		select {
		case outcome := <-results:
			if outcome.err == nil {
				if outcome.result.Receipt.Replayed || outcome.result.Receipt.Aggregate.Version != 1 {
					t.Fatalf("concurrent success = %+v", outcome.result)
				}
				committed++
			} else if errors.Is(outcome.err, ErrApprovalExecutionConflict) {
				conflicted++
			} else {
				t.Fatalf("concurrent outcome error = %v", outcome.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent execution did not complete")
		}
	}
	if committed != 1 || conflicted != 1 {
		t.Fatalf("concurrent execution outcomes committed=%d conflicted=%d", committed, conflicted)
	}
	snapshot, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("concurrent execution snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 || len(snapshot.Aggregates) != 1 {
		t.Fatalf("concurrent execution rows = %+v", snapshot)
	}
}

func TestApprovalExecutionServiceRejectsStaleDifferentEngineForSameRoot(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	otherEngine, err := workspace.NewSnapshotEngine(fixture.root, 0)
	if err != nil {
		t.Fatalf("open second workspace engine: %v", err)
	}
	if otherEngine.CanonicalRoot() != fixture.engine.CanonicalRoot() {
		t.Fatalf("second engine root = %q, want %q", otherEngine.CanonicalRoot(), fixture.engine.CanonicalRoot())
	}
	if _, _, err := otherEngine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "checkout.go", Content: []byte("func Submit() { return }"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
	}); err != nil {
		t.Fatalf("publish edit through second engine: %v", err)
	}
	if otherEngine.LiveHeadID() == fixture.engine.LiveHeadID() {
		t.Fatalf("second engine did not publish a distinct live head: %q", otherEngine.LiveHeadID())
	}

	result, err := service.Execute(context.Background(), fixture.access, approvalExecutionDraft(fixture, "stale-engine-command", "stale-engine-key", "approve", "none", 0, nil, nil))
	if err == nil || !errors.Is(err, ErrApprovalExecutionConflict) || !reflect.DeepEqual(result.Receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("approval through stale engine = result=%+v err=%v, want conflict and zero receipt", result, err)
	}
	assertApprovalExecutionStoreHasNoRows(t, service)
}

func TestApprovalExecutionServiceSerializesDifferentEnginesForSameRoot(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	otherEngine, err := workspace.NewSnapshotEngine(fixture.root, 0)
	if err != nil {
		t.Fatalf("open second workspace engine: %v", err)
	}

	editPersistence := &approvalAuthorityBlockingPersistence{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	if err := otherEngine.SetPersistence(editPersistence); err != nil {
		t.Fatalf("install blocking second-engine persistence: %v", err)
	}
	var releaseOnce sync.Once
	releaseEdit := func() { releaseOnce.Do(func() { close(editPersistence.release) }) }
	defer releaseEdit()

	editDone := make(chan error, 1)
	go func() {
		_, _, editErr := otherEngine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
			Path: "checkout.go", Content: []byte("func Submit() { println(\"concurrent edit\") }"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
		})
		editDone <- editErr
	}()
	select {
	case err := <-editDone:
		releaseEdit()
		t.Fatalf("second-engine edit returned before its persistence boundary: %v", err)
	case <-editPersistence.entered:
	case <-time.After(approvalAuthorityCoordinationTimeout):
		releaseEdit()
		t.Fatal("second-engine edit did not reach its persistence boundary")
	}

	approvalAttempted := make(chan struct{})
	var attemptOnce sync.Once
	fixture.engine.SetLiveHeadAttemptHook(func() { attemptOnce.Do(func() { close(approvalAttempted) }) })
	defer fixture.engine.SetLiveHeadAttemptHook(nil)
	approvalDone := make(chan struct {
		result ApprovalExecutionResult
		err    error
	}, 1)
	go func() {
		result, approvalErr := service.Execute(context.Background(), fixture.access, approvalExecutionDraft(fixture, "concurrent-stale-engine-command", "concurrent-stale-engine-key", "approve", "none", 0, nil, nil))
		approvalDone <- struct {
			result ApprovalExecutionResult
			err    error
		}{result: result, err: approvalErr}
	}()
	select {
	case outcome := <-approvalDone:
		releaseEdit()
		t.Fatalf("approval completed while second engine held the root authority: result=%+v err=%v", outcome.result, outcome.err)
	case <-approvalAttempted:
	case <-time.After(approvalAuthorityCoordinationTimeout):
		releaseEdit()
		t.Fatal("approval did not reach the live-head authority boundary")
	}

	releaseEdit()
	var editErr error
	select {
	case editErr = <-editDone:
	case <-time.After(approvalAuthorityCoordinationTimeout):
		t.Fatal("second-engine edit did not finish after release")
	}
	if editErr != nil {
		t.Fatalf("second-engine edit: %v", editErr)
	}
	var outcome struct {
		result ApprovalExecutionResult
		err    error
	}
	select {
	case outcome = <-approvalDone:
	case <-time.After(approvalAuthorityCoordinationTimeout):
		t.Fatal("approval did not finish after second-engine publication")
	}
	if outcome.err == nil || !errors.Is(outcome.err, ErrApprovalExecutionConflict) || !reflect.DeepEqual(outcome.result.Receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("concurrent stale-engine approval = result=%+v err=%v, want conflict and zero receipt", outcome.result, outcome.err)
	}
	assertApprovalExecutionStoreHasNoRows(t, service)
}

func mustNewApprovalExecutionService(t *testing.T, fixture approvalExecutionFixture) *ApprovalExecutionService {
	t.Helper()
	service, err := NewApprovalExecutionService(fixture.root, fixture.engine, fixture.proposalStore)
	if err != nil {
		t.Fatalf("new approval execution service: %v", err)
	}
	return service
}

func mustExecuteApproval(t *testing.T, service *ApprovalExecutionService, fixture approvalExecutionFixture, draft ApprovalCommandDraft) ApprovalExecutionResult {
	t.Helper()
	result, err := service.Execute(context.Background(), fixture.access, draft)
	if err != nil {
		t.Fatalf("execute %s: %v", draft.Decision, err)
	}
	return result
}

func assertApprovalExecutionStoreHasNoRows(t *testing.T, service *ApprovalExecutionService) {
	t.Helper()
	snapshot, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval execution empty snapshot: %v", err)
	}
	if len(snapshot.Events) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 || len(snapshot.Aggregates) != 0 {
		t.Fatalf("approval execution published rows after rejection: %+v", snapshot)
	}
}

func approvalExecutionDraft(fixture approvalExecutionFixture, commandID, key, decision, expectedState string, expectedVersion int64, predecessor, edited *string) ApprovalCommandDraft {
	return ApprovalCommandDraft{
		CommandID: commandID, ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
		ComputedBasisID: fixture.before.ComputedBasisID, GenerationID: fixture.before.GenerationID,
		IntentRevision: int64(fixture.before.Task.IntentRevision), Decision: decision, EditedText: edited,
		IdempotencyKey: key, ExpectedApprovalVersion: expectedVersion, ExpectedState: expectedState, PredecessorApprovalID: predecessor,
	}
}
