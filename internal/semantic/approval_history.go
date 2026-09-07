package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"codeflow/internal/contractharness"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

const (
	ApprovalHistoryV1SchemaID          = contractharness.ApprovalHistoryV1SchemaID
	ApprovalHistoryV1SchemaVersion     = contractharness.ApprovalHistoryV1SchemaVersion
	approvalHistoryQueryJSONMaxBytes   = 64 << 10
	approvalHistoryQueryTargetMaxRunes = 4096
	approvalHistoryQueryTokenMaxRunes  = 256
)

var (
	// ErrApprovalHistoryInvalid identifies a malformed query or a corrupt,
	// ambiguous, gapped, duplicated, or internally inconsistent history.
	ErrApprovalHistoryInvalid = errors.New("approval history invalid")
	// ErrApprovalHistoryUnavailable identifies a valid query whose exact
	// durable target pair is not available.
	ErrApprovalHistoryUnavailable = errors.New("approval history unavailable")
)

// ApprovalHistoryError is a bounded read-side error. It never includes query
// values, actor/session data, filesystem paths, proposal text, or storage
// diagnostics.
type ApprovalHistoryError struct {
	Kind string
}

func (e *ApprovalHistoryError) Error() string {
	if e == nil || e.Kind != "unavailable" {
		return ErrApprovalHistoryInvalid.Error()
	}
	return ErrApprovalHistoryUnavailable.Error()
}

func (e *ApprovalHistoryError) Unwrap() error {
	if e == nil {
		return ErrApprovalHistoryInvalid
	}
	base := ErrApprovalHistoryInvalid
	if e.Kind == "unavailable" {
		base = ErrApprovalHistoryUnavailable
	}
	return base
}

func approvalHistoryInvalid(_ error) error {
	return &ApprovalHistoryError{Kind: "invalid"}
}

func approvalHistoryUnavailable(_ error) error {
	return &ApprovalHistoryError{Kind: "unavailable"}
}

// ApprovalHistoryQuery identifies one exact durable proposal/evidence-pack
// pair. Workspace identity is supplied by the authenticated access gate.
type ApprovalHistoryQuery struct {
	ProposalID     string `json:"proposalId"`
	EvidencePackID string `json:"evidencePackId"`
}

// ApprovalHistoryQueryEnvelope is the strict MCP transport envelope for a
// durable approval-history query. Target and token are transport metadata and
// never become part of the semantic query authority.
type ApprovalHistoryQueryEnvelope struct {
	Query  ApprovalHistoryQuery
	Target *string
	Token  *string
}

// ParseApprovalHistoryQueryEnvelopeJSON decodes the exact MCP history query
// object before generic map conversion. It rejects duplicate or unknown keys,
// trailing JSON, invalid UTF-8, non-objects, type mismatches, and identities
// that fail the canonical lifecycle-ID rule.
func ParseApprovalHistoryQueryEnvelopeJSON(data []byte) (ApprovalHistoryQueryEnvelope, error) {
	var zero ApprovalHistoryQueryEnvelope
	allowed := map[string]struct{}{
		"proposalId": {}, "evidencePackId": {}, "target": {}, "token": {},
	}
	fields, err := decodeStrictApprovalFieldsWithLimit(data, allowed, approvalHistoryQueryJSONMaxBytes)
	if err != nil {
		return zero, ErrApprovalHistoryInvalid
	}
	decodeRequiredID := func(key string) (string, error) {
		raw, ok := fields[key]
		if !ok {
			return "", ErrApprovalHistoryInvalid
		}
		value, err := decodeApprovalJSONOptionalString(raw, true)
		if err != nil || value == nil || !ValidApprovalLifecycleID(*value) {
			return "", ErrApprovalHistoryInvalid
		}
		return *value, nil
	}
	proposalID, err := decodeRequiredID("proposalId")
	if err != nil {
		return zero, err
	}
	evidencePackID, err := decodeRequiredID("evidencePackId")
	if err != nil {
		return zero, err
	}
	decodeOptional := func(key string, maxRunes int) (*string, error) {
		raw, ok := fields[key]
		if !ok {
			return nil, nil
		}
		value, err := decodeApprovalJSONOptionalString(raw, false)
		if err != nil || value == nil || !utf8.ValidString(*value) || utf8.RuneCountInString(*value) > maxRunes {
			return nil, ErrApprovalHistoryInvalid
		}
		copy := *value
		return &copy, nil
	}
	target, err := decodeOptional("target", approvalHistoryQueryTargetMaxRunes)
	if err != nil {
		return zero, err
	}
	token, err := decodeOptional("token", approvalHistoryQueryTokenMaxRunes)
	if err != nil {
		return zero, err
	}
	if token != nil && !ValidApprovalAuthToken(*token) {
		return zero, ErrApprovalHistoryInvalid
	}
	return ApprovalHistoryQueryEnvelope{
		Query:  ApprovalHistoryQuery{ProposalID: proposalID, EvidencePackID: evidencePackID},
		Target: target,
		Token:  token,
	}, nil
}

