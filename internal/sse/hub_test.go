package sse

import (
	"testing"
	"time"
)

// unsub used to drain a channel that nothing ever closes, so it blocked for
// good. Every SSE disconnect parked a handler goroutine permanently.
func TestUnsubscribeReturns(t *testing.T) {
	h := New()
	_, unsub := h.Subscribe("campaign:1")

	done := make(chan struct{})
	go func() {
		unsub()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("unsubscribe did not return within 2s")
	}
}

func TestUnsubscribeReturnsWithBufferedEvents(t *testing.T) {
	h := New()
	_, unsub := h.Subscribe("campaign:1")
	for i := 0; i < 5; i++ {
		h.Publish("campaign:1", Event{Type: "progress", Data: "{}"})
	}

	done := make(chan struct{})
	go func() {
		unsub()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("unsubscribe did not return within 2s with events buffered")
	}
}

func TestPublishAfterUnsubscribeDoesNotPanic(t *testing.T) {
	h := New()
	_, unsub := h.Subscribe("campaign:1")
	unsub()
	h.Publish("campaign:1", Event{Type: "progress", Data: "{}"})
}

func TestSubscriberReceivesPublishedEvent(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe("campaign:1")
	defer unsub()

	h.Publish("campaign:1", Event{Type: "token_ready", Data: `{"token_id":"x"}`})
	select {
	case evt := <-ch:
		if evt.Type != "token_ready" {
			t.Fatalf("event type = %q, want token_ready", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber received nothing")
	}
}
