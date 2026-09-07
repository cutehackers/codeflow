package semantic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

type approvalHistorySnapshotFake struct {
	snapshot ApprovalTransactionSnapshot
	err      error
}

func TestParseApprovalHistoryQueryEnvelopeRejectsTrailingAndInvalidUTF8(t *testing.T) {
	maxID := strings.Repeat("é", 256)
	validMax, err := json.Marshal(map[string]string{
		"proposalId":     maxID,
		"evidencePackId": maxID,
	})
	if err != nil {
		t.Fatalf("marshal maximum Unicode query: %v", err)
	}
	parsed, err := ParseApprovalHistoryQueryEnvelopeJSON(validMax)
	if err != nil {
		t.Fatalf("maximum Unicode query rejected: %v", err)
	}
	if parsed.Query.ProposalID != maxID || parsed.Query.EvidencePackID != maxID || parsed.Target != nil || parsed.Token != nil {
		t.Fatalf("maximum Unicode query parsed as %+v, want exact required IDs only", parsed)
	}

	valid := []byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1"}`)
	trailingCases := []struct {
		name string
		data []byte
	}{
		{name: "second object", data: append(append([]byte(nil), valid...), []byte(`{}`)...)},
		{name: "second scalar", data: append(append([]byte(nil), valid...), []byte(` 0`)...)},
		{name: "whitespace and trailing document", data: append(append([]byte(nil), valid...), []byte(" \n{}")...)},
	}
	for _, tc := range trailingCases {
		t.Run(tc.name, func(t *testing.T) {
			assertApprovalHistoryQueryEnvelopeInvalid(t, tc.data)
		})
	}

	invalidUTF8Cases := []struct {
		name string
		data []byte
	}{
		{name: "proposalId string", data: append(append([]byte(`{"proposalId":"proposal-`), 0xff), []byte(`-1","evidencePackId":"pack-1"}`)...)},
		{name: "evidencePackId string", data: append(append([]byte(`{"proposalId":"proposal-1","evidencePackId":"pack-`), 0xff), []byte(`-1"}`)...)},
		{name: "target string", data: append(append([]byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1","target":"repo-`), 0xff), []byte(`-1"}`)...)},
		{name: "token string", data: append(append([]byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1","token":"token-`), 0xff), []byte(`-1"}`)...)},
	}
	for _, tc := range invalidUTF8Cases {
		t.Run(tc.name, func(t *testing.T) {
			assertApprovalHistoryQueryEnvelopeInvalid(t, tc.data)
		})
	}
}

func assertApprovalHistoryQueryEnvelopeInvalid(t *testing.T, data []byte) {
	t.Helper()
	parsed, err := ParseApprovalHistoryQueryEnvelopeJSON(data)
	if err == nil || !errors.Is(err, ErrApprovalHistoryInvalid) || errors.Is(err, ErrApprovalHistoryUnavailable) || !reflect.DeepEqual(parsed, ApprovalHistoryQueryEnvelope{}) || err.Error() != ErrApprovalHistoryInvalid.Error() {
		t.Fatalf("query envelope = %+v err=%v, want typed bounded invalid error and zero envelope", parsed, err)
	}
}

func TestApprovalHistoryQueryCanonicalJSONBoundary(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-canonical-command", "history-canonical-key", "approve", "none", 0, nil, nil))

	result, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err != nil {
		t.Fatalf("query canonical approval history: %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal canonical approval history: %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode canonical approval history: %v", err)
	}
	wantKeys := map[string]struct{}{
		"schemaId": {}, "schemaVersion": {}, "target": {}, "events": {}, "aggregate": {}, "freshness": {},
	}
	if len(document) != len(wantKeys) {
		t.Fatalf("canonical approval history keys = %v, want exactly six keys", document)
	}
	for key := range wantKeys {
		if _, ok := document[key]; !ok {
			t.Fatalf("canonical approval history is missing key %q", key)
		}
	}
	var schemaID string
	if err := json.Unmarshal(document["schemaId"], &schemaID); err != nil {
		t.Fatalf("decode approval history schemaId: %v", err)
	}
	if schemaID != ApprovalHistoryV1SchemaID {
		t.Fatalf("approval history schemaId = %q, want %q", schemaID, ApprovalHistoryV1SchemaID)
	}
	var schemaVersion int
	if err := json.Unmarshal(document["schemaVersion"], &schemaVersion); err != nil {
		t.Fatalf("decode approval history schemaVersion: %v", err)
	}
	if schemaVersion != ApprovalHistoryV1SchemaVersion {
		t.Fatalf("approval history schemaVersion = %d, want %d", schemaVersion, ApprovalHistoryV1SchemaVersion)
	}
	for key := range map[string]struct{}{
		"workspaceId": {}, "proposalId": {}, "evidencePackId": {}, "history": {}, "state": {}, "version": {}, "activeApprovalId": {},
	} {
		if _, ok := document[key]; ok {
			t.Fatalf("non-canonical approval history alias %q is present", key)
		}
	}
	if err := contractharness.ValidateVS09Contract(ApprovalHistoryV1SchemaID, raw); err != nil {
		t.Fatalf("canonical approval history semantic validation: %v", err)
	}
}

func (f approvalHistorySnapshotFake) Snapshot(context.Context) (ApprovalTransactionSnapshot, error) {
	return f.snapshot, f.err
}

type approvalHistoryCountingReader struct {
	calls atomic.Int64
	err   error
}

func (r *approvalHistoryCountingReader) Snapshot(context.Context) (ApprovalTransactionSnapshot, error) {
	r.calls.Add(1)
	return ApprovalTransactionSnapshot{}, r.err
}

func TestApprovalHistoryQueryProjectsOrderedAggregateAndFreshnessReadOnly(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	draft := approvalExecutionDraft(fixture, "history-query-command", "history-query-key", "approve", "none", 0, nil, nil)
	first := mustExecuteApproval(t, service, fixture, draft)

	query := ApprovalHistoryQuery{ProposalID: fixture.proposalID, EvidencePackID: fixture.packID}
	initial, err := service.QueryApprovalHistory(context.Background(), fixture.access, query)
	if err != nil {
		t.Fatalf("query approval history before live-head advance: %v", err)
	}
	if initial.Freshness != approvalExecutionFreshnessCurrent || initial.Aggregate.State != "active" || initial.Aggregate.Version != 1 || initial.Aggregate.ActiveApprovalID != first.Receipt.Event.ApprovalID {
		t.Fatalf("initial history projection = %+v", initial)
	}
	if len(initial.Events) != 1 || initial.Events[0].EventID != first.Receipt.Event.EventID || len(initial.Aggregate.History) != 2 || initial.Aggregate.History[1].ApprovalID != first.Receipt.Event.ApprovalID {
		t.Fatalf("initial history ordering = %+v", initial)
	}
	if initial.Target.WorkspaceID != fixture.access.Workspace().WorkspaceID() || initial.Target.ProposalID != fixture.proposalID || initial.Target.EvidencePackID != fixture.packID || initial.Target.ComputedBasisID != fixture.before.ComputedBasisID || initial.Target.GenerationID != fixture.before.GenerationID || initial.Target.IntentRevision != int64(fixture.before.Task.IntentRevision) || initial.Target.ValidatedSnapshotID != fixture.before.ValidatedAgainstSnapshotID || initial.Target.WorkspaceEpoch != fixture.before.Basis.WorkspaceEpoch || initial.Target.MapID != fixture.before.MapID || initial.Target.TaskID != fixture.before.Task.TaskID {
		t.Fatalf("initial persisted target identity = %+v", initial.Target)
	}

	transactionPath := filepath.Join(fixture.root, ".codeflow", "approval-transactions", approvalTransactionDatabaseName)
	pointerPath := filepath.Join(storage.New(fixture.root).BaseDir(), "active-pointer.json")
	databaseBefore, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database before live-head advance: %v", err)
	}
	pointerBefore, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read active pointer before live-head advance: %v", err)
	}
	transactionBefore, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read transaction snapshot before live-head advance: %v", err)
	}
	proofBefore, err := storage.New(fixture.root).ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read active proof before live-head advance: %v", err)
	}

	if _, _, err := fixture.engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "checkout.go", Content: []byte("func Submit() { println(\"history query live head\") }"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
	}); err != nil {
		t.Fatalf("advance durable workspace live head: %v", err)
	}

	failingStore := &approvalExecutionFailingLoadStore{}
	service.proposalStore = failingStore
	historical, err := service.QueryApprovalHistory(context.Background(), fixture.access, query)
	if err != nil {
		t.Fatalf("query approval history after live-head advance: %v", err)
	}
	if historical.Freshness != approvalExecutionFreshnessHistorical {
		t.Fatalf("history freshness after live-head advance = %q, want historical", historical.Freshness)
	}
	if calls := failingStore.loadCalls.Load(); calls != 0 {
		t.Fatalf("history query proposal lookup calls = %d, want zero", calls)
	}
	expected := initial
	expected.Freshness = historical.Freshness
	if !reflect.DeepEqual(expected, historical) {
		t.Fatalf("live-head advance changed durable history projection:\ninitial=%+v\nhistorical=%+v", initial, historical)
	}

	transactionAfter, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read transaction snapshot after history query: %v", err)
	}
	if !reflect.DeepEqual(transactionBefore, transactionAfter) {
		t.Fatal("history query changed durable transaction snapshot")
	}
	databaseAfter, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database after history query: %v", err)
	}
	if !bytes.Equal(databaseBefore, databaseAfter) {
		t.Fatal("history query changed transaction database bytes")
	}
	pointerAfter, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read active pointer after history query: %v", err)
	}
	if !bytes.Equal(pointerBefore, pointerAfter) {
		t.Fatal("history query changed active pointer bytes")
	}
	proofAfter, err := storage.New(fixture.root).ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read active proof after history query: %v", err)
	}
	assertApprovalExecutionProofBytesEqual(t, proofBefore, proofAfter)
}

