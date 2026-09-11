package semantic

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"codeflow/internal/evidence"
	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

func TestNewApprovalTransactionStoreCommitsOnlyForMatchingValidatedActiveProof(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-proof-key", "authority-proof-command", "authority-proof-event", "authority-proof-approval", "authority-proof-aggregate")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)
	if err := storage.New(root).WithValidatedActiveApprovalIdentity(func(storage.ValidatedActiveApprovalIdentity) error { return nil }); err != nil {
		t.Fatalf("published active proof is not identity-valid: %v", err)
	}

	store := NewApprovalTransactionStore(root, engine)
	receipt, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("matching active proof commit: %v", err)
	}
	if receipt.Event.EventID != "authority-proof-event" || receipt.Aggregate.AggregateID != "authority-proof-aggregate" || receipt.Replayed {
		t.Fatalf("matching active proof receipt = %+v", receipt)
	}
}

func TestNewApprovalTransactionStoreReplaysBeforeActiveProofCurrentnessCheck(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-replay-key", "authority-replay-command", "authority-replay-event", "authority-replay-approval", "authority-replay-aggregate")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)
	store := NewApprovalTransactionStore(root, engine)
	first, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("initial active-proof commit: %v", err)
	}

	advanced := cloneSemanticMap(before)
	advanced.GenerationID = "generation-authority-advanced"
	advanced.MapID = "map-authority-advanced"
	publishApprovalAuthorityProofAtWithEngine(t, root, advanced, before.GenerationID, 2, engine)

	replayed, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("exact replay after active proof advance: %v", err)
	}
	if !replayed.Replayed || replayed.Event.EventID != first.Event.EventID || replayed.Aggregate.AggregateID != first.Aggregate.AggregateID || !reflect.DeepEqual(replayed.EventPayload, first.EventPayload) {
		t.Fatalf("replay after active proof advance = %+v, want exact original receipt", replayed)
	}
}

func TestNewApprovalTransactionStoreRejectsValidatedActiveProofIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-mismatch-key", "authority-mismatch-command", "authority-mismatch-event", "authority-mismatch-approval", "authority-mismatch-aggregate")
	proofBefore := cloneSemanticMap(before)
	proofBefore.GenerationID = "different-active-generation"
	publishApprovalAuthorityProofWithEngine(t, root, proofBefore, engine)

	receipt, err := NewApprovalTransactionStore(root, engine).Commit(context.Background(), mutation)
	if !errors.Is(err, ErrApprovalTransactionConflict) {
		t.Fatalf("active proof identity mismatch error = %v, want conflict", err)
	}
	if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("active proof identity mismatch returned a receipt: %+v", receipt)
	}
	transactionSnapshot, snapshotErr := newFileApprovalTransactionStoreForTest(filepath.Join(root, ".codeflow", "approval-transactions")).Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatalf("identity mismatch snapshot: %v", snapshotErr)
	}
	if len(transactionSnapshot.Events) != 0 || len(transactionSnapshot.IdempotencyResults) != 0 {
		t.Fatalf("identity mismatch published records: %+v", transactionSnapshot)
	}
}

func TestActiveApprovalTransactionAuthorityRejectsEachExpectedIdentityMutation(t *testing.T) {
	type identityCase struct {
		name       string
		mutate     func(*approvalTransactionTarget)
		wantReject bool
	}
	cases := []identityCase{
		{name: "matching_control", wantReject: false},
		{name: "workspaceId", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.WorkspaceID += "-mismatch"
		}},
		{name: "computedBasisId", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.ComputedBasisID += "-mismatch"
		}},
		{name: "generationId", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.GenerationID += "-mismatch"
		}},
		{name: "validatedSnapshotId", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.ValidatedSnapshotID += "-mismatch"
		}},
		{name: "mapId", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.MapID += "-mismatch"
		}},
		{name: "taskId", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.TaskID += "-mismatch"
		}},
		{name: "intentRevision", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.IntentRevision++
		}},
		{name: "workspaceEpoch", wantReject: true, mutate: func(target *approvalTransactionTarget) {
			target.WorkspaceEpoch++
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
			access := approvalTransactionTestAccessForRoot(t, root)
			mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-identity-key-"+testCase.name, "authority-identity-command-"+testCase.name, "authority-identity-event-"+testCase.name, "authority-identity-approval-"+testCase.name, "authority-identity-aggregate-"+testCase.name)
			publishApprovalAuthorityProofWithEngine(t, root, before, engine)

			command := mutation.Command()
			target := approvalTransactionTarget{
				WorkspaceID:         command.WorkspaceID,
				ProposalID:          command.ProposalID,
				EvidencePackID:      command.EvidencePackID,
				ComputedBasisID:     command.ComputedBasisID,
				GenerationID:        command.GenerationID,
				IntentRevision:      command.IntentRevision,
				ValidatedSnapshotID: before.ValidatedAgainstSnapshotID,
				WorkspaceEpoch:      before.Basis.WorkspaceEpoch,
				MapID:               before.MapID,
				TaskID:              before.Task.TaskID,
			}
			candidate := target
			if testCase.mutate != nil {
				testCase.mutate(&candidate)
				if candidate == target {
					t.Fatal("identity mutation did not change the expected target")
				}
			}

			commitCalls := 0
			err := activeApprovalTransactionAuthority(storage.New(root), engine, target.WorkspaceID)(context.Background(), candidate, nil, func() error {
				commitCalls++
				return nil
			})
			if testCase.wantReject {
				if !errors.Is(err, ErrApprovalTransactionConflict) {
					t.Fatalf("%s mutation error = %v, want typed conflict", testCase.name, err)
				}
				var transactionErr *ApprovalTransactionError
				if !errors.As(err, &transactionErr) || transactionErr.Kind != "conflict" || transactionErr.CurrentState != "none" || transactionErr.CurrentVersion != 0 {
					t.Fatalf("%s mutation error = %v, want conflict at genesis none/0", testCase.name, err)
				}
				if commitCalls != 0 {
					t.Fatalf("%s mutation invoked commit callback %d times", testCase.name, commitCalls)
				}
			} else {
				if err != nil {
					t.Fatalf("matching identity control error = %v", err)
				}
				if commitCalls != 1 {
					t.Fatalf("matching identity control invoked commit callback %d times, want once", commitCalls)
				}
			}
			assertApprovalAuthorityStoreEmpty(t, root)
		})
	}
}

