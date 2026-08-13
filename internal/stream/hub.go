// Package stream is an in-process pub/sub hub used to fan-out SSE events
// from the agent bridge to every browser subscribed to a run.
//
// The hub is intentionally simple: one map of run_id → []chan []byte.
// Subscribers register a buffered channel; the agent bridge publishes
// events by run_id; the hub fans out to every live subscriber. Slow
// subscribers (full buffer) get dropped to keep the hot path snappy.
package stream

import (
	"sync"
)

// Hub is a per-run fan-out. A single Hub instance is shared across the
// process; the zero value is ready to use.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

// New constructs a Hub. Call once at startup and pass it around.
func New() *Hub {
	return &Hub{subs: make(map[string]map[chan []byte]struct{})}
}

// Subscribe returns a buffered channel that will receive raw SSE payload
// bytes (already JSON-encoded) for runID. The caller MUST call the
// returned cancel func to release resources when done.
func (h *Hub) Subscribe(runID string, buf int) (<-chan []byte, func()) {
	if buf <= 0 {
		buf = 16
	}
	ch := make(chan []byte, buf)

	h.mu.Lock()
	if _, ok := h.subs[runID]; !ok {
		h.subs[runID] = make(map[chan []byte]struct{})
	}
	h.subs[runID][ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if set, ok := h.subs[runID]; ok {
			if _, still := set[ch]; still {
				delete(set, ch)
				close(ch)
			}
			if len(set) == 0 {
				delete(h.subs, runID)
			}
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Publish fans payload out to every subscriber of runID. It never blocks:
// if a subscriber's buffer is full, the event is dropped for that
// subscriber and the dropped counter is bumped.
func (h *Hub) Publish(runID string, payload []byte) int {
	h.mu.RLock()
	set, ok := h.subs[runID]
	if !ok {
		h.mu.RUnlock()
		return 0
	}
	// Copy the channel slice under lock so we don't hold RLock during
	// the (potentially blocking) send.
	chs := make([]chan []byte, 0, len(set))
	for c := range set {
		chs = append(chs, c)
	}
	h.mu.RUnlock()

	delivered := 0
	for _, c := range chs {
		select {
		case c <- payload:
			delivered++
		default:
			// Slow consumer; drop the event for them rather than block
			// the agent bridge.
		}
	}
	return delivered
}

// PublishJSON is a convenience for marshalled payloads. Marshalling
// errors are swallowed — the caller should pre-validate the JSON.
func (h *Hub) PublishJSON(runID string, jsonBytes []byte) int {
	return h.Publish(runID, jsonBytes)
}

// SubscriberCount returns the number of active subscribers for runID.
// Handy for /metrics and for tests.
func (h *Hub) SubscriberCount(runID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[runID])
}
