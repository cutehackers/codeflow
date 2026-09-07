package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/semantic"
)

type approvalOutboxDeliveryService interface {
	PendingApprovalOutboxDeliveries(context.Context) ([]semantic.ApprovalOutboxDelivery, error)
	MarkApprovalOutboxDeliveryPublished(context.Context, semantic.ApprovalOutboxDelivery, time.Time) (semantic.ApprovalOutboxV1, error)
}

type approvalOutboxDeliveryPublisher interface {
	PublishApprovalUpdated(semantic.ApprovalOutboxCommittedEventV1, string, string, string, string) (*semantic.EventEnvelope, error)
}

var (
	errApprovalOutboxDispatchInvalid     = errors.New("approval outbox dispatch invalid")
	errApprovalOutboxDispatchUnavailable = errors.New("approval outbox dispatch unavailable")
)

type approvalOutboxDispatchError struct {
	invalid bool
	cause   error
}

func (e *approvalOutboxDispatchError) Error() string {
	if e == nil || e.invalid {
		return errApprovalOutboxDispatchInvalid.Error()
	}
	return errApprovalOutboxDispatchUnavailable.Error()
}

func (e *approvalOutboxDispatchError) Unwrap() error {
	if e == nil {
		return errApprovalOutboxDispatchInvalid
	}
	base := errApprovalOutboxDispatchUnavailable
	if e.invalid {
		base = errApprovalOutboxDispatchInvalid
	}
	if e.cause == nil {
		return base
	}
	return errors.Join(base, e.cause)
}

func approvalOutboxDispatchInvalid() error {
	return &approvalOutboxDispatchError{invalid: true}
}

func approvalOutboxDispatchUnavailable(cause error) error {
	return &approvalOutboxDispatchError{cause: cause}
}

func dispatchApprovalOutboxDelivery(ctx context.Context, service approvalOutboxDeliveryService, publisher approvalOutboxDeliveryPublisher, delivery semantic.ApprovalOutboxDelivery) (semantic.ApprovalOutboxV1, error) {
	var zero semantic.ApprovalOutboxV1
	if ctx == nil || service == nil || publisher == nil {
		return zero, approvalOutboxDispatchInvalid()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !validApprovalOutboxDeliveryForDispatch(delivery) {
		return zero, approvalOutboxDispatchInvalid()
	}
	envelope, err := publisher.PublishApprovalUpdated(delivery.Outbox.CommittedEvent, delivery.Outbox.CommittedAt, delivery.ComputedBasisID, delivery.ValidatedSnapshotID, delivery.GenerationID)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return zero, err
		}
		return zero, approvalOutboxDispatchUnavailable(err)
	}
	if envelope == nil {
		return zero, approvalOutboxDispatchInvalid()
	}
	if err := validateApprovalUpdatedDispatchEnvelope(envelope, delivery); err != nil {
		return zero, approvalOutboxDispatchInvalid()
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	marked, err := service.MarkApprovalOutboxDeliveryPublished(ctx, delivery, envelope.OccurredAt)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return zero, err
		}
		return zero, approvalOutboxDispatchUnavailable(err)
	}
	if !validApprovalPublishedOutbox(marked, delivery.Outbox, envelope.OccurredAt) {
		return zero, approvalOutboxDispatchInvalid()
	}
	return marked, nil
}

func recoverPendingApprovalOutboxDeliveries(ctx context.Context, service approvalOutboxDeliveryService, publisher approvalOutboxDeliveryPublisher) error {
	if ctx == nil || service == nil || publisher == nil {
		return approvalOutboxDispatchInvalid()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	deliveries, err := service.PendingApprovalOutboxDeliveries(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return approvalOutboxDispatchUnavailable(err)
	}
	for _, delivery := range deliveries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := dispatchApprovalOutboxDelivery(ctx, service, publisher, delivery); err != nil {
			return err
		}
	}
	return nil
}

