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
	ID   string `json:"id"`
	Text string `json:"text"`
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
		h := sha256.Sum256([]byte(trimmed))
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
		SchemaID:      "https://codeflow.local/schemas/task-intent.schema.json",
		SchemaVersion: 1,
		TaskID:        taskID,
		Revision:      rev,
		Request: TaskRequest{
			RawRequest: trimmed,
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
