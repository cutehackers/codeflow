package flowview

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codeflow/internal/semantic"
)

type approvalPublicationServiceFake struct {
	pending      []semantic.ApprovalOutboxDelivery
	pendingErr   error
	pendingCalls int
	onPending    func()
	marks        []semantic.ApprovalOutboxDelivery
	markTimes    []time.Time
	marked       semantic.ApprovalOutboxV1
	markErr      error
	markFunc     func(semantic.ApprovalOutboxDelivery, time.Time) (semantic.ApprovalOutboxV1, error)
	onMark       func(semantic.ApprovalOutboxDelivery, time.Time)
	callLog      *[]string
}

func (f *approvalPublicationServiceFake) PendingApprovalOutboxDeliveries(context.Context) ([]semantic.ApprovalOutboxDelivery, error) {
	f.pendingCalls++
	if f.onPending != nil {
		f.onPending()
	}
	if f.pendingErr != nil {
		return nil, f.pendingErr
	}
	return append([]semantic.ApprovalOutboxDelivery(nil), f.pending...), nil
}

func (f *approvalPublicationServiceFake) MarkApprovalOutboxDeliveryPublished(_ context.Context, delivery semantic.ApprovalOutboxDelivery, publishedAt time.Time) (semantic.ApprovalOutboxV1, error) {
	f.marks = append(f.marks, delivery)
	f.markTimes = append(f.markTimes, publishedAt)
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "mark:"+delivery.Outbox.OutboxID)
	}
	if f.onMark != nil {
		f.onMark(delivery, publishedAt)
	}
	if f.markFunc != nil {
		return f.markFunc(delivery, publishedAt)
	}
	if f.markErr != nil {
		return semantic.ApprovalOutboxV1{}, f.markErr
	}
	return f.marked, nil
}

type approvalPublicationPublisherFake struct {
	publish func(semantic.ApprovalOutboxCommittedEventV1, string, string, string, string) (*semantic.EventEnvelope, error)
	callLog *[]string
}

func (f *approvalPublicationPublisherFake) PublishApprovalUpdated(committed semantic.ApprovalOutboxCommittedEventV1, committedAt, computedBasisID, validatedSnapshotID, generationID string) (*semantic.EventEnvelope, error) {
	if f.callLog != nil {
		*f.callLog = append(*f.callLog, "publish:"+committed.EventID)
	}
	return f.publish(committed, committedAt, computedBasisID, validatedSnapshotID, generationID)
}

func TestApprovalPublicationDispatchPublishesBeforeMarkingAtEnvelopeTime(t *testing.T) {
	delivery := approvalPublicationTestDelivery()
	service := &approvalPublicationServiceFake{pending: []semantic.ApprovalOutboxDelivery{delivery}, marked: approvalPublicationPublishedOutbox(delivery)}
	occurredAt := time.Date(2026, 9, 7, 12, 34, 56, 123000000, time.UTC)
	order := make([]string, 0, 2)
	service.onMark = func(semantic.ApprovalOutboxDelivery, time.Time) {
		order = append(order, "mark")
	}
	publisher := &approvalPublicationPublisherFake{publish: func(committed semantic.ApprovalOutboxCommittedEventV1, committedAt, basis, snapshot, generation string) (*semantic.EventEnvelope, error) {
		order = append(order, "publish")
		if committed != delivery.Outbox.CommittedEvent || committedAt != delivery.Outbox.CommittedAt || basis != delivery.ComputedBasisID || snapshot != delivery.ValidatedSnapshotID || generation != delivery.GenerationID {
			t.Fatalf("publisher received mismatched delivery: event=%+v committedAt=%q basis=%q snapshot=%q generation=%q", committed, committedAt, basis, snapshot, generation)
		}
		return approvalPublicationTestEnvelope(delivery, occurredAt), nil
	}}
	service.marked = approvalPublicationPublishedOutbox(delivery)
	service.marked.PublishedAt = occurredAt.Format(time.RFC3339Nano)

	marked, err := dispatchApprovalOutboxDelivery(context.Background(), service, publisher, delivery)
	if err != nil {
		t.Fatalf("dispatch approval outbox delivery: %v", err)
	}
	if len(order) != 2 || order[0] != "publish" || order[1] != "mark" {
		t.Fatalf("dispatch order = %v, want publish then mark", order)
	}
	if len(service.marks) != 1 || len(service.markTimes) != 1 || service.marks[0] != delivery || !service.markTimes[0].Equal(occurredAt) {
		t.Fatalf("mark call = deliveries=%+v times=%+v, want exact delivery and envelope time", service.marks, service.markTimes)
	}
	if marked != service.marked || marked.DeliveryState != "published" || marked.PublishedAt != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("marked outbox = %+v, want %+v", marked, service.marked)
	}
}