func TestApprovalHistoryQueryFailsClosedForCorruptDurableHistory(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-corrupt-command", "history-corrupt-key", "approve", "none", 0, nil, nil))

	transactionPath := filepath.Join(fixture.root, ".codeflow", "approval-transactions", approvalTransactionDatabaseName)
	if err := os.WriteFile(transactionPath, []byte("not a sqlite approval history"), 0o600); err != nil {
		t.Fatalf("corrupt durable approval history: %v", err)
	}

	result, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err == nil || !errors.Is(err, ErrApprovalHistoryInvalid) || errors.Is(err, ErrApprovalHistoryUnavailable) || !reflect.DeepEqual(result, ApprovalHistoryResult{}) || err.Error() != ErrApprovalHistoryInvalid.Error() {
		t.Fatalf("corrupt durable history result = %+v err=%v, want bounded invalid error and zero result", result, err)
	}
}

func TestApprovalHistoryQueryAuthorizesBeforeDurableRead(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	reader := &approvalHistoryCountingReader{err: errors.New("sentinel history storage error")}
	service.historyReader = reader

	result, err := service.QueryApprovalHistory(context.Background(), ApprovalAccess{}, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err == nil || !errors.Is(err, ErrApprovalUnauthorized) || !reflect.DeepEqual(result, ApprovalHistoryResult{}) {
		t.Fatalf("unauthorized history query result = %+v err=%v, want unauthorized error and zero result", result, err)
	}
	if calls := reader.calls.Load(); calls != 0 {
		t.Fatalf("unauthorized history reader calls = %d, want zero", calls)
	}
}

func TestApprovalHistoryQueryReportsMissingExactTarget(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)

	result, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err == nil || !errors.Is(err, ErrApprovalHistoryUnavailable) || errors.Is(err, ErrApprovalHistoryInvalid) || !reflect.DeepEqual(result, ApprovalHistoryResult{}) || err.Error() != ErrApprovalHistoryUnavailable.Error() {
		t.Fatalf("missing exact target result = %+v err=%v, want bounded unavailable error and zero result", result, err)
	}
}

