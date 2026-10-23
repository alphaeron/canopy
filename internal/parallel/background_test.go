package parallel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestBackgroundDropsCallsBeyondItsLimit: calls run up to the limit, one
// more is dropped rather than queued, and Wait waits for those running.
func TestBackgroundDropsCallsBeyondItsLimit(t *testing.T) {
	b := NewBackground(2, time.Minute)
	release := make(chan struct{})
	var ran atomic.Int32
	for range 2 {
		if !b.Go(func(context.Context) { <-release; ran.Add(1) }) {
			t.Fatal("a call within the limit was dropped")
		}
	}
	if b.Go(func(context.Context) { ran.Add(1) }) {
		t.Error("a call beyond the limit was started")
	}
	close(release)
	b.Wait()
	if got := ran.Load(); got != 2 {
		t.Errorf("calls run = %d, want 2", got)
	}
	if !b.Go(func(context.Context) {}) {
		t.Error("a call after the others returned was dropped")
	}
	b.Wait()
}

// TestBackgroundCallsHaveTheirOwnDeadline: a call's context ends after the
// timeout, whatever the caller's context does.
func TestBackgroundCallsHaveTheirOwnDeadline(t *testing.T) {
	b := NewBackground(1, time.Millisecond)
	var deadline atomic.Bool
	b.Go(func(ctx context.Context) {
		<-ctx.Done()
		_, ok := ctx.Deadline()
		deadline.Store(ok)
	})
	b.Wait()
	if !deadline.Load() {
		t.Error("the call's context had no deadline")
	}
}

func TestNilBackgroundDropsEverything(t *testing.T) {
	var b *Background
	if b.Go(func(context.Context) { t.Error("a nil Background ran a call") }) {
		t.Error("a nil Background reported starting a call")
	}
	b.Wait()
}