// ValidApprovalAuthToken applies the bounded transport contract shared by
// configured MCP tokens and the public approval-history query envelope. It
// permits arbitrary non-control Unicode content while rejecting invalid UTF-8,
// blank or edge-whitespace values, and tokens longer than 256 code points.
func ValidApprovalAuthToken(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) > approvalHistoryQueryTokenMaxRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// approvalHistorySnapshotReader is the read-only durable history boundary.
// Production uses ApprovalTransactionStore; tests may supply a complete
// snapshot without widening the public service API.
type approvalHistorySnapshotReader interface {
	Snapshot(context.Context) (ApprovalTransactionSnapshot, error)
}

// ApprovalHistoryTarget is a defensive public copy of the persisted target
// identity used for query-time freshness projection.
type ApprovalHistoryTarget struct {
	WorkspaceID         string `json:"workspaceId"`
	ProposalID          string `json:"proposalId"`
	EvidencePackID      string `json:"evidencePackId"`
	ComputedBasisID     string `json:"computedBasisId"`
	GenerationID        string `json:"generationId"`
	IntentRevision      int64  `json:"intentRevision"`
	ValidatedSnapshotID string `json:"validatedSnapshotId"`
	WorkspaceEpoch      int64  `json:"workspaceEpoch"`
	MapID               string `json:"mapId"`
	TaskID              string `json:"taskId"`
}

// ApprovalHistoryResult is the canonical immutable read-time projection
// returned by the semantic history service. Events remain in durable append
// order, Aggregate is replay-derived, and Freshness is observed against the
// current validated active proof without changing durable state.
type ApprovalHistoryResult struct {
	SchemaID      string                `json:"schemaId"`
	SchemaVersion int                   `json:"schemaVersion"`
	Target        ApprovalHistoryTarget `json:"target"`
	Events        []ApprovalEventV2     `json:"events"`
	Aggregate     ApprovalAggregateV2   `json:"aggregate"`
	Freshness     string                `json:"freshness"`
}

// NewApprovalHistoryService opens only read-side dependencies. It cannot
// execute commands or recover pending publication, even on a cold process.
func NewApprovalHistoryService(repoRoot string) (*ApprovalExecutionService, error) {
	root, err := canonicalApprovalWorkspaceRoot(repoRoot)
	if err != nil {
		return nil, approvalHistoryInvalid(nil)
	}
	managedRoot := filepath.Join(root, ".codeflow", "approval-transactions")
	// Canonicalize only the authorized repository root. Validate managed
	// descendants before the generic store constructor can resolve an alias
	// and erase evidence of an escaping .codeflow ancestor.
	if err := validateApprovalTransactionRoot(managedRoot); err != nil {
		return nil, approvalHistoryInvalid(nil)
	}
	reader := newApprovalTransactionStore(managedRoot, approvalTransactionDependencies{
		currentAuthority: func(context.Context, approvalTransactionTarget, *approvalTransactionTargetRecord, func() error) error {
			return approvalTransactionInvalid(nil)
		},
	})
	if reader.initErr != nil || reader.root != managedRoot {
		return nil, approvalHistoryInvalid(nil)
	}
	return &ApprovalExecutionService{
		repoRoot: root, activeStorage: storage.New(root), historyReader: reader,
		historyLiveHead: func(expected string, read func() error) error {
			return workspace.WithDurableLiveHead(root, expected, read)
		},
	}, nil
}

