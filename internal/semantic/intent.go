package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// TaskIntent mirrors schemas/task-intent.schema.json.
type TaskIntent struct {
	SchemaID           string                `json:"schemaId"`
	SchemaVersion      int                   `json:"schemaVersion"`
	TaskID             string                `json:"taskId"`
	Revision           int                   `json:"revision"`
	Request            TaskRequest           `json:"request"`
	NormalizedIntent   NormalizedIntent      `json:"normalizedIntent"`
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptanceCriteria"`
	IntentStatus       string                `json:"intentStatus"` // parsed | needs_confirmation | user_confirmed
	ScopeHints         *ScopeHints           `json:"scopeHints,omitempty"`
	Mode               string                `json:"mode"`
	Authority          *IntentAuthority      `json:"authority,omitempty"`
}

type TaskRequest struct {
	RawRequest string `json:"rawRequest"`
}

type NormalizedIntent struct {
	Actor                     string   `json:"actor,omitempty"`
	Trigger                   string   `json:"trigger,omitempty"`
	ExpectedOutcome           string   `json:"expectedOutcome"`
	UnresolvedInterpretations []string `json:"unresolvedInterpretations"`
}

type AcceptanceCriterion struct {
	ID                    string   `json:"id"`
	Text                  string   `json:"text"`
	RequiredEvidenceKinds []string `json:"requiredEvidenceKinds,omitempty"`
}

type ScopeHints struct {
	EntrySymbols  []string `json:"entrySymbols,omitempty"`
	Domains       []string `json:"domains,omitempty"`
	ExcludedPaths []string `json:"excludedPaths,omitempty"`
}

type IntentAuthority struct {
	Source string `json:"source"`
}

type IntentOptions struct {
	Mode      string
	TaskID    string
	Revision  int
	Authority string
}

// NormalizeTaskIntent converts a user request into a normative TaskIntent.
// The raw request is preserved immutably. The intentStatus begins as 'parsed'
// or 'needs_confirmation' when multiple interpretations remain, never automatically
// promoted to 'user_confirmed' without user action.
func NormalizeTaskIntent(raw string, opts IntentOptions) (*TaskIntent, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("raw request cannot be empty")
	}

	mode := opts.Mode
	if mode == "" {
		mode = "feature"
	}

	taskID := opts.TaskID
	if taskID == "" {
		// The task identity is derived from the exact request bytes. Parsing may
		// trim whitespace, but two raw revisions must never collapse because of
		// normalization.
		h := sha256.Sum256([]byte(raw))
		taskID = "task-" + hex.EncodeToString(h[:8])
	}

	rev := opts.Revision
	if rev <= 0 {
		rev = 1
	}

	authSource := opts.Authority
	if authSource == "" {
		authSource = "user"
	}

	unresolved := make([]string, 0)
	lower := strings.ToLower(trimmed)

	// Check for ambiguity markers (e.g. A인지 B인지, 혹은, 또는, either/or)
	if strings.Contains(trimmed, "인지") || strings.Contains(trimmed, "혹은") || strings.Contains(trimmed, "또는") ||
		strings.Contains(lower, " either ") || strings.Contains(lower, " or ") {
		unresolved = append(unresolved, "multiple plausible flow candidates found in request phrasing")
	}

	// Simple heuristic extraction of intent parts
	actor := "user"
	trigger := "trigger event"

	if strings.HasPrefix(trimmed, "사용자 ") {
		actor = "user"
	} else if strings.HasPrefix(trimmed, "시스템 ") {
		actor = "system"
	}

	if strings.Contains(trimmed, "클릭") {
		trigger = "user click action"
	} else if strings.Contains(trimmed, "요청") || strings.Contains(trimmed, "제출") {
		trigger = "submission request"
	} else {
		trigger = "feature invocation"
	}

	// Outcome normalization: clean up trailing action words like "분석해줘", "보여줘"
	cleanOutcome := trimmed
	for _, suffix := range []string{"분석해줘", "보여줘", "알려줘", "설명해줘", "찾아줘"} {
		cleanOutcome = strings.TrimSuffix(cleanOutcome, suffix)
		cleanOutcome = strings.TrimSpace(cleanOutcome)
	}

	status := "parsed"
	if len(unresolved) > 0 {
		status = "needs_confirmation"
	}

	criteria := []AcceptanceCriterion{
		{
			ID:   "AC-01",
			Text: fmt.Sprintf("Execute %s from trigger to verified terminal state", cleanOutcome),
		},
	}

	intent := &TaskIntent{
		SchemaID:      TaskIntentSchemaID,
		SchemaVersion: SemanticSchemaVersion,
		TaskID:        taskID,
		Revision:      rev,
		Request: TaskRequest{
			RawRequest: raw,
		},
		NormalizedIntent: NormalizedIntent{
			Actor:                     actor,
			Trigger:                   trigger,
			ExpectedOutcome:           cleanOutcome,
			UnresolvedInterpretations: unresolved,
		},
		AcceptanceCriteria: criteria,
		IntentStatus:       status,
		Mode:               mode,
		Authority: &IntentAuthority{
			Source: authSource,
		},
	}

	return intent, nil
}

