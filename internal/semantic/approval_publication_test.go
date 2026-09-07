package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestApprovalPublicationPendingProjectionIsExactDefensiveValue(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	receipt := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "publication-pending-command", "publication-pending-key", "approve", "none", 0, nil, nil))

	deliveries, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("pending outbox deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("pending outbox deliveries = %d, want one", len(deliveries))
	}
	got := deliveries[0]
	if got.Outbox != receipt.Receipt.Outbox {
		t.Fatalf("projected outbox = %+v, want %+v", got.Outbox, receipt.Receipt.Outbox)
	}
	if got.ComputedBasisID != fixture.before.ComputedBasisID || got.ValidatedSnapshotID != fixture.before.ValidatedAgainstSnapshotID || got.GenerationID != fixture.before.GenerationID {
		t.Fatalf("projected identities = %+v, want basis=%q snapshot=%q generation=%q", got, fixture.before.ComputedBasisID, fixture.before.ValidatedAgainstSnapshotID, fixture.before.GenerationID)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal delivery: %v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode delivery: %v", err)
	}
	wantKeys := map[string]struct{}{"outbox": {}, "computedBasisId": {}, "validatedSnapshotId": {}, "generationId": {}}
	if !reflect.DeepEqual(documentKeys(document), wantKeys) {
		t.Fatalf("delivery JSON keys = %v, want %v", documentKeys(document), wantKeys)
	}
	for _, forbidden := range []string{"proposalId", "evidencePackId", "mapId", "taskId", "actorId", "sessionId", "approvedText", "path", "seal", "secret"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("delivery JSON contains forbidden field %q: %s", forbidden, raw)
		}
	}

	mutated := deliveries[0]
	mutated.Outbox.OutboxID = "caller-mutated-outbox"
	mutated.ComputedBasisID = "caller-mutated-basis"
	deliveries[0] = mutated
	again, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("pending outbox deliveries after caller mutation: %v", err)
	}
	if len(again) != 1 || !reflect.DeepEqual(again[0], got) {
		t.Fatalf("caller mutation changed projected value: got=%+v want=%+v", again, got)
	}
}

func TestApprovalPublicationPendingProjectionPreservesOrderAndExcludesPublished(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	first := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "publication-order-first-command", "publication-order-first-key", "approve", "none", 0, nil, nil))
	predecessor := first.Receipt.Event.ApprovalID
	editedText := "Publication order edited text"
	second := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "publication-order-second-command", "publication-order-second-key", "edit_then_approve", "active", 1, &predecessor, &editedText))

	deliveries, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("pending ordered deliveries: %v", err)
	}
	if len(deliveries) != 2 || deliveries[0].Outbox.OutboxID != first.Receipt.Outbox.OutboxID || deliveries[1].Outbox.OutboxID != second.Receipt.Outbox.OutboxID {
		t.Fatalf("pending outbox order = %+v, want [%s %s]", deliveries, first.Receipt.Outbox.OutboxID, second.Receipt.Outbox.OutboxID)
	}
	const publishedAt = "9999-12-31T23:59:59Z"
	if _, err := service.transactions.MarkApprovalOutboxPublished(context.Background(), first.Receipt.Outbox.OutboxID, first.Receipt.Outbox.EventID, first.Receipt.Outbox.AggregateID, publishedAt); err != nil {
		t.Fatalf("publish first outbox: %v", err)
	}
	deliveries, err = service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("pending deliveries after first publication: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Outbox.OutboxID != second.Receipt.Outbox.OutboxID {
		t.Fatalf("pending after first publication = %+v, want second only", deliveries)
	}
	if _, err := service.transactions.MarkApprovalOutboxPublished(context.Background(), second.Receipt.Outbox.OutboxID, second.Receipt.Outbox.EventID, second.Receipt.Outbox.AggregateID, publishedAt); err != nil {
		t.Fatalf("publish second outbox: %v", err)
	}
	deliveries, err = service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("pending deliveries after all publication: %v", err)
	}
	if deliveries == nil || len(deliveries) != 0 {
		t.Fatalf("all-published deliveries = %#v, want non-nil empty slice", deliveries)
	}
}