func TestApprovalPublicationDispatchDoesNotMarkWhenPublishFailsOrReturnsNil(t *testing.T) {
	delivery := approvalPublicationTestDelivery()
	publishErr := errors.New("publisher unavailable")
	tests := []struct {
		name      string
		publish   func() (*semantic.EventEnvelope, error)
		wantCause error
	}{
		{
			name: "publisher error",
			publish: func() (*semantic.EventEnvelope, error) {
				return nil, publishErr
			},
			wantCause: publishErr,
		},
		{
			name: "nil envelope",
			publish: func() (*semantic.EventEnvelope, error) {
				return nil, nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &approvalPublicationServiceFake{marked: approvalPublicationPublishedOutbox(delivery)}
			publishCalls := 0
			publisher := &approvalPublicationPublisherFake{publish: func(semantic.ApprovalOutboxCommittedEventV1, string, string, string, string) (*semantic.EventEnvelope, error) {
				publishCalls++
				return tt.publish()
			}}

			marked, err := dispatchApprovalOutboxDelivery(context.Background(), service, publisher, delivery)
			if marked != (semantic.ApprovalOutboxV1{}) {
				t.Fatalf("dispatch returned non-zero outbox on publish failure: %+v", marked)
			}
			if err == nil {
				t.Fatal("dispatch unexpectedly succeeded")
			}
			if tt.wantCause != nil && !errors.Is(err, tt.wantCause) {
				t.Fatalf("dispatch error = %v, want cause %v", err, tt.wantCause)
			}
			if publishCalls != 1 {
				t.Fatalf("publish calls = %d, want 1", publishCalls)
			}
			if len(service.marks) != 0 {
				t.Fatalf("mark calls = %d, want 0", len(service.marks))
			}
		})
	}
}

