package semantic

import (
	"context"
	"errors"
	"reflect"
	"time"
)

// ApprovalOutboxDelivery is the semantic-owned, read-only projection used by
// a delivery coordinator after a restart. It contains only a defensive
// outbox value and the persisted proof identities needed to bind delivery.
// Proposal text, evidence, maps, seals, actors, and paths do not cross this
// boundary.
type ApprovalOutboxDelivery struct {
	Outbox              ApprovalOutboxV1 `json:"outbox"`
	ComputedBasisID     string           `json:"computedBasisId"`
	ValidatedSnapshotID string           `json:"validatedSnapshotId"`
	GenerationID        string           `json:"generationId"`
}

// PendingApprovalOutboxDeliveries returns pending durable approval outboxes
// in their persisted append order. It snapshots and validates the complete
// transaction state before projecting any record and never performs a
// proposal lookup or durable mutation.
func (s *ApprovalExecutionService) PendingApprovalOutboxDeliveries(ctx context.Context) ([]ApprovalOutboxDelivery, error) {
	if s == nil || s.engine == nil || s.transactions == nil || s.repoRoot == "" || s.engine.CanonicalRoot() != s.repoRoot {
		return nil, approvalExecutionInvalid(nil)
	}
	if err := approvalContextError(ctx); err != nil {
		return nil, err
	}
	snapshot, err := s.transactions.Snapshot(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, approvalExecutionUnavailable(nil)
	}
	if err := approvalContextError(ctx); err != nil {
		return nil, err
	}
	return projectPendingApprovalOutboxDeliveries(snapshot)
}

func projectPendingApprovalOutboxDeliveries(snapshot ApprovalTransactionSnapshot) ([]ApprovalOutboxDelivery, error) {
	return projectApprovalOutboxDeliveries(snapshot, true)
}

func projectApprovalOutboxDeliveries(snapshot ApprovalTransactionSnapshot, pendingOnly bool) ([]ApprovalOutboxDelivery, error) {
	state := approvalTransactionState{
		StoreVersion:       approvalTransactionStoreVersion,
		Events:             append([]ApprovalEventV2(nil), snapshot.Events...),
		Aggregates:         append([]ApprovalAggregateV2(nil), snapshot.Aggregates...),
		IdempotencyResults: append([]ApprovalIdempotencyResultV1(nil), snapshot.IdempotencyResults...),
		Outbox:             append([]ApprovalOutboxV1(nil), snapshot.Outbox...),
		Targets:            append([]approvalTransactionTargetRecord(nil), snapshot.targets...),
	}
	if err := validateApprovalTransactionState(state); err != nil {
		return nil, approvalExecutionUnavailable(nil)
	}
	if err := replayApprovalTransactionState(state); err != nil {
		return nil, approvalExecutionUnavailable(nil)
	}

	deliveries := make([]ApprovalOutboxDelivery, 0, len(snapshot.Outbox))
	for _, outbox := range snapshot.Outbox {
		if pendingOnly && outbox.DeliveryState != "pending" {
			continue
		}
		selected, matches := approvalOutboxDeliveryTarget(snapshot.targets, outbox)
		if matches != 1 || selected == nil {
			return nil, approvalExecutionUnavailable(nil)
		}
		if err := validateApprovalOutboxDeliveryBinding(*selected, snapshot.Events, outbox); err != nil {
			return nil, approvalExecutionUnavailable(nil)
		}
		deliveries = append(deliveries, ApprovalOutboxDelivery{
			Outbox:              cloneApprovalOutbox(outbox),
			ComputedBasisID:     selected.Target.ComputedBasisID,
			ValidatedSnapshotID: selected.Target.ValidatedSnapshotID,
			GenerationID:        selected.Target.GenerationID,
		})
	}
	return deliveries, nil
}