func validApprovalOutboxDeliveryForDispatch(delivery semantic.ApprovalOutboxDelivery) bool {
	outbox := delivery.Outbox
	if outbox.SchemaID != contractharness.ApprovalOutboxV1SchemaID || outbox.SchemaVersion != 1 || outbox.DeliveryState != "pending" || outbox.PublishedAt != "" || outbox.FailureReason != "" {
		return false
	}
	encoded, err := json.Marshal(outbox)
	if err != nil || contractharness.ValidateVS09Contract(contractharness.ApprovalOutboxV1SchemaID, encoded) != nil {
		return false
	}
	for _, value := range []string{outbox.OutboxID, outbox.EventID, outbox.AggregateID, outbox.WorkspaceID} {
		if !semantic.ValidApprovalLifecycleID(value) {
			return false
		}
	}
	committedAt, err := time.Parse(time.RFC3339Nano, outbox.CommittedAt)
	if err != nil || committedAt.Location() != time.UTC || committedAt.Format(time.RFC3339Nano) != outbox.CommittedAt {
		return false
	}
	if validateApprovalUpdatedCommittedEvent(outbox.CommittedEvent) != nil || outbox.EventID != outbox.CommittedEvent.EventID || outbox.AggregateID != outbox.CommittedEvent.AggregateID || outbox.AggregateVersion != outbox.CommittedEvent.AggregateVersion || outbox.WorkspaceID != outbox.CommittedEvent.WorkspaceID || outbox.PayloadDigest != outbox.CommittedEvent.PayloadDigest {
		return false
	}
	return isCanonicalBasisID(delivery.ComputedBasisID) && semantic.ValidApprovalLifecycleID(delivery.ValidatedSnapshotID) && semantic.ValidApprovalLifecycleID(delivery.GenerationID)
}

func validateApprovalUpdatedDispatchEnvelope(envelope *semantic.EventEnvelope, delivery semantic.ApprovalOutboxDelivery) error {
	if err := validateCanonicalEventEnvelope(envelope); err != nil || envelope.EventType != "approval.updated" || envelope.OccurredAt.Location() != time.UTC || envelope.OccurredAt.Format(time.RFC3339Nano) != envelope.OccurredAt.UTC().Format(time.RFC3339Nano) {
		return errors.New("approval.updated envelope is not canonical")
	}
	publication, err := approvalUpdatedPublicationFromEnvelope(envelope)
	if err != nil {
		return err
	}
	data, _, err := validateApprovalUpdatedData(envelope.Data)
	if err != nil {
		return err
	}
	if publication.approvalEventID != delivery.Outbox.CommittedEvent.EventID || !reflect.DeepEqual(data.ApprovalEvent, delivery.Outbox.CommittedEvent) || data.ComputedBasisID != delivery.ComputedBasisID || data.ValidatedAgainstSnapshotID != delivery.ValidatedSnapshotID || data.GenerationID != delivery.GenerationID || data.CommittedAt != delivery.Outbox.CommittedAt {
		return errors.New("approval.updated envelope does not match outbox delivery")
	}
	if envelope.ComputedBasisID == nil || envelope.ValidatedAgainstSnapshotID == nil || envelope.GenerationID == nil || *envelope.ComputedBasisID != delivery.ComputedBasisID || *envelope.ValidatedAgainstSnapshotID != delivery.ValidatedSnapshotID || *envelope.GenerationID != delivery.GenerationID {
		return errors.New("approval.updated envelope identities do not match outbox delivery")
	}
	return nil
}

func validApprovalPublishedOutbox(published, pending semantic.ApprovalOutboxV1, occurredAt time.Time) bool {
	if published.DeliveryState != "published" || published.PublishedAt != occurredAt.Format(time.RFC3339Nano) || published.FailureReason != "" || occurredAt.Location() != time.UTC {
		return false
	}
	expected := pending
	expected.DeliveryState = "published"
	expected.PublishedAt = published.PublishedAt
	expected.FailureReason = ""
	if !reflect.DeepEqual(published, expected) {
		return false
	}
	encoded, err := json.Marshal(published)
	return err == nil && contractharness.ValidateVS09Contract(contractharness.ApprovalOutboxV1SchemaID, encoded) == nil
}