func TestNewApprovalTransactionStoreReportsCorruptActiveProofAtCurrentState(t *testing.T) {
	t.Run("genesis", func(t *testing.T) {
		root := t.TempDir()
		engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
		access := approvalTransactionTestAccessForRoot(t, root)
		mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-corrupt-genesis-key", "authority-corrupt-genesis-command", "authority-corrupt-genesis-event", "authority-corrupt-genesis-approval", "authority-corrupt-genesis-aggregate")
		publishApprovalAuthorityProofWithEngine(t, root, before, engine)
		corruptApprovalAuthorityPointer(t, root)

		store := NewApprovalTransactionStore(root, engine)
		receipt, err := store.Commit(context.Background(), mutation)
		assertApprovalAuthorityCorruptProofPersistence(t, err, "none", 0)
		if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
			t.Fatalf("corrupt genesis proof returned receipt: %+v", receipt)
		}
		assertApprovalAuthorityStoreEmpty(t, root)
	})

	t.Run("active", func(t *testing.T) {
		root := t.TempDir()
		engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
		access := approvalTransactionTestAccessForRoot(t, root)
		initial, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-corrupt-active-initial-key", "authority-corrupt-active-initial-command", "authority-corrupt-active-initial-event", "authority-corrupt-active-initial-approval", "authority-corrupt-active-aggregate")
		publishApprovalAuthorityProofWithEngine(t, root, before, engine)
		store := NewApprovalTransactionStore(root, engine)
		if receipt, err := store.Commit(context.Background(), initial); err != nil || receipt.Replayed {
			t.Fatalf("initial active proof commit: receipt=%+v err=%v", receipt, err)
		}
		beforeSnapshot, err := store.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("snapshot before corrupt active proof: %v", err)
		}

		next := approvalAuthorityNextMutation(t, initial)
		corruptApprovalAuthorityPointer(t, root)
		receipt, err := store.Commit(context.Background(), next)
		assertApprovalAuthorityCorruptProofPersistence(t, err, "active", 1)
		if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
			t.Fatalf("corrupt active proof returned receipt: %+v", receipt)
		}
		afterSnapshot, err := store.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("snapshot after corrupt active proof rejection: %v", err)
		}
		if !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
			t.Fatalf("corrupt active proof changed durable snapshot:\nbefore=%#v\nafter=%#v", beforeSnapshot, afterSnapshot)
		}
	})
}

func approvalAuthorityNextMutation(t *testing.T, initial *ValidatedApprovalMutation) *ValidatedApprovalMutation {
	t.Helper()
	if initial == nil {
		t.Fatal("initial approval mutation is nil")
	}
	command := initial.Command()
	access := initial.Access()
	stored := initial.StoredProposal()
	before := initial.BeforeMap()
	after := initial.AfterMap()
	active := initial.Aggregate()
	firstEvent := initial.Event()
	if stored == nil || before == nil || after == nil || active == nil || firstEvent == nil {
		t.Fatal("initial approval mutation is incomplete")
	}
	predecessor := firstEvent.ApprovalID
	editedText := "edited after active proof"
	nextCommand, err := BindApprovalCommandV2(access, ApprovalCommandDraft{
		CommandID:               "authority-corrupt-active-next-command",
		ProposalID:              command.ProposalID,
		EvidencePackID:          command.EvidencePackID,
		ComputedBasisID:         command.ComputedBasisID,
		GenerationID:            command.GenerationID,
		IntentRevision:          command.IntentRevision,
		Decision:                "edit_then_approve",
		EditedText:              &editedText,
		IdempotencyKey:          "authority-corrupt-active-next-key",
		ExpectedApprovalVersion: 1,
		ExpectedState:           "active",
		PredecessorApprovalID:   &predecessor,
	})
	if err != nil {
		t.Fatalf("bind next approval command: %v", err)
	}
	metadata := lifecycleTestMetadata(active.AggregateID, "authority-corrupt-active-next-event", "authority-corrupt-active-next-approval")
	nextEvent, nextAggregate, err := ReduceApprovalCommandV2(*active, nextCommand, metadata)
	if err != nil {
		t.Fatalf("reduce next approval command: %v", err)
	}
	mutation, err := ValidateApprovalMeaningMutation(nextCommand, stored, before, after, nextEvent, nextAggregate)
	if err != nil {
		t.Fatalf("validate next approval mutation: %v", err)
	}
	return mutation
}

func corruptApprovalAuthorityPointer(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(storage.New(root).BaseDir(), "active-pointer.json")
	if err := os.WriteFile(path, []byte(`{"schemaId":"corrupt-active-proof-sentinel"`), 0o600); err != nil {
		t.Fatalf("corrupt active proof pointer: %v", err)
	}
}

func assertApprovalAuthorityCorruptProofPersistence(t *testing.T, err error, state string, version int64) {
	t.Helper()
	if !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("corrupt active proof error = %v, want persistence", err)
	}
	var transactionErr *ApprovalTransactionError
	if !errors.As(err, &transactionErr) || transactionErr.Kind != "persistence" || transactionErr.CurrentState != state || transactionErr.CurrentVersion != version {
		t.Fatalf("corrupt active proof error = %v, want persistence at %s/%d", err, state, version)
	}
	if strings.Contains(err.Error(), "corrupt-active-proof-sentinel") {
		t.Fatal("corrupt active proof bytes leaked through the bounded error")
	}
}

func TestNewApprovalTransactionStoreRejectsProofAfterWorkspaceHeadEdit(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-edit-key", "authority-edit-command", "authority-edit-event", "authority-edit-approval", "authority-edit-aggregate")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)

	if _, _, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "checkout.go", Content: []byte("func Submit() { println(\"edited\") }"), DocumentVersion: 2, Source: "ide_versioned",
	}); err != nil {
		t.Fatalf("advance authoritative workspace head: %v", err)
	}

	receipt, err := NewApprovalTransactionStore(root, engine).Commit(context.Background(), mutation)
	if !errors.Is(err, ErrApprovalTransactionConflict) || !errors.Is(err, ErrApprovalTransactionWorkspaceLiveHeadConflict) {
		t.Fatalf("stale workspace head error = %v, want workspace live-head conflict", err)
	}
	if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("stale workspace head returned receipt: %+v", receipt)
	}
	assertApprovalAuthorityStoreEmpty(t, root)
}

