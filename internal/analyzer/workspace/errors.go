package workspace

import (
	"errors"
	"fmt"
)

var (
	// ErrDocumentVersionConflict identifies stale or duplicate edits. Callers
	// can retry after reading the latest revision for the path.
	ErrDocumentVersionConflict = errors.New("document version conflict")
	// ErrSnapshotPathNotFound is returned when a path was not part of a
	// snapshot. Snapshot VFS never falls back to the live filesystem.
	ErrSnapshotPathNotFound = errors.New("snapshot path not found")
	// ErrSourceIntegrityViolation identifies a CodeFlow source write attempt.
	ErrSourceIntegrityViolation = errors.New("source_integrity_violation")
	// ErrIncompatibleEpoch identifies a legacy string epoch at a canonical
	// integer boundary.
	ErrIncompatibleEpoch = errors.New("incompatible workspace epoch")
	// ErrInvalidDocumentVersion identifies versioned ingress that does not carry
	// a positive document version. Only watcher fallback may synthesize one.
	ErrInvalidDocumentVersion = errors.New("invalid document version")
	// ErrCaptureConflict identifies a whole-tree capture that observed more
	// than one filesystem state.
	ErrCaptureConflict = errors.New("workspace capture conflict")
	// ErrInvalidReconciliationTarget identifies a malformed watcher
	// reconciliation request.
	ErrInvalidReconciliationTarget = errors.New("invalid reconciliation target")
)

// PathPolicyError reports a repository-relative path policy violation.
type PathPolicyError struct {
	Path   string
	Reason string
}

func (e *PathPolicyError) Error() string {
	return fmt.Sprintf("path policy violation for %q: %s", e.Path, e.Reason)
}

// Is allows callers to classify all path-policy failures without parsing the
// diagnostic text.
func (e *PathPolicyError) Is(target error) bool {
	_, ok := target.(*PathPolicyError)
	return ok
}

// DocumentVersionConflict reports the accepted version and the current
// version for a path.
type DocumentVersionConflict struct {
	Path            string
	IncomingVersion int
	CurrentVersion  int
}

func (e *DocumentVersionConflict) Error() string {
	return fmt.Sprintf("%v: path %s documentVersion %d <= current %d", ErrDocumentVersionConflict, e.Path, e.IncomingVersion, e.CurrentVersion)
}

func (e *DocumentVersionConflict) Unwrap() error { return ErrDocumentVersionConflict }

func (e *DocumentVersionConflict) Is(target error) bool {
	return target == ErrDocumentVersionConflict
}

// InvalidDocumentVersion reports a malformed versioned ingress request.
type InvalidDocumentVersion struct {
	Path    string
	Version int
}

func (e *InvalidDocumentVersion) Error() string {
	return fmt.Sprintf("%v: path %s documentVersion must be >= 1, got %d", ErrInvalidDocumentVersion, e.Path, e.Version)
}

func (e *InvalidDocumentVersion) Unwrap() error { return ErrInvalidDocumentVersion }

func (e *InvalidDocumentVersion) Is(target error) bool {
	return target == ErrInvalidDocumentVersion
}

// ReconciliationTargetError reports the target that prevented a recrawl from
// starting.
type ReconciliationTargetError struct {
	Index  int
	Reason string
}

func (e *ReconciliationTargetError) Error() string {
	return fmt.Sprintf("%v: target %d: %s", ErrInvalidReconciliationTarget, e.Index, e.Reason)
}

func (e *ReconciliationTargetError) Unwrap() error { return ErrInvalidReconciliationTarget }

func (e *ReconciliationTargetError) Is(target error) bool {
	return target == ErrInvalidReconciliationTarget
}

// IncompatibleEpochError preserves the original artifact location and bytes
// classification when a legacy string epoch is rejected.
type IncompatibleEpochError struct {
	ArtifactPath  string
	HistoricalRef string
}

func (e *IncompatibleEpochError) Error() string {
	return fmt.Sprintf("%v: legacy string epoch artifact %s preserved as %s", ErrIncompatibleEpoch, e.ArtifactPath, e.HistoricalRef)
}

func (e *IncompatibleEpochError) Unwrap() error { return ErrIncompatibleEpoch }

func (e *IncompatibleEpochError) Is(target error) bool {
	return target == ErrIncompatibleEpoch
}
