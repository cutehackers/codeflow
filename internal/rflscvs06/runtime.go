// Package rflscvs06 contains the strict data contracts for the VS-06 runtime
// consent and one-shot isolation result boundaries.  Execution remains owned
// by the runtime adapter seam.  These types deliberately contain identity and
// evidence only, not a repository path or a mutable source handle.
package rflscvs06

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codeflow/internal/rflscvs02"
	"codeflow/internal/workspace"
)

const (
	RuntimeConsentSchemaID           = "https://codeflow.local/schemas/rflsc.runtime-consent.v1.schema.json"
	RuntimeIsolationResultSchemaID   = "https://codeflow.local/schemas/rflsc.runtime-isolation-result.v1.schema.json"
	RuntimeConsentV1SchemaID         = RuntimeConsentSchemaID
	RuntimeIsolationResultV1SchemaID = RuntimeIsolationResultSchemaID
	RuntimeSchemaVersion             = 1
)

const (
	RuntimeTerminalSuccess = "success"
	RuntimeTerminalFailure = "failure"
	RuntimeTerminalTimeout = "timeout"
	RuntimeTerminalCancel  = "cancel"

	RuntimeAuditClean         = "clean"
	RuntimeAuditViolation     = "source_integrity_violation"
	RuntimeAuditUnavailable   = "unavailable"
	RuntimeAuditIndeterminate = "indeterminate"

	RuntimePromotionEligible = "eligible"
	RuntimePromotionBlocked  = "blocked"
)

// RuntimeCommand is the exact configured executable and argument vector.  A
// consent binds this value byte-for-byte after JSON normalization.
type RuntimeCommand struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// RuntimeAccessScope is intentionally explicit.  Empty values are not a
// permissive default because consent must describe the access a run receives.
type RuntimeAccessScope struct {
	Source      string `json:"source"`
	Network     string `json:"network"`
	Credentials string `json:"credentials"`
}

// RuntimeIsolationScope describes the process boundary disclosed to the
// actor.  A repository path is never part of a runtime request.
type RuntimeIsolationScope struct {
	Level                 string `json:"level"`
	SourceMount           string `json:"sourceMount"`
	SourcePermission      string `json:"sourcePermission"`
	WorkingDirectory      string `json:"workingDirectory"`
	WritableLayer         string `json:"writableLayer"`
	RepositoryPathExposed bool   `json:"repositoryPathExposed"`
}

// RuntimeConsent is a one-shot actor approval.  The executor must compare
// command, scope, snapshot identity, and nonce with its configured values
// before starting a process.
type RuntimeConsent struct {
	SchemaID           string                `json:"schemaId"`
	SchemaVersion      int                   `json:"schemaVersion"`
	ConsentID          string                `json:"consentId"`
	ActorID            string                `json:"actorId"`
	ApprovedBy         string                `json:"approvedBy,omitempty"`
	Approved           bool                  `json:"approved"`
	IssuedAt           string                `json:"issuedAt"`
	ExpiresAt          string                `json:"expiresAt"`
	Command            string                `json:"command"`
	Args               []string              `json:"args"`
	CommandDigest      string                `json:"commandDigest,omitempty"`
	AccessScope        RuntimeAccessScope    `json:"accessScope"`
	IsolationScope     RuntimeIsolationScope `json:"isolationScope"`
	SnapshotID         string                `json:"snapshotId"`
	SnapshotTreeDigest string                `json:"snapshotTreeDigest"`
	Nonce              string                `json:"nonce"`
}

// RuntimeConsentV1 is the versioned name used by contract consumers.
type RuntimeConsentV1 = RuntimeConsent

// RuntimeExecutionSpec is the configured command and boundary that a
// consent must authorize.  It is useful to contract consumers even when the
// actual process executor lives in another package.
type RuntimeExecutionSpec struct {
	Command        RuntimeCommand        `json:"command"`
	CommandDigest  string                `json:"commandDigest,omitempty"`
	AccessScope    RuntimeAccessScope    `json:"accessScope"`
	IsolationScope RuntimeIsolationScope `json:"isolationScope"`
}

// RuntimeCleanupEvidence records disposal of the process-private writable
// layer after every terminal outcome.
type RuntimeCleanupEvidence struct {
	LayerCreated  bool `json:"layerCreated"`
	LayerDisposed bool `json:"layerDisposed"`
	Verified      bool `json:"verified"`
}

// RuntimeWorktreeComparison is intentionally separate from runtime source
// integrity.  A live worktree change observed around a run is reconciled and
// unattributed unless an audit provider proves the runtime caused a source
// write.
type RuntimeWorktreeComparison struct {
	Classification   string `json:"classification"`
	BeforeSnapshotID string `json:"beforeSnapshotId,omitempty"`
	AfterSnapshotID  string `json:"afterSnapshotId,omitempty"`
	BeforeTreeDigest string `json:"beforeTreeDigest,omitempty"`
	AfterTreeDigest  string `json:"afterTreeDigest,omitempty"`
}