func TestNewApprovalTransactionStoreRejectsProofAfterWorkspaceEpochAdvance(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-epoch-key", "authority-epoch-command", "authority-epoch-event", "authority-epoch-approval", "authority-epoch-aggregate")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)

	if err := engine.SetEpoch(before.Basis.WorkspaceEpoch + 1); err != nil {
		t.Fatalf("advance authoritative workspace epoch: %v", err)
	}

	receipt, err := NewApprovalTransactionStore(root, engine).Commit(context.Background(), mutation)
	if !errors.Is(err, ErrApprovalTransactionConflict) || !errors.Is(err, ErrApprovalTransactionWorkspaceLiveHeadConflict) {
		t.Fatalf("stale workspace epoch error = %v, want workspace live-head conflict", err)
	}
	if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("stale workspace epoch returned receipt: %+v", receipt)
	}
	assertApprovalAuthorityStoreEmpty(t, root)
}

func TestNewApprovalTransactionStoreHoldsWorkspaceHeadThroughSQLiteCommit(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-lock-key", "authority-lock-command", "authority-lock-event", "authority-lock-approval", "authority-lock-aggregate")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)

	store := NewApprovalTransactionStore(root, engine)
	if store == nil || store.commitExecutor == nil {
		t.Fatal("canonical store did not install a commit executor")
	}
	defaultCommit := store.commitExecutor
	commitEntered := make(chan struct{})
	releaseCommit := make(chan struct{})
	var releaseCommitOnce sync.Once
	releaseApproval := func() { releaseCommitOnce.Do(func() { close(releaseCommit) }) }
	defer releaseApproval()
	store.commitExecutor = func(ctx context.Context, conn *sql.Conn) error {
		close(commitEntered)
		select {
		case <-releaseCommit:
			return defaultCommit(ctx, conn)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	commitDone := make(chan error, 1)
	go func() {
		_, err := store.Commit(context.Background(), mutation)
		commitDone <- err
	}()
	select {
	case err := <-commitDone:
		releaseApproval()
		t.Fatalf("approval commit returned before SQLite commit hook release: %v", err)
	case <-commitEntered:
	case <-time.After(approvalAuthorityCoordinationTimeout):
		releaseApproval()
		t.Fatal("approval commit did not reach SQLite commit hook before timeout")
	}

	editDone := make(chan error, 1)
	go func() {
		_, _, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
			Path: "checkout.go", Content: []byte("func Submit() { println(\"after approval\") }"), DocumentVersion: 2, Source: "ide_versioned",
		})
		editDone <- err
	}()
	select {
	case err := <-editDone:
		releaseApproval()
		t.Fatalf("workspace edit passed while approval held live-head lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	releaseApproval()
	if err := waitForApprovalAuthorityError(t, commitDone, "approval commit"); err != nil {
		t.Fatalf("approval commit after live-head lock release: %v", err)
	}
	if err := waitForApprovalAuthorityError(t, editDone, "workspace edit"); err != nil {
		t.Fatalf("workspace edit after approval commit: %v", err)
	}
}

func TestApprovalAndGenerationPublicationOrdering(t *testing.T) {
	t.Run("approval_first_blocks_generation", func(t *testing.T) {
		root := t.TempDir()
		engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
		access := approvalTransactionTestAccessForRoot(t, root)
		mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-order-approval-first-key", "authority-order-approval-first-command", "authority-order-approval-first-event", "authority-order-approval-first-approval", "authority-order-approval-first-aggregate")
		publishApprovalAuthorityProofWithEngine(t, root, before, engine)
		store := NewApprovalTransactionStore(root, engine)
		if store == nil || store.commitExecutor == nil {
			t.Fatal("canonical approval transaction store is unavailable")
		}
		defaultCommit := store.commitExecutor
		commitEntered := make(chan struct{})
		releaseCommit := make(chan struct{})
		var releaseCommitOnce sync.Once
		releaseApproval := func() { releaseCommitOnce.Do(func() { close(releaseCommit) }) }
		store.commitExecutor = func(ctx context.Context, conn *sql.Conn) error {
			close(commitEntered)
			<-releaseCommit
			return defaultCommit(ctx, conn)
		}
		approvalDone := make(chan approvalAuthorityCommitResult, 1)
		go func() {
			receipt, err := store.Commit(context.Background(), mutation)
			approvalDone <- approvalAuthorityCommitResult{receipt: receipt, err: err}
		}()

		publicationAttempted := make(chan struct{})
		var publicationAttemptOnce sync.Once
		engine.SetLiveHeadAttemptHook(func() { publicationAttemptOnce.Do(func() { close(publicationAttempted) }) })
		defer func() {
			releaseApproval()
			engine.SetLiveHeadAttemptHook(nil)
		}()
		g2 := cloneSemanticMap(before)
		g2.GenerationID = "generation-authority-order-g2"
		g2.MapID = "map-authority-order-g2"
		publicationDone := make(chan error, 1)
		waitForApprovalAuthoritySignal(t, commitEntered, "approval commit SQL boundary")
		go func() {
			publicationDone <- publishApprovalAuthorityProofAtWithEngineResult(root, g2, before.GenerationID, 2, engine)
		}()
		select {
		case <-publicationAttempted:
		case <-time.After(5 * time.Second):
			t.Fatal("generation publication did not reach the live-head boundary")
		}
		select {
		case publicationErr := <-publicationDone:
			releaseApproval()
			t.Fatalf("generation publication completed before approval commit released: %v", publicationErr)
		case <-time.After(100 * time.Millisecond):
		}
		releaseApproval()
		if err := waitForApprovalAuthorityPublication(publicationDone); err != nil {
			t.Fatalf("generation publication after approval commit: %v", err)
		}
		approvalResult := waitForApprovalAuthorityCommit(t, approvalDone, "approval commit")
		if approvalResult.err != nil || approvalResult.receipt.Replayed {
			t.Fatalf("approval commit after generation publication: receipt=%+v err=%v", approvalResult.receipt, approvalResult.err)
		}
		approvalSnapshot, err := store.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("approval snapshot after ordered publication: %v", err)
		}
		if len(approvalSnapshot.Events) != 1 || len(approvalSnapshot.IdempotencyResults) != 1 || len(approvalSnapshot.Outbox) != 1 {
			t.Fatalf("ordered publication approval records = %#v, want one transaction", approvalSnapshot)
		}
		manifest, pointer, err := storage.New(root).ReadValidatedActiveProofManifest()
		if err != nil || manifest == nil || pointer == nil || manifest.GenerationID != g2.GenerationID || pointer.GenerationID != g2.GenerationID {
			t.Fatalf("ordered generation publication proof = manifest=%+v pointer=%+v err=%v", manifest, pointer, err)
		}
	})

	t.Run("generation_first_blocks_approval", func(t *testing.T) {
		root := t.TempDir()
		engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
		access := approvalTransactionTestAccessForRoot(t, root)
		mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-order-generation-first-key", "authority-order-generation-first-command", "authority-order-generation-first-event", "authority-order-generation-first-approval", "authority-order-generation-first-aggregate")
		publishApprovalAuthorityProofWithEngine(t, root, before, engine)
		store := NewApprovalTransactionStore(root, engine)
		if store == nil {
			t.Fatal("canonical approval transaction store is unavailable")
		}
		publicationEntered := make(chan struct{})
		releasePublication := make(chan struct{})
		var releasePublicationOnce sync.Once
		releaseGeneration := func() { releasePublicationOnce.Do(func() { close(releasePublication) }) }
		publicationDone := make(chan error, 1)
		g2 := cloneSemanticMap(before)
		g2.GenerationID = "generation-authority-order-g2-first"
		g2.MapID = "map-authority-order-g2-first"
		go func() {
			publicationDone <- publishApprovalAuthorityProofAtWithCommitAndLiveHeadResult(root, g2, before.GenerationID, 2, before.ValidatedAgainstSnapshotID, func(expected string, commit func() error) error {
				return engine.WithLiveHead(expected, func() error {
					close(publicationEntered)
					<-releasePublication
					return commit()
				})
			})
		}()
		defer releaseGeneration()
		select {
		case <-publicationEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("generation publication did not acquire the live-head lock")
		}

		approvalWaiting := make(chan struct{})
		var approvalWaitingOnce sync.Once
		engine.SetLiveHeadAttemptHook(func() { approvalWaitingOnce.Do(func() { close(approvalWaiting) }) })
		defer engine.SetLiveHeadAttemptHook(nil)
		approvalDone := make(chan approvalAuthorityCommitResult, 1)
		go func() {
			receipt, err := store.Commit(context.Background(), mutation)
			approvalDone <- approvalAuthorityCommitResult{receipt: receipt, err: err}
		}()
		select {
		case result := <-approvalDone:
			releaseGeneration()
			publicationErr := waitForApprovalAuthorityPublication(publicationDone)
			t.Fatalf("approval completed while generation held live-head lock: receipt=%+v err=%v publicationErr=%v", result.receipt, result.err, publicationErr)
		case <-approvalWaiting:
		case <-time.After(approvalAuthorityCoordinationTimeout):
			releaseGeneration()
			publicationErr := waitForApprovalAuthorityPublication(publicationDone)
			t.Fatalf("approval did not reach the live-head wait before timeout; publicationErr=%v", publicationErr)
		}
		releaseGeneration()
		if err := waitForApprovalAuthorityPublication(publicationDone); err != nil {
			t.Fatalf("generation publication release: %v", err)
		}
		result := waitForApprovalAuthorityCommit(t, approvalDone, "approval commit after generation publication")
		if !errors.Is(result.err, ErrApprovalTransactionConflict) {
			t.Fatalf("approval after generation publication error = %v, want typed conflict", result.err)
		}
		if !reflect.DeepEqual(result.receipt, ApprovalCommitReceipt{}) {
			t.Fatalf("approval after generation publication returned receipt: %+v", result.receipt)
		}
		var transactionErr *ApprovalTransactionError
		if !errors.As(result.err, &transactionErr) || transactionErr.CurrentState != "none" || transactionErr.CurrentVersion != 0 {
			t.Fatalf("approval conflict current state = %#v, want none/0", transactionErr)
		}
		assertApprovalAuthorityStoreEmpty(t, root)
		manifest, pointer, err := storage.New(root).ReadValidatedActiveProofManifest()
		if err != nil || manifest == nil || pointer == nil || manifest.GenerationID != g2.GenerationID || pointer.GenerationID != g2.GenerationID {
			t.Fatalf("generation-first proof = manifest=%+v pointer=%+v err=%v", manifest, pointer, err)
		}
	})
}

type approvalAuthorityCommitResult struct {
	receipt ApprovalCommitReceipt
	err     error
}

func waitForApprovalAuthoritySignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(approvalAuthorityCoordinationTimeout):
		t.Fatalf("%s did not arrive before timeout", label)
	}
}