func TestApprovalPublicationDispatchRejectsMismatchedEnvelopeWithoutMark(t *testing.T) {
	delivery := approvalPublicationTestDelivery()
	occurredAt := time.Date(2026, 9, 7, 12, 34, 56, 123000000, time.UTC)
	tests := []struct {
		name   string
		mutate func(*semantic.EventEnvelope)
	}{
		{
			name: "wrong event type",
			mutate: func(envelope *semantic.EventEnvelope) {
				envelope.EventType = "activity.updated"
			},
		},
		{
			name: "wrong committed approval event data",
			mutate: func(envelope *semantic.EventEnvelope) {
				data := envelope.Data.(approvalUpdatedEventData)
				data.ApprovalEvent.Decision = "reject"
				envelope.Data = data
			},
		},
		{
			name: "wrong outer computed basis",
			mutate: func(envelope *semantic.EventEnvelope) {
				value := delivery.ComputedBasisID[:63] + "c"
				envelope.ComputedBasisID = &value
			},
		},
		{
			name: "wrong outer validated snapshot",
			mutate: func(envelope *semantic.EventEnvelope) {
				value := delivery.ValidatedSnapshotID + "-wrong"
				envelope.ValidatedAgainstSnapshotID = &value
			},
		},
		{
			name: "wrong outer generation",
			mutate: func(envelope *semantic.EventEnvelope) {
				value := delivery.GenerationID + "-wrong"
				envelope.GenerationID = &value
			},
		},
		{
			name: "wrong inner computed basis",
			mutate: func(envelope *semantic.EventEnvelope) {
				data := envelope.Data.(approvalUpdatedEventData)
				data.ComputedBasisID = delivery.ComputedBasisID[:63] + "c"
				envelope.Data = data
			},
		},
		{
			name: "wrong inner validated snapshot",
			mutate: func(envelope *semantic.EventEnvelope) {
				data := envelope.Data.(approvalUpdatedEventData)
				data.ValidatedAgainstSnapshotID = delivery.ValidatedSnapshotID + "-wrong"
				envelope.Data = data
			},
		},
		{
			name: "wrong inner generation",
			mutate: func(envelope *semantic.EventEnvelope) {
				data := envelope.Data.(approvalUpdatedEventData)
				data.GenerationID = delivery.GenerationID + "-wrong"
				envelope.Data = data
			},
		},
		{
			name: "non-UTC occurred at",
			mutate: func(envelope *semantic.EventEnvelope) {
				envelope.OccurredAt = occurredAt.In(time.FixedZone("offset", 3600))
			},
		},
		{
			name: "zero occurred at",
			mutate: func(envelope *semantic.EventEnvelope) {
				envelope.OccurredAt = time.Time{}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &approvalPublicationServiceFake{}
			publishCalls := 0
			publisher := &approvalPublicationPublisherFake{publish: func(semantic.ApprovalOutboxCommittedEventV1, string, string, string, string) (*semantic.EventEnvelope, error) {
				publishCalls++
				envelope := approvalPublicationTestEnvelope(delivery, occurredAt)
				tt.mutate(envelope)
				return envelope, nil
			}}

			marked, err := dispatchApprovalOutboxDelivery(context.Background(), service, publisher, delivery)
			if marked != (semantic.ApprovalOutboxV1{}) {
				t.Fatalf("dispatch returned non-zero outbox: %+v", marked)
			}
			if err == nil {
				t.Fatal("dispatch unexpectedly accepted mismatched envelope")
			}
			if publishCalls != 1 {
				t.Fatalf("publish calls = %d, want 1", publishCalls)
			}
			if len(service.marks) != 0 {
				t.Fatalf("mark calls = %d, want 0", len(service.marks))
			}
		})
	}
}

func TestApprovalPublicationRecoveryReadsOnceAndProcessesSerially(t *testing.T) {
	deliveries := approvalPublicationTestDeliveries()
	order := make([]string, 0, 5)
	service := &approvalPublicationServiceFake{pending: deliveries, callLog: &order}
	service.markFunc = func(delivery semantic.ApprovalOutboxDelivery, publishedAt time.Time) (semantic.ApprovalOutboxV1, error) {
		marked := approvalPublicationPublishedOutbox(delivery)
		marked.PublishedAt = publishedAt.Format(time.RFC3339Nano)
		return marked, nil
	}
	publishCalls := 0
	publisher := &approvalPublicationPublisherFake{callLog: &order, publish: func(committed semantic.ApprovalOutboxCommittedEventV1, committedAt, basis, snapshot, generation string) (*semantic.EventEnvelope, error) {
		if publishCalls >= len(deliveries) || committed != deliveries[publishCalls].Outbox.CommittedEvent || committedAt != deliveries[publishCalls].Outbox.CommittedAt || basis != deliveries[publishCalls].ComputedBasisID || snapshot != deliveries[publishCalls].ValidatedSnapshotID || generation != deliveries[publishCalls].GenerationID {
			t.Fatalf("publisher call %d received out-of-order delivery: event=%+v committedAt=%q", publishCalls, committed, committedAt)
		}
		occurredAt := time.Date(2026, 9, 7, 12, 34, 56+publishCalls, 123000000, time.UTC)
		publishCalls++
		return approvalPublicationTestEnvelope(deliveries[publishCalls-1], occurredAt), nil
	}}

	if err := recoverPendingApprovalOutboxDeliveries(context.Background(), service, publisher); err != nil {
		t.Fatalf("recover pending deliveries: %v", err)
	}
	if service.pendingCalls != 1 {
		t.Fatalf("pending calls = %d, want 1", service.pendingCalls)
	}
	if publishCalls != len(deliveries) || len(service.marks) != len(deliveries) {
		t.Fatalf("publication calls = %d and mark calls = %d, want %d each", publishCalls, len(service.marks), len(deliveries))
	}
	wantOrder := []string{"publish:approval-event-1", "mark:outbox-1", "publish:approval-event-2", "mark:outbox-2", "publish:approval-event-3", "mark:outbox-3"}
	if len(order) != len(wantOrder) {
		t.Fatalf("call order = %v, want %v", order, wantOrder)
	}
	for index, want := range wantOrder {
		if order[index] != want {
			t.Fatalf("call order = %v, want %v", order, wantOrder)
		}
	}
	for index, delivery := range deliveries {
		if service.marks[index] != delivery {
			t.Fatalf("mark %d delivery = %+v, want %+v", index, service.marks[index], delivery)
		}
	}
}