// QueryApprovalHistory reads and validates the complete durable approval
// transaction state, selects exactly one authenticated proposal/pack target,
// derives its aggregate from the append-only events, and projects freshness
// at read time. It performs no proposal-store lookup and no durable write.
func (s *ApprovalExecutionService) QueryApprovalHistory(ctx context.Context, access ApprovalAccess, query ApprovalHistoryQuery) (ApprovalHistoryResult, error) {
	var zero ApprovalHistoryResult
	if s == nil || s.historyLiveHead == nil || s.historyReader == nil || s.repoRoot == "" {
		return zero, approvalHistoryInvalid(nil)
	}
	if err := approvalContextError(ctx); err != nil {
		return zero, err
	}
	if !validApprovalExecutionAccess(access, s.repoRoot) {
		return zero, &ApprovalUnauthorizedError{Reason: "approval workspace is not authorized"}
	}
	if !validApprovalLifecycleID(query.ProposalID) || !validApprovalLifecycleID(query.EvidencePackID) {
		return zero, approvalHistoryInvalid(nil)
	}

	snapshot, err := s.historyReader.Snapshot(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return zero, err
		}
		// A durable read failure means the append-only history cannot be
		// established. Keep that distinct from a valid query with no matching
		// target, and do not expose the storage cause at this boundary.
		return zero, approvalHistoryInvalid(nil)
	}
	state := approvalTransactionState{
		StoreVersion:       approvalTransactionStoreVersion,
		Events:             append([]ApprovalEventV2(nil), snapshot.Events...),
		Aggregates:         append([]ApprovalAggregateV2(nil), snapshot.Aggregates...),
		IdempotencyResults: append([]ApprovalIdempotencyResultV1(nil), snapshot.IdempotencyResults...),
		Outbox:             append([]ApprovalOutboxV1(nil), snapshot.Outbox...),
		Targets:            append([]approvalTransactionTargetRecord(nil), snapshot.targets...),
	}
	if err := validateApprovalTransactionState(state); err != nil {
		return zero, approvalHistoryInvalid(err)
	}
	if err := replayApprovalTransactionState(state); err != nil {
		return zero, approvalHistoryInvalid(err)
	}

	workspaceID := access.Workspace().WorkspaceID()
	var selected *approvalTransactionTargetRecord
	for index := range snapshot.targets {
		candidate := snapshot.targets[index]
		if candidate.Target.WorkspaceID != workspaceID || candidate.Target.ProposalID != query.ProposalID || candidate.Target.EvidencePackID != query.EvidencePackID {
			continue
		}
		if selected != nil {
			return zero, approvalHistoryInvalid(nil)
		}
		candidate.Aggregate = cloneApprovalAggregateV2(candidate.Aggregate)
		selected = &candidate
	}
	if selected == nil {
		return zero, approvalHistoryUnavailable(nil)
	}
	if !validApprovalTransactionTarget(selected.Target) || !validApprovalTransactionTargetDigest(selected.Target, selected.TargetDigest) || !approvalTransactionTargetAggregateIdentityMatches(selected.Target, selected.Aggregate) || selected.Aggregate.AggregateID == "" {
		return zero, approvalHistoryInvalid(nil)
	}

	events := make([]ApprovalEventV2, 0, selected.Aggregate.Version)
	for _, event := range snapshot.Events {
		if event.AggregateID == selected.Aggregate.AggregateID {
			events = append(events, event)
		}
	}
	derived, err := deriveApprovalHistoryAggregate(selected.Target, selected.Aggregate, events)
	if err != nil {
		return zero, approvalHistoryInvalid(err)
	}
	freshness, err := s.projectApprovalFreshness(ctx, selected.Target)
	if err != nil {
		return zero, err
	}
	target := approvalHistoryTargetFromStored(selected.Target)
	result := ApprovalHistoryResult{
		SchemaID: ApprovalHistoryV1SchemaID, SchemaVersion: ApprovalHistoryV1SchemaVersion,
		Target: target, Events: append([]ApprovalEventV2(nil), events...), Aggregate: cloneApprovalAggregateV2(derived), Freshness: freshness,
	}
	data, err := json.Marshal(result)
	if err != nil {
		return zero, approvalHistoryInvalid(err)
	}
	if err := contractharness.ValidateVS09Contract(ApprovalHistoryV1SchemaID, data); err != nil {
		return zero, approvalHistoryInvalid(err)
	}
	return result, nil
}

