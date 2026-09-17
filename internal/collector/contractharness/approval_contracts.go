package contractharness

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The approval contracts are deliberately named here as stable IDs rather
// than being reconstructed by callers.  A caller may use Validate for the
// structural contract, or ValidateVS09Contract for the structural contract
// plus the cross-field authority and lifecycle rules below.
const (
	ApprovalCommandV2SchemaID           = BaseURL + "rflsc.approval-command.v2.schema.json"
	ApprovalEventV2SchemaID             = BaseURL + "rflsc.approval-event.v2.schema.json"
	ApprovalAggregateV2SchemaID         = BaseURL + "rflsc.approval-aggregate.v2.schema.json"
	ApprovalIdempotencyResultV1SchemaID = BaseURL + "rflsc.approval-idempotency-result.v1.schema.json"
	ApprovalOutboxV1SchemaID            = BaseURL + "rflsc.approval-outbox.v1.schema.json"
	ApprovalHistoryV1SchemaID           = BaseURL + "rflsc.approval-history.v1.schema.json"
	ApprovalHistoryV1SchemaVersion      = 1
)

// VS09ValidationError identifies the semantic leaf that caused an approval
// contract to be rejected.  Category and Field are stable values consumed by
// the executable contract registry, so a fixture cannot pass by merely
// producing an unrelated schema or semantic error.
type VS09ValidationError struct {
	ContractID string
	Category   string
	Field      string
	Reason     string
}

func (e *VS09ValidationError) Error() string {
	return fmt.Sprintf("VS-09 %s validation failed at %s (%s): %s", e.ContractID, e.Field, e.Category, e.Reason)
}

// ValidatorForVS09Contract returns the complete validator for one of the
// VS-09 approval contracts. Unknown IDs intentionally return nil so a
// registry entry cannot silently fall back to schema-only validation.
func ValidatorForVS09Contract(schemaID string) func([]byte) error {
	if _, _, ok := normalizeVS09SchemaID(schemaID); !ok {
		return nil
	}
	return func(data []byte) error { return ValidateVS09Contract(schemaID, data) }
}

// ValidateVS09Contract validates a required VS-09 approval contract's JSON
// schema and then its authority, reference, version and lifecycle invariants.
func ValidateVS09Contract(schemaID string, data []byte) error {
	normalizedSchemaID, contractID, ok := normalizeVS09SchemaID(schemaID)
	if !ok {
		return fmt.Errorf("unsupported VS-09 schema ID %q", schemaID)
	}
	if err := Validate(normalizedSchemaID, data); err != nil {
		return fmt.Errorf("%s schema violation: %w", schemaID, err)
	}

	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("%s JSON decode: %w", schemaID, err)
	}

	switch contractID {
	case "rflsc.approval-command.v2":
		return validateVS09ApprovalCommand(document)
	case "rflsc.approval-event.v2":
		return validateVS09ApprovalEvent(document)
	case "rflsc.approval-aggregate.v2":
		return validateVS09ApprovalAggregate(document)
	case "rflsc.approval-idempotency-result.v1":
		return validateVS09ApprovalIdempotencyResult(document)
	case "rflsc.approval-outbox.v1":
		return validateVS09ApprovalOutbox(document)
	case "rflsc.approval-history.v1":
		return validateVS09ApprovalHistory(document)
	default:
		return fmt.Errorf("unsupported VS-09 schema ID %q", schemaID)
	}
}

func normalizeVS09SchemaID(id string) (schemaID, contractID string, ok bool) {
	switch id {
	case ApprovalCommandV2SchemaID:
		return id, "rflsc.approval-command.v2", true
	case ApprovalEventV2SchemaID:
		return id, "rflsc.approval-event.v2", true
	case ApprovalAggregateV2SchemaID:
		return id, "rflsc.approval-aggregate.v2", true
	case ApprovalIdempotencyResultV1SchemaID:
		return id, "rflsc.approval-idempotency-result.v1", true
	case ApprovalOutboxV1SchemaID:
		return id, "rflsc.approval-outbox.v1", true
	case ApprovalHistoryV1SchemaID:
		return id, "rflsc.approval-history.v1", true
	case "rflsc.approval-command.v2", "rflsc.approval-event.v2", "rflsc.approval-aggregate.v2", "rflsc.approval-idempotency-result.v1", "rflsc.approval-outbox.v1", "rflsc.approval-history.v1":
		return BaseURL + id + ".schema.json", id, true
	default:
		return "", "", false
	}
}