func TestApprovalPublicationRecoveryStopsAtFirstFailure(t *testing.T) {
	deliveries := approvalPublicationTestDeliveries()
	order := make([]string, 0, 3)
	publishErr := errors.New("second approval publication failed")
	service := &approvalPublicationServiceFake{pending: deliveries, callLog: &order}
	service.markFunc = func(delivery semantic.ApprovalOutboxDelivery, publishedAt time.Time) (semantic.ApprovalOutboxV1, error) {
		if delivery != deliveries[0] {
			t.Fatalf("unexpected mark after first failure: %+v", delivery)
		}
		marked := approvalPublicationPublishedOutbox(delivery)
		marked.PublishedAt = publishedAt.Format(time.RFC3339Nano)
		return marked, nil
	}
	publishCalls := 0
	publisher := &approvalPublicationPublisherFake{callLog: &order, publish: func(committed semantic.ApprovalOutboxCommittedEventV1, committedAt, basis, snapshot, generation string) (*semantic.EventEnvelope, error) {
		if publishCalls == 0 {
			publishCalls++
			return approvalPublicationTestEnvelope(deliveries[0], time.Date(2026, 9, 7, 12, 34, 56, 123000000, time.UTC)), nil
		}
		if publishCalls == 1 && committed == deliveries[1].Outbox.CommittedEvent && committedAt == deliveries[1].Outbox.CommittedAt && basis == deliveries[1].ComputedBasisID && snapshot == deliveries[1].ValidatedSnapshotID && generation == deliveries[1].GenerationID {
			publishCalls++
			return nil, publishErr
		}
		t.Fatalf("unexpected publisher call %d: event=%+v", publishCalls, committed)
		return nil, nil
	}}

	if err := recoverPendingApprovalOutboxDeliveries(context.Background(), service, publisher); err == nil || !errors.Is(err, publishErr) {
		t.Fatalf("recovery error = %v, want publisher error %v", err, publishErr)
	}
	if service.pendingCalls != 1 {
		t.Fatalf("pending calls = %d, want 1", service.pendingCalls)
	}
	if publishCalls != 2 || len(service.marks) != 1 {
		t.Fatalf("publication calls = %d and mark calls = %d, want 2 and 1", publishCalls, len(service.marks))
	}
	wantOrder := []string{"publish:approval-event-1", "mark:outbox-1", "publish:approval-event-2"}
	if len(order) != len(wantOrder) {
		t.Fatalf("call order = %v, want %v", order, wantOrder)
	}
	for index, want := range wantOrder {
		if order[index] != want {
			t.Fatalf("call order = %v, want %v", order, wantOrder)
		}
	}
}

