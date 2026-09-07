package semantic

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"codeflow/internal/contractharness"
)

// ApprovalEventV2SchemaID and ApprovalAggregateV2SchemaID are the pinned
// schema identities for the pure approval lifecycle reducer outputs.
const (
	ApprovalEventV2SchemaID     = contractharness.ApprovalEventV2SchemaID
	ApprovalAggregateV2SchemaID = contractharness.ApprovalAggregateV2SchemaID
)

const (
	approvalLifecycleSchemaVersion       = 2
	approvalLifecycleMaxVersion    int64 = 1_000_000_000
	approvalLifecycleMaxHistory          = 256
	approvalLifecycleMaxID               = 256
	approvalLifecycleMaxText             = 4096
)

var (
	// ErrApprovalLifecycleInvalid identifies malformed input, metadata, or a
	// transition that cannot be admitted by the pure lifecycle boundary.
	ErrApprovalLifecycleInvalid = errors.New("approval lifecycle invalid")
	// ErrApprovalLifecycleConflict identifies a valid command whose expected
	// state or version is stale relative to the supplied aggregate.
	ErrApprovalLifecycleConflict = errors.New("approval lifecycle conflict")
	// ErrApprovalLifecycleReferenceConflict identifies an identity or
	// predecessor mismatch between a command and the current aggregate.
	ErrApprovalLifecycleReferenceConflict = errors.New("approval lifecycle reference conflict")
)

// ApprovalLifecycleError is the bounded typed error returned by the reducer.
// CurrentState and CurrentVersion are populated for state/version conflicts,
// allowing callers to recover without exposing proposal or authority data.
type ApprovalLifecycleError struct {
	Kind           string
	CurrentState   string
	CurrentVersion int64
}

func (e *ApprovalLifecycleError) Error() string {
	if e == nil {
		return ErrApprovalLifecycleInvalid.Error()
	}
	switch e.Kind {
	case "conflict":
		return fmt.Sprintf("approval lifecycle conflict at current state %s version %d", e.CurrentState, e.CurrentVersion)
	case "reference":
		return ErrApprovalLifecycleReferenceConflict.Error()
	case "metadata":
		return "approval lifecycle invalid metadata"
	case "aggregate":
		return "approval lifecycle invalid aggregate"
	case "command":
		return "approval lifecycle invalid command"
	case "transition":
		return "approval lifecycle invalid transition"
	default:
		return ErrApprovalLifecycleInvalid.Error()
	}
}

func (e *ApprovalLifecycleError) Unwrap() error {
	if e == nil {
		return ErrApprovalLifecycleInvalid
	}
	switch e.Kind {
	case "conflict":
		return ErrApprovalLifecycleConflict
	case "reference":
		return errors.Join(ErrApprovalLifecycleConflict, ErrApprovalLifecycleReferenceConflict)
	default:
		return ErrApprovalLifecycleInvalid
	}
}

// ApprovalTransitionMetadata contains trusted Core transition metadata. The
// reducer does not derive event or approval identity, time, or stored
// proposal text from untrusted command fields.
type ApprovalTransitionMetadata struct {
	EventID            string
	ApprovalID         string
	AggregateID        string
	OccurredAt         time.Time
	StoredProposalText string
}

// ApprovalEventV2 is one immutable actor-attributed lifecycle event emitted by
// ReduceApprovalCommandV2. It contains no mutable slices or pointers.
type ApprovalEventV2 struct {
	SchemaID              string `json:"schemaId"`
	SchemaVersion         int    `json:"schemaVersion"`
	EventID               string `json:"eventId"`
	ApprovalID            string `json:"approvalId"`
	AggregateID           string `json:"aggregateId"`
	AggregateVersion      int64  `json:"aggregateVersion"`
	ActorID               string `json:"actorId"`
	SessionID             string `json:"sessionId"`
	WorkspaceID           string `json:"workspaceId"`
	ProposalID            string `json:"proposalId"`
	EvidencePackID        string `json:"evidencePackId"`
	ComputedBasisID       string `json:"computedBasisId"`
	GenerationID          string `json:"generationId"`
	IntentRevision        int64  `json:"intentRevision"`
	Decision              string `json:"decision"`
	ApprovedText          string `json:"approvedText"`
	Timestamp             string `json:"timestamp"`
	LifecycleRelation     string `json:"lifecycleRelation"`
	PredecessorApprovalID string `json:"predecessorApprovalId,omitempty"`
}