func waitForApprovalAuthorityCommit(t *testing.T, results <-chan approvalAuthorityCommitResult, label string) approvalAuthorityCommitResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(approvalAuthorityCoordinationTimeout):
		t.Fatalf("%s did not complete before timeout", label)
		return approvalAuthorityCommitResult{}
	}
}

func waitForApprovalAuthorityError(t *testing.T, results <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-results:
		return err
	case <-time.After(approvalAuthorityCoordinationTimeout):
		t.Fatalf("%s did not complete before timeout", label)
		return errors.New("coordination timeout")
	}
}

const approvalAuthorityCoordinationTimeout = 5 * time.Second

func waitForApprovalAuthorityPublication(publicationDone <-chan error) error {
	select {
	case err := <-publicationDone:
		return err
	case <-time.After(approvalAuthorityCoordinationTimeout):
		return errors.New("generation publication timed out")
	}
}

func TestNewApprovalTransactionStoreAllowsComputedSnapshotDistinctFromLiveHead(t *testing.T) {
	root := t.TempDir()
	engine, computedSnapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, computedSnapshot, "authority-distinct-snapshot-key", "authority-distinct-snapshot-command", "authority-distinct-snapshot-event", "authority-distinct-snapshot-approval", "authority-distinct-snapshot-aggregate")

	_, liveHead, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "checkout.go", Content: []byte("func Submit() { println(\"live\") }"), DocumentVersion: 2, Source: "ide_versioned",
	})
	if err != nil {
		t.Fatalf("advance live head for distinct snapshot proof: %v", err)
	}
	publishApprovalAuthorityProofAtWithEngineLiveHead(t, root, before, "", 1, engine, liveHead.SnapshotID)
	var identity storage.ValidatedActiveApprovalIdentity
	if err := storage.New(root).WithValidatedActiveApprovalIdentity(func(found storage.ValidatedActiveApprovalIdentity) error {
		identity = found
		return nil
	}); err != nil {
		t.Fatalf("validate distinct computed/live proof: %v", err)
	}
	if identity.ValidatedSnapshotID != before.ValidatedAgainstSnapshotID || identity.ExpectedLiveHeadSnapshotID != liveHead.SnapshotID || identity.ValidatedSnapshotID == identity.ExpectedLiveHeadSnapshotID {
		t.Fatalf("distinct proof identity = %+v, want computed %q and live %q", identity, before.ValidatedAgainstSnapshotID, liveHead.SnapshotID)
	}

	receipt, err := NewApprovalTransactionStore(root, engine).Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("commit with distinct computed/live snapshots: %v", err)
	}
	if receipt.Replayed || receipt.Event.EventID != "authority-distinct-snapshot-event" {
		t.Fatalf("distinct computed/live receipt = %+v", receipt)
	}
}