func TestApprovalPublicationRecoveryHandlesPendingErrorEmptyAndCanceled(t *testing.T) {
	pendingErr := errors.New("pending approval load failed")
	tests := []struct {
		name       string
		ctx        func() context.Context
		pending    []semantic.ApprovalOutboxDelivery
		pendingErr error
		wantErr    error
		wantNilErr bool
		wantCalls  int
	}{
		{
			name:       "pending error",
			pendingErr: pendingErr,
			wantErr:    pendingErr,
			wantCalls:  1,
		},
		{
			name:       "empty",
			pending:    make([]semantic.ApprovalOutboxDelivery, 0),
			wantNilErr: true,
			wantCalls:  1,
		},
		{
			name: "canceled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantErr:   context.Canceled,
			wantCalls: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &approvalPublicationServiceFake{pending: tt.pending, pendingErr: tt.pendingErr}
			publisher := &approvalPublicationPublisherFake{publish: func(semantic.ApprovalOutboxCommittedEventV1, string, string, string, string) (*semantic.EventEnvelope, error) {
				t.Fatal("publisher was called unexpectedly")
				return nil, nil
			}}
			ctx := context.Background()
			if tt.ctx != nil {
				ctx = tt.ctx()
			}

			err := recoverPendingApprovalOutboxDeliveries(ctx, service, publisher)
			if tt.wantNilErr {
				if err != nil {
					t.Fatalf("recovery error = %v, want nil", err)
				}
			} else if err == nil || !errors.Is(err, tt.wantErr) {
				t.Fatalf("recovery error = %v, want %v", err, tt.wantErr)
			}
			if service.pendingCalls != tt.wantCalls {
				t.Fatalf("pending calls = %d, want %d", service.pendingCalls, tt.wantCalls)
			}
			if len(service.marks) != 0 {
				t.Fatalf("mark calls = %d, want 0", len(service.marks))
			}
		})
	}
}

func TestApprovalPublicationRecoveryDeduplicatesPrepublishedEventAfterRestart(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	hub, err := NewDurableEventHub("approval-recovery", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable event hub: %v", err)
	}
	delivery := approvalPublicationTestDelivery()
	first, err := hub.PublishApprovalUpdated(delivery.Outbox.CommittedEvent, delivery.Outbox.CommittedAt, delivery.ComputedBasisID, delivery.ValidatedSnapshotID, delivery.GenerationID)
	if err != nil {
		t.Fatalf("prepublish approval event: %v", err)
	}
	if first == nil || first.Sequence != 1 {
		t.Fatalf("prepublished envelope = %+v, want sequence 1", first)
	}

	restarted, err := NewDurableEventHub("approval-recovery", 4, ledgerPath)
	if err != nil {
		t.Fatalf("restart durable event hub: %v", err)
	}
	ch, replay, needsSnapshotSync, cancel := restarted.Subscribe(first.EventID)
	defer cancel()
	if len(replay) != 0 || needsSnapshotSync {
		t.Fatalf("subscribe after restart replay=%+v needsSnapshotSync=%v, want no replay", replay, needsSnapshotSync)
	}

	service := &approvalPublicationServiceFake{pending: []semantic.ApprovalOutboxDelivery{delivery}}
	service.markFunc = func(markedDelivery semantic.ApprovalOutboxDelivery, publishedAt time.Time) (semantic.ApprovalOutboxV1, error) {
		marked := approvalPublicationPublishedOutbox(markedDelivery)
		marked.PublishedAt = publishedAt.Format(time.RFC3339Nano)
		return marked, nil
	}
	publisher := &approvalPublicationPublisherFake{publish: func(committed semantic.ApprovalOutboxCommittedEventV1, committedAt, basis, snapshot, generation string) (*semantic.EventEnvelope, error) {
		return restarted.PublishApprovalUpdated(committed, committedAt, basis, snapshot, generation)
	}}
	if err := recoverPendingApprovalOutboxDeliveries(context.Background(), service, publisher); err != nil {
		t.Fatalf("recover prepublished approval event: %v", err)
	}
	if service.pendingCalls != 1 || len(service.marks) != 1 {
		t.Fatalf("pending calls=%d marks=%d, want one each", service.pendingCalls, len(service.marks))
	}
	if service.marks[0] != delivery || len(service.markTimes) != 1 || !service.markTimes[0].Equal(first.OccurredAt) {
		t.Fatalf("mark delivery/time = %+v/%v, want exact delivery and %v", service.marks[0], service.markTimes, first.OccurredAt)
	}
	if restarted.headSeq != 1 || len(restarted.ringBuffer) != 1 {
		t.Fatalf("recovery changed restarted hub state: head=%d ring=%d", restarted.headSeq, len(restarted.ringBuffer))
	}
	select {
	case got := <-ch:
		t.Fatalf("deduplicated recovery broadcast an event: %+v", got)
	default:
	}

	ledger, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read approval ledger: %v", err)
	}
	if len(ledger) == 0 || ledger[len(ledger)-1] != '\n' || bytes.Count(ledger, []byte{'\n'}) != 1 {
		t.Fatalf("approval ledger has unexpected records: %q", ledger)
	}
	replayed, err := restarted.PublishApprovalUpdated(delivery.Outbox.CommittedEvent, delivery.Outbox.CommittedAt, delivery.ComputedBasisID, delivery.ValidatedSnapshotID, delivery.GenerationID)
	if err != nil {
		t.Fatalf("typed replay after recovery: %v", err)
	}
	if !reflect.DeepEqual(first, replayed) || restarted.headSeq != 1 {
		t.Fatalf("typed replay changed envelope or sequence: first=%+v replayed=%+v head=%d", first, replayed, restarted.headSeq)
	}
}

