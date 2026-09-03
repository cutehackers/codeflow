package flowview_test

import (
	"testing"

	"codeflow/internal/flowview"
)

func TestEventHub_PublishAndSubscribe(t *testing.T) {
	hub := flowview.NewEventHub("stream-test", 100)

	// Publish 5 events
	for i := 1; i <= 5; i++ {
		hub.Publish("activity.updated", map[string]any{"activity": "editing", "iter": i}, nil, nil, nil)
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
	hub.Publish("generation.published", map[string]any{"gen": "gen-1"}, nil, nil, nil)

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
