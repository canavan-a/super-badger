// Package notify is a tiny fan-out hub for server-originated events that
// need to reach every open /notifications/ws connection (see server/api/ws.go)
// from outside the API package — specifically the metrics poller's threshold
// notifications (server/metrics), which have no other way to reach a
// WebSocket handler living in a different package.
package notify

import "sync"

type Msg map[string]any

type Hub struct {
	mu   sync.Mutex
	subs map[chan Msg]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[chan Msg]struct{}{}}
}

func (h *Hub) Subscribe() (<-chan Msg, func()) {
	ch := make(chan Msg, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, unsubscribe
}

func (h *Hub) Broadcast(m Msg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
}