func TestApprovalPublicationProjectionRejectsMissingDuplicateAndInconsistentTarget(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "publication-invalid-command", "publication-invalid-key", "approve", "none", 0, nil, nil))
	committed, err := service.transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot committed publication: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(ApprovalTransactionSnapshot) ApprovalTransactionSnapshot
	}{
		{
			name: "missing-target",
			mutate: func(snapshot ApprovalTransactionSnapshot) ApprovalTransactionSnapshot {
				snapshot.targets = nil
				return snapshot
			},
		},
		{
			name: "duplicate-target",
			mutate: func(snapshot ApprovalTransactionSnapshot) ApprovalTransactionSnapshot {
				snapshot.targets = append(snapshot.targets, snapshot.targets[0])
				return snapshot
			},
		},
		{
			name: "inconsistent-target-digest",
			mutate: func(snapshot ApprovalTransactionSnapshot) ApprovalTransactionSnapshot {
				snapshot.targets[0].TargetDigest = "sha256:" + strings.Repeat("0", 64)
				return snapshot
			},
		},
		{
			name: "inconsistent-outbox-aggregate",
			mutate: func(snapshot ApprovalTransactionSnapshot) ApprovalTransactionSnapshot {
				snapshot.Outbox[0].AggregateID += "-mismatch"
				return snapshot
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := projectPendingApprovalOutboxDeliveries(testCase.mutate(committed))
			if err == nil || !errors.Is(err, ErrApprovalExecutionUnavailable) || got != nil {
				t.Fatalf("projection = %#v err=%v, want nil and bounded unavailable", got, err)
			}
		})
	}
}

func TestApprovalPublicationPendingProjectionHonorsCancellation(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := service.PendingApprovalOutboxDeliveries(ctx)
	if err == nil || !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("canceled pending projection = %#v err=%v, want nil context canceled", got, err)
	}
}

func TestApprovalPublicationMarkExactAndIdempotentAfterRestartState(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	receipt := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "publication-mark-command", "publication-mark-key", "approve", "none", 0, nil, nil))
	deliveries, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("pending delivery = %+v err=%v, want one", deliveries, err)
	}
	delivery := deliveries[0]
	publishedAt := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	published, err := service.MarkApprovalOutboxDeliveryPublished(context.Background(), delivery, publishedAt)
	if err != nil {
		t.Fatalf("mark pending delivery published: %v", err)
	}
	if published.DeliveryState != "published" || published.PublishedAt != publishedAt.Format(time.RFC3339Nano) || published.OutboxID != receipt.Receipt.Outbox.OutboxID || published.EventID != receipt.Receipt.Outbox.EventID || published.AggregateID != receipt.Receipt.Outbox.AggregateID {
		t.Fatalf("published outbox = %+v, want exact published receipt", published)
	}

	replayed, err := service.MarkApprovalOutboxDeliveryPublished(context.Background(), delivery, publishedAt)
	if err != nil {
		t.Fatalf("repeat mark published delivery: %v", err)
	}
	if !reflect.DeepEqual(replayed, published) {
		t.Fatalf("repeat published outbox = %+v, want %+v", replayed, published)
	}
	if pending, err := service.PendingApprovalOutboxDeliveries(context.Background()); err != nil || pending == nil || len(pending) != 0 {
		t.Fatalf("pending after publication = %#v err=%v, want non-nil empty", pending, err)
	}
}

