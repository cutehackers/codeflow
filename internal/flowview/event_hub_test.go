package flowview_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/flowview"
	"codeflow/internal/semantic"
)

func TestEventHub_PublishAndSubscribe(t *testing.T) {
	hub := flowview.NewEventHub("stream-test", 100)
	basis := strings.Repeat("a", 64)
	snapshot := "snapshot-test"
	generation := "generation-test"

	// Publish 5 events
	for i := 1; i <= 5; i++ {
		hub.Publish("activity.updated", map[string]any{"activity": "editing", "iter": i}, &basis, &snapshot, nil)
	}

	// Client reconnects with lastEventID = "event-3"
	ch, replay, needsSync, cancel := hub.Subscribe("event-3")
	defer cancel()

	if needsSync {
		t.Errorf("expected needsSync to be false when event-3 is in buffer")
	}
	if len(replay) != 2 {
		t.Fatalf("expected 2 replayed events (event-4, event-5), got %d", len(replay))
	}
	if replay[0].EventID != "event-4" || replay[1].EventID != "event-5" {
		t.Errorf("unexpected replay order: %s, %s", replay[0].EventID, replay[1].EventID)
	}

	// Next live event
	hub.Publish("generation.published", map[string]any{"gen": "gen-1"}, &basis, &snapshot, &generation)

	select {
	case liveEv := <-ch:
		if liveEv.EventID != "event-6" {
			t.Errorf("expected live event-6, got %s", liveEv.EventID)
		}
		if liveEv.EventType != "generation.published" {
			t.Errorf("expected generation.published, got %s", liveEv.EventType)
		}
	default:
		t.Fatalf("expected live event on subscriber channel")
	}
}

func TestEventHub_RejectsInvalidIdentityBeforePersistenceOrSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	hub, err := flowview.NewDurableEventHub("stream-test", 10, path)
	if err != nil {
		t.Fatal(err)
	}
	basis := strings.Repeat("a", 64)
	snapshot := "snapshot-test"
	generation := "generation-test"

	tests := []struct {
		name       string
		eventType  string
		basis      *string
		snapshot   *string
		generation *string
		data       any
	}{
		{name: "published requires all identities", eventType: "generation.published", snapshot: &snapshot, generation: &generation, data: map[string]any{}},
		{name: "gap requires basis", eventType: "generation.gap", snapshot: &snapshot, data: map[string]any{"freshness": "last_verified"}},
		{name: "gap rejects generation", eventType: "generation.gap", basis: &basis, snapshot: &snapshot, generation: &generation, data: map[string]any{"freshness": "last_verified"}},
		{name: "editing activity requires snapshot", eventType: "activity.updated", basis: &basis, data: map[string]any{"activity": "editing"}},
		{name: "snapshot sync is transport only", eventType: "snapshot_sync", basis: &basis, data: map[string]any{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if env, err := hub.PublishChecked(test.eventType, test.data, test.basis, test.snapshot, test.generation); err == nil || env != nil {
				t.Fatalf("invalid event was accepted: env=%+v err=%v", env, err)
			}
		})
	}
	if data, err := os.ReadFile(path); !os.IsNotExist(err) && len(data) != 0 {
		t.Fatalf("invalid events were persisted: %q", data)
	}
	valid, err := hub.PublishChecked("generation.gap", map[string]any{"freshness": "last_verified"}, &basis, &snapshot, nil)
	if err != nil {
		t.Fatalf("valid gap was rejected: %v", err)
	}
	if valid == nil || valid.Sequence != 1 {
		t.Fatalf("invalid events advanced sequence: %+v", valid)
	}
	transport := hub.SnapshotSync(map[string]any{"activePointer": nil})
	if transport.EventType != "snapshot_sync" || transport.ComputedBasisID != nil || transport.ValidatedAgainstSnapshotID != nil || transport.GenerationID != nil || transport.Sequence != 1 {
		t.Fatalf("invalid snapshot sync envelope: %+v", transport)
	}
}