func assertApprovalAuthorityStoreEmpty(t *testing.T, root string) {
	t.Helper()
	snapshot, err := newFileApprovalTransactionStoreForTest(filepath.Join(root, ".codeflow", "approval-transactions")).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read rejected approval transaction state: %v", err)
	}
	if len(snapshot.Events) != 0 || len(snapshot.Aggregates) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 {
		t.Fatalf("rejected approval published records: %+v", snapshot)
	}
}

func TestActiveApprovalTransactionAuthorityRechecksContextAfterPublicationLock(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-cancel-key", "authority-cancel-command", "authority-cancel-event", "authority-cancel-approval", "authority-cancel-aggregate")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)

	command := mutation.Command()
	target := approvalTransactionTarget{
		WorkspaceID:         command.WorkspaceID,
		ProposalID:          command.ProposalID,
		EvidencePackID:      command.EvidencePackID,
		ComputedBasisID:     command.ComputedBasisID,
		GenerationID:        command.GenerationID,
		IntentRevision:      command.IntentRevision,
		ValidatedSnapshotID: before.ValidatedAgainstSnapshotID,
		WorkspaceEpoch:      before.Basis.WorkspaceEpoch,
		MapID:               before.MapID,
		TaskID:              before.Task.TaskID,
	}
	activeStorage := storage.New(root)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releasePublication := func() { releaseOnce.Do(func() { close(release) }) }
	defer releasePublication()
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- activeStorage.WithValidatedActiveApprovalIdentity(func(storage.ValidatedActiveApprovalIdentity) error {
			close(entered)
			<-release
			return nil
		})
	}()
	waitForApprovalAuthoritySignal(t, entered, "active-proof publication lock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	commitCalls := 0
	authorityDone := make(chan error, 1)
	go func() {
		authorityDone <- activeApprovalTransactionAuthority(activeStorage, engine, access.Workspace().WorkspaceID())(ctx, target, nil, func() error {
			commitCalls++
			return nil
		})
	}()
	select {
	case err := <-authorityDone:
		releasePublication()
		t.Fatalf("authority returned while publication lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-authorityDone:
		releasePublication()
		t.Fatalf("authority returned before publication lock release: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releasePublication()
	if err := waitForApprovalAuthorityError(t, lockDone, "active-proof publication lock callback"); err != nil {
		t.Fatalf("release publication lock: %v", err)
	}
	err := waitForApprovalAuthorityError(t, authorityDone, "cancelled authority")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled authority error = %v, want context cancellation", err)
	}
	if commitCalls != 0 {
		t.Fatalf("cancelled authority invoked commit callback %d times", commitCalls)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".codeflow", "approval-transactions")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cancelled authority created approval artifacts, stat error = %v", statErr)
	}
}

func TestNewApprovalTransactionStoreCancellationWhileWorkspaceHeadLockWaits(t *testing.T) {
	root := t.TempDir()
	engine, snapshot := approvalAuthorityWorkspaceEngine(t, root)
	access := approvalTransactionTestAccessForRoot(t, root)
	mutation, before := approvalAuthorityMutationWithSnapshot(t, access, snapshot, "authority-edit-cancel-key", "authority-edit-cancel-command", "authority-edit-cancel-event", "authority-edit-cancel-approval", "authority-edit-cancel-aggregate")
	publishApprovalAuthorityProofWithEngine(t, root, before, engine)
	store := NewApprovalTransactionStore(root, engine)
	if store == nil {
		t.Fatal("canonical approval transaction store is nil")
	}

	editPersistence := &approvalAuthorityBlockingPersistence{entered: make(chan struct{}), release: make(chan struct{})}
	if err := engine.SetPersistence(editPersistence); err != nil {
		t.Fatalf("install blocking workspace persistence: %v", err)
	}
	var releaseEditOnce sync.Once
	releaseEdit := func() { releaseEditOnce.Do(func() { close(editPersistence.release) }) }
	defer releaseEdit()
	authorityWaiting := make(chan struct{})
	var authorityWaitingOnce sync.Once
	engine.SetLiveHeadAttemptHook(func() { authorityWaitingOnce.Do(func() { close(authorityWaiting) }) })
	defer engine.SetLiveHeadAttemptHook(nil)

	editDone := make(chan error, 1)
	go func() {
		_, _, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
			Path: "checkout.go", Content: []byte("func Submit() { println(\"cancelled\") }"), DocumentVersion: 2, Source: "ide_versioned",
		})
		editDone <- err
	}()
	waitForApprovalAuthoritySignal(t, editPersistence.entered, "workspace edit persistence boundary")

	ctx, cancel := context.WithCancel(context.Background())
	commitDone := make(chan approvalAuthorityCommitResult, 1)
	go func() {
		receipt, err := store.Commit(ctx, mutation)
		commitDone <- approvalAuthorityCommitResult{receipt: receipt, err: err}
	}()
	select {
	case result := <-commitDone:
		releaseEdit()
		editErr := waitForApprovalAuthorityError(t, editDone, "workspace edit after premature approval")
		t.Fatalf("approval returned while workspace edit owned engine lock: receipt=%+v err=%v editErr=%v", result.receipt, result.err, editErr)
	case <-authorityWaiting:
	case <-time.After(approvalAuthorityCoordinationTimeout):
		releaseEdit()
		editErr := waitForApprovalAuthorityError(t, editDone, "workspace edit after live-head wait timeout")
		commitResult := waitForApprovalAuthorityCommit(t, commitDone, "approval after live-head wait timeout")
		t.Fatalf("approval did not reach the live-head wait before timeout: receipt=%+v err=%v editErr=%v", commitResult.receipt, commitResult.err, editErr)
	}

	cancel()
	releaseEdit()
	if err := waitForApprovalAuthorityError(t, editDone, "workspace edit"); err != nil {
		t.Fatalf("blocked workspace edit: %v", err)
	}
	result := waitForApprovalAuthorityCommit(t, commitDone, "cancelled approval")
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancelled approval after live-head changed = %v, want context cancellation", result.err)
	}
	if !reflect.DeepEqual(result.receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("cancelled approval returned receipt: %+v", result.receipt)
	}
	assertApprovalAuthorityStoreEmpty(t, root)
}

