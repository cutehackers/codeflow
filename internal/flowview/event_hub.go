package flowview

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"codeflow/internal/semantic"
)

// EventHub manages a memory ring buffer of recent EventEnvelopes and coordinates
// SSE streaming, replay by Last-Event-ID, and fallback to snapshot_sync (Raw §10.14, SID-C6).
type EventHub struct {
	mu          sync.RWMutex
	streamID    string
	bufferCap   int
	headSeq     int
	ringBuffer  []*semantic.EventEnvelope
	subscribers map[chan *semantic.EventEnvelope]bool
}

// NewEventHub creates an EventHub with specified ring buffer capacity (default 100).
func NewEventHub(streamID string, bufferCap int) *EventHub {
	if bufferCap <= 0 {
		bufferCap = 100
	}
	if streamID == "" {
		streamID = "live-comprehension-stream"
	}
	return &EventHub{
		streamID:    streamID,
		bufferCap:   bufferCap,
		headSeq:     0,
		ringBuffer:  make([]*semantic.EventEnvelope, 0, bufferCap),
		subscribers: make(map[chan *semantic.EventEnvelope]bool),
	}
}

// Publish creates, stores, and broadcasts an EventEnvelope.
func (h *EventHub) Publish(
	eventType string,
	data any,
	optBasisID, optSnapshotID, optGenID *string,
) *semantic.EventEnvelope {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.headSeq++
	eventID := fmt.Sprintf("event-%d", h.headSeq)
	now := time.Now().UTC()

	env := &semantic.EventEnvelope{
		SchemaID:                   "https://codeflow.local/schemas/event-envelope.schema.json",
		SchemaVersion:              1,
		StreamID:                   h.streamID,
		Sequence:                   h.headSeq,
		EventID:                    eventID,
		EventType:                  eventType,
		OccurredAt:                 now,
		ComputedBasisID:            optBasisID,
		ValidatedAgainstSnapshotID: optSnapshotID,
		GenerationID:               optGenID,
		Data:                       data,
	}

	// Append to ring buffer, maintaining capacity
	if len(h.ringBuffer) >= h.bufferCap {
		h.ringBuffer = append(h.ringBuffer[1:], env)
	} else {
		h.ringBuffer = append(h.ringBuffer, env)
	}

	// Broadcast to active subscribers
	for ch := range h.subscribers {
		select {
		case ch <- env:
		default:
			// Subscriber channel full, skip to avoid blocking other clients
		}
	}

	return env
}

// Subscribe registers a subscriber channel and returns replayed events or snapshot_sync flag.
func (h *EventHub) Subscribe(lastEventID string) (
	ch chan *semantic.EventEnvelope,
	replay []*semantic.EventEnvelope,
	needsSnapshotSync bool,
	cancel func(),
) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch = make(chan *semantic.EventEnvelope, 50)
	h.subscribers[ch] = true

	cancel = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers, ch)
	}

	if lastEventID == "" {
		return ch, nil, false, cancel
	}

	// Search ring buffer for lastEventID
	foundIdx := -1
	for i, env := range h.ringBuffer {
		if env.EventID == lastEventID {
			foundIdx = i
			break
		}
	}

	if foundIdx >= 0 {
		// Replay from foundIdx + 1
		replayed := make([]*semantic.EventEnvelope, len(h.ringBuffer)-foundIdx-1)
		copy(replayed, h.ringBuffer[foundIdx+1:])
		return ch, replayed, false, cancel
	}

	// Not found in ring buffer -> client gap exceeded buffer capacity! (SID-C6)
	return ch, nil, true, cancel
}

// FormatSSE formats an EventEnvelope as standard Server-Sent Events text.
func FormatSSE(env *semantic.EventEnvelope) ([]byte, error) {
	dataBytes, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal event envelope: %w", err)
	}
	return []byte(fmt.Sprintf("id: %s\nevent: %s\ndata: %s\n\n", env.EventID, env.EventType, string(dataBytes))), nil
}
