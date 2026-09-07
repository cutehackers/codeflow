package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"codeflow/internal/contractharness"
)

// ApprovalCommandV2SchemaID is the pinned schema identity for the v2
// approval command. The semantic boundary uses the contract harness as the
// sole schema and cross-field authority.
const ApprovalCommandV2SchemaID = contractharness.ApprovalCommandV2SchemaID

const (
	approvalCommandIDMax   = 256
	approvalCommandTextMax = 4096
)

var (
	// ErrApprovalCommandInvalid identifies a command that cannot be admitted to
	// the approval lifecycle. Its typed wrapper deliberately omits command and
	// authority values from its public error text.
	ErrApprovalCommandInvalid = errors.New("approval command invalid")
)

// ApprovalCommandError is the typed failure returned by command binding and
// strict decoding. Kind contains only a bounded validation class such as
// "authority", "reference", "version", "transition", or "schema".
type ApprovalCommandError struct {
	Kind string
}

func (e *ApprovalCommandError) Error() string {
	if e == nil || e.Kind == "" {
		return ErrApprovalCommandInvalid.Error()
	}
	return ErrApprovalCommandInvalid.Error() + ": " + e.Kind
}

func (e *ApprovalCommandError) Unwrap() error { return ErrApprovalCommandInvalid }

func newApprovalCommandError(kind string) error {
	if strings.TrimSpace(kind) == "" {
		kind = "validation"
	}
	return &ApprovalCommandError{Kind: kind}
}

// ApprovalCommandDraft is untrusted command intent. It deliberately has no
// actorId, sessionId, or workspaceId fields. Those values can only be bound
// from a gate-issued ApprovalAccess.
type ApprovalCommandDraft struct {
	CommandID               string  `json:"commandId"`
	ProposalID              string  `json:"proposalId"`
	EvidencePackID          string  `json:"evidencePackId"`
	ComputedBasisID         string  `json:"computedBasisId"`
	GenerationID            string  `json:"generationId"`
	IntentRevision          int64   `json:"intentRevision"`
	Decision                string  `json:"decision"`
	EditedText              *string `json:"editedText,omitempty"`
	IdempotencyKey          string  `json:"idempotencyKey"`
	ExpectedApprovalVersion int64   `json:"expectedApprovalVersion"`
	ExpectedState           string  `json:"expectedState"`
	PredecessorApprovalID   *string `json:"predecessorApprovalId,omitempty"`
}

const approvalCommandDraftJSONMaxBytes = 1 << 20

// ApprovalCommandDraftEnvelope is the strict untrusted approval request
// envelope used by the MCP transport. Target and token are transport
// metadata. They are never copied into ApprovalCommandDraft or used as
// authority. The draft itself remains actorless until a gate binds it.
type ApprovalCommandDraftEnvelope struct {
	Draft  ApprovalCommandDraft
	Target *string
	Token  *string
}

// ParseApprovalCommandDraftJSON decodes the exact actorless v2 draft object.
// It rejects duplicate keys at every nesting level, unknown fields, trailing
// JSON documents, invalid UTF-8, missing fields (including numeric zero), and
// type mismatches before any approval work is attempted. Full semantic and
// authority validation is performed by BindApprovalCommandV2 after the Core
// access gate supplies authority.
func ParseApprovalCommandDraftJSON(data []byte) (ApprovalCommandDraft, error) {
	fields, err := decodeStrictApprovalFields(data, approvalCommandDraftJSONFields())
	if err != nil {
		return ApprovalCommandDraft{}, err
	}
	draft, err := approvalCommandDraftFromJSONFields(fields)
	if err != nil {
		return ApprovalCommandDraft{}, err
	}
	if err := validateApprovalCommandDraftStrings(draft); err != nil {
		return ApprovalCommandDraft{}, err
	}
	return draft, nil
}