func TestApprovalPublicationMarkRejectsChangedBindingAndPreCommitTime(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	receipt := mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "publication-reject-command", "publication-reject-key", "approve", "none", 0, nil, nil))
	deliveries, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("pending delivery = %+v err=%v, want one", deliveries, err)
	}
	base := deliveries[0]
	future := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	cases := []struct {
		name   string
		mutate func(ApprovalOutboxDelivery) ApprovalOutboxDelivery
		at     time.Time
	}{
		{name: "basis", mutate: func(value ApprovalOutboxDelivery) ApprovalOutboxDelivery {
			value.ComputedBasisID += "-wrong"
			return value
		}, at: future},
		{name: "snapshot", mutate: func(value ApprovalOutboxDelivery) ApprovalOutboxDelivery {
			value.ValidatedSnapshotID += "-wrong"
			return value
		}, at: future},
		{name: "generation", mutate: func(value ApprovalOutboxDelivery) ApprovalOutboxDelivery {
			value.GenerationID += "-wrong"
			return value
		}, at: future},
		{name: "event", mutate: func(value ApprovalOutboxDelivery) ApprovalOutboxDelivery {
			value.Outbox.EventID += "-wrong"
			return value
		}, at: future},
		{name: "aggregate", mutate: func(value ApprovalOutboxDelivery) ApprovalOutboxDelivery {
			value.Outbox.AggregateID += "-wrong"
			return value
		}, at: future},
		{name: "outbox", mutate: func(value ApprovalOutboxDelivery) ApprovalOutboxDelivery {
			value.Outbox.OutboxID += "-wrong"
			return value
		}, at: future},
	}
	committedAt, err := time.Parse(time.RFC3339Nano, receipt.Receipt.Outbox.CommittedAt)
	if err != nil {
		t.Fatalf("parse committedAt: %v", err)
	}
	cases = append(cases, struct {
		name   string
		mutate func(ApprovalOutboxDelivery) ApprovalOutboxDelivery
		at     time.Time
	}{name: "before-commit", mutate: func(value ApprovalOutboxDelivery) ApprovalOutboxDelivery { return value }, at: committedAt.Add(-time.Nanosecond)})
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := service.MarkApprovalOutboxDeliveryPublished(context.Background(), testCase.mutate(base), testCase.at)
			if err == nil || !errors.Is(err, ErrApprovalExecutionInvalid) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
				t.Fatalf("changed delivery publication = %+v err=%v, want bounded invalid and zero", got, err)
			}
			pending, snapshotErr := service.PendingApprovalOutboxDeliveries(context.Background())
			if snapshotErr != nil || len(pending) != 1 || !reflect.DeepEqual(pending[0], base) {
				t.Fatalf("invalid publication changed pending state = %+v err=%v, want %+v", pending, snapshotErr, base)
			}
		})
	}
}

func TestApprovalPublicationMarkRejectsNonUTCAndCanceledWithoutMutation(t *testing.T) {
	fixture := newApprovalExecutionFixture(t)
	service := mustNewApprovalExecutionService(t, fixture)
	mustExecuteApproval(t, service, fixture, approvalExecutionDraft(fixture, "publication-time-command", "publication-time-key", "approve", "none", 0, nil, nil))
	deliveries, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("pending delivery = %+v err=%v, want one", deliveries, err)
	}
	delivery := deliveries[0]
	future := time.Date(9999, 12, 31, 23, 59, 59, 0, time.FixedZone("not-utc", 3600))
	if got, err := service.MarkApprovalOutboxDeliveryPublished(context.Background(), delivery, future); err == nil || !errors.Is(err, ErrApprovalExecutionInvalid) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
		t.Fatalf("non-UTC publication = %+v err=%v, want bounded invalid and zero", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := service.MarkApprovalOutboxDeliveryPublished(ctx, delivery, time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)); err == nil || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
		t.Fatalf("canceled publication = %+v err=%v, want context canceled and zero", got, err)
	}
	pending, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil || len(pending) != 1 || !reflect.DeepEqual(pending[0], delivery) {
		t.Fatalf("time/cancel publication changed pending state = %+v err=%v, want %+v", pending, err, delivery)
	}
}

func documentKeys(document map[string]json.RawMessage) map[string]struct{} {
	keys := make(map[string]struct{}, len(document))
	for key := range document {
		keys[key] = struct{}{}
	}
	return keys
}