func TestEventHub_PublishAtomicValidatesBeforeCommitCallback(t *testing.T) {
	hub := flowview.NewEventHub("stream-test", 10)
	basis := strings.Repeat("a", 64)
	snapshot := "snapshot-test"
	generation := "generation-test"
	called := false
	if env, err := hub.PublishAtomic("generation.published", map[string]any{}, nil, &snapshot, &generation, func(_ *semantic.EventEnvelope, _ []byte) error {
		called = true
		return nil
	}); err == nil || env != nil {
		t.Fatalf("invalid atomic event was accepted: env=%+v err=%v", env, err)
	}
	if called {
		t.Fatal("atomic callback ran for an invalid envelope")
	}
	valid, err := hub.PublishAtomic("generation.published", map[string]any{}, &basis, &snapshot, &generation, func(_ *semantic.EventEnvelope, _ []byte) error {
		called = true
		return nil
	})
	if err != nil || valid == nil || valid.Sequence != 1 || !called {
		t.Fatalf("valid atomic event did not commit: env=%+v err=%v called=%v", valid, err, called)
	}
}

func TestEventHub_LatestTerminalEventSurvivesRestartAndIsolatedFromMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	hub, err := flowview.NewDurableEventHub("stream-test", 10, path)
	if err != nil {
		t.Fatal(err)
	}
	basis := strings.Repeat("a", 64)
	snapshot := "snapshot-test"
	gapData := map[string]any{"freshness": "last_verified", "affected": []any{"service.go"}}
	gap, err := hub.PublishChecked("generation.gap", gapData, &basis, &snapshot, nil)
	if err != nil {
		t.Fatalf("publish gap: %v", err)
	}
	gapData["freshness"] = "caller-mutated"
	if _, err := hub.PublishChecked("activity.updated", map[string]any{"activity": "idle"}, nil, nil, nil); err != nil {
		t.Fatalf("publish activity: %v", err)
	}
	latest := hub.LatestTerminalEvent()
	if latest == nil || latest.EventID != gap.EventID || latest.EventType != "generation.gap" {
		t.Fatalf("latest terminal event did not ignore trailing activity: %+v", latest)
	}
	if data, ok := latest.Data.(map[string]any); ok {
		data["freshness"] = "mutated"
	}
	if latestAgain := hub.LatestTerminalEvent(); latestAgain == nil || latestAgain.Data.(map[string]any)["freshness"] != "last_verified" {
		t.Fatalf("latest terminal event aliases hub state: %+v", latestAgain)
	}

	restarted, err := flowview.NewDurableEventHub("stream-test", 10, path)
	if err != nil {
		t.Fatal(err)
	}
	restartedLatest := restarted.LatestTerminalEvent()
	if restartedLatest == nil || restartedLatest.EventID != gap.EventID || restartedLatest.EventType != "generation.gap" {
		t.Fatalf("latest terminal event was not rebuilt from durable ledger: %+v", restartedLatest)
	}
	if data, ok := restartedLatest.Data.(map[string]any); !ok || data["freshness"] != "last_verified" {
		t.Fatalf("durable terminal event was unexpectedly mutated: %+v", restartedLatest.Data)
	}

	generation := "generation-test"
	if _, err := restarted.PublishChecked("generation.published", map[string]any{}, &basis, &snapshot, &generation); err != nil {
		t.Fatalf("publish current event: %v", err)
	}
	latestCurrent := restarted.LatestTerminalEvent()
	if latestCurrent == nil || latestCurrent.EventType != "generation.published" {
		t.Fatalf("latest terminal event returned stale gap after current publication: %+v", latestCurrent)
	}
}

func TestEventHub_SlowSubscriberIsClosedAndCanReplayWithoutLoss(t *testing.T) {
	hub := flowview.NewEventHub("stream-test", 200)
	ch, _, _, cancel := hub.Subscribe("")
	defer cancel()
	// Fill the bounded subscriber queue, then publish one more event. The
	// implementation must close the stream instead of silently skipping it.
	for i := 0; i < 51; i++ {
		hub.Publish("activity.updated", map[string]any{"iter": i}, nil, nil, nil)
	}
	closed := false
	for range ch {
		// Drain until the producer's explicit close is observable.
	}
	closed = true
	if !closed {
		t.Fatal("subscriber should be closed on overflow")
	}
	_, replay, needsSync, cancel2 := hub.Subscribe("event-1")
	defer cancel2()
	if needsSync || len(replay) != 50 {
		t.Fatalf("ring replay lost events after subscriber overflow: needsSync=%v len=%d", needsSync, len(replay))
	}
}