// ParseApprovalCommandDraftEnvelopeJSON decodes the MCP approval envelope.
// Only target and token are accepted in addition to the exact actorless
// draft fields. This keeps raw JSON available to the MCP handler without
// moving routing or transport credentials into the semantic command.
func ParseApprovalCommandDraftEnvelopeJSON(data []byte) (ApprovalCommandDraftEnvelope, error) {
	allowed := approvalCommandDraftJSONFields()
	allowed["target"] = struct{}{}
	allowed["token"] = struct{}{}
	fields, err := decodeStrictApprovalFields(data, allowed)
	if err != nil {
		return ApprovalCommandDraftEnvelope{}, err
	}
	draft, err := approvalCommandDraftFromJSONFields(fields)
	if err != nil {
		return ApprovalCommandDraftEnvelope{}, err
	}
	if err := validateApprovalCommandDraftStrings(draft); err != nil {
		return ApprovalCommandDraftEnvelope{}, err
	}
	var target, token *string
	if raw, ok := fields["target"]; ok {
		target, err = decodeApprovalJSONOptionalString(raw, false)
		if err != nil {
			return ApprovalCommandDraftEnvelope{}, newApprovalCommandError("schema")
		}
	}
	if raw, ok := fields["token"]; ok {
		token, err = decodeApprovalJSONOptionalString(raw, false)
		if err != nil {
			return ApprovalCommandDraftEnvelope{}, newApprovalCommandError("schema")
		}
	}
	return ApprovalCommandDraftEnvelope{Draft: draft, Target: target, Token: token}, nil
}

func approvalCommandDraftJSONFields() map[string]struct{} {
	return map[string]struct{}{
		"commandId": {}, "proposalId": {}, "evidencePackId": {}, "computedBasisId": {},
		"generationId": {}, "intentRevision": {}, "decision": {}, "editedText": {},
		"idempotencyKey": {}, "expectedApprovalVersion": {}, "expectedState": {},
		"predecessorApprovalId": {},
	}
}

func decodeStrictApprovalFields(data []byte, allowed map[string]struct{}) (map[string]json.RawMessage, error) {
	return decodeStrictApprovalFieldsWithLimit(data, allowed, approvalCommandDraftJSONMaxBytes)
}

func decodeStrictApprovalFieldsWithLimit(data []byte, allowed map[string]struct{}, maxBytes int) (map[string]json.RawMessage, error) {
	if maxBytes <= 0 || len(data) == 0 || len(data) > maxBytes || !utf8.Valid(data) {
		return nil, newApprovalCommandError("syntax")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, newApprovalCommandError("syntax")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, newApprovalCommandError("syntax")
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return nil, newApprovalCommandError("schema")
		}
	}
	return fields, nil
}

func approvalCommandDraftFromJSONFields(fields map[string]json.RawMessage) (ApprovalCommandDraft, error) {
	var zero ApprovalCommandDraft
	stringField := func(key string) (string, error) {
		raw, ok := fields[key]
		if !ok {
			return "", newApprovalCommandError("schema")
		}
		value, err := decodeApprovalJSONOptionalString(raw, true)
		if err != nil || value == nil {
			return "", newApprovalCommandError("schema")
		}
		return *value, nil
	}
	intField := func(key string) (int64, error) {
		raw, ok := fields[key]
		if !ok {
			return 0, newApprovalCommandError("schema")
		}
		var value *int64
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return 0, newApprovalCommandError("schema")
		}
		return *value, nil
	}
	commandID, err := stringField("commandId")
	if err != nil {
		return zero, err
	}
	proposalID, err := stringField("proposalId")
	if err != nil {
		return zero, err
	}
	packID, err := stringField("evidencePackId")
	if err != nil {
		return zero, err
	}
	basisID, err := stringField("computedBasisId")
	if err != nil {
		return zero, err
	}
	generationID, err := stringField("generationId")
	if err != nil {
		return zero, err
	}
	intentRevision, err := intField("intentRevision")
	if err != nil {
		return zero, err
	}
	decision, err := stringField("decision")
	if err != nil {
		return zero, err
	}
	idempotencyKey, err := stringField("idempotencyKey")
	if err != nil {
		return zero, err
	}
	expectedVersion, err := intField("expectedApprovalVersion")
	if err != nil {
		return zero, err
	}
	expectedState, err := stringField("expectedState")
	if err != nil {
		return zero, err
	}
	draft := ApprovalCommandDraft{
		CommandID: commandID, ProposalID: proposalID, EvidencePackID: packID,
		ComputedBasisID: basisID, GenerationID: generationID, IntentRevision: intentRevision,
		Decision: decision, IdempotencyKey: idempotencyKey, ExpectedApprovalVersion: expectedVersion,
		ExpectedState: expectedState,
	}
	for key, destination := range map[string]**string{
		"editedText":            &draft.EditedText,
		"predecessorApprovalId": &draft.PredecessorApprovalID,
	} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		value, err := decodeApprovalJSONOptionalString(raw, true)
		if err != nil || value == nil {
			return zero, newApprovalCommandError("schema")
		}
		*destination = value
	}
	return draft, nil
}

