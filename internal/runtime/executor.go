// Package runtime contains the one-shot runtime execution boundary.
//
// A runtime execution is deliberately different from the long-lived adapter
// pool. One invocation owns one process, one disposable working directory,
// and one immutable snapshot payload. The process is closed on every terminal
// path and the result records what could be verified about that lifecycle.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"codeflow/internal/evidence"
	"codeflow/internal/isolation"
	"codeflow/internal/protocol"
	"codeflow/internal/workspace"
)

// Forward aliases from isolation package.
type Consent = isolation.Consent
type IsolationResult = isolation.IsolationResult
type RuntimeConsent = isolation.RuntimeConsent
type RuntimeConsentV1 = isolation.RuntimeConsentV1
type RuntimeIsolationResult = isolation.RuntimeIsolationResult
type RuntimeIsolationResultV1 = isolation.RuntimeIsolationResultV1
type RuntimeCommand = isolation.RuntimeCommand
type RuntimeAccessScope = isolation.RuntimeAccessScope
type RuntimeIsolationScope = isolation.RuntimeIsolationScope
type RuntimeExecutionSpec = isolation.RuntimeExecutionSpec
type RuntimeCleanupEvidence = isolation.RuntimeCleanupEvidence
type RuntimeWorktreeComparison = isolation.RuntimeWorktreeComparison
type RuntimeProcessOutput = isolation.RuntimeProcessOutput

const (
	RuntimeConsentSchemaID           = isolation.RuntimeConsentSchemaID
	RuntimeIsolationResultSchemaID   = isolation.RuntimeIsolationResultSchemaID
	RuntimeConsentV1SchemaID         = isolation.RuntimeConsentV1SchemaID
	RuntimeIsolationResultV1SchemaID = isolation.RuntimeIsolationResultV1SchemaID
	RuntimeSchemaVersion             = isolation.RuntimeSchemaVersion

	RuntimeTerminalSuccess = isolation.RuntimeTerminalSuccess
	RuntimeTerminalFailure = isolation.RuntimeTerminalFailure
	RuntimeTerminalTimeout = isolation.RuntimeTerminalTimeout
	RuntimeTerminalCancel  = isolation.RuntimeTerminalCancel

	RuntimeAuditClean         = isolation.RuntimeAuditClean
	RuntimeAuditViolation     = isolation.RuntimeAuditViolation
	RuntimeAuditUnavailable   = isolation.RuntimeAuditUnavailable
	RuntimeAuditIndeterminate = isolation.RuntimeAuditIndeterminate

	RuntimePromotionEligible = isolation.RuntimePromotionEligible
	RuntimePromotionBlocked  = isolation.RuntimePromotionBlocked
)

// CommandDigest computes a command digest for consent binding.
func CommandDigest(command string, args []string) string {
	return isolation.CommandDigest(command, args)
}

// ErrSnapshotIdentityMismatch means that the bytes or identity supplied to
// the executor do not describe the declared immutable tree.
var ErrSnapshotIdentityMismatch = errors.New("runtime snapshot identity mismatch")

// ErrRepositoryPathRejected means that a runtime request attempted to carry a
// repository path. Runtime input is a captured snapshot payload only.
var ErrRepositoryPathRejected = errors.New("runtime repository path rejected")

// ErrConsentReplay means that an already-consumed consent and nonce pair was
// presented to the same one-shot executor again.
var ErrConsentReplay = errors.New("runtime consent replay")

// SourceAuditRequest is the only input exposed to a source-audit provider.
// It contains snapshot identity and the audit captured before execution, but
// never a repository path or a live filesystem handle.
type SourceAuditRequest struct {
	ExecutionID           string
	SnapshotID            string
	SnapshotTreeDigest    string
	BeforeRepositoryAudit workspace.SourceWriteAudit
	AfterRepositoryAudit  workspace.SourceWriteAudit
	TerminalStatus        string
	ProcessObserved       bool
}

// SourceAuditReport is returned by an injectable source-audit provider. A
// provider that cannot establish attribution must return unavailable or
// indeterminate. Neither state can promote runtime evidence.
type SourceAuditReport struct {
	Status string
	Audit  workspace.SourceWriteAudit
}

// SourceAuditProvider observes source-write attribution after the disposable
// process has been closed. Production uses an indeterminate provider. Tests
// and controlled integrations may inject a provider that returns clean or
// source_integrity_violation.
type SourceAuditProvider interface {
	Audit(context.Context, SourceAuditRequest) (SourceAuditReport, error)
}