func TestApprovalHistoryQueryReportsProposalMismatchForCommittedTarget(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-proposal-mismatch-command", "history-proposal-mismatch-key", "approve", "none", 0, nil, nil))

	result, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID + "-mismatch", EvidencePackID: fixture.packID,
	})
	if err == nil || !errors.Is(err, ErrApprovalHistoryUnavailable) || errors.Is(err, ErrApprovalHistoryInvalid) || !reflect.DeepEqual(result, ApprovalHistoryResult{}) || err.Error() != ErrApprovalHistoryUnavailable.Error() {
		t.Fatalf("proposal-mismatch history result = %+v err=%v, want bounded unavailable error and zero result", result, err)
	}
}

func TestApprovalHistoryQueryReportsEvidencePackMismatchForCommittedTarget(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-pack-mismatch-command", "history-pack-mismatch-key", "approve", "none", 0, nil, nil))

	result, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID + "-mismatch",
	})
	if err == nil || !errors.Is(err, ErrApprovalHistoryUnavailable) || errors.Is(err, ErrApprovalHistoryInvalid) || !reflect.DeepEqual(result, ApprovalHistoryResult{}) || err.Error() != ErrApprovalHistoryUnavailable.Error() {
		t.Fatalf("evidence-pack-mismatch history result = %+v err=%v, want bounded unavailable error and zero result", result, err)
	}
}