// MarkApprovalOutboxDeliveryPublished advances one exact projected pending
// delivery after its external publication succeeds. It also accepts an
// exact replay of the original pending projection after the durable record is
// already published, returning the persisted publication without another
// write.
func (s *ApprovalExecutionService) MarkApprovalOutboxDeliveryPublished(ctx context.Context, delivery ApprovalOutboxDelivery, publishedAt time.Time) (ApprovalOutboxV1, error) {
	var zero ApprovalOutboxV1
	if s == nil || s.engine == nil || s.transactions == nil || s.repoRoot == "" || s.engine.CanonicalRoot() != s.repoRoot {
		return zero, approvalExecutionInvalid(nil)
	}
	if err := approvalContextError(ctx); err != nil {
		return zero, err
	}
	if !validApprovalOutboxDeliveryInput(delivery, publishedAt) {
		return zero, approvalExecutionInvalid(nil)
	}
	publishedAtValue := publishedAt.Format(time.RFC3339Nano)

	pending, err := s.PendingApprovalOutboxDeliveries(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return zero, err
		}
		return zero, err
	}
	pendingMatch, pendingMatches := approvalOutboxDeliveryMatchByID(pending, delivery)
	if pendingMatches > 1 {
		return zero, approvalExecutionInvalid(nil)
	}
	if pendingMatches == 1 {
		if !reflect.DeepEqual(pendingMatch, delivery) {
			return zero, approvalExecutionInvalid(nil)
		}
		if err := approvalContextError(ctx); err != nil {
			return zero, err
		}
		published, err := s.transactions.MarkApprovalOutboxPublished(ctx, pendingMatch.Outbox.OutboxID, pendingMatch.Outbox.EventID, pendingMatch.Outbox.AggregateID, publishedAtValue)
		if err != nil {
			return zero, approvalExecutionPublicationError(err)
		}
		if validateApprovalTransactionOutbox(published) != nil || published.DeliveryState != "published" || published.OutboxID != pendingMatch.Outbox.OutboxID || published.EventID != pendingMatch.Outbox.EventID || published.AggregateID != pendingMatch.Outbox.AggregateID {
			return zero, approvalExecutionUnavailable(nil)
		}
		return cloneApprovalOutbox(published), nil
	}

	if err := approvalContextError(ctx); err != nil {
		return zero, err
	}
	snapshot, err := s.transactions.Snapshot(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return zero, err
		}
		return zero, approvalExecutionUnavailable(nil)
	}
	if err := approvalContextError(ctx); err != nil {
		return zero, err
	}
	all, err := projectApprovalOutboxDeliveries(snapshot, false)
	if err != nil {
		return zero, err
	}
	for _, candidate := range all {
		if candidate.Outbox.OutboxID != delivery.Outbox.OutboxID {
			continue
		}
		if candidate.Outbox.DeliveryState != "published" || !approvalOutboxDeliveryPublishedReplayMatches(candidate, delivery) {
			return zero, approvalExecutionInvalid(nil)
		}
		return cloneApprovalOutbox(candidate.Outbox), nil
	}
	return zero, approvalExecutionInvalid(nil)
}

func validApprovalOutboxDeliveryInput(delivery ApprovalOutboxDelivery, publishedAt time.Time) bool {
	if publishedAt.IsZero() || publishedAt.Location() != time.UTC || validateApprovalTransactionOutbox(delivery.Outbox) != nil || !validApprovalLifecycleID(delivery.ComputedBasisID) || !validApprovalLifecycleID(delivery.ValidatedSnapshotID) || !validApprovalLifecycleID(delivery.GenerationID) {
		return false
	}
	publishedAtValue := publishedAt.Format(time.RFC3339Nano)
	return validApprovalTransactionPublishedAt(publishedAtValue) && approvalTransactionPublishedAtNotBeforeCommittedAt(publishedAtValue, delivery.Outbox.CommittedAt)
}

func approvalOutboxDeliveryMatchByID(deliveries []ApprovalOutboxDelivery, wanted ApprovalOutboxDelivery) (ApprovalOutboxDelivery, int) {
	var matched ApprovalOutboxDelivery
	matches := 0
	for _, candidate := range deliveries {
		if candidate.Outbox.OutboxID != wanted.Outbox.OutboxID {
			continue
		}
		matched = candidate
		matches++
	}
	return matched, matches
}

