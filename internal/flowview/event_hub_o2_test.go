package flowview

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"codeflow/internal/semantic"
)

func validApprovalOutboxEvent() (semantic.ApprovalOutboxCommittedEventV1, string, string, string) {
	return semantic.ApprovalOutboxCommittedEventV1{
			EventID:          "approval-event-1",
			ApprovalID:       "approval-1",
			AggregateID:      "aggregate-1",
			AggregateVersion: 1,
			WorkspaceID:      "workspace-1",
			Decision:         "approve",
			PayloadDigest:    "sha256:" + strings.Repeat("a", 64),
		},
		strings.Repeat("b", 64),
		"snapshot-1",
		"generation-1"
}

func receiveEvent(t *testing.T, ch <-chan *semantic.EventEnvelope) *semantic.EventEnvelope {
	t.Helper()
	select {
	case env := <-ch:
		return env
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event publication")
		return nil
	}
}

func validApprovalOutboxCommittedAt() string {
	return "2026-09-07T00:00:00Z"
}

func TestEventHubGenericApprovalUpdatedRequiresTypedPublisher(t *testing.T) {
	hub := NewEventHub("o2-bypass", 4)

	env, err := hub.PublishChecked("approval.updated", map[string]any{
		"approvalEvent": map[string]any{
			"eventId": "approval-event-1",
		},
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("generic approval.updated publication unexpectedly succeeded")
	}
	if env != nil {
		t.Fatalf("generic approval.updated publication returned envelope: %+v", env)
	}
	if hub.headSeq != 0 || len(hub.ringBuffer) != 0 {
		t.Fatalf("generic approval.updated publication changed hub state: head=%d ring=%d", hub.headSeq, len(hub.ringBuffer))
	}
}

func TestEventHubPublishApprovalUpdatedClampsClockRollbackToCommittedAt(t *testing.T) {
	hub := NewEventHub("o2-committed-at-clamp", 4)
	hub.now = func() time.Time {
		return time.Date(2026, 9, 6, 23, 59, 59, 0, time.UTC)
	}
	ch, _, _, cancel := hub.Subscribe("")
	defer cancel()
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	committedAt := "2026-09-07T00:00:00Z"

	envelope, err := hub.PublishApprovalUpdated(committed, committedAt, basis, snapshot, generation)
	if err != nil {
		t.Fatalf("publish approval event: %v", err)
	}
	if envelope == nil || envelope.OccurredAt.Format(time.RFC3339Nano) != committedAt {
		t.Fatalf("occurredAt = %v, want committedAt %q", envelope, committedAt)
	}
	if got := receiveEvent(t, ch); got == nil || !got.OccurredAt.Equal(envelope.OccurredAt) {
		t.Fatalf("broadcast envelope = %+v, want clamped time %v", got, envelope.OccurredAt)
	}
}

func TestEventHubPublishApprovalUpdatedRejectsNoncanonicalCommittedAtWithoutMutation(t *testing.T) {
	committedAtValues := []struct {
		name  string
		value string
	}{
		{name: "offset equivalent", value: "2026-09-07T01:00:00+01:00"},
		{name: "redundant fraction", value: "2026-09-07T00:00:00.000Z"},
		{name: "empty", value: ""},
	}
	for _, test := range committedAtValues {
		t.Run(test.name, func(t *testing.T) {
			hub := NewEventHub("o3b-f1a-invalid-committed-at", 4)
			ch, _, _, cancel := hub.Subscribe("")
			defer cancel()
			committed, basis, snapshot, generation := validApprovalOutboxEvent()

			envelope, err := hub.PublishApprovalUpdated(committed, test.value, basis, snapshot, generation)
			if envelope != nil || err == nil {
				t.Fatalf("invalid committedAt accepted: envelope=%+v err=%v", envelope, err)
			}
			if hub.headSeq != 0 || len(hub.ringBuffer) != 0 || len(hub.approvalDedup) != 0 {
				t.Fatalf("invalid committedAt mutated hub: head=%d ring=%d dedup=%d", hub.headSeq, len(hub.ringBuffer), len(hub.approvalDedup))
			}
			select {
			case got := <-ch:
				t.Fatalf("invalid committedAt broadcast an event: %+v", got)
			default:
			}
		})
	}
}

func TestEventHubPublishApprovalUpdatedRejectsCommittedAtConflictWithoutMutation(t *testing.T) {
	hub := NewEventHub("o3b-f1a-committed-at-conflict", 4)
	ch, _, _, cancel := hub.Subscribe("")
	defer cancel()
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	committedAt := validApprovalOutboxCommittedAt()
	first, err := hub.PublishApprovalUpdated(committed, committedAt, basis, snapshot, generation)
	if err != nil {
		t.Fatalf("first approval publication: %v", err)
	}
	if first == nil {
		t.Fatal("first approval publication returned nil envelope")
	}
	if got := receiveEvent(t, ch); got == nil || got.EventID != first.EventID {
		t.Fatalf("subscriber did not receive first publication: %+v", got)
	}

	beforeHead, beforeRing, beforeDedup := hub.headSeq, len(hub.ringBuffer), len(hub.approvalDedup)
	conflictAt := "2026-09-07T00:00:01Z"
	envelope, err := hub.PublishApprovalUpdated(committed, conflictAt, basis, snapshot, generation)
	if envelope != nil || err == nil {
		t.Fatalf("committedAt conflict accepted: envelope=%+v err=%v", envelope, err)
	}
	if hub.headSeq != beforeHead || len(hub.ringBuffer) != beforeRing || len(hub.approvalDedup) != beforeDedup {
		t.Fatalf("committedAt conflict mutated hub: head=%d ring=%d dedup=%d", hub.headSeq, len(hub.ringBuffer), len(hub.approvalDedup))
	}
	select {
	case got := <-ch:
		t.Fatalf("committedAt conflict broadcast an event: %+v", got)
	default:
	}
}

func TestEventHubPublishApprovalUpdatedClampsAndDeduplicatesAfterDurableRestart(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	firstHub, err := NewDurableEventHub("o3b-f1a-durable", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create first durable hub: %v", err)
	}
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	committedAt := validApprovalOutboxCommittedAt()
	firstHub.now = func() time.Time {
		return time.Date(2026, 9, 6, 23, 59, 59, 0, time.UTC)
	}
	first, err := firstHub.PublishApprovalUpdated(committed, committedAt, basis, snapshot, generation)
	if err != nil {
		t.Fatalf("first durable approval publication: %v", err)
	}
	if first == nil || first.OccurredAt.Format(time.RFC3339Nano) != committedAt {
		t.Fatalf("first occurredAt = %+v, want committedAt %q", first, committedAt)
	}

	restarted, err := NewDurableEventHub("o3b-f1a-durable", 4, ledgerPath)
	if err != nil {
		t.Fatalf("restart durable hub: %v", err)
	}
	ch, replay, needsSnapshotSync, cancel := restarted.Subscribe(first.EventID)
	defer cancel()
	if len(replay) != 0 || needsSnapshotSync {
		t.Fatalf("restart subscription replay=%+v needsSnapshotSync=%v, want no replay", replay, needsSnapshotSync)
	}
	ledgerBefore, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read durable ledger before replay: %v", err)
	}

	replayed, err := restarted.PublishApprovalUpdated(committed, committedAt, basis, snapshot, generation)
	if err != nil {
		t.Fatalf("identical durable replay: %v", err)
	}
	if !reflect.DeepEqual(first, replayed) {
		t.Fatalf("durable replay changed envelope:\nfirst=%+v\nreplayed=%+v", first, replayed)
	}
	if restarted.headSeq != 1 || len(restarted.ringBuffer) != 1 {
		t.Fatalf("durable replay changed state: head=%d ring=%d", restarted.headSeq, len(restarted.ringBuffer))
	}
	select {
	case got := <-ch:
		t.Fatalf("durable replay broadcast an event: %+v", got)
	default:
	}
	ledgerAfter, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read durable ledger after replay: %v", err)
	}
	if !bytes.Equal(ledgerBefore, ledgerAfter) || bytes.Count(ledgerAfter, []byte{'\n'}) != 1 {
		t.Fatalf("durable replay changed ledger: before=%q after=%q", ledgerBefore, ledgerAfter)
	}
}

