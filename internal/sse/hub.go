package sse

import (
	"sync"
)

// Event represents a server-sent event.
type Event struct {
	Type string // e.g. "progress", "token_ready"
	Data string // JSON payload
}

// Hub is an in-memory pub/sub hub for SSE events.
type Hub struct {
	mu      sync.Mutex
	clients map[string]map[chan Event]struct{}
}

// New creates a new SSE Hub.
func New() *Hub {
	return &Hub{
		clients: make(map[string]map[chan Event]struct{}),
	}
}

// Subscribe registers a listener on the given topic.
// Returns a receive-only channel and an unsubscribe function.
func (h *Hub) Subscribe(topic string) (<-chan Event, func()) {
	ch := make(chan Event, 16)

	h.mu.Lock()
	if h.clients[topic] == nil {
		h.clients[topic] = make(map[chan Event]struct{})
	}
	h.clients[topic][ch] = struct{}{}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		delete(h.clients[topic], ch)
		if len(h.clients[topic]) == 0 {
			delete(h.clients, topic)
		}
		h.mu.Unlock()
		// drain channel to prevent goroutine leaks
		for range ch {
		}
	}

	return ch, unsub
}

// Publish sends an event to all subscribers on the given topic.
// Non-blocking: slow clients are skipped.
func (h *Hub) Publish(topic string, event Event) {
	h.mu.Lock()
	subs := h.clients[topic]
	// Copy the set under lock to avoid holding it during sends
	channels := make([]chan Event, 0, len(subs))
	for ch := range subs {
		channels = append(channels, ch)
	}
	h.mu.Unlock()

	for _, ch := range channels {
		select {
		case ch <- event:
		default:
			// skip slow client
		}
	}
}