func validateVS09ApprovalCommand(document map[string]any) error {
	const contractID = "rflsc.approval-command.v2"
	if err := requireVS09Authority(document, contractID, "/actorId", "/sessionId", "/workspaceId"); err != nil {
		return err
	}
	if err := requireVS09References(document, contractID, "/proposalId", "/evidencePackId", "/computedBasisId", "/generationId"); err != nil {
		return err
	}
	if err := requireVS09NonEmpty(document, contractID, "reference", "/commandId", "/idempotencyKey"); err != nil {
		return err
	}

	decision, err := requireVS09String(document, contractID, "transition", "/decision")
	if err != nil {
		return err
	}
	expectedState, err := requireVS09String(document, contractID, "transition", "/expectedState")
	if err != nil {
		return err
	}
	expectedVersion, err := requireVS09Integer(document, contractID, "version", "/expectedApprovalVersion")
	if err != nil {
		return err
	}
	predecessor, predecessorPresent := document["predecessorApprovalId"]
	predecessorID, predecessorIsString := predecessor.(string)
	if predecessorPresent && (!predecessorIsString || strings.TrimSpace(predecessorID) == "") {
		return newVS09Error(contractID, "transition", "/predecessorApprovalId", "predecessorApprovalId must be non-empty when supplied")
	}
	edited, editedPresent := document["editedText"]
	editedText, editedIsString := edited.(string)
	if editedPresent && !editedIsString {
		return newVS09Error(contractID, "transition", "/editedText", "editedText must be a string when supplied")
	}
	if editedPresent && strings.TrimSpace(editedText) == "" {
		return newVS09Error(contractID, "transition", "/editedText", "editedText must be non-empty when supplied")
	}

	// A command against the empty aggregate starts at version zero.  This
	// cross-field rule gives expectedApprovalVersion a semantic meaning in
	// addition to the schema's numeric bound.
	if expectedState == "none" && expectedVersion != 0 {
		return newVS09Error(contractID, "version", "/expectedApprovalVersion", "expectedState none requires expectedApprovalVersion zero")
	}

	switch decision {
	case "approve":
		if expectedState != "none" {
			return newVS09Error(contractID, "transition", "/expectedState", "approve requires expectedState none")
		}
		if predecessorPresent {
			return newVS09Error(contractID, "transition", "/predecessorApprovalId", "approve cannot name a predecessor")
		}
		if editedPresent {
			return newVS09Error(contractID, "transition", "/editedText", "approve cannot carry editedText")
		}
	case "edit_then_approve":
		if expectedState != "active" {
			return newVS09Error(contractID, "transition", "/expectedState", "edit_then_approve requires expectedState active")
		}
		if !predecessorPresent {
			return newVS09Error(contractID, "transition", "/predecessorApprovalId", "edit_then_approve requires a predecessor")
		}
		if !editedPresent || strings.TrimSpace(editedText) == "" {
			return newVS09Error(contractID, "transition", "/editedText", "edit_then_approve requires editedText")
		}
	case "reject":
		if expectedState != "none" && expectedState != "active" {
			return newVS09Error(contractID, "transition", "/expectedState", "reject requires expectedState none or active")
		}
		if editedPresent {
			return newVS09Error(contractID, "transition", "/editedText", "reject cannot carry editedText")
		}
	case "revoke", "supersede":
		if expectedState != "active" {
			return newVS09Error(contractID, "transition", "/expectedState", decision+" requires expectedState active")
		}
		if !predecessorPresent {
			return newVS09Error(contractID, "transition", "/predecessorApprovalId", decision+" requires a predecessor")
		}
		if editedPresent {
			return newVS09Error(contractID, "transition", "/editedText", decision+" cannot carry editedText")
		}
	}
	return nil
}