// SourceAuditProviderFunc adapts a function to SourceAuditProvider.
type SourceAuditProviderFunc func(context.Context, SourceAuditRequest) (SourceAuditReport, error)

func (f SourceAuditProviderFunc) Audit(ctx context.Context, request SourceAuditRequest) (SourceAuditReport, error) {
	if f == nil {
		return SourceAuditReport{}, errors.New("source audit provider is nil")
	}
	return f(ctx, request)
}

// IndeterminateSourceAuditProvider is the safe production default. The
// current runtime boundary has no OS-level attribution telemetry, so it must
// not claim a clean source audit.
type IndeterminateSourceAuditProvider struct{}

func (IndeterminateSourceAuditProvider) Audit(_ context.Context, request SourceAuditRequest) (SourceAuditReport, error) {
	audit := request.AfterRepositoryAudit
	if audit.CapturedSnapshotTreeDigest == "" {
		audit = request.BeforeRepositoryAudit
	}
	return SourceAuditReport{Status: RuntimeAuditIndeterminate, Audit: audit}, nil
}

// CleanSourceAuditProvider is a deterministic test provider. It is exported
// so focused integration tests do not need to invent an attribution seam.
type CleanSourceAuditProvider struct{}

func (CleanSourceAuditProvider) Audit(_ context.Context, request SourceAuditRequest) (SourceAuditReport, error) {
	audit := request.BeforeRepositoryAudit
	if audit.CapturedSnapshotTreeDigest == "" {
		audit.CapturedSnapshotTreeDigest = request.SnapshotTreeDigest
	}
	return SourceAuditReport{Status: RuntimeAuditClean, Audit: audit}, nil
}

// AttributedWriteSourceAuditProvider is a deterministic test provider for a
// runtime-attributed source write. The executor still discards the process
// writable layer and blocks evidence promotion.
type AttributedWriteSourceAuditProvider struct {
	Path string
}

func (p AttributedWriteSourceAuditProvider) Audit(_ context.Context, request SourceAuditRequest) (SourceAuditReport, error) {
	audit := request.BeforeRepositoryAudit
	if audit.CapturedSnapshotTreeDigest == "" {
		audit.CapturedSnapshotTreeDigest = request.SnapshotTreeDigest
	}
	audit.CodeFlowWriteCount++
	audit.SourceIntegrityViolation = true
	path := p.Path
	if path == "" {
		path = "runtime-attributed-source-write"
	}
	audit.RepositoryPathWrites = append(append([]string(nil), audit.RepositoryPathWrites...), path)
	return SourceAuditReport{Status: RuntimeAuditViolation, Audit: audit}, nil
}

// ExecutorConfig configures one-shot execution. AdapterConfig is passed to
// protocol.Spawn after environment sanitization. Engine is optional, but when
// present it is used to validate the selected snapshot and reconcile the live
// head after the process exits.
type ExecutorConfig struct {
	AdapterConfig  protocol.Config
	Spec           RuntimeExecutionSpec
	Engine         *workspace.SnapshotEngine
	AuditProvider  SourceAuditProvider
	Clock          func() time.Time
	RequireConsent bool
}

// ExecutionRequest contains the immutable snapshot payload and operation
// parameters. RepoRoot is retained only as a negative compatibility seam: a
// non-empty value is rejected before process creation.
type ExecutionRequest struct {
	ExecutionID          string
	Nonce                string
	Consent              RuntimeConsent
	Snapshot             protocol.Snapshot
	Operation            string
	Params               map[string]any
	TaskScope            []string
	RequiredObservations []string
	CapabilityRequest    []string
	Payload              map[string]any
	RepoRoot             string
}

// OneShotRequest is a descriptive alias for callers that use the contract's
// terminology.
type OneShotRequest = ExecutionRequest

// ExecutionResult contains the isolation result and, for analysis operations,
// the validated analyzer result. The latter is intentionally kept outside the
// runtime-isolation contract so runtime lifecycle authority cannot be confused
// with static analyzer authority.
type ExecutionResult struct {
	Isolation RuntimeIsolationResult
	Analyzer  *evidence.Result
}

// RuntimeIsolationResult returns the contract payload without exposing a
// mutable pointer owned by the executor.
func (r ExecutionResult) RuntimeIsolationResult() RuntimeIsolationResult {
	return r.Isolation
}