func decodeApprovalJSONOptionalString(raw []byte, rejectEmpty bool) (*string, error) {
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, errors.New("approval field is not a string")
	}
	if !utf8.ValidString(*value) || (rejectEmpty && strings.TrimSpace(*value) == "") {
		return nil, errors.New("approval field is empty or invalid")
	}
	copy := *value
	return &copy, nil
}

func validateApprovalCommandDraftStrings(draft ApprovalCommandDraft) error {
	for _, value := range []string{draft.CommandID, draft.ProposalID, draft.EvidencePackID, draft.ComputedBasisID, draft.GenerationID, draft.Decision, draft.IdempotencyKey, draft.ExpectedState} {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > approvalCommandIDMax || strings.TrimSpace(value) == "" {
			return newApprovalCommandError("encoding")
		}
	}
	for _, field := range []struct {
		value *string
		max   int
	}{
		{value: draft.EditedText, max: approvalCommandTextMax},
		{value: draft.PredecessorApprovalID, max: approvalCommandIDMax},
	} {
		if field.value == nil {
			continue
		}
		if !utf8.ValidString(*field.value) || strings.TrimSpace(*field.value) == "" {
			return newApprovalCommandError("encoding")
		}
		if utf8.RuneCountInString(*field.value) > field.max {
			return newApprovalCommandError("encoding")
		}
	}
	return nil
}

// ApprovalCommandV2 mirrors rflsc.approval-command.v2.schema.json exactly.
// It is a wire/value model. Callers must use ValidatedApprovalCommandV2 as
// the lifecycle input because this model itself is intentionally constructible
// for decoding and contract validation.
type ApprovalCommandV2 struct {
	SchemaID                string  `json:"schemaId"`
	SchemaVersion           int     `json:"schemaVersion"`
	CommandID               string  `json:"commandId"`
	ActorID                 string  `json:"actorId"`
	SessionID               string  `json:"sessionId"`
	WorkspaceID             string  `json:"workspaceId"`
	ProposalID              string  `json:"proposalId"`
	EvidencePackID          string  `json:"evidencePackId"`
	ComputedBasisID         string  `json:"computedBasisId"`
	GenerationID            string  `json:"generationId"`
	IntentRevision          int64   `json:"intentRevision"`
	Decision                string  `json:"decision"`
	EditedText              *string `json:"editedText,omitempty"`
	IdempotencyKey          string  `json:"idempotencyKey"`
	ExpectedApprovalVersion int64   `json:"expectedApprovalVersion"`
	ExpectedState           string  `json:"expectedState"`
	PredecessorApprovalID   *string `json:"predecessorApprovalId,omitempty"`
}

// ValidatedApprovalCommandV2 is the only command value suitable for a later
// approval reducer. Its command, access, and digest are private. Accessors
// return defensive values and Validate rechecks the Core authority seal,
// exact authority binding, contract rules, and the immutable content seal.
type ValidatedApprovalCommandV2 struct {
	command ApprovalCommandV2
	access  ApprovalAccess
	digest  [sha256.Size]byte
}

// BindApprovalCommandV2 fills authority and schema identity from Core and
// validates the complete command contract before returning a sealed value.
func BindApprovalCommandV2(access ApprovalAccess, draft ApprovalCommandDraft) (*ValidatedApprovalCommandV2, error) {
	if !validApprovalCommandAccess(access) {
		return nil, newApprovalCommandError("authority")
	}
	command := ApprovalCommandV2{
		SchemaID:                ApprovalCommandV2SchemaID,
		SchemaVersion:           2,
		CommandID:               draft.CommandID,
		ActorID:                 access.actor.ActorID,
		SessionID:               access.actor.SessionID,
		WorkspaceID:             access.workspace.workspaceID,
		ProposalID:              draft.ProposalID,
		EvidencePackID:          draft.EvidencePackID,
		ComputedBasisID:         draft.ComputedBasisID,
		GenerationID:            draft.GenerationID,
		IntentRevision:          draft.IntentRevision,
		Decision:                draft.Decision,
		EditedText:              cloneApprovalCommandOptionalString(draft.EditedText),
		IdempotencyKey:          draft.IdempotencyKey,
		ExpectedApprovalVersion: draft.ExpectedApprovalVersion,
		ExpectedState:           draft.ExpectedState,
		PredecessorApprovalID:   cloneApprovalCommandOptionalString(draft.PredecessorApprovalID),
	}
	if err := validateApprovalCommandValue(command); err != nil {
		return nil, err
	}
	digest, err := approvalCommandDigest(command)
	if err != nil {
		return nil, newApprovalCommandError("integrity")
	}
	return &ValidatedApprovalCommandV2{command: command, access: access, digest: digest}, nil
}