func TestEventHubPublishApprovalUpdatedDeduplicatesWithoutBroadcast(t *testing.T) {
	hub := NewEventHub("o2-dedup", 4)
	ch, _, _, cancel := hub.Subscribe("")
	defer cancel()
	committed, basis, snapshot, generation := validApprovalOutboxEvent()

	first, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil {
		t.Fatalf("first typed publication: %v", err)
	}
	if first == nil || first.EventType != "approval.updated" || first.Sequence != 1 || first.OccurredAt.IsZero() {
		t.Fatalf("unexpected first publication: %+v", first)
	}
	if got := receiveEvent(t, ch); got == nil || got.EventID != first.EventID {
		t.Fatalf("subscriber received wrong first publication: %+v", got)
	}

	replayed, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil {
		t.Fatalf("identical typed replay: %v", err)
	}
	if !reflect.DeepEqual(first, replayed) {
		t.Fatalf("identical replay changed publication metadata:\nfirst=%+v\nreplayed=%+v", first, replayed)
	}
	select {
	case got := <-ch:
		t.Fatalf("identical replay broadcast an event: %+v", got)
	default:
	}
	if hub.headSeq != 1 || len(hub.ringBuffer) != 1 {
		t.Fatalf("identical replay changed sequence/ring: head=%d ring=%d", hub.headSeq, len(hub.ringBuffer))
	}
	if _, err := hub.PublishChecked("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil); err != nil {
		t.Fatalf("next generic event: %v", err)
	}
	if hub.headSeq != 2 {
		t.Fatalf("duplicate advanced sequence: %d", hub.headSeq)
	}

	conflict := committed
	conflict.Decision = "reject"
	if got, err := hub.PublishApprovalUpdated(conflict, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err == nil || got != nil {
		t.Fatalf("conflicting source event was accepted: env=%+v err=%v", got, err)
	}
	if hub.headSeq != 2 || len(hub.ringBuffer) != 2 {
		t.Fatalf("conflicting replay changed sequence/ring: head=%d ring=%d", hub.headSeq, len(hub.ringBuffer))
	}
}

func TestEventHubPublishApprovalUpdatedPersistenceFailureReservesNothing(t *testing.T) {
	persistErr := errors.New("injected approval ledger failure")
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	hub, err := NewDurableEventHub("o2-persist-failure", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	hub.persist = func([]byte) error { return persistErr }
	ch, _, _, cancel := hub.Subscribe("")
	defer cancel()
	committed, basis, snapshot, generation := validApprovalOutboxEvent()

	if got, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err == nil || got != nil || !errors.Is(err, persistErr) {
		t.Fatalf("persistence failure was not returned without a result: env=%+v err=%v", got, err)
	}
	if hub.headSeq != 0 || len(hub.ringBuffer) != 0 || len(hub.approvalDedup) != 0 {
		t.Fatalf("persistence failure reserved state: head=%d ring=%d dedup=%d", hub.headSeq, len(hub.ringBuffer), len(hub.approvalDedup))
	}
	select {
	case got := <-ch:
		t.Fatalf("persistence failure broadcast an event: %+v", got)
	default:
	}
	hub.persist = nil
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err != nil || env == nil || env.Sequence != 1 {
		t.Fatalf("retry after persistence failure did not start at sequence one: env=%+v err=%v", env, err)
	}
}

func TestEventHubFirstLedgerCreateSyncsContainingDirectory(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	hub, err := NewDurableEventHub("o2-first-create-sync", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	var synced []string
	hub.syncLedgerDir = func(path string) error {
		synced = append(synced, path)
		return nil
	}
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err != nil || env == nil {
		t.Fatalf("first publication: env=%+v err=%v", env, err)
	}
	if !reflect.DeepEqual(synced, []string{filepath.Dir(ledgerPath)}) {
		t.Fatalf("first ledger creation did not sync its containing directory: %v", synced)
	}
}

func TestEventHubFirstAppendRollbackSyncsRemovalAndRetries(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	hub, err := NewDurableEventHub("o2-first-rollback-sync", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	partialErr := errors.New("injected first append error")
	var synced []string
	hub.syncLedgerDir = func(path string) error {
		synced = append(synced, path)
		return nil
	}
	hub.persist = func(data []byte) error {
		if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(ledgerPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		record := append(append([]byte(nil), data...), '\n')
		if _, err := file.Write(record[:len(record)/2]); err != nil {
			_ = file.Close()
			return err
		}
		_ = file.Close()
		return partialErr
	}
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); env != nil || err == nil || !errors.Is(err, partialErr) {
		t.Fatalf("partial first append was not rejected: env=%+v err=%v", env, err)
	}
	if _, err := os.Stat(ledgerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial first append was not removed: err=%v", err)
	}
	if !reflect.DeepEqual(synced, []string{filepath.Dir(ledgerPath)}) {
		t.Fatalf("first append rollback did not sync its containing directory: %v", synced)
	}
	hub.persist = appendEventLedger(ledgerPath)
	retried, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil || retried == nil || retried.Sequence != 1 {
		t.Fatalf("retry after first append rollback failed: env=%+v err=%v", retried, err)
	}
	restarted, err := NewDurableEventHub("o2-first-rollback-sync", 4, ledgerPath)
	if err != nil {
		t.Fatalf("restart after first append retry: %v", err)
	}
	replayed, err := restarted.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil || replayed == nil || !reflect.DeepEqual(retried, replayed) || restarted.headSeq != 1 {
		t.Fatalf("restart did not retain one retried event: env=%+v replayed=%+v head=%d err=%v", retried, replayed, restarted.headSeq, err)
	}
}

func TestEventHubFirstAppendRollbackDirectorySyncFailurePoisons(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	hub, err := NewDurableEventHub("o2-first-rollback-poison", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	partialErr := errors.New("injected first append error")
	directoryErr := errors.New("injected directory sync error")
	hub.syncLedgerDir = func(string) error { return directoryErr }
	hub.persist = func(data []byte) error {
		if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(ledgerPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		record := append(append([]byte(nil), data...), '\n')
		if _, err := file.Write(record[:len(record)/2]); err != nil {
			_ = file.Close()
			return err
		}
		_ = file.Close()
		return partialErr
	}
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); env != nil || err == nil || !errors.Is(err, errEventHubPoisoned) || !errors.Is(err, directoryErr) {
		t.Fatalf("directory sync failure did not poison first append: env=%+v err=%v", env, err)
	}
	if hub.headSeq != 0 || len(hub.ringBuffer) != 0 || !hub.poisoned {
		t.Fatalf("poisoned first append changed in-memory state: head=%d ring=%d poisoned=%t", hub.headSeq, len(hub.ringBuffer), hub.poisoned)
	}
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); env != nil || !errors.Is(err, errEventHubPoisoned) {
		t.Fatalf("poisoned hub accepted a later typed event: env=%+v err=%v", env, err)
	}
	if env, err := hub.PublishChecked("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil); env != nil || !errors.Is(err, errEventHubPoisoned) {
		t.Fatalf("poisoned hub accepted a later generic event: env=%+v err=%v", env, err)
	}
	if env, err := hub.PublishAtomic("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil, nil); env != nil || !errors.Is(err, errEventHubPoisoned) {
		t.Fatalf("poisoned hub accepted a later atomic event: env=%+v err=%v", env, err)
	}
}

func TestEventHubAtomicExternalLedgerAppendRefreshesOffset(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	hub, err := NewDurableEventHub("o2-atomic-offset", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	if env, err := hub.PublishAtomic("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil, func(_ *semantic.EventEnvelope, encoded []byte) error {
		return appendEventLedger(ledgerPath)(encoded)
	}); err != nil || env == nil || env.Sequence != 1 {
		t.Fatalf("external atomic append: env=%+v err=%v", env, err)
	}
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	second, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil || second == nil || second.Sequence != 2 {
		t.Fatalf("normal append after external atomic append: env=%+v err=%v", second, err)
	}
	restarted, err := NewDurableEventHub("o2-atomic-offset", 4, ledgerPath)
	if err != nil || restarted.headSeq != 2 {
		t.Fatalf("restart did not preserve externally appended ledger: hub=%+v err=%v", restarted, err)
	}
}

func TestEventHubPublishApprovalUpdatedDedupSurvivesRingEviction(t *testing.T) {
	hub := NewEventHub("o2-eviction", 1)
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	first, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil {
		t.Fatalf("first typed publication: %v", err)
	}
	if _, err := hub.PublishChecked("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil); err != nil {
		t.Fatalf("ring eviction event: %v", err)
	}
	if len(hub.ringBuffer) != 1 || hub.ringBuffer[0].EventType != "activity.updated" {
		t.Fatalf("approval publication was not evicted as expected: %+v", hub.ringBuffer)
	}
	replayed, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil {
		t.Fatalf("replay after ring eviction: %v", err)
	}
	if !reflect.DeepEqual(first, replayed) || hub.headSeq != 2 {
		t.Fatalf("ring eviction lost dedup metadata: first=%+v replayed=%+v head=%d", first, replayed, hub.headSeq)
	}
}

func TestEventHubPublishApprovalUpdatedConcurrentIdenticalCallsPublishOnce(t *testing.T) {
	hub := NewEventHub("o2-concurrent", 4)
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	const callers = 8
	results := make([]*semantic.EventEnvelope, callers)
	errors := make([]error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for index := 0; index < callers; index++ {
		go func(index int) {
			defer group.Done()
			results[index], errors[index] = hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
		}(index)
	}
	group.Wait()
	for index := range results {
		if errors[index] != nil || results[index] == nil || results[index].Sequence != 1 {
			t.Fatalf("concurrent call %d failed: env=%+v err=%v", index, results[index], errors[index])
		}
		if !reflect.DeepEqual(results[0], results[index]) {
			t.Fatalf("concurrent call %d returned different publication: first=%+v got=%+v", index, results[0], results[index])
		}
	}
	if hub.headSeq != 1 || len(hub.ringBuffer) != 1 || len(hub.approvalDedup) != 1 {
		t.Fatalf("concurrent identical calls published more than once: head=%d ring=%d dedup=%d", hub.headSeq, len(hub.ringBuffer), len(hub.approvalDedup))
	}
}

func TestEventHubPublishApprovalUpdatedPersistsAndReplaysAfterRestart(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	firstHub, err := NewDurableEventHub("o2-restart", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	first, err := firstHub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil {
		t.Fatalf("first durable typed publication: %v", err)
	}
	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read initial ledger: %v", err)
	}

	restarted, err := NewDurableEventHub("o2-restart", 4, ledgerPath)
	if err != nil {
		t.Fatalf("restart durable hub: %v", err)
	}
	ch, _, _, cancel := restarted.Subscribe("")
	defer cancel()
	replayed, err := restarted.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil {
		t.Fatalf("durable typed replay: %v", err)
	}
	if !reflect.DeepEqual(first, replayed) {
		t.Fatalf("restart replay changed original publication metadata:\nfirst=%+v\nreplayed=%+v", first, replayed)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read replayed ledger: %v", err)
	}
	if !bytes.Equal(before, after) || restarted.headSeq != 1 || len(restarted.ringBuffer) != 1 {
		t.Fatalf("durable replay changed ledger/sequence: bytesChanged=%t head=%d ring=%d", !bytes.Equal(before, after), restarted.headSeq, len(restarted.ringBuffer))
	}
	select {
	case got := <-ch:
		t.Fatalf("restart replay broadcast an event: %+v", got)
	default:
	}
	if _, err := restarted.PublishChecked("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil); err != nil {
		t.Fatalf("publish after restart replay: %v", err)
	}
	if restarted.headSeq != 2 {
		t.Fatalf("restart replay did not preserve sequence: %d", restarted.headSeq)
	}
}

func TestEventHubPublishApprovalUpdatedReconcilesFullAppendCloseError(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	closeErr := errors.New("injected close error after durable append")
	hub, err := NewDurableEventHub("o2-close-reconcile", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	hub.persist = func(data []byte) error {
		if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(ledgerPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		record := append(append([]byte(nil), data...), '\n')
		if _, err := file.Write(record); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		return closeErr
	}
	ch, _, _, cancel := hub.Subscribe("")
	defer cancel()
	committed, basis, snapshot, generation := validApprovalOutboxEvent()

	env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil || env == nil {
		t.Fatalf("durable full append was not reconciled: env=%+v err=%v", env, err)
	}
	if got := receiveEvent(t, ch); got == nil || got.EventID != env.EventID {
		t.Fatalf("reconciled publication was not broadcast exactly once: %+v", got)
	}
	if hub.headSeq != 1 || len(hub.ringBuffer) != 1 {
		t.Fatalf("reconciled publication changed sequence unexpectedly: head=%d ring=%d", hub.headSeq, len(hub.ringBuffer))
	}
	restarted, err := NewDurableEventHub("o2-close-reconcile", 4, ledgerPath)
	if err != nil {
		t.Fatalf("restart reconciled ledger: %v", err)
	}
	replayed, err := restarted.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil || replayed == nil || !reflect.DeepEqual(env, replayed) {
		t.Fatalf("reconciled ledger did not provide one durable replay: env=%+v replayed=%+v err=%v", env, replayed, err)
	}
}

func TestEventHubPublishApprovalUpdatedRollsBackPartialAppendBeforeRetry(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	hub, err := NewDurableEventHub("o2-partial-rollback", 1, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	first, basis, snapshot, generation := validApprovalOutboxEvent()
	if _, err := hub.PublishApprovalUpdated(first, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err != nil {
		t.Fatalf("first publication: %v", err)
	}
	before, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read baseline ledger: %v", err)
	}
	second := first
	second.EventID = "approval-event-2"
	second.ApprovalID = "approval-2"
	partialErr := errors.New("injected partial append error")
	hub.persist = func(data []byte) error {
		file, err := os.OpenFile(ledgerPath, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		record := append(append([]byte(nil), data...), '\n')
		partial := record[:len(record)/2]
		if _, err := file.Write(partial); err != nil {
			_ = file.Close()
			return err
		}
		_ = file.Close()
		return partialErr
	}
	if got, err := hub.PublishApprovalUpdated(second, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err == nil || got != nil || !errors.Is(err, partialErr) {
		t.Fatalf("partial append did not fail without a result: env=%+v err=%v", got, err)
	}
	afterRollback, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read rolled-back ledger: %v", err)
	}
	if !bytes.Equal(before, afterRollback) || hub.headSeq != 1 || len(hub.ringBuffer) != 1 || hub.poisoned {
		t.Fatalf("partial append was not restored exactly: bytesChanged=%t head=%d ring=%d poisoned=%t", !bytes.Equal(before, afterRollback), hub.headSeq, len(hub.ringBuffer), hub.poisoned)
	}
	hub.persist = appendEventLedger(ledgerPath)
	retried, err := hub.PublishApprovalUpdated(second, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil || retried == nil || retried.Sequence != 2 {
		t.Fatalf("retry after rollback did not publish exactly once: env=%+v err=%v", retried, err)
	}
	restarted, err := NewDurableEventHub("o2-partial-rollback", 1, ledgerPath)
	if err != nil {
		t.Fatalf("restart after rollback retry: %v", err)
	}
	replayed, err := restarted.PublishApprovalUpdated(second, validApprovalOutboxCommittedAt(), basis, snapshot, generation)
	if err != nil || replayed == nil || !reflect.DeepEqual(retried, replayed) || restarted.headSeq != 2 {
		t.Fatalf("restart did not retain one retried publication: retried=%+v replayed=%+v head=%d err=%v", retried, replayed, restarted.headSeq, err)
	}
}

func TestEventHubPublishApprovalUpdatedPoisonsOnUnrestorableAppend(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	hub, err := NewDurableEventHub("o2-poison", 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	partialErr := errors.New("injected ambiguous append error")
	restoreErr := errors.New("injected restore error")
	hub.persist = func(data []byte) error {
		file, err := os.OpenFile(ledgerPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		record := append(append([]byte(nil), data...), '\n')
		if _, err := file.Write(record[:len(record)/2]); err != nil {
			_ = file.Close()
			return err
		}
		_ = file.Close()
		return partialErr
	}
	hub.restoreLedger = func(string, int64, bool) error { return restoreErr }
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err == nil || env != nil || !errors.Is(err, errEventHubPoisoned) {
		t.Fatalf("unrestorable append did not poison hub: env=%+v err=%v", env, err)
	}
	if hub.headSeq != 0 || len(hub.ringBuffer) != 0 || len(hub.approvalDedup) != 0 || !hub.poisoned {
		t.Fatalf("poisoned append changed in-memory state: head=%d ring=%d dedup=%d poisoned=%t", hub.headSeq, len(hub.ringBuffer), len(hub.approvalDedup), hub.poisoned)
	}
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); env != nil || !errors.Is(err, errEventHubPoisoned) {
		t.Fatalf("poisoned hub accepted later typed publication: env=%+v err=%v", env, err)
	}
	if env, err := hub.PublishChecked("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil); env != nil || !errors.Is(err, errEventHubPoisoned) {
		t.Fatalf("poisoned hub accepted later generic publication: env=%+v err=%v", env, err)
	}
}

func TestEventHubDurableLoaderRejectsMalformedAndDuplicateApprovalSources(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
		committed, basis, snapshot, generation := validApprovalOutboxEvent()
		hub, err := NewDurableEventHub("o2-malformed", 4, ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err != nil {
			t.Fatal(err)
		}
		line, err := os.ReadFile(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		var env semantic.EventEnvelope
		if err := json.Unmarshal(bytes.TrimSpace(line), &env); err != nil {
			t.Fatal(err)
		}
		env.Data = map[string]any{"approvalEvent": map[string]any{"eventId": committed.EventID}}
		encoded, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ledgerPath, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewDurableEventHub("o2-malformed", 4, ledgerPath); err == nil {
			t.Fatal("malformed approval.updated record was accepted")
		}
	})

	t.Run("duplicate-source", func(t *testing.T) {
		ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
		committed, basis, snapshot, generation := validApprovalOutboxEvent()
		hub, err := NewDurableEventHub("o2-duplicate", 4, ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err != nil {
			t.Fatal(err)
		}
		line, err := os.ReadFile(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		var env semantic.EventEnvelope
		if err := json.Unmarshal(bytes.TrimSpace(line), &env); err != nil {
			t.Fatal(err)
		}
		env.Sequence = 2
		env.EventID = "event-2"
		encoded, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ledgerPath, append(append(line, encoded...), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewDurableEventHub("o2-duplicate", 4, ledgerPath); err == nil {
			t.Fatal("duplicate approval source record was accepted")
		}
	})
}

func TestEventHubDurableLoaderRejectsDuplicateApprovalJSONKeyWithoutVisibility(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	hub, err := NewDurableEventHub("o2-f3-duplicate-key", 4, ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err != nil || env == nil {
		t.Fatalf("seed approval event: env=%+v err=%v", env, err)
	}
	canonical, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read canonical approval ledger: %v", err)
	}
	needle := []byte(`"approvalId":"approval-1"`)
	duplicate := []byte(`"approvalId":"approval-1","approvalId":"approval-1"`)
	if !bytes.Contains(canonical, needle) {
		t.Fatalf("canonical approval record did not contain the expected approvalId field: %s", canonical)
	}
	tampered := bytes.Replace(canonical, needle, duplicate, 1)
	if err := os.WriteFile(ledgerPath, tampered, 0o600); err != nil {
		t.Fatalf("write duplicate-key approval ledger: %v", err)
	}
	loaded, err := NewDurableEventHub("o2-f3-duplicate-key", 4, ledgerPath)
	if loaded != nil || err == nil {
		t.Fatalf("durable loader exposed a duplicate approval JSON key: loaded=%+v err=%v", loaded, err)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read rejected duplicate-key ledger: %v", err)
	}
	if !bytes.Equal(tampered, after) {
		t.Fatal("duplicate-key rejection changed the durable ledger")
	}
}

func TestEventHubDurableLoaderRejectsInvalidUTF8ApprovalRecordWithoutVisibility(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	committed, basis, snapshot, generation := validApprovalOutboxEvent()
	hub, err := NewDurableEventHub("o2-f3-invalid-utf8", 4, ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err != nil || env == nil {
		t.Fatalf("seed approval event: env=%+v err=%v", env, err)
	}
	canonical, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read canonical approval ledger: %v", err)
	}
	needle := []byte("approval-1")
	index := bytes.Index(canonical, needle)
	if index < 0 {
		t.Fatalf("canonical approval record did not contain the expected identity: %s", canonical)
	}
	tampered := append([]byte(nil), canonical...)
	tampered[index] = 0xff
	if !bytes.HasSuffix(tampered, []byte{'\n'}) || utf8.Valid(tampered[:len(tampered)-1]) {
		t.Fatal("invalid UTF-8 fixture was not a complete newline-terminated record")
	}
	if err := os.WriteFile(ledgerPath, tampered, 0o600); err != nil {
		t.Fatalf("write invalid UTF-8 approval ledger: %v", err)
	}
	loaded, err := NewDurableEventHub("o2-f3-invalid-utf8", 4, ledgerPath)
	if loaded != nil || err == nil {
		t.Fatalf("durable loader exposed invalid UTF-8 approval record: loaded=%+v err=%v", loaded, err)
	}
	after, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read rejected invalid UTF-8 ledger: %v", err)
	}
	if !bytes.Equal(tampered, after) {
		t.Fatal("invalid UTF-8 rejection changed the durable ledger")
	}
}

func rewriteEventLedgerRecord(t *testing.T, ledgerPath string, mutate func(map[string]json.RawMessage)) {
	t.Helper()
	data, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger: %v", err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
		t.Fatalf("decode event ledger: %v", err)
	}
	mutate(record)
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode event ledger: %v", err)
	}
	if err := os.WriteFile(ledgerPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("rewrite event ledger: %v", err)
	}
}

func seedActivityLedger(t *testing.T, streamID string) (string, *EventHub) {
	t.Helper()
	ledgerPath := filepath.Join(t.TempDir(), "events", "ledger.jsonl")
	hub, err := NewDurableEventHub(streamID, 4, ledgerPath)
	if err != nil {
		t.Fatalf("create durable hub: %v", err)
	}
	if env, err := hub.PublishChecked("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil); err != nil || env == nil {
		t.Fatalf("seed activity event: env=%+v err=%v", env, err)
	}
	return ledgerPath, hub
}

func TestEventHubDurableLoaderRejectsUnknownOuterField(t *testing.T) {
	ledgerPath, _ := seedActivityLedger(t, "o2-f2-unknown-field")
	rewriteEventLedgerRecord(t, ledgerPath, func(record map[string]json.RawMessage) {
		record["unexpected"] = json.RawMessage(`"must-reject"`)
	})
	if _, err := NewDurableEventHub("o2-f2-unknown-field", 4, ledgerPath); err == nil {
		t.Fatal("durable loader accepted an unknown outer envelope field")
	}
}

func TestEventHubDurableLoaderRejectsNonCanonicalOccurredAt(t *testing.T) {
	cases := []struct {
		name       string
		occurredAt string
	}{
		{name: "equivalent offset", occurredAt: "2026-09-07T12:00:00+09:00"},
		{name: "redundant fractional zero", occurredAt: "2026-09-07T03:00:00.000Z"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ledgerPath, _ := seedActivityLedger(t, "o2-f2-occurred-at-"+strings.ReplaceAll(test.name, " ", "-"))
			rewriteEventLedgerRecord(t, ledgerPath, func(record map[string]json.RawMessage) {
				occurredAt, err := json.Marshal(test.occurredAt)
				if err != nil {
					t.Fatalf("encode occurredAt: %v", err)
				}
				record["occurredAt"] = occurredAt
			})
			if _, err := NewDurableEventHub("o2-f2-occurred-at-"+strings.ReplaceAll(test.name, " ", "-"), 4, ledgerPath); err == nil {
				t.Fatalf("durable loader accepted noncanonical occurredAt %q", test.occurredAt)
			}
		})
	}
}

func TestEventHubDurableLoaderAcceptsCanonicalWriterOccurredAtAfterRestart(t *testing.T) {
	ledgerPath, hub := seedActivityLedger(t, "o2-f2-canonical-occurred-at")
	if _, err := NewDurableEventHub("o2-f2-canonical-occurred-at", 4, ledgerPath); err != nil {
		t.Fatalf("canonical writer occurredAt was rejected on restart: %v", err)
	}
	if hub.headSeq != 1 {
		t.Fatalf("seed hub did not publish one event: head=%d", hub.headSeq)
	}
}

func TestEventHubGenericApprovalUpdatedAtomicCannotBypassDedup(t *testing.T) {
	hub := NewEventHub("o2-atomic-bypass", 4)
	called := false
	env, err := hub.PublishAtomic("approval.updated", map[string]any{"approvalEvent": map[string]any{"eventId": "approval-event-1"}}, nil, nil, nil, func(*semantic.EventEnvelope, []byte) error {
		called = true
		return nil
	})
	if err == nil || env != nil || called {
		t.Fatalf("generic approval.updated atomic path bypassed typed publisher: env=%+v err=%v called=%t", env, err, called)
	}
	if hub.headSeq != 0 || len(hub.ringBuffer) != 0 {
		t.Fatalf("generic atomic bypass changed hub state: head=%d ring=%d", hub.headSeq, len(hub.ringBuffer))
	}
}

func TestEventHubPublishApprovalUpdatedRejectsInvalidShapeWithoutMutation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*semantic.ApprovalOutboxCommittedEventV1, *string, *string, *string)
	}{
		{name: "empty approval id", mutate: func(event *semantic.ApprovalOutboxCommittedEventV1, _ *string, _ *string, _ *string) {
			event.ApprovalID = ""
		}},
		{name: "bad basis", mutate: func(_ *semantic.ApprovalOutboxCommittedEventV1, basis *string, _ *string, _ *string) {
			*basis = "not-a-digest"
		}},
		{name: "bad decision", mutate: func(event *semantic.ApprovalOutboxCommittedEventV1, _ *string, _ *string, _ *string) {
			event.Decision = "unknown"
		}},
		{name: "bad payload digest", mutate: func(event *semantic.ApprovalOutboxCommittedEventV1, _ *string, _ *string, _ *string) {
			event.PayloadDigest = "sha256:" + strings.Repeat("A", 64)
		}},
		{name: "zero version", mutate: func(event *semantic.ApprovalOutboxCommittedEventV1, _ *string, _ *string, _ *string) {
			event.AggregateVersion = 0
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			hub := NewEventHub("o2-invalid", 4)
			committed, basis, snapshot, generation := validApprovalOutboxEvent()
			test.mutate(&committed, &basis, &snapshot, &generation)
			if env, err := hub.PublishApprovalUpdated(committed, validApprovalOutboxCommittedAt(), basis, snapshot, generation); err == nil || env != nil {
				t.Fatalf("invalid publication was accepted: env=%+v err=%v", env, err)
			}
			if hub.headSeq != 0 || len(hub.ringBuffer) != 0 || len(hub.approvalDedup) != 0 {
				t.Fatalf("invalid publication changed state: head=%d ring=%d dedup=%d", hub.headSeq, len(hub.ringBuffer), len(hub.approvalDedup))
			}
		})
	}
}