func TestApprovalPublicationRecoveryMarksClockRollbackEventAfterDurableRestart(t *testing.T) {
	srv, root, _ := newFlowViewApprovalHistoryFixture(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	service, err := semantic.NewApprovalExecutionService(root, srv.engine, srv.proposalStore)
	if err != nil {
		t.Fatalf("create approval execution service: %v", err)
	}
	deliveries, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("load pending approval deliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("pending approval deliveries = %d, want 1", len(deliveries))
	}
	delivery := deliveries[0]
	committedAt, err := time.Parse(time.RFC3339Nano, delivery.Outbox.CommittedAt)
	if err != nil {
		t.Fatalf("parse committedAt: %v", err)
	}
	srv.hub.now = func() time.Time { return committedAt.Add(-time.Hour) }
	ch, _, _, cancel := srv.hub.Subscribe("")
	defer cancel()
	ledgerBeforePublish, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read ledger before publication: %v", err)
	}
	headBeforePublish := srv.hub.headSeq
	approvalRecordsBefore := bytes.Count(ledgerBeforePublish, []byte(`"eventType":"approval.updated"`))

	first, err := srv.hub.PublishApprovalUpdated(delivery.Outbox.CommittedEvent, delivery.Outbox.CommittedAt, delivery.ComputedBasisID, delivery.ValidatedSnapshotID, delivery.GenerationID)
	if err != nil {
		t.Fatalf("publish pending approval event: %v", err)
	}
	if first == nil || first.OccurredAt.Format(time.RFC3339Nano) != delivery.Outbox.CommittedAt {
		t.Fatalf("published occurredAt = %+v, want committedAt %q", first, delivery.Outbox.CommittedAt)
	}
	if got := receiveEvent(t, ch); got == nil || got.EventID != first.EventID || got.OccurredAt.Format(time.RFC3339Nano) != delivery.Outbox.CommittedAt {
		t.Fatalf("first approval broadcast = %+v, want one clamped event", got)
	}
	ledgerBeforeRecovery, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read ledger after publication: %v", err)
	}
	if srv.hub.headSeq != headBeforePublish+1 || bytes.Count(ledgerBeforeRecovery, []byte(`"eventType":"approval.updated"`)) != approvalRecordsBefore+1 {
		t.Fatalf("first approval publication state: head=%d ledger approvals=%d, want head=%d approvals=%d", srv.hub.headSeq, bytes.Count(ledgerBeforeRecovery, []byte(`"eventType":"approval.updated"`)), headBeforePublish+1, approvalRecordsBefore+1)
	}

	restarted, err := NewDurableEventHub("flowview-live-stream", 100, srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("restart approval event hub: %v", err)
	}
	restartedCh, replay, needsSnapshotSync, cancelRestart := restarted.Subscribe(first.EventID)
	defer cancelRestart()
	if len(replay) != 0 || needsSnapshotSync {
		t.Fatalf("restart subscription replay=%+v needsSnapshotSync=%v, want no replay", replay, needsSnapshotSync)
	}
	if restarted.headSeq != srv.hub.headSeq || len(restarted.ringBuffer) != len(srv.hub.ringBuffer) {
		t.Fatalf("restart state head=%d ring=%d, want head=%d ring=%d", restarted.headSeq, len(restarted.ringBuffer), srv.hub.headSeq, len(srv.hub.ringBuffer))
	}

	if err := recoverPendingApprovalOutboxDeliveries(context.Background(), service, restarted); err != nil {
		t.Fatalf("recover pending approval delivery: %v", err)
	}
	snapshot, err := semantic.NewApprovalTransactionStore(root, srv.engine).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after recovery: %v", err)
	}
	if len(snapshot.Outbox) != 1 || snapshot.Outbox[0].DeliveryState != "published" || snapshot.Outbox[0].PublishedAt != first.OccurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("recovered outbox = %+v, want publishedAt %q", snapshot.Outbox, first.OccurredAt.Format(time.RFC3339Nano))
	}
	remaining, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("load pending deliveries after recovery: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("pending deliveries after recovery = %d, want 0", len(remaining))
	}
	ledgerAfterRecovery, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read ledger after recovery: %v", err)
	}
	if !bytes.Equal(ledgerBeforeRecovery, ledgerAfterRecovery) || restarted.headSeq != srv.hub.headSeq || bytes.Count(ledgerAfterRecovery, []byte(`"eventType":"approval.updated"`)) != approvalRecordsBefore+1 {
		t.Fatalf("recovery changed publication state: head=%d ledger=%q", restarted.headSeq, ledgerAfterRecovery)
	}
	select {
	case got := <-restartedCh:
		t.Fatalf("deduplicated recovery broadcast an event: %+v", got)
	default:
	}

	if err := recoverPendingApprovalOutboxDeliveries(context.Background(), service, restarted); err != nil {
		t.Fatalf("repeat recovery: %v", err)
	}
	ledgerAfterRepeat, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read ledger after repeat recovery: %v", err)
	}
	if !bytes.Equal(ledgerAfterRecovery, ledgerAfterRepeat) || restarted.headSeq != srv.hub.headSeq {
		t.Fatalf("repeat recovery changed state: head=%d ledger=%q", restarted.headSeq, ledgerAfterRepeat)
	}
	select {
	case got := <-restartedCh:
		t.Fatalf("repeat recovery broadcast an event: %+v", got)
	default:
	}
}