// ApprovalHistoryEntry is the compact immutable replay entry stored in an
// ApprovalAggregateV2 history.
type ApprovalHistoryEntry struct {
	Version    int64  `json:"version"`
	State      string `json:"state"`
	EventID    string `json:"eventId"`
	ApprovalID string `json:"approvalId,omitempty"`
	Decision   string `json:"decision"`
}

// ApprovalAggregateV2 is the replay-derived lifecycle state. History is
// ordered from the required version-zero genesis entry through Version.
type ApprovalAggregateV2 struct {
	SchemaID         string                 `json:"schemaId"`
	SchemaVersion    int                    `json:"schemaVersion"`
	AggregateID      string                 `json:"aggregateId"`
	WorkspaceID      string                 `json:"workspaceId"`
	ProposalID       string                 `json:"proposalId"`
	EvidencePackID   string                 `json:"evidencePackId"`
	ComputedBasisID  string                 `json:"computedBasisId"`
	GenerationID     string                 `json:"generationId"`
	IntentRevision   int64                  `json:"intentRevision"`
	Version          int64                  `json:"version"`
	State            string                 `json:"state"`
	LastEventID      string                 `json:"lastEventId"`
	LastDecision     string                 `json:"lastDecision"`
	ActiveApprovalID string                 `json:"activeApprovalId,omitempty"`
	History          []ApprovalHistoryEntry `json:"history"`
}

// NewApprovalGenesisAggregateV2 creates the required version-zero/none
// aggregate for one Core-bound command identity. genesisEventID is a
// non-empty sentinel and is not reused as a transition event ID.
func NewApprovalGenesisAggregateV2(command *ValidatedApprovalCommandV2, aggregateID, genesisEventID string) (ApprovalAggregateV2, error) {
	if command == nil || command.Validate() != nil {
		return ApprovalAggregateV2{}, newApprovalLifecycleError("command", "", 0)
	}
	if !validApprovalLifecycleID(aggregateID) || !validApprovalLifecycleID(genesisEventID) {
		return ApprovalAggregateV2{}, newApprovalLifecycleError("metadata", "", 0)
	}
	value := command.Command()
	aggregate := ApprovalAggregateV2{
		SchemaID:        ApprovalAggregateV2SchemaID,
		SchemaVersion:   approvalLifecycleSchemaVersion,
		AggregateID:     aggregateID,
		WorkspaceID:     value.WorkspaceID,
		ProposalID:      value.ProposalID,
		EvidencePackID:  value.EvidencePackID,
		ComputedBasisID: value.ComputedBasisID,
		GenerationID:    value.GenerationID,
		IntentRevision:  value.IntentRevision,
		Version:         0,
		State:           "none",
		LastEventID:     genesisEventID,
		LastDecision:    "none",
		History: []ApprovalHistoryEntry{{
			Version: 0, State: "none", EventID: genesisEventID, Decision: "none",
		}},
	}
	if err := validateApprovalLifecycleAggregate(aggregate); err != nil {
		return ApprovalAggregateV2{}, newApprovalLifecycleError("aggregate", "", 0)
	}
	return cloneApprovalAggregateV2(aggregate), nil
}