func validateVS09ApprovalEvent(document map[string]any) error {
	const contractID = "rflsc.approval-event.v2"
	if err := requireVS09Authority(document, contractID, "/actorId", "/sessionId", "/workspaceId"); err != nil {
		return err
	}
	if err := requireVS09References(document, contractID, "/proposalId", "/evidencePackId", "/computedBasisId", "/generationId"); err != nil {
		return err
	}
	if err := requireVS09NonEmpty(document, contractID, "reference", "/eventId", "/approvalId", "/aggregateId", "/timestamp"); err != nil {
		return err
	}
	if _, err := requireVS09Integer(document, contractID, "version", "/aggregateVersion"); err != nil {
		return err
	}

	decision, err := requireVS09String(document, contractID, "transition", "/decision")
	if err != nil {
		return err
	}
	relation, err := requireVS09String(document, contractID, "transition", "/lifecycleRelation")
	if err != nil {
		return err
	}
	version, _ := document["aggregateVersion"].(float64)
	predecessor, predecessorPresent := document["predecessorApprovalId"]
	predecessorID, predecessorIsString := predecessor.(string)
	if predecessorPresent && (!predecessorIsString || strings.TrimSpace(predecessorID) == "") {
		return newVS09Error(contractID, "transition", "/predecessorApprovalId", "predecessorApprovalId must be non-empty when supplied")
	}

	// The first event is the only event represented by lifecycleRelation
	// initial, and therefore must advance an empty aggregate to version one.
	if relation == "initial" && version != 1 {
		return newVS09Error(contractID, "version", "/aggregateVersion", "initial event must have aggregateVersion one")
	}

	wantRelation := map[string]string{
		"approve":           "initial",
		"edit_then_approve": "edit",
		"reject":            "reject",
		"revoke":            "revoke",
		"supersede":         "supersede",
	}[decision]
	if relation != wantRelation {
		return newVS09Error(contractID, "transition", "/lifecycleRelation", "lifecycleRelation does not match decision")
	}
	approvedText, _ := document["approvedText"].(string)
	if (decision == "approve" || decision == "edit_then_approve") && strings.TrimSpace(approvedText) == "" {
		return newVS09Error(contractID, "transition", "/approvedText", "approval event requires approvedText")
	}
	if decision == "approve" && predecessorPresent {
		return newVS09Error(contractID, "transition", "/predecessorApprovalId", "initial approval cannot name a predecessor")
	}
	if (decision == "edit_then_approve" || decision == "revoke" || decision == "supersede") && !predecessorPresent {
		return newVS09Error(contractID, "transition", "/predecessorApprovalId", "non-initial event requires a predecessor")
	}
	return nil
}

