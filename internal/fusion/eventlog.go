// Package fusion event ledger implementation (tickets 14 & 18).
//
// Records append-only events for E2 session drafts and E3 step approvals,
// and derives the active semantic view.
package fusion

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"codeflow/internal/secret"
)

// EventType represents the kind of semantic update in the ledger.
type EventType string

const (
	EventSessionDraftSubmitted EventType = "session_draft_submitted"
	EventStepApproved          EventType = "step_approved"
	EventStepRejected          EventType = "step_rejected"
)

// Event is one append-only entry in .codeflow/semantics/events/events.jsonl.
type Event struct {
	EventID    string    `json:"eventId"`
	Type       EventType `json:"type"`
	Timestamp  time.Time `json:"timestamp"`
	FlowID     string    `json:"flowId"`
	StepID     string    `json:"stepId,omitempty"`
	SymbolPath string    `json:"symbolPath,omitempty"`
	Name       string    `json:"name,omitempty"`
	Rules      []string  `json:"rules,omitempty"`
	Author     string    `json:"author,omitempty"`
}

// EventLog manages the append-only event ledger.
type EventLog struct {
	repoRoot string
	logPath  string
}

// NewEventLog creates an EventLog manager.
func NewEventLog(repoRoot string) *EventLog {
	return &EventLog{
		repoRoot: repoRoot,
		logPath:  filepath.Join(repoRoot, ".codeflow", "semantics", "events", "events.jsonl"),
	}
}

// Append writes one new event to the ledger atomically.
func (el *EventLog) Append(evt Event) error {
	dir := filepath.Dir(el.logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create event dir: %w", err)
	}

	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	if evt.EventID == "" {
		evt.EventID = fmt.Sprintf("evt-%d", time.Now().UTC().UnixNano())
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	cleanData, _, err := secret.RedactJSON(data)
	if err != nil {
		return fmt.Errorf("redact event: %w", err)
	}

	f, err := os.OpenFile(el.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(cleanData, '\n')); err != nil {
		return fmt.Errorf("write event: %w", err)
	}

	return nil
}

// ReadAll reads all events from the ledger in chronological order.
func (el *EventLog) ReadAll() ([]Event, error) {
	f, err := os.Open(el.logPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open event log: %w", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := json.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("unmarshal event line: %w", err)
		}
		events = append(events, evt)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan event log: %w", err)
	}
	return events, nil
}

// MaterializeView replays all events into active Approved and Session maps.
func (el *EventLog) MaterializeView() (map[string]ApprovedStep, map[string]SessionDraftStep, error) {
	events, err := el.ReadAll()
	if err != nil {
		return nil, nil, err
	}

	approved := make(map[string]ApprovedStep)
	session := make(map[string]SessionDraftStep)

	for _, evt := range events {
		key := evt.SymbolPath
		if key == "" {
			key = evt.StepID
		}
		if key == "" {
			continue
		}

		switch evt.Type {
		case EventStepApproved:
			approved[key] = ApprovedStep{
				Name:       evt.Name,
				Rules:      evt.Rules,
				ApprovedAt: evt.Timestamp,
			}
		case EventSessionDraftSubmitted:
			session[key] = SessionDraftStep{
				Name:  evt.Name,
				Rules: evt.Rules,
			}
		case EventStepRejected:
			delete(approved, key)
			delete(session, key)
		}
	}

	return approved, session, nil
}
