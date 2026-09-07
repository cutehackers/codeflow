package semantic

import (
	"context"
	"errors"
)

// ApprovalConflictCurrentVersion projects only a known committed aggregate
// version from a typed execution conflict. Adapters retain their existing
// authentication and error classification before calling this projection.
// Conflicts without aggregate metadata must not manufacture version zero.
func ApprovalConflictCurrentVersion(err error) (int64, bool) {
	if !errors.Is(err, ErrApprovalExecutionConflict) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrApprovalUnauthenticated) || errors.Is(err, ErrApprovalUnauthorized) {
		return 0, false
	}
	var state string
	var version int64
	var lifecycle *ApprovalLifecycleError
	var transaction *ApprovalTransactionError
	switch {
	case errors.As(err, &lifecycle) && lifecycle != nil && (lifecycle.Kind == "conflict" || lifecycle.Kind == "reference"):
		state, version = lifecycle.CurrentState, lifecycle.CurrentVersion
	case errors.As(err, &transaction) && transaction != nil && transaction.Kind == "conflict":
		state, version = transaction.CurrentState, transaction.CurrentVersion
	default:
		return 0, false
	}
	if version < 1 || version > approvalLifecycleMaxVersion {
		return 0, false
	}
	switch state {
	case "active", "rejected", "revoked", "superseded":
		return version, true
	default:
		return 0, false
	}
}