func TestEventHub_DurableSequenceSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	hub, err := flowview.NewDurableEventHub("stream-test", 10, path)
	if err != nil {
		t.Fatal(err)
	}
	first := hub.Publish("activity.updated", map[string]any{"iter": 1}, nil, nil, nil)
	if first == nil || first.Sequence != 1 {
		t.Fatalf("unexpected first event: %+v", first)
	}
	hub2, err := flowview.NewDurableEventHub("stream-test", 10, path)
	if err != nil {
		t.Fatal(err)
	}
	second := hub2.Publish("activity.updated", map[string]any{"iter": 2}, nil, nil, nil)
	if second == nil || second.Sequence != 2 {
		t.Fatalf("sequence was not restored: %+v", second)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("event ledger was not durable: %v", err)
	}
}

func TestEventHub_RingBufferEvictionAndSnapshotSync(t *testing.T) {
	// Small ring buffer of 5
	hub := flowview.NewEventHub("stream-test", 5)

	for i := 1; i <= 10; i++ {
		hub.Publish("activity.updated", map[string]any{"iter": i}, nil, nil, nil)
	}

	// Client connects with lastEventID = "event-2" (which has been evicted from 5-item buffer)
	_, replay, needsSync, cancel := hub.Subscribe("event-2")
	defer cancel()

	if !needsSync {
		t.Errorf("expected needsSync=true when Last-Event-ID was evicted from ring buffer")
	}
	if len(replay) != 0 {
		t.Errorf("expected empty replay when snapshot_sync is required")
	}
}

func TestEventHub_SnapshotSyncCursorResumesFromDurableHead(t *testing.T) {
	hub := flowview.NewEventHub("stream-test", 2)
	for i := 0; i < 3; i++ {
		hub.Publish("activity.updated", map[string]any{"iter": i}, nil, nil, nil)
	}
	sync := hub.SnapshotSync(map[string]any{"state": "current"})
	if sync.Sequence != 3 || sync.EventID != "event-3" {
		t.Fatalf("snapshot sync must bind to durable head, got sequence=%d id=%q", sync.Sequence, sync.EventID)
	}
	hub.Publish("activity.updated", map[string]any{"iter": 3}, nil, nil, nil)
	_, replay, needsSync, cancel := hub.Subscribe(sync.EventID)
	defer cancel()
	if needsSync || len(replay) != 1 || replay[0].Sequence != 4 || replay[0].EventID != "event-4" {
		t.Fatalf("sync cursor did not resume monotonically: needsSync=%v replay=%+v", needsSync, replay)
	}
}

func TestEventHub_DurableRecoveryTruncatesTrailingPartialBeforeAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	hub, err := flowview.NewDurableEventHub("stream-test", 10, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := hub.Publish("activity.updated", map[string]any{"iter": 1}, nil, nil, nil); got == nil || got.Sequence != 1 {
		t.Fatalf("unexpected first event: %+v", got)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"schemaId":"https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json"`); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := flowview.NewDurableEventHub("stream-test", 10, path)
	if err != nil {
		t.Fatalf("recover trailing partial: %v", err)
	}
	if got := recovered.Publish("activity.updated", map[string]any{"iter": 2}, nil, nil, nil); got == nil || got.Sequence != 2 {
		t.Fatalf("append after recovery reused wrong sequence: %+v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("recovery did not remove unterminated bytes: %q", data)
	}
	if _, err := flowview.NewDurableEventHub("stream-test", 10, path); err != nil {
		t.Fatalf("restart after repaired ledger failed: %v", err)
	}
}

func TestEventHub_DurableRecoveryUsesPerRecordBoundNotLifetimeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	hub, err := flowview.NewDurableEventHub("stream-test", 2, path)
	if err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", 100_000)
	for i := 0; i < 50; i++ {
		if hub.Publish("activity.updated", map[string]any{"diagnostic": large}, nil, nil, nil) == nil {
			t.Fatalf("event %d was not published", i)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Size() <= 4*1024*1024 {
		t.Fatalf("test ledger did not exceed former lifetime cap: info=%+v err=%v", info, err)
	}
	restarted, err := flowview.NewDurableEventHub("stream-test", 2, path)
	if err != nil {
		t.Fatalf("healthy long ledger must restart: %v", err)
	}
	if got := restarted.Publish("activity.updated", map[string]any{"iter": 51}, nil, nil, nil); got == nil || got.Sequence != 51 {
		t.Fatalf("restarted sequence mismatch: %+v", got)
	}
}