// Executor owns no reusable process state. It is safe to use one Executor
// concurrently because every call constructs a fresh local invocation.
type Executor struct {
	cfg      ExecutorConfig
	nonceMu  sync.Mutex
	consumed map[string]struct{}
}

// NewExecutor constructs a one-shot executor. Configuration errors are
// reported by Execute so callers can retain a simple construction seam.
func NewExecutor(cfg ExecutorConfig) *Executor {
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	if cfg.AuditProvider == nil {
		cfg.AuditProvider = IndeterminateSourceAuditProvider{}
	}
	// Consent is part of the runtime boundary, not an optional execution hint.
	// Keep the field for source compatibility with early callers, but a missing
	// consent is always rejected by validateConsent below.
	return &Executor{cfg: cfg, consumed: make(map[string]struct{})}
}

// Execute runs exactly one fresh subprocess and one request. It never uses a
// Pool and never retries after a crash. Terminal adapter errors are returned
// alongside their recorded isolation result.
func (e *Executor) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if e == nil {
		return ExecutionResult{}, errors.New("runtime executor is nil")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionResult{}, err
	}

	frozen, err := freezeRequest(request)
	if err != nil {
		return ExecutionResult{}, err
	}
	if err := e.validateConsent(frozen); err != nil {
		return ExecutionResult{}, err
	}
	if e.cfg.Engine != nil {
		if err := validateEngineSnapshot(e.cfg.Engine, frozen.snapshot); err != nil {
			return ExecutionResult{}, err
		}
	}

	started := e.now()
	beforeHead := e.liveHead()
	beforeAudit := frozen.snapshot.SourceWriteAudit
	if beforeAudit.CapturedSnapshotTreeDigest == "" {
		beforeAudit.CapturedSnapshotTreeDigest = frozen.treeDigest
	}

	result := ExecutionResult{}
	iso := RuntimeIsolationResult{
		SchemaID:                 RuntimeIsolationResultSchemaID,
		SchemaVersion:            RuntimeSchemaVersion,
		ResultAuthority:          "runtime_executor",
		ProcessObserved:          false,
		ExecutionID:              frozen.executionID,
		ConsentID:                frozen.consent.ConsentID,
		ActorID:                  frozen.consent.ActorID,
		Nonce:                    frozen.nonce,
		Status:                   RuntimeTerminalFailure,
		ResultCode:               "not_started",
		Command:                  e.spec().Command.Command,
		Args:                     cloneArgs(e.spec().Command.Args),
		CommandDigest:            commandDigest(e.spec().Command),
		SnapshotID:               frozen.snapshot.SnapshotID,
		SnapshotTreeDigest:       frozen.snapshot.RootTreeID,
		RecomputedTreeDigest:     frozen.treeDigest,
		InputTreeVerified:        frozen.treeVerified,
		AccessScope:              e.spec().AccessScope,
		IsolationScope:           e.spec().IsolationScope,
		ConcurrentWorktree:       worktreeNotConfigured(),
		StartedAt:                started.UTC().Format(time.RFC3339Nano),
		FinishedAt:               started.UTC().Format(time.RFC3339Nano),
		RepositoryPathWriteAudit: beforeAudit,
		SourceWriteAuditStatus:   RuntimeAuditIndeterminate,
		SourceIntegrityStatus:    "audit_indeterminate",
		EvidencePromotion:        RuntimePromotionBlocked,
		PromotionBlockedReason:   "runtime audit is indeterminate",
	}

	if !frozen.treeVerified {
		iso.ResultCode = "snapshot_tree_digest_mismatch"
		iso.PromotionBlockedReason = ErrSnapshotIdentityMismatch.Error()
		iso.FinishedAt = e.now().UTC().Format(time.RFC3339Nano)
		result.Isolation = iso
		return result, fmt.Errorf("%w: declared %s recomputed %s", ErrSnapshotIdentityMismatch, frozen.snapshot.RootTreeID, frozen.treeDigest)
	}
	if err := e.consumeConsentNonce(frozen.consent.ConsentID, frozen.nonce); err != nil {
		iso.ResultCode = "consent_replay"
		iso.FailureReason = boundedFailure(err)
		iso.PromotionBlockedReason = ErrConsentReplay.Error()
		iso.FinishedAt = e.now().UTC().Format(time.RFC3339Nano)
		result.Isolation = iso
		return result, err
	}

	adapterCfg := e.cfg.AdapterConfig
	adapterCfg.Env = SanitizeEnvironment(adapterCfg.Env)
	conn, err := protocol.Spawn(ctx, adapterCfg)
	if err != nil {
		iso.ResultCode = errorCode(err)
		iso.FailureReason = boundedFailure(err)
		iso.FinishedAt = e.now().UTC().Format(time.RFC3339Nano)
		result.Isolation = iso
		return result, err
	}
	iso.ProcessObserved = true

	// Close is unconditional. Conn.Close kills any remaining child, waits for
	// its terminal sequence, and removes the process-private working directory.
	var callErr error
	var analyzer evidence.Result
	callErr = conn.Call(ctx, frozen.operation, frozen.params, &analyzer)
	if callErr == nil {
		result.Analyzer = &analyzer
	}
	closeErr := conn.Close()
	if callErr == nil && closeErr != nil {
		callErr = closeErr
	}

	finished := e.now()
	iso.MountPermissionEvidence = conn.MountPermissionEvidence()
	iso.Cleanup = RuntimeCleanupEvidence{
		LayerCreated:  iso.MountPermissionEvidence.Disposable,
		LayerDisposed: iso.MountPermissionEvidence.CleanupVerified,
		Verified:      iso.MountPermissionEvidence.Disposable && iso.MountPermissionEvidence.CleanupVerified,
	}
	iso.Status, iso.ResultCode = terminalStatus(callErr)
	if callErr != nil {
		iso.FailureReason = boundedFailure(callErr)
	}
	iso.FinishedAt = finished.UTC().Format(time.RFC3339Nano)

	afterAudit := beforeAudit
	if e.cfg.Engine != nil {
		afterAudit = e.cfg.Engine.SourceWriteAudit()
	}
	auditReport := e.audit(ctx, SourceAuditRequest{
		ExecutionID:           frozen.executionID,
		SnapshotID:            frozen.snapshot.SnapshotID,
		SnapshotTreeDigest:    frozen.snapshot.RootTreeID,
		BeforeRepositoryAudit: beforeAudit,
		AfterRepositoryAudit:  afterAudit,
		TerminalStatus:        iso.Status,
		ProcessObserved:       iso.ProcessObserved,
	})
	iso.SourceWriteAuditStatus, iso.SourceIntegrityStatus, iso.RepositoryPathWriteAudit = normalizeAudit(auditReport, beforeAudit, frozen.snapshot.RootTreeID)

	iso.ConcurrentWorktree = e.reconcileLiveHead(beforeHead, frozen.snapshot.SnapshotID)
	if !iso.InputTreeVerified {
		iso.EvidencePromotion = RuntimePromotionBlocked
		iso.PromotionBlockedReason = ErrSnapshotIdentityMismatch.Error()
	} else if !iso.Cleanup.Verified {
		iso.EvidencePromotion = RuntimePromotionBlocked
		iso.PromotionBlockedReason = "disposable runtime cleanup was not verified"
	} else if iso.SourceWriteAuditStatus != RuntimeAuditClean {
		iso.EvidencePromotion = RuntimePromotionBlocked
		iso.PromotionBlockedReason = promotionBlockReason(iso.SourceWriteAuditStatus)
	} else if responseIdentityFailure(callErr) {
		iso.EvidencePromotion = RuntimePromotionBlocked
		iso.PromotionBlockedReason = ErrSnapshotIdentityMismatch.Error()
	} else {
		iso.EvidencePromotion = RuntimePromotionEligible
		iso.PromotionBlockedReason = ""
	}
	result.Isolation = iso
	return result, callErr
}