// ConfirmTaskIntent records an explicit user-selected candidate and scope as a
// new immutable revision. In particular, a needs_confirmation intent cannot be
// promoted by a caller that omits either selection.
func ConfirmTaskIntent(previous *TaskIntent, candidate, scope string) (*TaskIntent, error) {
	if previous == nil {
		return nil, errors.New("previous task intent cannot be nil")
	}
	if strings.TrimSpace(candidate) == "" || strings.TrimSpace(scope) == "" {
		return nil, errors.New("explicit candidate and scope are required for confirmation")
	}

	next := cloneTaskIntent(previous)
	next.Revision = previous.Revision + 1
	next.IntentStatus = "user_confirmed"
	next.NormalizedIntent.UnresolvedInterpretations = nil
	next.ScopeHints = &ScopeHints{
		EntrySymbols: []string{strings.TrimSpace(candidate)},
		Domains:      []string{strings.TrimSpace(scope)},
	}
	next.Authority = &IntentAuthority{Source: "user"}
	return next, nil
}

// ReviseTaskIntent parses a new raw request into a new revision while leaving
// the prior revision untouched. The raw bytes are preserved exactly in the new
// request as well.
func ReviseTaskIntent(previous *TaskIntent, raw string, opts IntentOptions) (*TaskIntent, error) {
	if previous == nil {
		return nil, errors.New("previous task intent cannot be nil")
	}
	if opts.TaskID == "" {
		opts.TaskID = previous.TaskID
	}
	if opts.Revision <= previous.Revision {
		opts.Revision = previous.Revision + 1
	}
	if opts.Authority == "" && previous.Authority != nil {
		opts.Authority = previous.Authority.Source
	}
	return NormalizeTaskIntent(raw, opts)
}

func cloneTaskIntent(previous *TaskIntent) *TaskIntent {
	next := *previous
	next.Request = previous.Request
	next.NormalizedIntent = previous.NormalizedIntent
	next.NormalizedIntent.UnresolvedInterpretations = append([]string(nil), previous.NormalizedIntent.UnresolvedInterpretations...)
	next.AcceptanceCriteria = append([]AcceptanceCriterion(nil), previous.AcceptanceCriteria...)
	if previous.ScopeHints != nil {
		scope := *previous.ScopeHints
		scope.EntrySymbols = append([]string(nil), previous.ScopeHints.EntrySymbols...)
		scope.Domains = append([]string(nil), previous.ScopeHints.Domains...)
		scope.ExcludedPaths = append([]string(nil), previous.ScopeHints.ExcludedPaths...)
		next.ScopeHints = &scope
	}
	if previous.Authority != nil {
		authority := *previous.Authority
		next.Authority = &authority
	}
	return &next
}
