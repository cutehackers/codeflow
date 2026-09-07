package flowview

import (
	"context"
	"errors"

	"codeflow/internal/semantic"
)

// SubmitSemanticApproval executes one already-authorized approval command and
// completes delivery of a durable pending replay before returning it.
func (s *Server) SubmitSemanticApproval(ctx context.Context, access semantic.ApprovalAccess, draft semantic.ApprovalCommandDraft) (semantic.ApprovalExecutionResult, error) {
	var zero semantic.ApprovalExecutionResult
	if s == nil || s.approvalService == nil || s.hub == nil {
		return zero, semantic.ErrApprovalExecutionUnavailable
	}

	result, err := s.approvalService.Execute(ctx, access, draft)
	if err != nil {
		return zero, err
	}

	switch result.Receipt.Outbox.DeliveryState {
	case "published":
		return result, nil
	case "pending":
		delivery := semantic.ApprovalOutboxDelivery{
			Outbox:              result.Receipt.Outbox,
			ComputedBasisID:     result.ComputedBasisID,
			ValidatedSnapshotID: result.ValidatedSnapshotID,
			GenerationID:        result.GenerationID,
		}
		published, err := dispatchApprovalOutboxDelivery(ctx, s.approvalService, s.hub, delivery)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return zero, err
			}
			return zero, semantic.ErrApprovalExecutionUnavailable
		}
		result.Receipt.Outbox = published
		return result, nil
	default:
		return zero, semantic.ErrApprovalExecutionUnavailable
	}
}