// ExecuteOneShot is a convenience function for callers that do not need to
// retain the executor. It still creates exactly one process and never retries.
func ExecuteOneShot(ctx context.Context, cfg ExecutorConfig, request ExecutionRequest) (ExecutionResult, error) {
	return NewExecutor(cfg).Execute(ctx, request)
}

type frozenRequest struct {
	snapshot     protocol.Snapshot
	input        evidence.SnapshotInput
	params       map[string]any
	operation    string
	executionID  string
	nonce        string
	consent      RuntimeConsent
	treeDigest   string
	treeVerified bool
}

func freezeRequest(request ExecutionRequest) (frozenRequest, error) {
	if strings.TrimSpace(request.RepoRoot) != "" {
		return frozenRequest{}, fmt.Errorf("%w: RepoRoot is not accepted", ErrRepositoryPathRejected)
	}
	if containsRepositoryPathKey(request.Params) || containsRepositoryPathKey(request.Payload) {
		return frozenRequest{}, fmt.Errorf("%w: params contain repoRoot", ErrRepositoryPathRejected)
	}
	if request.Snapshot.RootTreeID == "" {
		return frozenRequest{}, fmt.Errorf("%w: snapshot rootTreeId is required", ErrSnapshotIdentityMismatch)
	}
	input, err := request.Snapshot.AnalyzerInput()
	if err != nil {
		return frozenRequest{}, fmt.Errorf("validate immutable snapshot: %w", err)
	}
	if input.SourceWriteAudit.CapturedSnapshotTreeDigest == "" {
		input.SourceWriteAudit.CapturedSnapshotTreeDigest = input.RootTreeID
	}
	if input.SourceWriteAudit.CapturedSnapshotTreeDigest != input.RootTreeID {
		return frozenRequest{}, fmt.Errorf("%w: snapshot write audit tree digest differs from rootTreeId", ErrSnapshotIdentityMismatch)
	}
	if request.Snapshot.Files != nil && request.Snapshot.ContentOverlay != nil && !sameFiles(request.Snapshot.Files, request.Snapshot.ContentOverlay) {
		return frozenRequest{}, fmt.Errorf("%w: files and contentOverlay differ", ErrSnapshotIdentityMismatch)
	}
	treeDigest, verified := recomputeTreeDigest(input, request.Snapshot.RootTreeID)
	if treeDigest == "" {
		return frozenRequest{}, fmt.Errorf("%w: snapshot has no documents", ErrSnapshotIdentityMismatch)
	}
	operation := strings.TrimSpace(request.Operation)
	if operation == "" {
		operation = protocol.OpDetect
	}
	if operation != protocol.OpDetect && operation != protocol.OpHarvestCandidates && operation != protocol.OpSlice {
		return frozenRequest{}, fmt.Errorf("runtime operation %q is not an analysis operation", operation)
	}
	executionID := strings.TrimSpace(request.ExecutionID)
	if executionID == "" {
		executionID = "runtime-" + input.SnapshotID
	}
	nonce := strings.TrimSpace(request.Nonce)
	if nonce == "" {
		nonce = request.Consent.Nonce
	}
	params := analyzerParams(input, request)
	return frozenRequest{
		snapshot:     request.Snapshot,
		input:        input,
		params:       params,
		operation:    operation,
		executionID:  executionID,
		nonce:        nonce,
		consent:      cloneConsent(request.Consent),
		treeDigest:   treeDigest,
		treeVerified: verified,
	}, nil
}