func TestApprovalHistoryQueryFailsClosedForCoherentAmbiguousTarget(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-ambiguous-command", "history-ambiguous-key", "approve", "none", 0, nil, nil))

	committed, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("read committed history snapshot: %v", err)
	}
	ambiguous := approvalHistorySnapshotWithCoherentAmbiguousTarget(t, committed)
	state := approvalHistoryTransactionState(ambiguous)
	if err := validateApprovalTransactionState(state); err != nil {
		t.Fatalf("ambiguous snapshot state validation = %v, want coherent state", err)
	}
	if err := replayApprovalTransactionState(state); err != nil {
		t.Fatalf("ambiguous snapshot replay = %v, want coherent replay", err)
	}

	service.historyReader = approvalHistorySnapshotFake{snapshot: ambiguous}
	result, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err == nil || !errors.Is(err, ErrApprovalHistoryInvalid) || errors.Is(err, ErrApprovalHistoryUnavailable) || !reflect.DeepEqual(result, ApprovalHistoryResult{}) || err.Error() != ErrApprovalHistoryInvalid.Error() {
		t.Fatalf("coherent ambiguous history result = %+v err=%v, want bounded invalid error and zero result", result, err)
	}
}

func TestApprovalHistoryQueryFailsClosedWithoutSnapshotReader(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	service.historyReader = nil

	result, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err == nil || !errors.Is(err, ErrApprovalHistoryInvalid) || !reflect.DeepEqual(result, ApprovalHistoryResult{}) || err.Error() != ErrApprovalHistoryInvalid.Error() {
		t.Fatalf("missing history snapshot reader result = %+v err=%v, want bounded invalid error and zero result", result, err)
	}
}