// RuntimeProcessOutput is bounded, redacted process output.  It is optional
// evidence and never controls the terminal status.
type RuntimeProcessOutput struct {
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// RuntimeIsolationResult is produced by the runtime executor.  ResultAuthority
// and ProcessObserved make it explicit that a caller-supplied result cannot
// be promoted as if it came from a real process.
type RuntimeIsolationResult struct {
	SchemaID                 string                            `json:"schemaId"`
	SchemaVersion            int                               `json:"schemaVersion"`
	ResultAuthority          string                            `json:"resultAuthority"`
	ProcessObserved          bool                              `json:"processObserved"`
	ExecutionID              string                            `json:"executionId"`
	ConsentID                string                            `json:"consentId"`
	ActorID                  string                            `json:"actorId"`
	Nonce                    string                            `json:"nonce"`
	Status                   string                            `json:"status"`
	ResultCode               string                            `json:"resultCode"`
	FailureReason            string                            `json:"failureReason,omitempty"`
	Command                  string                            `json:"command"`
	Args                     []string                          `json:"args"`
	CommandDigest            string                            `json:"commandDigest"`
	SnapshotID               string                            `json:"snapshotId"`
	SnapshotTreeDigest       string                            `json:"snapshotTreeDigest"`
	RecomputedTreeDigest     string                            `json:"recomputedTreeDigest"`
	InputTreeVerified        bool                              `json:"inputTreeVerified"`
	AccessScope              RuntimeAccessScope                `json:"accessScope"`
	IsolationScope           RuntimeIsolationScope             `json:"isolationScope"`
	MountPermissionEvidence  rflscvs02.MountPermissionEvidence `json:"mountPermissionEvidence"`
	Cleanup                  RuntimeCleanupEvidence            `json:"cleanup"`
	SourceWriteAuditStatus   string                            `json:"sourceWriteAuditStatus"`
	RepositoryPathWriteAudit workspace.SourceWriteAudit        `json:"repositoryPathWriteAudit"`
	SourceIntegrityStatus    string                            `json:"sourceIntegrityStatus"`
	EvidencePromotion        string                            `json:"evidencePromotion"`
	PromotionBlockedReason   string                            `json:"promotionBlockedReason,omitempty"`
	ConcurrentWorktree       RuntimeWorktreeComparison         `json:"concurrentWorktree"`
	StartedAt                string                            `json:"startedAt"`
	FinishedAt               string                            `json:"finishedAt"`
	Output                   RuntimeProcessOutput              `json:"output,omitempty"`
}

// RuntimeIsolationResultV1 is the versioned name used by contract consumers.
type RuntimeIsolationResultV1 = RuntimeIsolationResult

// Validate checks the static and temporal properties of consent.  Exact
// command/scope matching against a configured runtime is performed by
// Matches, because the configured runtime is not part of this contract.
func (c RuntimeConsent) Validate(now time.Time) error {
	if c.SchemaID != RuntimeConsentSchemaID || c.SchemaVersion != RuntimeSchemaVersion {
		return fmt.Errorf("runtime consent has an unexpected schema")
	}
	if strings.TrimSpace(c.ConsentID) == "" || strings.TrimSpace(c.ActorID) == "" || strings.TrimSpace(c.IssuedAt) == "" || strings.TrimSpace(c.ExpiresAt) == "" || strings.TrimSpace(c.Command) == "" || strings.TrimSpace(c.SnapshotID) == "" || strings.TrimSpace(c.SnapshotTreeDigest) == "" || strings.TrimSpace(c.Nonce) == "" {
		return fmt.Errorf("runtime consent identity is incomplete")
	}
	if len(c.Nonce) < 16 {
		return fmt.Errorf("runtime consent nonce is too short")
	}
	if c.ApprovedBy != "" && c.ApprovedBy != c.ActorID {
		return fmt.Errorf("runtime consent actorId and approvedBy differ")
	}
	if !c.Approved {
		return fmt.Errorf("runtime consent is not approved")
	}
	issued, err := time.Parse(time.RFC3339Nano, c.IssuedAt)
	if err != nil {
		return fmt.Errorf("runtime consent issuedAt is not RFC3339: %w", err)
	}
	expires, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if err != nil {
		return fmt.Errorf("runtime consent expiresAt is not RFC3339: %w", err)
	}
	if !expires.After(issued) {
		return fmt.Errorf("runtime consent expiresAt must be after issuedAt")
	}
	if !now.IsZero() && !expires.After(now) {
		return fmt.Errorf("runtime consent is expired")
	}
	if !now.IsZero() && issued.After(now) {
		return fmt.Errorf("runtime consent issuedAt is in the future")
	}
	if err := c.AccessScope.validate(); err != nil {
		return fmt.Errorf("runtime consent accessScope: %w", err)
	}
	if err := c.IsolationScope.validate(); err != nil {
		return fmt.Errorf("runtime consent isolationScope: %w", err)
	}
	if c.CommandDigest != "" && c.CommandDigest != CommandDigest(c.Command, c.Args) {
		return fmt.Errorf("runtime consent commandDigest does not match command and args")
	}
	return nil
}

// Matches binds a consent to one configured command, scope, snapshot and
// nonce.  It deliberately does not normalize command arguments.
func (c RuntimeConsent) Matches(spec RuntimeExecutionSpec, snapshotID, treeDigest, nonce string) error {
	if c.Command != spec.Command.Command || !sameStrings(c.Args, spec.Command.Args) {
		return fmt.Errorf("runtime consent command or args do not match configured command")
	}
	if spec.CommandDigest != "" && c.CommandDigest != spec.CommandDigest {
		return fmt.Errorf("runtime consent command digest does not match configured command")
	}
	if c.AccessScope != spec.AccessScope {
		return fmt.Errorf("runtime consent access scope does not match configured scope")
	}
	if c.IsolationScope != spec.IsolationScope {
		return fmt.Errorf("runtime consent isolation scope does not match configured scope")
	}
	if c.SnapshotID != snapshotID || c.SnapshotTreeDigest != treeDigest {
		return fmt.Errorf("runtime consent snapshot identity does not match immutable input")
	}
	if c.Nonce != nonce {
		return fmt.Errorf("runtime consent nonce does not match execution nonce")
	}
	return nil
}

func (s RuntimeAccessScope) validate() error {
	if strings.TrimSpace(s.Source) == "" || strings.TrimSpace(s.Network) == "" || strings.TrimSpace(s.Credentials) == "" {
		return fmt.Errorf("source, network, and credentials are required")
	}
	if s.Source != "immutable_snapshot" {
		return fmt.Errorf("source scope must be immutable_snapshot")
	}
	switch strings.ToLower(strings.TrimSpace(s.Source)) {
	case "live_worktree", "repository", "repository_path", "reporoot", "live_source":
		return fmt.Errorf("source scope must not grant live repository access")
	}
	return nil
}

func (s RuntimeIsolationScope) validate() error {
	if strings.TrimSpace(s.Level) == "" || strings.TrimSpace(s.SourceMount) == "" || strings.TrimSpace(s.SourcePermission) == "" || strings.TrimSpace(s.WorkingDirectory) == "" || strings.TrimSpace(s.WritableLayer) == "" {
		return fmt.Errorf("level, sourceMount, sourcePermission, workingDirectory, and writableLayer are required")
	}
	if s.RepositoryPathExposed {
		return fmt.Errorf("repository path exposure is not permitted")
	}
	switch strings.ToLower(strings.TrimSpace(s.SourceMount)) {
	case "repository", "live_worktree", "reporoot":
		return fmt.Errorf("isolation scope must not mount the live repository")
	}
	return nil
}

// Validate performs the result invariants that are independent of a live
// executor. In particular, unavailable or indeterminate telemetry can never
// be represented as an eligible evidence promotion.
func (r RuntimeIsolationResult) Validate() error {
	if r.SchemaID != RuntimeIsolationResultSchemaID || r.SchemaVersion != RuntimeSchemaVersion {
		return fmt.Errorf("runtime isolation result has an unexpected schema")
	}
	if r.ResultAuthority != "runtime_executor" || !r.ProcessObserved {
		return fmt.Errorf("runtime isolation result is not attributed to an observed runtime executor")
	}
	if strings.TrimSpace(r.ExecutionID) == "" || strings.TrimSpace(r.ConsentID) == "" || strings.TrimSpace(r.ActorID) == "" || strings.TrimSpace(r.Nonce) == "" || strings.TrimSpace(r.Command) == "" || strings.TrimSpace(r.CommandDigest) == "" || strings.TrimSpace(r.SnapshotID) == "" || strings.TrimSpace(r.SnapshotTreeDigest) == "" || strings.TrimSpace(r.RecomputedTreeDigest) == "" || strings.TrimSpace(r.StartedAt) == "" || strings.TrimSpace(r.FinishedAt) == "" {
		return fmt.Errorf("runtime isolation result identity is incomplete")
	}
	switch r.Status {
	case RuntimeTerminalSuccess, RuntimeTerminalFailure, RuntimeTerminalTimeout, RuntimeTerminalCancel:
	default:
		return fmt.Errorf("runtime isolation result has unsupported terminal status %q", r.Status)
	}
	if !r.InputTreeVerified {
		return fmt.Errorf("runtime isolation result did not verify input tree identity")
	}
	if !r.Cleanup.LayerCreated || !r.Cleanup.LayerDisposed || !r.Cleanup.Verified {
		return fmt.Errorf("runtime isolation result did not verify disposable layer cleanup")
	}
	if err := r.AccessScope.validate(); err != nil {
		return fmt.Errorf("runtime isolation result accessScope: %w", err)
	}
	if err := r.IsolationScope.validate(); err != nil {
		return fmt.Errorf("runtime isolation result isolationScope: %w", err)
	}
	if r.CommandDigest != CommandDigest(r.Command, r.Args) {
		return fmt.Errorf("runtime isolation result commandDigest does not match command and args")
	}
	if r.MountPermissionEvidence.RepositoryPathExposed || !r.MountPermissionEvidence.ReadOnlySource || !r.MountPermissionEvidence.Disposable || !r.MountPermissionEvidence.CleanupVerified {
		return fmt.Errorf("runtime isolation result does not prove source isolation")
	}
	switch r.SourceWriteAuditStatus {
	case RuntimeAuditClean:
		if r.SourceIntegrityStatus != RuntimeAuditClean {
			return fmt.Errorf("clean runtime audit requires clean source integrity status")
		}
		if r.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest != r.SnapshotTreeDigest || r.RepositoryPathWriteAudit.CodeFlowWriteCount != 0 || r.RepositoryPathWriteAudit.SourceIntegrityViolation || len(r.RepositoryPathWriteAudit.RepositoryPathWrites) != 0 {
			return fmt.Errorf("clean runtime audit contains a source write")
		}
	case RuntimeAuditViolation:
		if r.SourceIntegrityStatus != RuntimeAuditViolation {
			return fmt.Errorf("source write audit violation requires source integrity violation status")
		}
		if !r.RepositoryPathWriteAudit.SourceIntegrityViolation && r.RepositoryPathWriteAudit.CodeFlowWriteCount == 0 && len(r.RepositoryPathWriteAudit.RepositoryPathWrites) == 0 {
			return fmt.Errorf("source integrity violation has no write evidence")
		}
	case RuntimeAuditUnavailable, RuntimeAuditIndeterminate:
		if r.SourceIntegrityStatus != "audit_unavailable" && r.SourceIntegrityStatus != "audit_indeterminate" {
			return fmt.Errorf("unavailable or indeterminate audit requires an explicit blocked integrity status")
		}
	default:
		return fmt.Errorf("runtime isolation result has unsupported source audit status %q", r.SourceWriteAuditStatus)
	}
	if r.SourceIntegrityStatus == RuntimeAuditViolation && r.EvidencePromotion != RuntimePromotionBlocked {
		return fmt.Errorf("source integrity violation cannot be promoted")
	}
	if (r.SourceWriteAuditStatus == RuntimeAuditUnavailable || r.SourceWriteAuditStatus == RuntimeAuditIndeterminate) && r.EvidencePromotion == RuntimePromotionEligible {
		return fmt.Errorf("missing or indeterminate audit cannot be promoted")
	}
	if r.EvidencePromotion != RuntimePromotionEligible && r.EvidencePromotion != RuntimePromotionBlocked {
		return fmt.Errorf("runtime isolation result has unsupported promotion state")
	}
	if r.Status != RuntimeTerminalSuccess && r.EvidencePromotion != RuntimePromotionBlocked {
		return fmt.Errorf("non-success runtime outcome cannot be promoted")
	}
	if r.SourceIntegrityStatus == RuntimeAuditViolation && r.ResultCode != RuntimeAuditViolation {
		return fmt.Errorf("source integrity violation must be the result code")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.StartedAt); err != nil {
		return fmt.Errorf("runtime isolation result startedAt is not RFC3339: %w", err)
	}
	started, _ := time.Parse(time.RFC3339Nano, r.StartedAt)
	finished, err := time.Parse(time.RFC3339Nano, r.FinishedAt)
	if err != nil {
		return fmt.Errorf("runtime isolation result finishedAt is not RFC3339: %w", err)
	}
	if finished.Before(started) {
		return fmt.Errorf("runtime isolation result finishedAt is before startedAt")
	}
	return nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// CommandDigest returns the stable digest used to bind consent and observed
// results to an exact command/argument vector.
func CommandDigest(command string, args []string) string {
	payload, _ := json.Marshal(RuntimeCommand{Command: command, Args: append([]string(nil), args...)})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