func analyzerParams(input evidence.SnapshotInput, request ExecutionRequest) map[string]any {
	files := make(map[string]string, len(input.Documents))
	documents := make([]map[string]any, 0, len(input.Documents))
	for _, doc := range input.Documents {
		files[doc.Path] = string(append([]byte(nil), doc.Bytes...))
		documents = append(documents, map[string]any{
			"path": doc.Path, "documentRevisionId": doc.RevisionID,
			"contentId": doc.ContentID, "documentVersion": doc.DocumentVersion,
			"contentHash": doc.ContentID, "byteLength": len(doc.Bytes),
		})
	}
	snapshot := map[string]any{
		"schemaId":                 evidence.AnalyzerRequestSchemaID,
		"schemaVersion":            evidence.SchemaVersion,
		"snapshotId":               input.SnapshotID,
		"workspaceEpoch":           input.WorkspaceEpoch,
		"computedBasisId":          input.ComputedBasisID,
		"rootTreeId":               input.RootTreeID,
		"configurationFingerprint": input.ConfigurationFingerprint,
		"dependencyFingerprint":    input.DependencyFingerprint,
		"documents":                documents,
		"files":                    files,
		"contentOverlay":           cloneStringMap(files),
		"repositoryPathWriteAudit": input.SourceWriteAudit,
	}
	params := map[string]any{
		"computedBasisId": input.ComputedBasisID,
		"workspaceEpoch":  input.WorkspaceEpoch,
		"snapshot":        snapshot,
		"files":           cloneStringMap(files),
		"contentOverlay":  cloneStringMap(files),
	}
	payload := cloneAnyMap(request.Payload)
	for key, value := range request.Params {
		if key == "payload" {
			if nested, ok := value.(map[string]any); ok {
				for nestedKey, nestedValue := range nested {
					payload[nestedKey] = nestedValue
				}
			}
			continue
		}
		payload[key] = value
	}
	if len(request.TaskScope) > 0 {
		params["taskScope"] = append([]string(nil), request.TaskScope...)
	}
	if len(request.RequiredObservations) > 0 {
		params["requiredObservations"] = append([]string(nil), request.RequiredObservations...)
	}
	if len(request.CapabilityRequest) > 0 {
		params["capabilityRequest"] = append([]string(nil), request.CapabilityRequest...)
	}
	if len(payload) > 0 {
		params["payload"] = payload
	}
	return params
}