func approvalHistorySnapshotWithCoherentAmbiguousTarget(t *testing.T, committed ApprovalTransactionSnapshot) ApprovalTransactionSnapshot {
	t.Helper()
	state := approvalHistoryTransactionState(committed)
	if len(state.Events) != 1 || len(state.Aggregates) != 1 || len(state.IdempotencyResults) != 1 || len(state.Outbox) != 1 || len(state.Targets) != 1 {
		t.Fatalf("committed history fixture rows = events %d aggregates %d idempotency %d outbox %d targets %d, want one each", len(state.Events), len(state.Aggregates), len(state.IdempotencyResults), len(state.Outbox), len(state.Targets))
	}

	secondTarget := state.Targets[0]
	secondTarget.Target.ComputedBasisID += "-ambiguous"
	secondTarget.Target.GenerationID += "-ambiguous"
	secondTarget.Target.IntentRevision++
	secondTarget.Target.ValidatedSnapshotID += "-ambiguous"
	secondTarget.Target.MapID += "-ambiguous"
	secondTarget.Target.TaskID += "-ambiguous"
	secondTarget.TargetDigest = approvalTransactionTargetDigest(secondTarget.Target)

	secondAggregate := cloneApprovalAggregateV2(state.Aggregates[0])
	secondAggregate.AggregateID += "-ambiguous"
	secondAggregate.ComputedBasisID = secondTarget.Target.ComputedBasisID
	secondAggregate.GenerationID = secondTarget.Target.GenerationID
	secondAggregate.IntentRevision = secondTarget.Target.IntentRevision
	secondAggregate.History[0].EventID += "-ambiguous"
	secondAggregate.History[1].EventID += "-ambiguous"
	secondAggregate.History[1].ApprovalID += "-ambiguous"
	secondAggregate.LastEventID = secondAggregate.History[1].EventID
	secondAggregate.ActiveApprovalID = secondAggregate.History[1].ApprovalID
	secondTarget.GenesisEventID = secondAggregate.History[0].EventID
	secondTarget.Aggregate = cloneApprovalAggregateV2(secondAggregate)

	secondEvent := state.Events[0]
	secondEvent.EventID += "-ambiguous"
	secondEvent.ApprovalID += "-ambiguous"
	secondEvent.AggregateID = secondAggregate.AggregateID
	secondEvent.ComputedBasisID = secondTarget.Target.ComputedBasisID
	secondEvent.GenerationID = secondTarget.Target.GenerationID
	secondEvent.IntentRevision = secondTarget.Target.IntentRevision

	secondIdempotency := state.IdempotencyResults[0]
	secondIdempotency.IdempotencyKey += "-ambiguous"
	secondIdempotency.CommandID += "-ambiguous"
	secondIdempotency.ComputedBasisID = secondTarget.Target.ComputedBasisID
	secondIdempotency.GenerationID = secondTarget.Target.GenerationID
	secondIdempotency.ApprovalID = secondEvent.ApprovalID
	secondIdempotency.EventID = secondEvent.EventID
	secondIdempotency.AggregateID = secondAggregate.AggregateID
	secondIdempotency.OriginalCommand.CommandID = secondIdempotency.CommandID
	secondIdempotency.OriginalCommand.IdempotencyKey = secondIdempotency.IdempotencyKey
	secondIdempotency.OriginalResult = ApprovalIdempotencyOriginalResultV1{
		Outcome: "committed", ApprovalID: secondEvent.ApprovalID, EventID: secondEvent.EventID,
		AggregateID: secondAggregate.AggregateID, AggregateVersion: secondEvent.AggregateVersion, State: secondAggregate.State,
	}
	secondCommand := ApprovalCommandV2{
		SchemaID: ApprovalCommandV2SchemaID, SchemaVersion: 2, CommandID: secondIdempotency.CommandID,
		ActorID: secondIdempotency.ActorID, SessionID: secondIdempotency.SessionID, WorkspaceID: secondIdempotency.WorkspaceID,
		ProposalID: secondIdempotency.ProposalID, EvidencePackID: secondIdempotency.EvidencePackID,
		ComputedBasisID: secondIdempotency.ComputedBasisID, GenerationID: secondIdempotency.GenerationID,
		IntentRevision: secondTarget.Target.IntentRevision, Decision: secondEvent.Decision,
		IdempotencyKey: secondIdempotency.IdempotencyKey, ExpectedApprovalVersion: secondEvent.AggregateVersion - 1, ExpectedState: "none",
	}
	requestDigest, err := approvalCommandIdempotencyDigest(secondCommand)
	if err != nil {
		t.Fatalf("derive ambiguous idempotency digest: %v", err)
	}
	secondIdempotency.RequestDigest = "sha256:" + hex.EncodeToString(requestDigest[:])

	secondPayload := approvalTransactionEventPayload(secondEvent)
	payloadDigest := sha256.Sum256(secondPayload)
	secondOutbox := state.Outbox[0]
	secondOutbox.OutboxID = approvalTransactionOutboxID(secondEvent.EventID)
	secondOutbox.EventID = secondEvent.EventID
	secondOutbox.AggregateID = secondAggregate.AggregateID
	secondOutbox.WorkspaceID = secondEvent.WorkspaceID
	secondOutbox.PayloadDigest = "sha256:" + hex.EncodeToString(payloadDigest[:])
	secondOutbox.CommittedEvent = ApprovalOutboxCommittedEventV1{
		EventID: secondEvent.EventID, ApprovalID: secondEvent.ApprovalID, AggregateID: secondAggregate.AggregateID,
		AggregateVersion: secondEvent.AggregateVersion, WorkspaceID: secondEvent.WorkspaceID, Decision: secondEvent.Decision,
		PayloadDigest: secondOutbox.PayloadDigest,
	}

	state.Events = append(state.Events, secondEvent)
	state.Aggregates = append(state.Aggregates, secondAggregate)
	state.IdempotencyResults = append(state.IdempotencyResults, secondIdempotency)
	state.Outbox = append(state.Outbox, secondOutbox)
	state.Targets = append(state.Targets, secondTarget)
	return snapshotFromApprovalTransactionState(state)
}