// ParseAndBindApprovalCommandV2 strictly decodes a complete command JSON
// document, rejects duplicate keys and trailing documents, verifies the
// contract, and requires its authority fields to equal the supplied Core
// access.
func ParseAndBindApprovalCommandV2(data []byte, access ApprovalAccess) (*ValidatedApprovalCommandV2, error) {
	if !validApprovalCommandAccess(access) {
		return nil, newApprovalCommandError("authority")
	}
	if !utf8.Valid(data) {
		return nil, newApprovalCommandError("encoding")
	}
	command, err := decodeStrictApprovalCommandV2(data)
	if err != nil {
		return nil, err
	}
	if err := validateApprovalCommandWireStrings(command); err != nil {
		return nil, err
	}
	if err := validateApprovalCommandJSON(data); err != nil {
		return nil, err
	}
	if err := validateApprovalCommandValue(command); err != nil {
		return nil, err
	}
	if !approvalCommandAuthorityMatches(command, access) {
		return nil, newApprovalCommandError("authority")
	}
	digest, err := approvalCommandDigest(command)
	if err != nil {
		return nil, newApprovalCommandError("integrity")
	}
	return &ValidatedApprovalCommandV2{command: command, access: access, digest: digest}, nil
}

// DecodeAndBindApprovalCommandV2 is an explicit alias for callers that name
// the input path as decoding rather than parsing.
func DecodeAndBindApprovalCommandV2(data []byte, access ApprovalAccess) (*ValidatedApprovalCommandV2, error) {
	return ParseAndBindApprovalCommandV2(data, access)
}

// Command returns a deep defensive copy of the validated wire value.
func (v *ValidatedApprovalCommandV2) Command() ApprovalCommandV2 {
	if v == nil {
		return ApprovalCommandV2{}
	}
	return cloneApprovalCommandValue(v.command)
}

// Value returns the same defensive command copy as Command.
func (v *ValidatedApprovalCommandV2) Value() ApprovalCommandV2 {
	return v.Command()
}

// Access returns the Core-bound access value used to issue this command.
// ApprovalAccess itself contains private seals and is returned by value.
func (v *ValidatedApprovalCommandV2) Access() ApprovalAccess {
	if v == nil {
		return ApprovalAccess{}
	}
	return v.access
}

// Validate rechecks all authority, contract, and content-seal invariants.
func (v *ValidatedApprovalCommandV2) Validate() error {
	if v == nil || !validApprovalCommandAccess(v.access) {
		return newApprovalCommandError("authority")
	}
	if !approvalCommandAuthorityMatches(v.command, v.access) {
		return newApprovalCommandError("authority")
	}
	if err := validateApprovalCommandValue(v.command); err != nil {
		return err
	}
	digest, err := approvalCommandDigest(v.command)
	if err != nil || digest != v.digest {
		return newApprovalCommandError("integrity")
	}
	return nil
}

// MarshalJSON emits a command only while its private validation remains true.
func (v *ValidatedApprovalCommandV2) MarshalJSON() ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(v.command)
}

func validApprovalCommandAccess(access ApprovalAccess) bool {
	return access.actor.valid() && access.workspace.validFor(access.actor) && access.actor.seal.gate == access.workspace.seal.gate
}

func approvalCommandAuthorityMatches(command ApprovalCommandV2, access ApprovalAccess) bool {
	return command.ActorID == access.actor.ActorID && command.SessionID == access.actor.SessionID && command.WorkspaceID == access.workspace.workspaceID
}

func validateApprovalCommandValue(command ApprovalCommandV2) error {
	if err := validateApprovalCommandWireStrings(command); err != nil {
		return err
	}
	data, err := json.Marshal(command)
	if err != nil {
		return newApprovalCommandError("encoding")
	}
	return validateApprovalCommandJSON(data)
}

