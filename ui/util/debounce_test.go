package util

import (
	"testing"
	"time"
)

func TestDebouncerRejectsAlreadyQueuedCallback(t *testing.T) {
	queued := make(chan func(), 10)
	calls := 0
	debounce := newDebouncer(10*time.Millisecond, func() { calls++ }, func(f func()) { queued <- f })
	next := func() func() {
		t.Helper()
		select {
		case f := <-queued:
			return f
		case <-time.After(2 * time.Second):
			t.Fatal("timer did not dispatch")
			return nil
		}
	}
	debounce()
	obsolete := next()
	// The old timer has fired, but its UI callback has not run yet.
	debounce()
	obsolete()
	if calls != 0 {
		t.Fatal("obsolete queued callback ran")
	}
	next()()
	if calls != 1 {
		t.Fatalf("got %d calls, want 1", calls)
	}
}

func TestDebouncerCoalescesBurst(t *testing.T) {
	queued := make(chan func(), 100)
	calls := 0
	debounce := newDebouncer(20*time.Millisecond, func() { calls++ }, func(f func()) { queued <- f })
	for range 20 {
		debounce()
	}
	// Even callbacks whose timers raced with Stop must be rejected.
	deadline := time.After(2 * time.Second)
	for calls == 0 {
		select {
		case f := <-queued:
			f()
		case <-deadline:
			t.Fatal("latest timer did not dispatch")
		}
	}
	for {
		select {
		case f := <-queued:
			f()
		default:
			if calls != 1 {
				t.Fatalf("got %d calls, want 1", calls)
			}
			return
		}
	}
}