func validateVS09ApprovalAggregate(document map[string]any) error {
	const contractID = "rflsc.approval-aggregate.v2"
	if err := requireVS09Authority(document, contractID, "/workspaceId"); err != nil {
		return err
	}
	if err := requireVS09References(document, contractID, "/proposalId", "/evidencePackId", "/computedBasisId", "/generationId"); err != nil {
		return err
	}
	if err := requireVS09NonEmpty(document, contractID, "reference", "/aggregateId", "/lastEventId"); err != nil {
		return err
	}

	history, ok := document["history"].([]any)
	if !ok || len(history) == 0 {
		return newVS09Error(contractID, "version", "/history", "history must contain an ordered genesis entry")
	}
	previousState := ""
	previousVersion := int64(-1)
	seenEventIDs := make(map[string]struct{}, len(history))
	seenApprovalIDs := make(map[string]struct{}, len(history))
	for index, rawEntry := range history {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			return newVS09Error(contractID, "version", fmt.Sprintf("/history/%d", index), "history entry must be an object")
		}
		fieldPrefix := fmt.Sprintf("/history/%d", index)
		version, err := requireVS09Integer(entry, contractID, "version", "/version")
		if err != nil {
			return rebaseVS09Error(err, fieldPrefix+"/version")
		}
		state, err := requireVS09String(entry, contractID, "transition", "/state")
		if err != nil {
			return rebaseVS09Error(err, fieldPrefix+"/state")
		}
		decision, err := requireVS09String(entry, contractID, "transition", "/decision")
		if err != nil {
			return rebaseVS09Error(err, fieldPrefix+"/decision")
		}
		if err := requireVS09NonEmpty(entry, contractID, "reference", "/eventId"); err != nil {
			return rebaseVS09Error(err, fieldPrefix+"/eventId")
		}
		eventID := entry["eventId"].(string)
		if _, seen := seenEventIDs[eventID]; seen {
			return newVS09Error(contractID, "reference", fieldPrefix+"/eventId", "history eventId must be unique")
		}
		seenEventIDs[eventID] = struct{}{}
		if index == 0 {
			if version != 0 || state != "none" || decision != "none" {
				return newVS09Error(contractID, "transition", fieldPrefix+"/state", "genesis must be version zero, none state and none decision")
			}
			if _, present := entry["approvalId"]; present {
				return newVS09Error(contractID, "reference", fieldPrefix+"/approvalId", "genesis cannot carry an approvalId")
			}
		} else {
			if err := requireVS09NonEmpty(entry, contractID, "reference", "/approvalId"); err != nil {
				return rebaseVS09Error(err, fieldPrefix+"/approvalId")
			}
			approvalID := entry["approvalId"].(string)
			if _, seen := seenApprovalIDs[approvalID]; seen {
				return newVS09Error(contractID, "reference", fieldPrefix+"/approvalId", "history approvalId must be unique")
			}
			seenApprovalIDs[approvalID] = struct{}{}
			if version != previousVersion+1 {
				return newVS09Error(contractID, "version", fieldPrefix+"/version", "aggregate history versions must increase by one")
			}
			if err := validateVS09AggregateTransition(contractID, previousState, state, decision, fieldPrefix+"/state"); err != nil {
				return err
			}
		}
		previousVersion, previousState = version, state
	}

	lastVersion, err := requireVS09Integer(document, contractID, "version", "/version")
	if err != nil {
		return err
	}
	lastState, err := requireVS09String(document, contractID, "transition", "/state")
	if err != nil {
		return err
	}
	lastDecision, err := requireVS09String(document, contractID, "transition", "/lastDecision")
	if err != nil {
		return err
	}
	if lastVersion != previousVersion {
		return newVS09Error(contractID, "version", "/version", "aggregate version must equal the last history version")
	}
	if lastState != previousState {
		return newVS09Error(contractID, "transition", "/state", "aggregate state must equal the last history state")
	}
	lastEntry := history[len(history)-1].(map[string]any)
	if eventID, _ := document["lastEventId"].(string); eventID != lastEntry["eventId"] {
		return newVS09Error(contractID, "reference", "/lastEventId", "lastEventId must equal the final history eventId")
	}
	if lastDecision != lastEntry["decision"] {
		return newVS09Error(contractID, "transition", "/lastDecision", "lastDecision must equal the final history decision")
	}
	activeApproval, activeApprovalPresent := document["activeApprovalId"]
	if lastState == "active" {
		if !activeApprovalPresent {
			return newVS09Error(contractID, "reference", "/activeApprovalId", "active aggregate requires activeApprovalId")
		}
		lastApproval, ok := lastEntry["approvalId"].(string)
		if !ok || activeApproval != lastApproval {
			return newVS09Error(contractID, "reference", "/activeApprovalId", "activeApprovalId must equal the final history approvalId")
		}
	} else if activeApprovalPresent && strings.TrimSpace(fmt.Sprint(activeApproval)) != "" {
		return newVS09Error(contractID, "transition", "/activeApprovalId", "inactive aggregate cannot expose activeApprovalId")
	}
	return nil
}

