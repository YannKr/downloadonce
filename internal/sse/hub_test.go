package sse

import (
	"sync"
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

// Publish copies the subscriber set under the lock and sends after releasing
// it, so there is a window where a channel can leave the map while a send to it
// is already in flight. Closing the channel in unsub would turn that window
// into a panic. The test above cannot see it, because unsubscribing first means
// Publish finds no subscriber and never sends at all. This one runs the two
// concurrently so the send and the unsubscribe overlap.
//
// A send on a closed channel is always ready in a select, so it is chosen over
// the default branch and panics. A panic in any of these goroutines takes the
// test binary down with it.
func TestPublishRacingUnsubscribeDoesNotPanic(t *testing.T) {
	const (
		rounds      = 200
		subscribers = 8
		publishers  = 4
		perSender   = 50
	)

	for i := 0; i < rounds; i++ {
		h := New()

		unsubs := make([]func(), 0, subscribers)
		for j := 0; j < subscribers; j++ {
			_, unsub := h.Subscribe("campaign:1")
			unsubs = append(unsubs, unsub)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})

		for p := 0; p < publishers; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := 0; k < perSender; k++ {
					h.Publish("campaign:1", Event{Type: "progress", Data: "{}"})
				}
			}()
		}
		for _, unsub := range unsubs {
			wg.Add(1)
			go func(unsub func()) {
				defer wg.Done()
				<-start
				unsub()
			}(unsub)
		}

		close(start)
		wg.Wait()
	}
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