// ReduceApprovalCommandV2 applies exactly one validated lifecycle command to
// an aggregate. It is pure with respect to all inputs and returns nil outputs
// on every failure.
func ReduceApprovalCommandV2(current ApprovalAggregateV2, command *ValidatedApprovalCommandV2, metadata ApprovalTransitionMetadata) (*ApprovalEventV2, *ApprovalAggregateV2, error) {
	if command == nil || command.Validate() != nil {
		return nil, nil, newApprovalLifecycleError("command", "", 0)
	}
	if err := validateApprovalLifecycleAggregate(current); err != nil {
		return nil, nil, newApprovalLifecycleError("aggregate", "", 0)
	}
	value := command.Command()
	if !approvalLifecycleIdentityMatches(current, value) {
		return nil, nil, newApprovalLifecycleError("reference", current.State, current.Version)
	}
	if current.State != value.ExpectedState || current.Version != value.ExpectedApprovalVersion {
		return nil, nil, newApprovalLifecycleError("conflict", current.State, current.Version)
	}
	if !validApprovalLifecycleID(metadata.AggregateID) || metadata.AggregateID != current.AggregateID {
		if validApprovalLifecycleID(metadata.AggregateID) {
			return nil, nil, newApprovalLifecycleError("reference", current.State, current.Version)
		}
		return nil, nil, newApprovalLifecycleError("metadata", current.State, current.Version)
	}
	if err := validateApprovalTransitionMetadata(metadata, value.Decision); err != nil {
		return nil, nil, newApprovalLifecycleError("metadata", current.State, current.Version)
	}
	if current.Version >= approvalLifecycleMaxVersion || len(current.History) >= approvalLifecycleMaxHistory {
		return nil, nil, newApprovalLifecycleError("transition", current.State, current.Version)
	}
	if approvalLifecycleIdentityAlreadyUsed(current, metadata.EventID, metadata.ApprovalID) {
		return nil, nil, newApprovalLifecycleError("metadata", current.State, current.Version)
	}

	nextState, relation, approvedText, predecessor, err := approvalLifecycleTransition(current, value)
	if err != nil {
		return nil, nil, err
	}
	if value.Decision == "approve" {
		approvedText = metadata.StoredProposalText
	}
	nextVersion := current.Version + 1
	timestamp, timestampOK := canonicalApprovalLifecycleTimestamp(metadata.OccurredAt)
	if !timestampOK {
		return nil, nil, newApprovalLifecycleError("metadata", current.State, current.Version)
	}
	event := ApprovalEventV2{
		SchemaID:              ApprovalEventV2SchemaID,
		SchemaVersion:         approvalLifecycleSchemaVersion,
		EventID:               metadata.EventID,
		ApprovalID:            metadata.ApprovalID,
		AggregateID:           current.AggregateID,
		AggregateVersion:      nextVersion,
		ActorID:               value.ActorID,
		SessionID:             value.SessionID,
		WorkspaceID:           value.WorkspaceID,
		ProposalID:            value.ProposalID,
		EvidencePackID:        value.EvidencePackID,
		ComputedBasisID:       value.ComputedBasisID,
		GenerationID:          value.GenerationID,
		IntentRevision:        value.IntentRevision,
		Decision:              value.Decision,
		ApprovedText:          approvedText,
		Timestamp:             timestamp,
		LifecycleRelation:     relation,
		PredecessorApprovalID: predecessor,
	}
	next := cloneApprovalAggregateV2(current)
	next.SchemaID = ApprovalAggregateV2SchemaID
	next.SchemaVersion = approvalLifecycleSchemaVersion
	next.Version = nextVersion
	next.State = nextState
	next.LastEventID = metadata.EventID
	next.LastDecision = value.Decision
	next.History = append(next.History, ApprovalHistoryEntry{
		Version: nextVersion, State: nextState, EventID: metadata.EventID, ApprovalID: metadata.ApprovalID, Decision: value.Decision,
	})
	if nextState == "active" {
		next.ActiveApprovalID = metadata.ApprovalID
	} else {
		next.ActiveApprovalID = ""
	}

	if err := validateApprovalLifecycleEvent(event); err != nil || validateApprovalLifecycleAggregate(next) != nil {
		return nil, nil, newApprovalLifecycleError("transition", current.State, current.Version)
	}
	return &event, &next, nil
}

// ApplyApprovalCommandV2 is a pointer-taking convenience wrapper for callers
// that retain the current aggregate as an owned pointer.
func ApplyApprovalCommandV2(current *ApprovalAggregateV2, command *ValidatedApprovalCommandV2, metadata ApprovalTransitionMetadata) (*ApprovalEventV2, *ApprovalAggregateV2, error) {
	if current == nil {
		return nil, nil, newApprovalLifecycleError("aggregate", "", 0)
	}
	return ReduceApprovalCommandV2(*current, command, metadata)
}

func newApprovalLifecycleError(kind, state string, version int64) error {
	return &ApprovalLifecycleError{Kind: kind, CurrentState: state, CurrentVersion: version}
}