// validateVS09ApprovalHistory validates the canonical read-side projection
// after its nested v2 event and aggregate schemas have passed.  The nested
// contracts describe the individual records, while this boundary proves that
// the records describe one target and one replayable append-only history.
func validateVS09ApprovalHistory(document map[string]any) error {
	const contractID = "rflsc.approval-history.v1"
	target, ok := document["target"].(map[string]any)
	if !ok {
		return newVS09Error(contractID, "reference", "/target", "target is required")
	}
	aggregate, ok := document["aggregate"].(map[string]any)
	if !ok {
		return newVS09Error(contractID, "reference", "/aggregate", "aggregate is required")
	}
	events, ok := document["events"].([]any)
	if !ok || len(events) == 0 {
		return newVS09Error(contractID, "version", "/events", "events must contain an ordered append-only history")
	}

	if err := requireVS09Authority(target, contractID, "/workspaceId"); err != nil {
		return err
	}
	if err := requireVS09References(target, contractID, "/proposalId", "/evidencePackId", "/computedBasisId", "/generationId", "/validatedSnapshotId", "/mapId", "/taskId"); err != nil {
		return err
	}

	if err := validateVS09NestedHistoryContract(contractID, "/aggregate", aggregate, validateVS09ApprovalAggregate); err != nil {
		return err
	}
	for index, rawEvent := range events {
		event, ok := rawEvent.(map[string]any)
		if !ok {
			return newVS09Error(contractID, "reference", fmt.Sprintf("/events/%d", index), "event must be an object")
		}
		if err := validateVS09NestedHistoryContract(contractID, fmt.Sprintf("/events/%d", index), event, validateVS09ApprovalEvent); err != nil {
			return err
		}
	}

	for _, field := range []string{"workspaceId", "proposalId", "evidencePackId", "computedBasisId", "generationId", "intentRevision"} {
		if target[field] != aggregate[field] {
			category := "reference"
			if field == "workspaceId" {
				category = "authority"
			}
			return newVS09Error(contractID, category, "/target/"+field, "target identity does not match aggregate identity")
		}
	}

	aggregateID, ok := aggregate["aggregateId"].(string)
	if !ok || strings.TrimSpace(aggregateID) == "" {
		return newVS09Error(contractID, "reference", "/aggregate/aggregateId", "aggregateId must be non-empty")
	}
	history, ok := aggregate["history"].([]any)
	if !ok || len(history) != len(events)+1 {
		return newVS09Error(contractID, "version", "/events", "events must correspond to aggregate history after genesis")
	}

	for index, rawEvent := range events {
		event := rawEvent.(map[string]any)
		prefix := fmt.Sprintf("/events/%d", index)
		for _, field := range []string{"workspaceId", "proposalId", "evidencePackId", "computedBasisId", "generationId", "intentRevision"} {
			if event[field] != target[field] {
				category := "reference"
				if field == "workspaceId" {
					category = "authority"
				}
				return newVS09Error(contractID, category, prefix+"/"+field, "event identity does not match target identity")
			}
		}
		if event["aggregateId"] != aggregateID {
			return newVS09Error(contractID, "reference", prefix+"/aggregateId", "event aggregateId does not match aggregate")
		}
		wantVersion := int64(index + 1)
		version, err := requireVS09Integer(event, contractID, "version", "/aggregateVersion")
		if err != nil {
			return err
		}
		if version != wantVersion {
			return newVS09Error(contractID, "version", prefix+"/aggregateVersion", "events must form a contiguous total order")
		}

		previousEntry, ok := history[index].(map[string]any)
		if !ok {
			return newVS09Error(contractID, "reference", fmt.Sprintf("/aggregate/history/%d", index), "aggregate history predecessor entry must be an object")
		}
		previousState, _ := previousEntry["state"].(string)
		previousApprovalID, _ := previousEntry["approvalId"].(string)
		predecessor, predecessorPresent := event["predecessorApprovalId"]
		predecessorID, _ := predecessor.(string)
		decision, _ := event["decision"].(string)
		switch decision {
		case "approve":
			if predecessorPresent {
				return newVS09Error(contractID, "transition", prefix+"/predecessorApprovalId", "initial approval cannot name a predecessor")
			}
		case "edit_then_approve", "revoke", "supersede":
			if !predecessorPresent || predecessorID != previousApprovalID {
				return newVS09Error(contractID, "transition", prefix+"/predecessorApprovalId", "lifecycle event predecessor does not match the active history entry")
			}
		case "reject":
			if previousState == "none" && predecessorPresent {
				return newVS09Error(contractID, "transition", prefix+"/predecessorApprovalId", "genesis rejection cannot name a predecessor")
			}
			if previousState == "active" && predecessorPresent && predecessorID != previousApprovalID {
				return newVS09Error(contractID, "transition", prefix+"/predecessorApprovalId", "rejection predecessor does not match the active history entry")
			}
		}

		entry, ok := history[index+1].(map[string]any)
		if !ok {
			return newVS09Error(contractID, "reference", fmt.Sprintf("/aggregate/history/%d", index+1), "aggregate history entry must be an object")
		}
		entryPrefix := fmt.Sprintf("/aggregate/history/%d", index+1)
		entryVersion, err := requireVS09Integer(entry, contractID, "version", "/version")
		if err != nil {
			return rebaseVS09Error(err, entryPrefix+"/version")
		}
		if entryVersion != wantVersion {
			return newVS09Error(contractID, "version", entryPrefix+"/version", "aggregate history version does not match event order")
		}
		if event["eventId"] != entry["eventId"] {
			return newVS09Error(contractID, "reference", entryPrefix+"/eventId", "aggregate history eventId does not match event")
		}
		if event["approvalId"] != entry["approvalId"] {
			return newVS09Error(contractID, "reference", entryPrefix+"/approvalId", "aggregate history approvalId does not match event")
		}
		if event["decision"] != entry["decision"] {
			return newVS09Error(contractID, "transition", entryPrefix+"/decision", "aggregate history decision does not match event")
		}
	}
	return nil
}