type approvalAuthorityBlockingPersistence struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *approvalAuthorityBlockingPersistence) WriteAtomic(path string, data []byte) error {
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return os.WriteFile(path, data, 0o600)
}

func (p *approvalAuthorityBlockingPersistence) Remove(path string) error {
	return os.Remove(path)
}

func approvalAuthorityMutationWithAccess(t *testing.T, access ApprovalAccess, idempotencyKey, commandID, eventID, approvalID, aggregateID string) (*ValidatedApprovalMutation, *SemanticMapIR) {
	t.Helper()
	basis := strings.Repeat("a", 64)
	snapshot, err := protocol.NewSnapshot(1, map[string]string{"checkout.go": "func Submit() {}"}, basis)
	if err != nil {
		t.Fatalf("approval authority snapshot: %v", err)
	}
	snapshot.RepositoryID = "repo-authority"
	snapshot.WorktreeID = "worktree-authority"
	return approvalAuthorityMutationWithSnapshot(t, access, snapshot, idempotencyKey, commandID, eventID, approvalID, aggregateID)
}

func approvalAuthorityWorkspaceEngine(t *testing.T, root string) (*workspace.SnapshotEngine, protocol.Snapshot) {
	t.Helper()
	engine, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("approval authority workspace engine: %v", err)
	}
	const source = "func Submit() {}"
	_, workspaceSnapshot, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "checkout.go", Content: []byte(source), DocumentVersion: 1, Source: "ide_versioned"})
	if err != nil {
		t.Fatalf("approval authority workspace snapshot: %v", err)
	}
	return engine, protocol.Snapshot{
		SnapshotID: workspaceSnapshot.SnapshotID, ComputedBasisID: workspaceSnapshot.ComputedBasisID,
		WorkspaceEpoch: workspaceSnapshot.WorkspaceEpoch, RootTreeID: workspaceSnapshot.RootTreeID,
		ConfigurationFingerprint: workspaceSnapshot.ConfigurationFingerprint, DependencyFingerprint: workspaceSnapshot.DependencyFingerprint,
		RepositoryID: "repo-authority", WorktreeID: "worktree-authority",
		Files: map[string]string{"checkout.go": source}, ContentOverlay: map[string]string{"checkout.go": source},
	}
}

func approvalAuthorityMutationWithSnapshot(t *testing.T, access ApprovalAccess, snapshot protocol.Snapshot, idempotencyKey, commandID, eventID, approvalID, aggregateID string) (*ValidatedApprovalMutation, *SemanticMapIR) {
	t.Helper()
	if snapshot.RepositoryID == "" {
		snapshot.RepositoryID = "repo-authority"
	}
	if snapshot.WorktreeID == "" {
		snapshot.WorktreeID = "worktree-authority"
	}
	basis := snapshot.ComputedBasisID
	hash := sha256.Sum256([]byte("func Submit() {}"))
	anchor := slicing.Anchor{RepoRelativePath: "checkout.go", ByteRange: [2]int{0, len("func Submit() {}")}, FileHash: hex.EncodeToString(hash[:]), EnclosingSymbolPath: "Submit"}
	before := testEvidenceMap(snapshot, anchor, "e-authority", "step-authority")
	before.MapID = "map-authority"
	before.GenerationID = "generation-authority"
	before.Freshness = "current"
	before.PublicationKind = "initial"
	before.Settlement = "pending"
	before.EnrichmentStatus = "available"
	before.Quality.Stage = "Q3"
	before.Quality.UnresolvedCriticalCount = 0
	before.Quality.ConflictingCriticalCount = 0
	before.Basis = MapBasisContext{RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, WorkspaceEpoch: snapshot.WorkspaceEpoch, ComputedWorkspaceSnapshotID: snapshot.SnapshotID, ComputedBasisID: basis, SnapshotTreeID: snapshot.RootTreeID, DependencyFingerprint: "dependency-authority", AnalysisReadSetID: "read-set-authority", CausalObservationClosureID: "closure-authority"}
	before.Task = MapTaskContext{TaskID: "task-authority", IntentRevision: 1, Mode: "feature"}
	before.Edges = []SemanticEdge{}
	before.Unknowns = []fusion.Unknown{}
	before.Coverage = &CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{"outside-scope"}}
	before.Authority = "candidate"
	before.Steps[0].StructuralIdentity = "Submit"
	before.Steps[0].Ordinal = 1
	before.Steps[0].Anchor.SpanHash = before.Steps[0].Anchor.FileHash
	before.Evidence[0].DocumentRevisionID = "revision-authority"
	before.Evidence[0].LineRange = [2]int{1, 1}
	request := bindCurrentEvidenceRequest(snapshot, before, "step-authority")
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatalf("approval authority evidence pack: %v", err)
	}
	digests, err := CanonicalQ3Digests(before)
	if err != nil {
		t.Fatalf("approval authority Q3 digests: %v", err)
	}
	proposal := &ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-authority",
		ComputedBasisID: basis, GenerationID: before.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-authority", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "authority-model", ModelRevision: "r1", PromptRevision: "prompt-authority", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-authority"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation, AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	}
	stored := &StoredProposal{WorkspaceID: access.Workspace().WorkspaceID(), Proposal: proposal, Pack: pack}
	draft := approvalCommandTestDraft()
	draft.CommandID = commandID
	draft.ProposalID = proposal.ProposalID
	draft.EvidencePackID = pack.EvidencePackID
	draft.ComputedBasisID = basis
	draft.GenerationID = before.GenerationID
	draft.IntentRevision = int64(before.Task.IntentRevision)
	draft.Decision = "approve"
	draft.IdempotencyKey = idempotencyKey
	command, err := BindApprovalCommandV2(access, draft)
	if err != nil {
		t.Fatalf("approval authority command: %v", err)
	}
	genesis, err := NewApprovalGenesisAggregateV2(command, aggregateID, "genesis-"+aggregateID)
	if err != nil {
		t.Fatalf("approval authority genesis: %v", err)
	}
	metadata := lifecycleTestMetadata(aggregateID, eventID, approvalID)
	metadata.StoredProposalText = proposal.ProposedTitle
	event, aggregate, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("approval authority reduction: %v", err)
	}
	after := cloneSemanticMap(before)
	after.EnrichmentStatus = "available"
	mutation, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("approval authority mutation: %v", err)
	}
	return mutation, before
}