func cloneConsent(in RuntimeConsent) RuntimeConsent {
	out := in
	out.Args = append([]string(nil), in.Args...)
	return out
}

func (e *Executor) validateConsent(request frozenRequest) error {
	if request.consent.ConsentID == "" {
		return errors.New("runtime consent is required")
	}
	now := e.now()
	if err := request.consent.Validate(now); err != nil {
		return fmt.Errorf("validate runtime consent: %w", err)
	}
	spec := e.spec()
	if err := request.consent.Matches(spec, request.snapshot.SnapshotID, request.snapshot.RootTreeID, request.nonce); err != nil {
		return fmt.Errorf("runtime consent does not authorize execution: %w", err)
	}
	return nil
}

func (e *Executor) consumeConsentNonce(consentID, nonce string) error {
	key := consentID + "\x00" + nonce
	e.nonceMu.Lock()
	defer e.nonceMu.Unlock()
	if e.consumed == nil {
		e.consumed = make(map[string]struct{})
	}
	if _, consumed := e.consumed[key]; consumed {
		return fmt.Errorf("%w: consent %q and nonce have already been consumed", ErrConsentReplay, consentID)
	}
	e.consumed[key] = struct{}{}
	return nil
}

func responseIdentityFailure(err error) bool {
	if err == nil {
		return false
	}
	var perr *protocol.Error
	if !errors.As(err, &perr) || perr.Code != protocol.EBadRequest {
		return false
	}
	message := strings.ToLower(perr.Message)
	return strings.Contains(message, "analyzer result") || strings.Contains(message, "snapshot") || strings.Contains(message, "identity")
}

func (e *Executor) spec() RuntimeExecutionSpec {
	if e == nil {
		return RuntimeExecutionSpec{}
	}
	spec := e.cfg.Spec
	if spec.Command.Command == "" {
		spec.Command.Command = e.cfg.AdapterConfig.BinPath
		spec.Command.Args = cloneArgs(e.cfg.AdapterConfig.Args)
	}
	if spec.CommandDigest == "" {
		spec.CommandDigest = commandDigest(spec.Command)
	}
	if spec.AccessScope == (RuntimeAccessScope{}) {
		spec.AccessScope = RuntimeAccessScope{
			Source: "immutable_snapshot", Network: "disabled", Credentials: "not_available",
		}
	}
	if spec.IsolationScope == (RuntimeIsolationScope{}) {
		spec.IsolationScope = RuntimeIsolationScope{
			Level: "trusted_local", SourceMount: "not_mounted", SourcePermission: "read_only_protocol",
			WorkingDirectory: "process_private_disposable", WritableLayer: "discarded_after_terminal",
			RepositoryPathExposed: false,
		}
	}
	return spec
}