func validateVS09NestedHistoryContract(contractID, prefix string, document map[string]any, validator func(map[string]any) error) error {
	if err := validator(document); err != nil {
		var semanticErr *VS09ValidationError
		if errors.As(err, &semanticErr) {
			return newVS09Error(contractID, semanticErr.Category, prefix+semanticErr.Field, semanticErr.Reason)
		}
		return newVS09Error(contractID, "reference", prefix, "nested approval contract is invalid")
	}
	return nil
}

func validateVS09AggregateTransition(contractID, previousState, state, decision, field string) error {
	if previousState == "rejected" || previousState == "revoked" || previousState == "superseded" {
		return newVS09Error(contractID, "transition", field, "terminal aggregate state cannot have a successor")
	}
	wantState := ""
	switch decision {
	case "approve", "edit_then_approve":
		wantState = "active"
	case "reject":
		wantState = "rejected"
	case "revoke":
		wantState = "revoked"
	case "supersede":
		wantState = "superseded"
	default:
		return newVS09Error(contractID, "transition", field, "unknown aggregate transition")
	}
	if state != wantState {
		return newVS09Error(contractID, "transition", field, "history decision does not produce the recorded state")
	}
	if decision == "approve" && previousState != "none" {
		return newVS09Error(contractID, "transition", field, "approve may only start an empty aggregate")
	}
	if decision == "edit_then_approve" && previousState != "active" {
		return newVS09Error(contractID, "transition", field, "edit_then_approve requires active predecessor")
	}
	if (decision == "revoke" || decision == "supersede") && previousState != "active" {
		return newVS09Error(contractID, "transition", field, decision+" requires active predecessor")
	}
	return nil
}

