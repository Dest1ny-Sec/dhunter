package stream

import (
	"testing"
	"time"
)

func TestPublishFansOutToAllSubscribers(t *testing.T) {
	h := New()
	ch1, cancel1 := h.Subscribe("run-1", 8)
	ch2, cancel2 := h.Subscribe("run-1", 8)
	defer cancel1()
	defer cancel2()

	if n := h.Publish("run-1", []byte("evt")); n != 2 {
		t.Fatalf("delivered = %d, want 2", n)
	}
	if got := string(<-ch1); got != "evt" {
		t.Fatalf("ch1 = %q", got)
	}
	if got := string(<-ch2); got != "evt" {
		t.Fatalf("ch2 = %q", got)
	}
}

func TestPublishIgnoresUnknownRun(t *testing.T) {
	h := New()
	if n := h.Publish("nope", []byte("x")); n != 0 {
		t.Fatalf("delivered = %d, want 0", n)
	}
}

func TestCancelStopsDeliveryAndClosesChannel(t *testing.T) {
	h := New()
	ch, cancel := h.Subscribe("run-2", 8)
	if h.SubscriberCount("run-2") != 1 {
		t.Fatal("subscriber not registered")
	}
	cancel()
	if h.SubscriberCount("run-2") != 0 {
		t.Fatal("subscriber not unregistered")
	}
	if n := h.Publish("run-2", []byte("x")); n != 0 {
		t.Fatalf("delivered after cancel = %d, want 0", n)
	}
	// Channel is closed, not left dangling.
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
}

func TestSlowSubscriberIsDroppedNotBlocked(t *testing.T) {
	h := New()
	_, cancel := h.Subscribe("run-3", 1) // tiny buffer
	defer cancel()
	h.Publish("run-3", []byte("full")) // fill the buffer via the hub

	done := make(chan struct{})
	go func() {
		// Must return immediately even though the subscriber is full.
		h.Publish("run-3", []byte("drop-me"))
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked on a full subscriber")
	}
}

func TestSubscriberCountPerRun(t *testing.T) {
	h := New()
	_, c1 := h.Subscribe("a", 4)
	_, c2 := h.Subscribe("a", 4)
	_, c3 := h.Subscribe("b", 4)
	defer c1()
	defer c2()
	defer c3()
	if h.SubscriberCount("a") != 2 {
		t.Fatalf("run a count = %d, want 2", h.SubscriberCount("a"))
	}
	if h.SubscriberCount("b") != 1 {
		t.Fatalf("run b count = %d, want 1", h.SubscriberCount("b"))
	}
}