func commandDigest(command RuntimeCommand) string {
	data, _ := json.Marshal(struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{command.Command, command.Args})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func cloneArgs(args []string) []string {
	if args == nil {
		return []string{}
	}
	return append([]string{}, args...)
}

func (e *Executor) now() time.Time {
	if e == nil || e.cfg.Clock == nil {
		return time.Now().UTC()
	}
	return e.cfg.Clock().UTC()
}

func (e *Executor) liveHead() *workspace.WorkspaceSnapshot {
	if e == nil || e.cfg.Engine == nil {
		return nil
	}
	return e.cfg.Engine.LiveHead()
}

func validateEngineSnapshot(engine *workspace.SnapshotEngine, snapshot protocol.Snapshot) error {
	selected, err := engine.GetSnapshot(snapshot.SnapshotID)
	if err != nil {
		return fmt.Errorf("validate selected workspace snapshot: %w", err)
	}
	if selected.RootTreeID != snapshot.RootTreeID || selected.ComputedBasisID != snapshot.ComputedBasisID || selected.WorkspaceEpoch != snapshot.WorkspaceEpoch {
		return fmt.Errorf("%w: workspace snapshot %s identity differs", ErrSnapshotIdentityMismatch, snapshot.SnapshotID)
	}
	if len(selected.Entries) != len(snapshot.Documents) {
		return fmt.Errorf("%w: selected snapshot document count differs", ErrSnapshotIdentityMismatch)
	}
	for _, doc := range snapshot.Documents {
		entry, ok := selected.Entries[doc.Path]
		if !ok || entry.ContentID != doc.ContentID || entry.RevisionID != doc.RevisionID || entry.DocumentVersion != doc.DocumentVersion || entry.ByteLength != doc.ByteLength {
			return fmt.Errorf("%w: selected snapshot document %s differs", ErrSnapshotIdentityMismatch, doc.Path)
		}
	}
	return nil
}

func recomputeTreeDigest(input evidence.SnapshotInput, declared string) (string, bool) {
	if len(input.Documents) == 0 {
		return "", false
	}
	documentDigest := sha256.New()
	metadataDigest := sha256.New()
	docs := append([]evidence.SnapshotDocument(nil), input.Documents...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	for _, doc := range docs {
		content := sha256.Sum256(doc.Bytes)
		contentHex := hex.EncodeToString(content[:])
		_, _ = fmt.Fprintf(documentDigest, "%s:%s\n", doc.Path, contentHex)
		_, _ = fmt.Fprintf(metadataDigest, "%s:%s:%s:%d:%d\n", doc.Path, doc.RevisionID, doc.ContentID, doc.DocumentVersion, len(doc.Bytes))
	}
	contentTree := hex.EncodeToString(documentDigest.Sum(nil))
	metadataTree := hex.EncodeToString(metadataDigest.Sum(nil))
	if declared == contentTree {
		return contentTree, true
	}
	if declared == metadataTree {
		return metadataTree, true
	}
	return contentTree, false
}

func (e *Executor) audit(ctx context.Context, request SourceAuditRequest) SourceAuditReport {
	provider := e.cfg.AuditProvider
	if provider == nil {
		provider = IndeterminateSourceAuditProvider{}
	}
	report, err := provider.Audit(ctx, request)
	if err != nil {
		return SourceAuditReport{Status: RuntimeAuditUnavailable, Audit: request.BeforeRepositoryAudit}
	}
	return report
}

func normalizeAudit(report SourceAuditReport, before workspace.SourceWriteAudit, treeDigest string) (string, string, workspace.SourceWriteAudit) {
	audit := report.Audit
	if audit.CapturedSnapshotTreeDigest == "" {
		audit.CapturedSnapshotTreeDigest = treeDigest
	}
	if audit.SourceIntegrityViolation || audit.CodeFlowWriteCount > 0 || len(audit.RepositoryPathWrites) > 0 {
		return RuntimeAuditViolation, RuntimeAuditViolation, audit
	}
	status := strings.TrimSpace(report.Status)
	switch status {
	case RuntimeAuditClean:
		if audit.CapturedSnapshotTreeDigest != treeDigest {
			return RuntimeAuditViolation, RuntimeAuditViolation, audit
		}
		return RuntimeAuditClean, RuntimeAuditClean, audit
	case RuntimeAuditViolation:
		return RuntimeAuditViolation, RuntimeAuditViolation, audit
	case RuntimeAuditUnavailable:
		return RuntimeAuditUnavailable, "audit_unavailable", audit
	case RuntimeAuditIndeterminate:
		return RuntimeAuditIndeterminate, "audit_indeterminate", audit
	default:
		if before.CapturedSnapshotTreeDigest == treeDigest && before.CodeFlowWriteCount == 0 && !before.SourceIntegrityViolation && len(before.RepositoryPathWrites) == 0 {
			// An omitted provider status is still not a clean assertion.
			return RuntimeAuditIndeterminate, "audit_indeterminate", audit
		}
		return RuntimeAuditUnavailable, "audit_unavailable", audit
	}
}

func promotionBlockReason(status string) string {
	switch status {
	case RuntimeAuditViolation:
		return RuntimeAuditViolation
	case RuntimeAuditUnavailable:
		return "runtime source audit unavailable"
	case RuntimeAuditIndeterminate:
		return "runtime source audit indeterminate"
	default:
		return "runtime source audit is not clean"
	}
}

func terminalStatus(err error) (string, string) {
	if err == nil {
		return RuntimeTerminalSuccess, "ok"
	}
	var perr *protocol.Error
	if errors.As(err, &perr) {
		switch perr.Code {
		case protocol.ETimeout:
			return RuntimeTerminalTimeout, string(perr.Code)
		case protocol.ECancelled:
			return RuntimeTerminalCancel, string(perr.Code)
		}
		return RuntimeTerminalFailure, string(perr.Code)
	}
	return RuntimeTerminalFailure, "runtime_error"
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var perr *protocol.Error
	if errors.As(err, &perr) {
		return string(perr.Code)
	}
	return "runtime_error"
}

func boundedFailure(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 512 {
		return text[:512]
	}
	return text
}

func (e *Executor) reconcileLiveHead(before *workspace.WorkspaceSnapshot, selectedSnapshotID string) RuntimeWorktreeComparison {
	if e == nil || e.cfg.Engine == nil {
		return worktreeNotConfigured()
	}
	comparison := RuntimeWorktreeComparison{Classification: "reconcile_pending"}
	if before != nil {
		comparison.BeforeSnapshotID = before.SnapshotID
		comparison.BeforeTreeDigest = before.RootTreeID
	}
	// Reconciliation intentionally uses a fresh bounded context. Caller
	// cancellation must not skip cleanup or leave the live head unclassified.
	reconcileCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	after, err := e.cfg.Engine.Reconcile(reconcileCtx, nil)
	if err != nil {
		return comparison
	}
	if after != nil {
		comparison.AfterSnapshotID = after.SnapshotID
		comparison.AfterTreeDigest = after.RootTreeID
	}
	if before == nil {
		if after == nil {
			comparison.Classification = "not_configured"
		} else {
			comparison.Classification = "reconciled_unattributed"
		}
		return comparison
	}
	if before.RootTreeID == after.RootTreeID {
		comparison.Classification = "unchanged"
	} else {
		comparison.Classification = "reconciled_unattributed"
	}
	_ = selectedSnapshotID
	return comparison
}

func worktreeNotConfigured() RuntimeWorktreeComparison {
	return RuntimeWorktreeComparison{Classification: "not_configured"}
}

// SanitizeEnvironment removes path-bearing repository/worktree variables and
// forces callers to rely on protocol snapshot bytes. Spawn adds disposable
// TMP/PWD/OLDPWD values after this function returns.
func SanitizeEnvironment(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env))
	seen := make(map[string]bool, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		upper := strings.ToUpper(key)
		if repositoryEnvironmentKey(upper) {
			continue
		}
		// A caller can use an arbitrary CODEFLOW_* variable to smuggle a path.
		// Keep non-path configuration, but remove values that explicitly look
		// like a repository-root hint under a CODEFLOW namespace.
		if strings.HasPrefix(upper, "CODEFLOW_") && strings.Contains(strings.ToUpper(value), "REPO") {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key+"="+value)
	}
	return out
}