func validateVS09ApprovalIdempotencyResult(document map[string]any) error {
	const contractID = "rflsc.approval-idempotency-result.v1"
	if err := requireVS09Authority(document, contractID, "/actorId", "/sessionId", "/workspaceId"); err != nil {
		return err
	}
	if err := requireVS09References(document, contractID, "/proposalId", "/evidencePackId", "/computedBasisId", "/generationId"); err != nil {
		return err
	}
	if err := requireVS09NonEmpty(document, contractID, "reference", "/idempotencyKey", "/commandId", "/committedAt"); err != nil {
		return err
	}

	originalCommand, ok := document["originalCommand"].(map[string]any)
	if !ok {
		return newVS09Error(contractID, "reference", "/originalCommand", "originalCommand is required")
	}
	originalResult, ok := document["originalResult"].(map[string]any)
	if !ok {
		return newVS09Error(contractID, "reference", "/originalResult", "originalResult is required")
	}
	for _, field := range []string{"idempotencyKey", "commandId", "actorId", "workspaceId", "proposalId"} {
		if document[field] != originalCommand[field] {
			return newVS09Error(contractID, "reference", "/originalCommand/"+field, "originalCommand does not match the top-level command identity")
		}
	}
	if err := requireVS09NonEmpty(originalCommand, contractID, "authority", "/actorId", "/workspaceId"); err != nil {
		return err
	}
	if err := requireVS09NonEmpty(originalCommand, contractID, "reference", "/proposalId"); err != nil {
		return err
	}
	if outcome, _ := document["outcome"].(string); outcome == "committed" {
		state, _ := document["state"].(string)
		if state == "none" {
			return newVS09Error(contractID, "transition", "/state", "committed result cannot have none state")
		}
		decision, _ := originalCommand["decision"].(string)
		wantState := map[string]string{
			"approve":           "active",
			"edit_then_approve": "active",
			"reject":            "rejected",
			"revoke":            "revoked",
			"supersede":         "superseded",
		}[decision]
		if wantState != "" && state != wantState {
			return newVS09Error(contractID, "transition", "/state", "committed result state does not match the original command decision")
		}
		expectedApprovalVersion, expectedOK := originalCommand["expectedApprovalVersion"].(float64)
		aggregateVersion, aggregateOK := document["aggregateVersion"].(float64)
		if expectedOK && aggregateOK && aggregateVersion != expectedApprovalVersion+1 {
			return newVS09Error(contractID, "version", "/aggregateVersion", "committed result must advance expected approval version by one")
		}
	}
	for _, field := range []string{"outcome", "approvalId", "eventId", "aggregateId", "aggregateVersion", "state"} {
		if document[field] != originalResult[field] {
			return newVS09Error(contractID, "reference", "/originalResult/"+field, "originalResult does not match the durable result")
		}
	}
	if _, err := requireVS09Integer(document, contractID, "version", "/aggregateVersion"); err != nil {
		return err
	}
	return nil
}

func validateVS09ApprovalOutbox(document map[string]any) error {
	const contractID = "rflsc.approval-outbox.v1"
	if err := requireVS09Authority(document, contractID, "/workspaceId"); err != nil {
		return err
	}
	if err := requireVS09NonEmpty(document, contractID, "reference", "/outboxId", "/eventId", "/aggregateId", "/committedAt"); err != nil {
		return err
	}
	committedEvent, ok := document["committedEvent"].(map[string]any)
	if !ok {
		return newVS09Error(contractID, "reference", "/committedEvent", "committedEvent is required")
	}
	if err := requireVS09Authority(committedEvent, contractID, "/workspaceId"); err != nil {
		return rebaseVS09Error(err, "/committedEvent/workspaceId")
	}
	if err := requireVS09NonEmpty(committedEvent, contractID, "reference", "/eventId", "/approvalId", "/aggregateId"); err != nil {
		return prefixVS09Error(err, "/committedEvent")
	}
	for _, field := range []string{"eventId", "aggregateId", "workspaceId", "payloadDigest"} {
		if document[field] != committedEvent[field] {
			return newVS09Error(contractID, "reference", "/committedEvent/"+field, "committed event does not match the outbox envelope")
		}
	}
	outerVersion, err := requireVS09Integer(document, contractID, "version", "/aggregateVersion")
	if err != nil {
		return err
	}
	innerVersion, err := requireVS09Integer(committedEvent, contractID, "version", "/aggregateVersion")
	if err != nil {
		return rebaseVS09Error(err, "/committedEvent/aggregateVersion")
	}
	if outerVersion != innerVersion {
		return newVS09Error(contractID, "version", "/committedEvent/aggregateVersion", "committed event version must equal outbox version")
	}
	deliveryState, err := requireVS09String(document, contractID, "transition", "/deliveryState")
	if err != nil {
		return err
	}
	switch deliveryState {
	case "pending":
		if value, present := document["publishedAt"]; present && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return newVS09Error(contractID, "transition", "/deliveryState", "pending outbox cannot have publishedAt")
		}
	case "published":
		if value, _ := document["publishedAt"].(string); strings.TrimSpace(value) == "" {
			return newVS09Error(contractID, "transition", "/deliveryState", "published outbox requires publishedAt")
		}
	case "failed":
		if value, _ := document["failureReason"].(string); strings.TrimSpace(value) == "" {
			return newVS09Error(contractID, "transition", "/deliveryState", "failed outbox requires failureReason")
		}
	}
	return nil
}

