package contractharness

import (
	"encoding/json"
	"fmt"
	"time"

	"codeflow/internal/isolation"
)

// ValidateRuntimeConsentV1 validates the schema and the cross-field
// invariants for one-shot runtime consent.  It checks timestamp ordering but
// does not use the wall clock.  Call ValidateRuntimeConsentV1At at an
// execution boundary when expiry must be evaluated against a selected clock.
func ValidateRuntimeConsentV1(data []byte) error {
	if err := Validate(RuntimeConsentV1SchemaID, data); err != nil {
		return fmt.Errorf("runtime-consent.v1 schema: %w", err)
	}
	var consent isolation.RuntimeConsent
	if err := json.Unmarshal(data, &consent); err != nil {
		return fmt.Errorf("parse runtime-consent.v1: %w", err)
	}
	if err := consent.Validate(time.Time{}); err != nil {
		return fmt.Errorf("runtime-consent.v1 invariant: %w", err)
	}
	return nil
}

// ValidateRuntimeConsentV1At adds the nonexpired check used by an executor.
func ValidateRuntimeConsentV1At(data []byte, now time.Time) error {
	if err := ValidateRuntimeConsentV1(data); err != nil {
		return err
	}
	var consent isolation.RuntimeConsent
	if err := json.Unmarshal(data, &consent); err != nil {
		return fmt.Errorf("parse runtime-consent.v1: %w", err)
	}
	if err := consent.Validate(now); err != nil {
		return fmt.Errorf("runtime-consent.v1 temporal invariant: %w", err)
	}
	return nil
}

// ValidateRuntimeIsolationResultV1 validates the observed one-shot lifecycle
// result.  A result marked as caller supplied, unobserved, unverified, or
// clean despite missing audit telemetry is rejected before evidence promotion.
func ValidateRuntimeIsolationResultV1(data []byte) error {
	if err := Validate(RuntimeIsolationResultV1SchemaID, data); err != nil {
		return fmt.Errorf("runtime-isolation-result.v1 schema: %w", err)
	}
	var result isolation.RuntimeIsolationResult
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("parse runtime-isolation-result.v1: %w", err)
	}
	if result.SnapshotTreeDigest != result.RecomputedTreeDigest {
		return fmt.Errorf("runtime-isolation-result.v1 input tree digest was not preserved")
	}
	if result.MountPermissionEvidence.SourceDelivery != "protocol_snapshot_bytes" || result.MountPermissionEvidence.SourceMount != "not_mounted" || result.MountPermissionEvidence.WorkingDirectoryMode != "process_private_disposable" || result.MountPermissionEvidence.WorkingDirectoryPermission != "0700" || !result.MountPermissionEvidence.ReadOnlySource || !result.MountPermissionEvidence.Disposable || result.MountPermissionEvidence.RepositoryPathExposed || !result.MountPermissionEvidence.CleanupVerified {
		return fmt.Errorf("runtime-isolation-result.v1 does not prove the process-private read-only boundary")
	}
	if result.EvidencePromotion == isolation.RuntimePromotionBlocked && result.PromotionBlockedReason == "" {
		return fmt.Errorf("runtime-isolation-result.v1 blocked promotion requires a reason")
	}
	if err := result.Validate(); err != nil {
		return fmt.Errorf("runtime-isolation-result.v1 invariant: %w", err)
	}
	return nil
}