func approvalHistoryTargetFromStored(target approvalTransactionTarget) ApprovalHistoryTarget {
	return ApprovalHistoryTarget{
		WorkspaceID: target.WorkspaceID, ProposalID: target.ProposalID, EvidencePackID: target.EvidencePackID,
		ComputedBasisID: target.ComputedBasisID, GenerationID: target.GenerationID, IntentRevision: target.IntentRevision,
		ValidatedSnapshotID: target.ValidatedSnapshotID, WorkspaceEpoch: target.WorkspaceEpoch, MapID: target.MapID, TaskID: target.TaskID,
	}
}

func deriveApprovalHistoryAggregate(target approvalTransactionTarget, persisted ApprovalAggregateV2, events []ApprovalEventV2) (ApprovalAggregateV2, error) {
	if len(persisted.History) == 0 || persisted.History[0].Version != 0 || persisted.History[0].State != "none" || persisted.History[0].Decision != "none" || persisted.History[0].ApprovalID != "" {
		return ApprovalAggregateV2{}, errors.New("approval history genesis is invalid")
	}
	derived := cloneApprovalAggregateV2(persisted)
	derived.Version = 0
	derived.State = "none"
	derived.LastEventID = persisted.History[0].EventID
	derived.LastDecision = "none"
	derived.ActiveApprovalID = ""
	derived.History = []ApprovalHistoryEntry{persisted.History[0]}
	state := "none"
	active := ""
	for index, event := range events {
		wantVersion := int64(index + 1)
		if event.AggregateID != persisted.AggregateID || event.AggregateVersion != wantVersion || event.WorkspaceID != target.WorkspaceID || event.ProposalID != target.ProposalID || event.EvidencePackID != target.EvidencePackID || event.ComputedBasisID != target.ComputedBasisID || event.GenerationID != target.GenerationID || event.IntentRevision != target.IntentRevision {
			return ApprovalAggregateV2{}, errors.New("approval history event identity is inconsistent")
		}
		nextState, nextActive, ok := approvalHistoryEventTransition(state, active, event)
		if !ok {
			return ApprovalAggregateV2{}, errors.New("approval history event transition is inconsistent")
		}
		derived.History = append(derived.History, ApprovalHistoryEntry{Version: wantVersion, State: nextState, EventID: event.EventID, ApprovalID: event.ApprovalID, Decision: event.Decision})
		derived.Version = wantVersion
		derived.State = nextState
		derived.LastEventID = event.EventID
		derived.LastDecision = event.Decision
		derived.ActiveApprovalID = nextActive
		state, active = nextState, nextActive
	}
	if !reflect.DeepEqual(derived, persisted) {
		return ApprovalAggregateV2{}, errors.New("approval history aggregate projection is inconsistent")
	}
	return derived, nil
}

func approvalHistoryEventTransition(state, active string, event ApprovalEventV2) (string, string, bool) {
	switch event.Decision {
	case "approve":
		if state != "none" || event.LifecycleRelation != "initial" || event.PredecessorApprovalID != "" || event.ApprovedText == "" {
			return "", "", false
		}
		return "active", event.ApprovalID, true
	case "edit_then_approve":
		if state != "active" || event.LifecycleRelation != "edit" || event.PredecessorApprovalID != active || event.ApprovedText == "" {
			return "", "", false
		}
		return "active", event.ApprovalID, true
	case "reject":
		if (state != "none" && state != "active") || event.LifecycleRelation != "reject" || event.ApprovedText != "" || (state == "none" && event.PredecessorApprovalID != "") || (state == "active" && event.PredecessorApprovalID != "" && event.PredecessorApprovalID != active) {
			return "", "", false
		}
		return "rejected", "", true
	case "revoke":
		if state != "active" || event.LifecycleRelation != "revoke" || event.PredecessorApprovalID != active || event.ApprovedText != "" {
			return "", "", false
		}
		return "revoked", "", true
	case "supersede":
		if state != "active" || event.LifecycleRelation != "supersede" || event.PredecessorApprovalID != active || event.ApprovedText != "" {
			return "", "", false
		}
		return "superseded", "", true
	default:
		return "", "", false
	}
}
