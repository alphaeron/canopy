package approval

import (
	"strconv"
	"testing"
)

// TestBuilderKeepsEventOrderAcrossBlocks: the trace keeps every event in
// order however many blocks it spans, and an empty trace is empty, not nil.
func TestBuilderKeepsEventOrderAcrossBlocks(t *testing.T) {
	if got := NewReviewStateBuilder().Build().Events; got == nil || len(got) != 0 {
		t.Errorf("empty trace = %#v, want an empty, non-nil slice", got)
	}
	b := NewReviewStateBuilder()
	n := 3*eventBlock + 3 // past the doubling blocks and into full-size ones
	for i := range n {
		b.AddEvent("sift", strconv.Itoa(i), "", "", StatusDone, "")
	}
	events := b.Build().Events
	if len(events) != n {
		t.Fatalf("events = %d, want %d", len(events), n)
	}
	for i, e := range events {
		if e.Path != strconv.Itoa(i) {
			t.Fatalf("event %d is %q, want the events in the order added", i, e.Path)
		}
	}
}

// TestEventBlockSize: blocks double up to eventBlock and stay there,
// however many a trace needs.
func TestEventBlockSize(t *testing.T) {
	tests := []struct{ n, want int }{
		{0, firstEventBlock},
		{1, 2 * firstEventBlock},
		{eventBlockDoublings, eventBlock},
		{eventBlockDoublings + 1, eventBlock},
		{100, eventBlock}, // past where an unbounded shift overflows
	}
	for _, tt := range tests {
		if got := eventBlockSize(tt.n); got != tt.want {
			t.Errorf("eventBlockSize(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}