func approvalOutboxDeliveryPublishedReplayMatches(persisted, supplied ApprovalOutboxDelivery) bool {
	if reflect.DeepEqual(persisted, supplied) {
		return true
	}
	if supplied.Outbox.DeliveryState != "pending" || supplied.Outbox.PublishedAt != "" || supplied.Outbox.FailureReason != "" {
		return false
	}
	comparable := persisted
	comparable.Outbox.DeliveryState = "pending"
	comparable.Outbox.PublishedAt = ""
	return reflect.DeepEqual(comparable, supplied)
}

func approvalExecutionPublicationError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, ErrApprovalTransactionInvalid) {
		return approvalExecutionInvalid(nil)
	}
	return approvalExecutionUnavailable(nil)
}

func approvalOutboxDeliveryTarget(targets []approvalTransactionTargetRecord, outbox ApprovalOutboxV1) (*approvalTransactionTargetRecord, int) {
	var selected *approvalTransactionTargetRecord
	matches := 0
	for index := range targets {
		candidate := targets[index]
		if candidate.Target.WorkspaceID != outbox.WorkspaceID || candidate.Aggregate.AggregateID != outbox.AggregateID {
			continue
		}
		matches++
		candidate.Aggregate = cloneApprovalAggregateV2(candidate.Aggregate)
		selected = &candidate
	}
	return selected, matches
}

func validateApprovalOutboxDeliveryBinding(target approvalTransactionTargetRecord, events []ApprovalEventV2, outbox ApprovalOutboxV1) error {
	if !validApprovalTransactionTarget(target.Target) || !validApprovalTransactionTargetDigest(target.Target, target.TargetDigest) || !approvalTransactionTargetAggregateIdentityMatches(target.Target, target.Aggregate) || target.Target.WorkspaceID != outbox.WorkspaceID || target.Aggregate.AggregateID != outbox.AggregateID || target.Aggregate.WorkspaceID != outbox.WorkspaceID || !validApprovalLifecycleID(target.Target.ComputedBasisID) || !validApprovalLifecycleID(target.Target.ValidatedSnapshotID) || !validApprovalLifecycleID(target.Target.GenerationID) {
		return errors.New("approval outbox target identity is inconsistent")
	}
	var matched *ApprovalEventV2
	for index := range events {
		if events[index].EventID != outbox.EventID {
			continue
		}
		if matched != nil {
			return errors.New("approval outbox event is ambiguous")
		}
		candidate := events[index]
		matched = &candidate
	}
	if matched == nil {
		return errors.New("approval outbox event is unavailable")
	}
	if matched.AggregateID != target.Aggregate.AggregateID || matched.WorkspaceID != target.Target.WorkspaceID || matched.ProposalID != target.Target.ProposalID || matched.EvidencePackID != target.Target.EvidencePackID || matched.ComputedBasisID != target.Target.ComputedBasisID || matched.GenerationID != target.Target.GenerationID || matched.IntentRevision != target.Target.IntentRevision || outbox.EventID != matched.EventID || outbox.AggregateID != matched.AggregateID || outbox.AggregateVersion != matched.AggregateVersion || outbox.WorkspaceID != matched.WorkspaceID || outbox.CommittedAt != matched.Timestamp || outbox.CommittedEvent.EventID != matched.EventID || outbox.CommittedEvent.ApprovalID != matched.ApprovalID || outbox.CommittedEvent.AggregateID != matched.AggregateID || outbox.CommittedEvent.AggregateVersion != matched.AggregateVersion || outbox.CommittedEvent.WorkspaceID != matched.WorkspaceID || outbox.CommittedEvent.Decision != matched.Decision || outbox.CommittedEvent.PayloadDigest != outbox.PayloadDigest {
		return errors.New("approval outbox binding is inconsistent")
	}
	return nil
}