func approvalTransactionTestAccessForRoot(t *testing.T, root string) ApprovalAccess {
	t.Helper()
	authorizer := NewApprovalWorkspaceAuthorizer(root)
	access, err := NewApprovalAccessGate(NewLocalProcessApprovalAuthenticator(), authorizer).AuthenticateAndAuthorize(context.Background(), authorizer.WorkspaceID(), root)
	if err != nil {
		t.Fatalf("create root-bound approval access: %v", err)
	}
	return access
}

func publishApprovalAuthorityProof(t *testing.T, root string, mapIR *SemanticMapIR) {
	publishApprovalAuthorityProofAt(t, root, mapIR, "", 1)
}

func publishApprovalAuthorityProofAt(t *testing.T, root string, mapIR *SemanticMapIR, previousGeneration string, sequence int) {
	publishApprovalAuthorityProofAtWithCommit(t, root, mapIR, previousGeneration, sequence, func(expected string, commit func() error) error {
		if expected != mapIR.ValidatedAgainstSnapshotID {
			return storage.ErrCASConflict
		}
		return commit()
	})
}

func publishApprovalAuthorityProofWithEngine(t *testing.T, root string, mapIR *SemanticMapIR, engine *workspace.SnapshotEngine) {
	publishApprovalAuthorityProofAtWithEngine(t, root, mapIR, "", 1, engine)
}

func publishApprovalAuthorityProofAtWithEngine(t *testing.T, root string, mapIR *SemanticMapIR, previousGeneration string, sequence int, engine *workspace.SnapshotEngine) {
	if engine == nil {
		t.Fatal("approval authority workspace engine is nil")
	}
	publishApprovalAuthorityProofAtWithEngineLiveHead(t, root, mapIR, previousGeneration, sequence, engine, mapIR.ValidatedAgainstSnapshotID)
}

func publishApprovalAuthorityProofAtWithEngineResult(root string, mapIR *SemanticMapIR, previousGeneration string, sequence int, engine *workspace.SnapshotEngine) error {
	if engine == nil {
		return errors.New("approval authority workspace engine is nil")
	}
	if mapIR == nil || strings.TrimSpace(mapIR.ValidatedAgainstSnapshotID) == "" {
		return errors.New("approval authority live-head snapshot ID is empty")
	}
	return publishApprovalAuthorityProofAtWithCommitAndLiveHeadResult(root, mapIR, previousGeneration, sequence, mapIR.ValidatedAgainstSnapshotID, engine.WithLiveHead)
}

func publishApprovalAuthorityProofAtWithCommit(t *testing.T, root string, mapIR *SemanticMapIR, previousGeneration string, sequence int, liveHeadCommit func(string, func() error) error) {
	publishApprovalAuthorityProofAtWithCommitAndLiveHead(t, root, mapIR, previousGeneration, sequence, mapIR.ValidatedAgainstSnapshotID, liveHeadCommit)
}

func publishApprovalAuthorityProofAtWithEngineLiveHead(t *testing.T, root string, mapIR *SemanticMapIR, previousGeneration string, sequence int, engine *workspace.SnapshotEngine, liveHeadSnapshotID string) {
	t.Helper()
	if engine == nil {
		t.Fatal("approval authority workspace engine is nil")
	}
	if liveHeadSnapshotID == "" {
		t.Fatal("approval authority live-head snapshot ID is empty")
	}
	publishApprovalAuthorityProofAtWithCommitAndLiveHead(t, root, mapIR, previousGeneration, sequence, liveHeadSnapshotID, engine.WithLiveHead)
}

func publishApprovalAuthorityProofAtWithCommitAndLiveHead(t *testing.T, root string, mapIR *SemanticMapIR, previousGeneration string, sequence int, liveHeadSnapshotID string, liveHeadCommit func(string, func() error) error) {
	t.Helper()
	if err := publishApprovalAuthorityProofAtWithCommitAndLiveHeadResult(root, mapIR, previousGeneration, sequence, liveHeadSnapshotID, liveHeadCommit); err != nil {
		t.Fatalf("publish approval authority proof: %v", err)
	}
}