func TestApprovalPublicationDispatchReturnsMarkFailureAndRejectsBadMarkedOutbox(t *testing.T) {
	delivery := approvalPublicationTestDelivery()
	occurredAt := time.Date(2026, 9, 7, 12, 34, 56, 123000000, time.UTC)
	tests := []struct {
		name    string
		markErr error
		marked  semantic.ApprovalOutboxV1
	}{
		{
			name:    "mark error",
			markErr: errors.New("mark unavailable"),
			marked:  semantic.ApprovalOutboxV1{},
		},
		{
			name:   "pending result",
			marked: delivery.Outbox,
		},
		{
			name: "identity mismatch",
			marked: func() semantic.ApprovalOutboxV1 {
				marked := approvalPublicationPublishedOutbox(delivery)
				marked.PublishedAt = occurredAt.Format(time.RFC3339Nano)
				marked.AggregateID = "aggregate-other"
				return marked
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &approvalPublicationServiceFake{marked: tt.marked, markErr: tt.markErr}
			publishCalls := 0
			publisher := &approvalPublicationPublisherFake{publish: func(semantic.ApprovalOutboxCommittedEventV1, string, string, string, string) (*semantic.EventEnvelope, error) {
				publishCalls++
				return approvalPublicationTestEnvelope(delivery, occurredAt), nil
			}}

			marked, err := dispatchApprovalOutboxDelivery(context.Background(), service, publisher, delivery)
			if marked != (semantic.ApprovalOutboxV1{}) {
				t.Fatalf("dispatch returned non-zero outbox after mark failure: %+v", marked)
			}
			if err == nil {
				t.Fatal("dispatch unexpectedly succeeded")
			}
			if tt.markErr != nil && !errors.Is(err, tt.markErr) {
				t.Fatalf("dispatch error = %v, want cause %v", err, tt.markErr)
			}
			if publishCalls != 1 {
				t.Fatalf("publish calls = %d, want 1", publishCalls)
			}
			if len(service.marks) != 1 {
				t.Fatalf("mark calls = %d, want 1", len(service.marks))
			}
			if len(service.markTimes) != 1 || !service.markTimes[0].Equal(occurredAt) {
				t.Fatalf("mark times = %v, want envelope time %v", service.markTimes, occurredAt)
			}
		})
	}
}