func validateApprovalCommandWireStrings(command ApprovalCommandV2) error {
	for _, value := range []struct {
		value string
		max   int
	}{
		{value: command.SchemaID, max: approvalCommandIDMax},
		{value: command.CommandID, max: approvalCommandIDMax},
		{value: command.ActorID, max: approvalCommandIDMax},
		{value: command.SessionID, max: approvalCommandIDMax},
		{value: command.WorkspaceID, max: approvalCommandIDMax},
		{value: command.ProposalID, max: approvalCommandIDMax},
		{value: command.EvidencePackID, max: approvalCommandIDMax},
		{value: command.ComputedBasisID, max: approvalCommandIDMax},
		{value: command.GenerationID, max: approvalCommandIDMax},
		{value: command.Decision, max: approvalCommandIDMax},
		{value: command.IdempotencyKey, max: approvalCommandIDMax},
		{value: command.ExpectedState, max: approvalCommandIDMax},
	} {
		if !utf8.ValidString(value.value) || utf8.RuneCountInString(value.value) > value.max {
			return newApprovalCommandError("encoding")
		}
	}
	if command.EditedText != nil && (!utf8.ValidString(*command.EditedText) || utf8.RuneCountInString(*command.EditedText) > approvalCommandTextMax) {
		return newApprovalCommandError("encoding")
	}
	if command.PredecessorApprovalID != nil && (!utf8.ValidString(*command.PredecessorApprovalID) || utf8.RuneCountInString(*command.PredecessorApprovalID) > approvalCommandIDMax) {
		return newApprovalCommandError("encoding")
	}
	return nil
}

func validateApprovalCommandJSON(data []byte) error {
	if err := contractharness.ValidateVS09Contract(ApprovalCommandV2SchemaID, data); err != nil {
		kind := "schema"
		var semanticErr *contractharness.VS09ValidationError
		if errors.As(err, &semanticErr) && strings.TrimSpace(semanticErr.Category) != "" {
			kind = semanticErr.Category
		}
		return newApprovalCommandError(kind)
	}
	return nil
}

func approvalCommandDigest(command ApprovalCommandV2) ([sha256.Size]byte, error) {
	data, err := json.Marshal(command)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(append([]byte("codeflow/approval-command/v2\x00"), data...)), nil
}

// approvalCommandIdempotencyDigest identifies the stable semantic request
// for durable replay. SessionID is intentionally omitted because Core may
// issue a fresh session when the process restarts. The actor and workspace
// remain part of the digest, as do every other command field, so a rotated
// session is the only authority value that can preserve equivalence.
func approvalCommandIdempotencyDigest(command ApprovalCommandV2) ([sha256.Size]byte, error) {
	stable := cloneApprovalCommandValue(command)
	stable.SessionID = ""
	data, err := json.Marshal(stable)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(append([]byte("codeflow/approval-command-idempotency/v2\x00"), data...)), nil
}

func cloneApprovalCommandOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneApprovalCommandValue(value ApprovalCommandV2) ApprovalCommandV2 {
	value.EditedText = cloneApprovalCommandOptionalString(value.EditedText)
	value.PredecessorApprovalID = cloneApprovalCommandOptionalString(value.PredecessorApprovalID)
	return value
}

func decodeStrictApprovalCommandV2(data []byte) (ApprovalCommandV2, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return ApprovalCommandV2{}, newApprovalCommandError("syntax")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var command ApprovalCommandV2
	if err := decoder.Decode(&command); err != nil {
		return ApprovalCommandV2{}, newApprovalCommandError("syntax")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ApprovalCommandV2{}, newApprovalCommandError("syntax")
	}
	return command, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkApprovalJSONValue(decoder); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON document")
		}
		return errors.New("invalid trailing JSON")
	}
	return nil
}

func walkApprovalJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || strings.TrimSpace(key) == "" {
				return errors.New("invalid JSON object key")
			}
			if _, exists := seen[key]; exists {
				return errors.New("duplicate JSON object key")
			}
			seen[key] = struct{}{}
			if err := walkApprovalJSONValue(decoder); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := walkApprovalJSONValue(decoder); err != nil {
				return err
			}
		}
		closeToken, err := decoder.Token()
		if err != nil || closeToken != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	}
	return nil
}