func publishApprovalAuthorityProofAtWithCommitAndLiveHeadResult(root string, mapIR *SemanticMapIR, previousGeneration string, sequence int, liveHeadSnapshotID string, liveHeadCommit func(string, func() error) error) error {
	if mapIR == nil {
		return errors.New("approval authority map is nil")
	}
	if strings.TrimSpace(liveHeadSnapshotID) == "" {
		return errors.New("approval authority live-head snapshot ID is empty")
	}
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		return fmt.Errorf("initialize approval authority storage: %w", err)
	}
	basis := mapIR.ComputedBasisID
	snapshotID := mapIR.ValidatedAgainstSnapshotID
	generationID := mapIR.GenerationID
	readSetID := mapIR.Basis.AnalysisReadSetID
	closureID := mapIR.Basis.CausalObservationClosureID
	if basis == "" || snapshotID == "" || generationID == "" || readSetID == "" || closureID == "" {
		return errors.New("approval authority map identity is incomplete")
	}
	observations := []evidence.Observation{
		{Kind: "negative_lookup", Path: "test/missing.go", Measured: true},
		{Kind: "membership", Path: "test", Measured: true},
		{Kind: "dependency_frontier", Path: "test/go.mod", Measured: true},
	}
	readSet := evidence.AnalysisReadSet{
		SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion,
		ReadSetID: readSetID, ComputedBasisID: basis, WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch,
		Documents: []evidence.ReadDocument{}, NegativeObservations: observations[:1],
		MembershipObservations: observations[1:2], DependencyFrontiers: observations[2:],
	}
	closureDigest := strings.Repeat("c", 64)
	closure := evidence.ObservationClosure{
		SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion,
		ClosureID: closureID, AnalysisReadSetID: readSetID, ComputedBasisID: basis, WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch,
		Status: "closed", NegativeObservations: observations[:1], MembershipObservations: observations[1:2], DependencyFrontiers: observations[2:],
		RequiredObservations: []string{"negative_lookup", "membership", "dependency_frontier"},
		MeasuredObservations: []string{"negative_lookup", "membership", "dependency_frontier"}, ClosureDigest: closureDigest,
	}
	capability := evidence.CapabilityProfile{Adapter: "test-adapter", AdapterVersion: "adapter-1", AnalyzerRevision: "analyzer-1", Features: []string{"snapshot_bytes"}}
	capabilityBytes, err := json.Marshal(capability)
	if err != nil {
		return fmt.Errorf("marshal approval authority capability: %w", err)
	}
	capabilityHash := sha256.Sum256(capabilityBytes)
	analyzer := evidence.Result{
		SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion,
		RequestID: "approval-authority-request", Operation: "detect", AdapterVersion: capability.AdapterVersion,
		AnalyzerRevision: capability.AnalyzerRevision, WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch, ComputedBasisID: basis,
		SnapshotID: snapshotID, SnapshotTreeDigest: mapIR.Basis.SnapshotTreeID, DependencyFingerprint: mapIR.Basis.DependencyFingerprint,
		ReadSet: readSet, Closure: closure, Capability: capability,
		Coverage:    evidence.Coverage{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{"outside-scope"}, Measured: true},
		Diagnostics: []evidence.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`),
	}
	readSetBytes, err := json.Marshal(readSet)
	if err != nil {
		return fmt.Errorf("marshal approval authority read set: %w", err)
	}
	closureBytes, err := json.Marshal(closure)
	if err != nil {
		return fmt.Errorf("marshal approval authority closure: %w", err)
	}
	analyzerBytes, err := json.Marshal(analyzer)
	if err != nil {
		return fmt.Errorf("marshal approval authority analyzer result: %w", err)
	}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		return fmt.Errorf("marshal approval authority map: %w", err)
	}
	projectionBytes, err := json.Marshal(map[string]any{
		"schemaId": "https://codeflow.local/schemas/rflsc.flowview-projection.v2.schema.json", "schemaVersion": 2,
		"projectionId": "projection-" + generationID, "generationId": generationID, "computedBasisId": basis, "mode": "feature",
		"displayBudget":   map[string]any{"targetMin": 1, "targetMax": 1, "enforcement": "soft"},
		"visibleStepRefs": []any{}, "preservedStepRefs": []any{}, "foldedSubflows": []any{}, "unknownBoundaryRefs": []any{},
	})
	if err != nil {
		return fmt.Errorf("marshal approval authority projection: %w", err)
	}
	mapRef := storage.ArtifactCASRef(mapBytes)
	projectionRef := storage.ArtifactCASRef(projectionBytes)
	readSetRef := storage.ArtifactCASRef(readSetBytes)
	closureRef := storage.ArtifactCASRef(closureBytes)
	analyzerRef := storage.ArtifactCASRef(analyzerBytes)
	const (
		manifestSchema = "https://codeflow.local/schemas/rflsc.generation-proof-manifest.v2.schema.json"
		pointerSchema  = "https://codeflow.local/schemas/rflsc.active-pointer.v2.schema.json"
	)
	previous := previousGeneration
	queryHash := strings.Repeat("b", 64)
	now := time.Unix(1, 0).UTC()
	manifest := &storage.GenerationProofManifest{
		SchemaID: manifestSchema, SchemaVersion: 2, ProofID: "proof-" + generationID, GenerationID: generationID, ComputedBasisID: basis,
		ComputedSnapshotID: snapshotID, ValidatedAgainstSnapshotID: liveHeadSnapshotID, TaskIntentRevision: mapIR.Task.IntentRevision,
		NormalizedQueryHash: queryHash, AnalysisReadSetID: readSetID, CausalObservationClosureID: closureID, CausalObservationClosureDigest: closureDigest,
		CapabilityProfileDigest: hex.EncodeToString(capabilityHash[:]), WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch,
		CurrentPublication:         storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"},
		SettlementEvaluation:       storage.SettlementEvaluation{Gate: mapIR.Settlement, BlockingObligationRefs: []string{}},
		ArtifactRefs:               storage.ArtifactRefs{SemanticMap: mapRef, Projection: projectionRef, AnalysisReadSet: readSetRef, ObservationClosure: closureRef, AnalyzerResult: analyzerRef},
		ExpectedLiveHeadSnapshotID: liveHeadSnapshotID, ExpectedPreviousGenerationID: &previous, PublishedAt: now,
	}
	pointer := &storage.ActivePointer{
		SchemaID: pointerSchema, SchemaVersion: 2, GenerationID: generationID, ComputedBasisID: basis,
		ValidatedAgainstSnapshotID: liveHeadSnapshotID, ExpectedLiveHeadSnapshotID: liveHeadSnapshotID, ExpectedPreviousGenerationID: &previous,
		WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch, TaskIntentRevision: mapIR.Task.IntentRevision, NormalizedQueryHash: queryHash,
		FlowCount: 1, RepositoryID: mapIR.Basis.RepositoryID, WorktreeID: mapIR.Basis.WorktreeID, TaskID: mapIR.Task.TaskID, PublishedAt: now,
	}
	eventBytes, err := json.Marshal(map[string]any{
		"schemaId": "https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json", "schemaVersion": 2, "streamId": "approval-authority", "sequence": sequence,
		"eventId": fmt.Sprintf("approval-authority-publication-%d", sequence), "eventType": "generation.published", "occurredAt": now,
		"computedBasisId": basis, "validatedAgainstSnapshotId": liveHeadSnapshotID, "generationId": generationID,
	})
	if err != nil {
		return fmt.Errorf("marshal approval authority event: %w", err)
	}
	_, err = st.PublishGeneration(storage.PublicationTransaction{
		Manifest: manifest, Pointer: pointer, Event: eventBytes,
		Artifacts:                  map[string][]byte{mapRef: mapBytes, projectionRef: projectionBytes, readSetRef: readSetBytes, closureRef: closureBytes, analyzerRef: analyzerBytes},
		ExpectedLiveHeadSnapshotID: liveHeadSnapshotID, ActualLiveHeadSnapshotID: liveHeadSnapshotID, ExpectedPreviousGenerationID: previous,
		LiveHeadCommit: liveHeadCommit,
	})
	if err != nil {
		return fmt.Errorf("publish approval authority proof: %w", err)
	}
	return nil
}