func requireVS09Authority(document map[string]any, contractID string, fields ...string) error {
	for _, field := range fields {
		if err := requireVS09NonEmpty(document, contractID, "authority", field); err != nil {
			return err
		}
	}
	return nil
}

func requireVS09References(document map[string]any, contractID string, fields ...string) error {
	for _, field := range fields {
		if err := requireVS09NonEmpty(document, contractID, "reference", field); err != nil {
			return err
		}
	}
	return nil
}

func requireVS09NonEmpty(document map[string]any, contractID, category string, fields ...string) error {
	for _, field := range fields {
		value, ok := valueAtVS09Path(document, field)
		if !ok {
			return newVS09Error(contractID, category, field, "field is required")
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return newVS09Error(contractID, category, field, "value must be a non-empty string")
		}
	}
	return nil
}

func requireVS09String(document map[string]any, contractID, category, field string) (string, error) {
	value, ok := valueAtVS09Path(document, field)
	if !ok {
		return "", newVS09Error(contractID, category, field, "field is required")
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", newVS09Error(contractID, category, field, "value must be a non-empty string")
	}
	return text, nil
}

func requireVS09Integer(document map[string]any, contractID, category, field string) (int64, error) {
	value, ok := valueAtVS09Path(document, field)
	if !ok {
		return 0, newVS09Error(contractID, category, field, "field is required")
	}
	number, ok := value.(float64)
	if !ok || number != float64(int64(number)) {
		return 0, newVS09Error(contractID, category, field, "value must be an integer")
	}
	return int64(number), nil
}

func valueAtVS09Path(document map[string]any, field string) (any, bool) {
	parts := strings.Split(strings.TrimPrefix(field, "/"), "/")
	var current any = document
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func newVS09Error(contractID, category, field, reason string) error {
	return &VS09ValidationError{ContractID: contractID, Category: category, Field: field, Reason: reason}
}

func rebaseVS09Error(err error, field string) error {
	var semanticErr *VS09ValidationError
	if !errors.As(err, &semanticErr) {
		return err
	}
	copyOfError := *semanticErr
	copyOfError.Field = field
	return &copyOfError
}

func prefixVS09Error(err error, prefix string) error {
	var semanticErr *VS09ValidationError
	if !errors.As(err, &semanticErr) {
		return err
	}
	copyOfError := *semanticErr
	copyOfError.Field = prefix + copyOfError.Field
	return &copyOfError
}

// validateVS09FixtureData is used by FixtureTree so the 30 approval fixtures
// exercise the same semantic boundary as callers, not just their JSON schema.
func validateVS09FixtureData(schemaID string, data []byte) error {
	if ValidatorForVS09Contract(schemaID) != nil {
		return ValidateVS09Contract(schemaID, data)
	}
	return Validate(schemaID, data)
}