func repositoryEnvironmentKey(key string) bool {
	if key == "PWD" || key == "OLDPWD" || key == "TMPDIR" || key == "TMP" || key == "TEMP" || key == "CODEFLOW_ADAPTER_WORKDIR" {
		return true
	}
	for _, marker := range []string{"REPO_ROOT", "REPOSITORY_ROOT", "WORKTREE_ROOT", "WORKSPACE_ROOT", "SOURCE_ROOT", "PROJECT_ROOT", "GIT_DIR", "GIT_WORK_TREE", "GITHUB_WORKSPACE", "INIT_CWD", "NPM_CONFIG_LOCAL_PREFIX"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func containsRepositoryPathKey(value any) bool {
	switch current := value.(type) {
	case map[string]any:
		for key, nested := range current {
			if strings.EqualFold(strings.TrimSpace(key), "repoRoot") || strings.EqualFold(strings.TrimSpace(key), "repositoryRoot") || strings.EqualFold(strings.TrimSpace(key), "worktreeRoot") {
				return true
			}
			if containsRepositoryPathKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range current {
			if containsRepositoryPathKey(nested) {
				return true
			}
		}
	}
	return false
}

func sameFiles(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for path, content := range left {
		if right[path] != content {
			return false
		}
	}
	return true
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return make(map[string]any)
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneAny(value)
	}
	return out
}

func cloneAny(value any) any {
	switch current := value.(type) {
	case map[string]any:
		return cloneAnyMap(current)
	case []any:
		out := make([]any, len(current))
		for i, item := range current {
			out[i] = cloneAny(item)
		}
		return out
	default:
		return value
	}
}