func validApprovalLifecycleID(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > approvalLifecycleMaxID || strings.TrimSpace(value) != value || strings.Contains(value, "..") {
		return false
	}
	for _, r := range value {
		if r == '/' || r == '\\' || r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidApprovalLifecycleID exposes the canonical bounded lifecycle identity
// rule to protocol adapters. It keeps transport validation aligned with the
// durable approval boundary without duplicating its UTF-8, rune-count, and
// path/control-character checks.
func ValidApprovalLifecycleID(value string) bool {
	return validApprovalLifecycleID(value)
}

func validApprovalLifecycleWireString(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
}

func validApprovalLifecycleText(value string) bool {
	return validApprovalLifecycleWireString(value, approvalLifecycleMaxText) && strings.TrimSpace(value) != ""
}

func canonicalApprovalLifecycleTimestamp(value time.Time) (string, bool) {
	if value.IsZero() {
		return "", false
	}
	timestamp := value.UTC().Format(time.RFC3339Nano)
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || !parsed.Equal(value) || parsed.Format(time.RFC3339Nano) != timestamp {
		return "", false
	}
	return timestamp, true
}

func validApprovalLifecycleTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.Format(time.RFC3339Nano) == value
}

func validateApprovalTransitionMetadata(metadata ApprovalTransitionMetadata, decision string) error {
	if !validApprovalLifecycleID(metadata.EventID) || !validApprovalLifecycleID(metadata.ApprovalID) || metadata.OccurredAt.IsZero() {
		return ErrApprovalLifecycleInvalid
	}
	if timestamp, ok := canonicalApprovalLifecycleTimestamp(metadata.OccurredAt); !ok || len(timestamp) > 128 {
		return ErrApprovalLifecycleInvalid
	}
	if decision == "approve" {
		if !validApprovalLifecycleText(metadata.StoredProposalText) {
			return ErrApprovalLifecycleInvalid
		}
	} else if strings.TrimSpace(metadata.StoredProposalText) != "" {
		return ErrApprovalLifecycleInvalid
	}
	return nil
}

func approvalLifecycleIdentityMatches(current ApprovalAggregateV2, command ApprovalCommandV2) bool {
	return current.WorkspaceID == command.WorkspaceID && current.ProposalID == command.ProposalID && current.EvidencePackID == command.EvidencePackID && current.ComputedBasisID == command.ComputedBasisID && current.GenerationID == command.GenerationID && current.IntentRevision == command.IntentRevision
}

func approvalLifecycleIdentityAlreadyUsed(current ApprovalAggregateV2, eventID, approvalID string) bool {
	for _, entry := range current.History {
		if entry.EventID == eventID || (entry.ApprovalID != "" && entry.ApprovalID == approvalID) {
			return true
		}
	}
	return false
}

func approvalLifecycleTransition(current ApprovalAggregateV2, command ApprovalCommandV2) (state, relation, text, predecessor string, err error) {
	predecessorValue := ""
	if command.PredecessorApprovalID != nil {
		predecessorValue = *command.PredecessorApprovalID
	}
	switch command.Decision {
	case "approve":
		if current.State != "none" || command.PredecessorApprovalID != nil {
			return "", "", "", "", newApprovalLifecycleError("transition", current.State, current.Version)
		}
		return "active", "initial", "", "", nil
	case "edit_then_approve":
		if current.State != "active" || command.PredecessorApprovalID == nil || predecessorValue != current.ActiveApprovalID || command.EditedText == nil || !validApprovalLifecycleText(*command.EditedText) {
			return "", "", "", "", newApprovalLifecycleError("reference", current.State, current.Version)
		}
		return "active", "edit", *command.EditedText, predecessorValue, nil
	case "reject":
		if current.State != "none" && current.State != "active" {
			return "", "", "", "", newApprovalLifecycleError("transition", current.State, current.Version)
		}
		if current.State == "none" && command.PredecessorApprovalID != nil {
			return "", "", "", "", newApprovalLifecycleError("reference", current.State, current.Version)
		}
		if current.State == "active" && command.PredecessorApprovalID != nil && predecessorValue != current.ActiveApprovalID {
			return "", "", "", "", newApprovalLifecycleError("reference", current.State, current.Version)
		}
		return "rejected", "reject", "", predecessorValue, nil
	case "revoke":
		if current.State != "active" || command.PredecessorApprovalID == nil || predecessorValue != current.ActiveApprovalID {
			return "", "", "", "", newApprovalLifecycleError("reference", current.State, current.Version)
		}
		return "revoked", "revoke", "", predecessorValue, nil
	case "supersede":
		if current.State != "active" || command.PredecessorApprovalID == nil || predecessorValue != current.ActiveApprovalID {
			return "", "", "", "", newApprovalLifecycleError("reference", current.State, current.Version)
		}
		return "superseded", "supersede", "", predecessorValue, nil
	default:
		return "", "", "", "", newApprovalLifecycleError("transition", current.State, current.Version)
	}
}

func validateApprovalLifecycleEvent(event ApprovalEventV2) error {
	if !validApprovalLifecycleID(event.EventID) || !validApprovalLifecycleID(event.ApprovalID) || !validApprovalLifecycleID(event.AggregateID) {
		return ErrApprovalLifecycleInvalid
	}
	for _, value := range []struct {
		value string
		max   int
	}{
		{value: event.SchemaID, max: approvalLifecycleMaxID},
		{value: event.EventID, max: approvalLifecycleMaxID},
		{value: event.ApprovalID, max: approvalLifecycleMaxID},
		{value: event.AggregateID, max: approvalLifecycleMaxID},
		{value: event.ActorID, max: approvalLifecycleMaxID},
		{value: event.SessionID, max: approvalLifecycleMaxID},
		{value: event.WorkspaceID, max: approvalLifecycleMaxID},
		{value: event.ProposalID, max: approvalLifecycleMaxID},
		{value: event.EvidencePackID, max: approvalLifecycleMaxID},
		{value: event.ComputedBasisID, max: approvalLifecycleMaxID},
		{value: event.GenerationID, max: approvalLifecycleMaxID},
		{value: event.Decision, max: approvalLifecycleMaxID},
		{value: event.ApprovedText, max: approvalLifecycleMaxText},
		{value: event.Timestamp, max: 128},
		{value: event.LifecycleRelation, max: approvalLifecycleMaxID},
	} {
		if !validApprovalLifecycleWireString(value.value, value.max) {
			return ErrApprovalLifecycleInvalid
		}
	}
	if !validApprovalLifecycleTimestamp(event.Timestamp) {
		return ErrApprovalLifecycleInvalid
	}
	if event.PredecessorApprovalID != "" && !validApprovalLifecycleID(event.PredecessorApprovalID) {
		return ErrApprovalLifecycleInvalid
	}
	data, err := json.Marshal(event)
	if err != nil {
		return ErrApprovalLifecycleInvalid
	}
	return contractharness.ValidateVS09Contract(ApprovalEventV2SchemaID, data)
}

func validateApprovalLifecycleAggregate(aggregate ApprovalAggregateV2) error {
	if !validApprovalLifecycleID(aggregate.AggregateID) || !validApprovalLifecycleID(aggregate.LastEventID) {
		return ErrApprovalLifecycleInvalid
	}
	for _, value := range []struct {
		value string
		max   int
	}{
		{value: aggregate.SchemaID, max: approvalLifecycleMaxID},
		{value: aggregate.AggregateID, max: approvalLifecycleMaxID},
		{value: aggregate.WorkspaceID, max: approvalLifecycleMaxID},
		{value: aggregate.ProposalID, max: approvalLifecycleMaxID},
		{value: aggregate.EvidencePackID, max: approvalLifecycleMaxID},
		{value: aggregate.ComputedBasisID, max: approvalLifecycleMaxID},
		{value: aggregate.GenerationID, max: approvalLifecycleMaxID},
		{value: aggregate.State, max: approvalLifecycleMaxID},
		{value: aggregate.LastEventID, max: approvalLifecycleMaxID},
		{value: aggregate.LastDecision, max: approvalLifecycleMaxID},
		{value: aggregate.ActiveApprovalID, max: approvalLifecycleMaxID},
	} {
		if !validApprovalLifecycleWireString(value.value, value.max) {
			return ErrApprovalLifecycleInvalid
		}
	}
	if aggregate.ActiveApprovalID != "" && !validApprovalLifecycleID(aggregate.ActiveApprovalID) {
		return ErrApprovalLifecycleInvalid
	}
	for index, entry := range aggregate.History {
		if !validApprovalLifecycleID(entry.EventID) {
			return ErrApprovalLifecycleInvalid
		}
		if index == 0 {
			if entry.ApprovalID != "" {
				return ErrApprovalLifecycleInvalid
			}
		} else if !validApprovalLifecycleID(entry.ApprovalID) {
			return ErrApprovalLifecycleInvalid
		}
		if !validApprovalLifecycleWireString(entry.State, approvalLifecycleMaxID) || !validApprovalLifecycleWireString(entry.Decision, approvalLifecycleMaxID) {
			return ErrApprovalLifecycleInvalid
		}
	}
	data, err := json.Marshal(aggregate)
	if err != nil {
		return ErrApprovalLifecycleInvalid
	}
	if err := contractharness.ValidateVS09Contract(ApprovalAggregateV2SchemaID, data); err != nil {
		return err
	}
	seenEvents := make(map[string]struct{}, len(aggregate.History))
	seenApprovals := make(map[string]struct{}, len(aggregate.History))
	for _, entry := range aggregate.History {
		if _, exists := seenEvents[entry.EventID]; exists {
			return ErrApprovalLifecycleInvalid
		}
		seenEvents[entry.EventID] = struct{}{}
		if entry.ApprovalID != "" {
			if _, exists := seenApprovals[entry.ApprovalID]; exists {
				return ErrApprovalLifecycleInvalid
			}
			seenApprovals[entry.ApprovalID] = struct{}{}
		}
	}
	return nil
}

func cloneApprovalAggregateV2(value ApprovalAggregateV2) ApprovalAggregateV2 {
	if value.History == nil {
		return value
	}
	value.History = append([]ApprovalHistoryEntry(nil), value.History...)
	return value
}