func approvalPublicationTestDelivery() semantic.ApprovalOutboxDelivery {
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	return semantic.ApprovalOutboxDelivery{
		Outbox: semantic.ApprovalOutboxV1{
			SchemaID: "https://codeflow.local/schemas/rflsc.approval-outbox.v1.schema.json", SchemaVersion: 1,
			OutboxID: "outbox-1", EventID: committed.EventID, AggregateID: committed.AggregateID,
			AggregateVersion: committed.AggregateVersion, WorkspaceID: committed.WorkspaceID,
			PayloadDigest: committed.PayloadDigest, CommittedAt: validApprovalOutboxCommittedAt(), DeliveryState: "pending",
			CommittedEvent: committed,
		},
		ComputedBasisID: basis, ValidatedSnapshotID: snapshot, GenerationID: generation,
	}
}

func approvalPublicationTestDeliveries() []semantic.ApprovalOutboxDelivery {
	base := approvalPublicationTestDelivery()
	deliveries := make([]semantic.ApprovalOutboxDelivery, 3)
	for index, suffix := range []string{"1", "2", "3"} {
		delivery := base
		delivery.Outbox.OutboxID = "outbox-" + suffix
		delivery.Outbox.EventID = "approval-event-" + suffix
		delivery.Outbox.AggregateID = "aggregate-" + suffix
		delivery.Outbox.WorkspaceID = "workspace-" + suffix
		delivery.Outbox.PayloadDigest = delivery.Outbox.PayloadDigest[:len(delivery.Outbox.PayloadDigest)-1] + suffix
		delivery.Outbox.CommittedEvent.EventID = delivery.Outbox.EventID
		delivery.Outbox.CommittedEvent.ApprovalID = "approval-" + suffix
		delivery.Outbox.CommittedEvent.AggregateID = delivery.Outbox.AggregateID
		delivery.Outbox.CommittedEvent.WorkspaceID = delivery.Outbox.WorkspaceID
		delivery.Outbox.CommittedEvent.PayloadDigest = delivery.Outbox.PayloadDigest
		delivery.ComputedBasisID = delivery.ComputedBasisID[:63] + suffix
		delivery.ValidatedSnapshotID = "snapshot-" + suffix
		delivery.GenerationID = "generation-" + suffix
		deliveries[index] = delivery
	}
	return deliveries
}

func approvalPublicationPublishedOutbox(delivery semantic.ApprovalOutboxDelivery) semantic.ApprovalOutboxV1 {
	published := delivery.Outbox
	published.DeliveryState = "published"
	published.PublishedAt = "2026-09-07T12:34:56.123Z"
	return published
}

func approvalPublicationTestEnvelope(delivery semantic.ApprovalOutboxDelivery, occurredAt time.Time) *semantic.EventEnvelope {
	basis, snapshot, generation := delivery.ComputedBasisID, delivery.ValidatedSnapshotID, delivery.GenerationID
	return &semantic.EventEnvelope{
		SchemaID: semantic.EventEnvelopeSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		StreamID: "approval-publication-test", Sequence: 1, EventID: "event-1", EventType: "approval.updated", OccurredAt: occurredAt,
		ComputedBasisID: &basis, ValidatedAgainstSnapshotID: &snapshot, GenerationID: &generation,
		Data: approvalUpdatedEventData{ApprovalEvent: delivery.Outbox.CommittedEvent, ComputedBasisID: basis, ValidatedAgainstSnapshotID: snapshot, GenerationID: generation, CommittedAt: delivery.Outbox.CommittedAt},
	}
}