func approvalHistoryTransactionState(snapshot ApprovalTransactionSnapshot) approvalTransactionState {
	state := approvalTransactionState{
		StoreVersion:       approvalTransactionStoreVersion,
		Events:             append([]ApprovalEventV2(nil), snapshot.Events...),
		Aggregates:         make([]ApprovalAggregateV2, len(snapshot.Aggregates)),
		IdempotencyResults: append([]ApprovalIdempotencyResultV1(nil), snapshot.IdempotencyResults...),
		Outbox:             append([]ApprovalOutboxV1(nil), snapshot.Outbox...),
		Targets:            make([]approvalTransactionTargetRecord, len(snapshot.targets)),
	}
	for index := range snapshot.Aggregates {
		state.Aggregates[index] = cloneApprovalAggregateV2(snapshot.Aggregates[index])
	}
	for index := range snapshot.targets {
		state.Targets[index] = snapshot.targets[index]
		state.Targets[index].Aggregate = cloneApprovalAggregateV2(snapshot.targets[index].Aggregate)
	}
	return state
}

func TestApprovalHistoryQueryReplaysDurableLifecycleAfterRestart(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	first := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-replay-initial-command", "history-replay-initial-key", "approve", "none", 0, nil, nil))
	predecessor := first.Receipt.Event.ApprovalID
	editedText := "History replay edited title"
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-replay-edit-command", "history-replay-edit-key", "edit_then_approve", "active", 1, &predecessor, &editedText))

	beforeRestart, err := service.QueryApprovalHistory(context.Background(), fixture.access, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err != nil {
		t.Fatalf("query history before restart: %v", err)
	}

	restartedEngine, err := workspace.NewSnapshotEngine(fixture.root, 0)
	if err != nil {
		t.Fatalf("reopen workspace engine: %v", err)
	}
	restartedService, err := NewApprovalExecutionService(fixture.root, restartedEngine, NewDurableProposalStore(fixture.root))
	if err != nil {
		t.Fatalf("reopen approval execution service: %v", err)
	}
	authorizer := NewApprovalWorkspaceAuthorizer(fixture.root)
	rotatedAccess, err := NewApprovalAccessGate(NewLocalProcessApprovalAuthenticator(), authorizer).AuthenticateAndAuthorize(context.Background(), authorizer.WorkspaceID(), fixture.root)
	if err != nil {
		t.Fatalf("reopen approval access: %v", err)
	}
	afterRestart, err := restartedService.QueryApprovalHistory(context.Background(), rotatedAccess, ApprovalHistoryQuery{
		ProposalID: fixture.proposalID, EvidencePackID: fixture.packID,
	})
	if err != nil {
		t.Fatalf("query history after restart: %v", err)
	}
	if !reflect.DeepEqual(beforeRestart, afterRestart) {
		t.Fatalf("restart changed replayed history:\nbefore=%+v\nafter=%+v", beforeRestart, afterRestart)
	}
	if afterRestart.Aggregate.State != "active" || afterRestart.Aggregate.Version != 2 || afterRestart.Aggregate.ActiveApprovalID != afterRestart.Events[1].ApprovalID || len(afterRestart.Events) != 2 || len(afterRestart.Aggregate.History) != 3 || afterRestart.Aggregate.History[2].Decision != "edit_then_approve" {
		t.Fatalf("replayed lifecycle projection = %+v", afterRestart)
	}
}

func TestApprovalHistoryQueryProjectsTerminalLifecycleStatesAfterRestart(t *testing.T) {
	cases := []struct {
		name                  string
		initialApprove        bool
		followupDecision      string
		expectedState         string
		expectedDecisions     []string
		expectedHistoryStates []string
	}{
		{
			name:                  "reject-from-none",
			followupDecision:      "reject",
			expectedState:         "rejected",
			expectedDecisions:     []string{"reject"},
			expectedHistoryStates: []string{"rejected"},
		},
		{
			name:                  "reject-from-active",
			initialApprove:        true,
			followupDecision:      "reject",
			expectedState:         "rejected",
			expectedDecisions:     []string{"approve", "reject"},
			expectedHistoryStates: []string{"active", "rejected"},
		},
		{
			name:                  "revoke-from-active",
			initialApprove:        true,
			followupDecision:      "revoke",
			expectedState:         "revoked",
			expectedDecisions:     []string{"approve", "revoke"},
			expectedHistoryStates: []string{"active", "revoked"},
		},
		{
			name:                  "supersede-from-active",
			initialApprove:        true,
			followupDecision:      "supersede",
			expectedState:         "superseded",
			expectedDecisions:     []string{"approve", "supersede"},
			expectedHistoryStates: []string{"active", "superseded"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newApprovalExecutionFixture(t)
			service := mustNewApprovalExecutionService(t, fixture)
			var initial ApprovalExecutionResult
			if testCase.initialApprove {
				initial = mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-"+testCase.name+"-initial-command", "history-"+testCase.name+"-initial-key", "approve", "none", 0, nil, nil))
			}

			var predecessor *string
			expectedState := "none"
			expectedVersion := int64(0)
			if testCase.initialApprove {
				predecessor = &initial.Receipt.Event.ApprovalID
				expectedState = "active"
				expectedVersion = 1
			}
			mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "history-"+testCase.name+"-final-command", "history-"+testCase.name+"-final-key", testCase.followupDecision, expectedState, expectedVersion, predecessor, nil))

			query := ApprovalHistoryQuery{ProposalID: fixture.proposalID, EvidencePackID: fixture.packID}
			beforeRestart, err := service.QueryApprovalHistory(context.Background(), fixture.access, query)
			if err != nil {
				t.Fatalf("query %s history before restart: %v", testCase.name, err)
			}
			if beforeRestart.Aggregate.State != testCase.expectedState || beforeRestart.Aggregate.Version != int64(len(testCase.expectedDecisions)) || beforeRestart.Aggregate.ActiveApprovalID != "" {
				t.Fatalf("%s terminal projection = %+v", testCase.name, beforeRestart)
			}
			if len(beforeRestart.Events) != len(testCase.expectedDecisions) || len(beforeRestart.Aggregate.History) != len(testCase.expectedDecisions)+1 {
				t.Fatalf("%s projection lengths = events %d aggregate history %d", testCase.name, len(beforeRestart.Events), len(beforeRestart.Aggregate.History))
			}
			genesis := beforeRestart.Aggregate.History[0]
			if genesis.Version != 0 || genesis.State != "none" || genesis.Decision != "none" || genesis.ApprovalID != "" || genesis.EventID == "" {
				t.Fatalf("%s invalid history genesis = %+v", testCase.name, genesis)
			}
			for index, expectedDecision := range testCase.expectedDecisions {
				event := beforeRestart.Events[index]
				history := beforeRestart.Aggregate.History[index+1]
				aggregateHistory := beforeRestart.Aggregate.History[index+1]
				wantVersion := int64(index + 1)
				if event.AggregateVersion != wantVersion || event.Decision != expectedDecision || history.Version != wantVersion || history.State != testCase.expectedHistoryStates[index] || history.EventID != event.EventID || history.ApprovalID != event.ApprovalID || history.Decision != event.Decision || aggregateHistory.Version != wantVersion || aggregateHistory.State != testCase.expectedHistoryStates[index] || aggregateHistory.EventID != event.EventID || aggregateHistory.ApprovalID != event.ApprovalID || aggregateHistory.Decision != expectedDecision {
					t.Fatalf("%s ordered projection index %d = event=%+v aggregate history=%+v", testCase.name, index, event, aggregateHistory)
				}
			}
			if beforeRestart.Events[0].PredecessorApprovalID != "" {
				t.Fatalf("%s initial predecessor = %q, want empty", testCase.name, beforeRestart.Events[0].PredecessorApprovalID)
			}
			if testCase.initialApprove && beforeRestart.Events[1].PredecessorApprovalID != beforeRestart.Events[0].ApprovalID {
				t.Fatalf("%s follow-up predecessor = %q, want %q", testCase.name, beforeRestart.Events[1].PredecessorApprovalID, beforeRestart.Events[0].ApprovalID)
			}

			restartedEngine, err := workspace.NewSnapshotEngine(fixture.root, 0)
			if err != nil {
				t.Fatalf("reopen %s workspace engine: %v", testCase.name, err)
			}
			restartedService, err := NewApprovalExecutionService(fixture.root, restartedEngine, NewDurableProposalStore(fixture.root))
			if err != nil {
				t.Fatalf("reopen %s approval execution service: %v", testCase.name, err)
			}
			authorizer := NewApprovalWorkspaceAuthorizer(fixture.root)
			rotatedAccess, err := NewApprovalAccessGate(NewLocalProcessApprovalAuthenticator(), authorizer).AuthenticateAndAuthorize(context.Background(), authorizer.WorkspaceID(), fixture.root)
			if err != nil {
				t.Fatalf("reopen %s approval access: %v", testCase.name, err)
			}
			afterRestart, err := restartedService.QueryApprovalHistory(context.Background(), rotatedAccess, query)
			if err != nil {
				t.Fatalf("query %s history after restart: %v", testCase.name, err)
			}
			if !reflect.DeepEqual(beforeRestart, afterRestart) {
				t.Fatalf("%s restart changed history projection:\nbefore=%+v\nafter=%+v", testCase.name, beforeRestart, afterRestart)
			}
			if !reflect.DeepEqual(beforeRestart.Aggregate.History, afterRestart.Aggregate.History) {
				t.Fatalf("%s restart changed aggregate history:\nbefore=%+v\nafter=%+v", testCase.name, beforeRestart.Aggregate.History, afterRestart.Aggregate.History)
			}
		})
	}
}
