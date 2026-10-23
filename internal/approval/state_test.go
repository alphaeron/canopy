package approval

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

type statusCarrier struct {
	name   string
	status Status
}

func TestFirstMatchingReturnsFirstMatch(t *testing.T) {
	items := []statusCarrier{
		{"ok", StatusSatisfied},
		{"missing", StatusMissingApprovers},
		{"unresolved", StatusUnresolved},
	}
	got, ok := firstMatching(items, func(sc statusCarrier) bool { return sc.status != StatusSatisfied })
	if !ok || got.name != "missing" {
		t.Errorf("first unsatisfied = %+v (found %v), want the missing element", got, ok)
	}
}

func TestFirstMatchingReturnsZeroWithoutMatch(t *testing.T) {
	items := []statusCarrier{{"ok", StatusSatisfied}}
	got, ok := firstMatching(items, func(sc statusCarrier) bool { return sc.status == StatusUnresolved })
	if ok || got != (statusCarrier{}) {
		t.Errorf("expected zero value and false, got %+v (found %v)", got, ok)
	}
	// An empty slice also yields the zero value.
	if _, ok := firstMatching([]statusCarrier{}, func(sc statusCarrier) bool { return true }); ok {
		t.Errorf("found=true for an empty slice, want false")
	}
}

func TestReviewStateSortedFilesAndRequiredTargets(t *testing.T) {
	target := func(name string, n int) ApproverTargetState {
		return ApproverTargetState{ApproverTarget: rulesfile.ApproverTarget{Target: name, RequiredCount: n}}
	}
	rs := &ReviewState{Files: map[string]FileState{
		"b.go":   {Path: "b.go", Status: StatusSatisfied, Required: []ApproverTargetState{target("team", 1)}},
		"a.go":   {Path: "a.go", Status: StatusSatisfied, Required: []ApproverTargetState{target("team", 2), target("lead", 1)}},
		"err.go": {Path: "err.go", Status: StatusError, UsedFallback: true},
		"m.go":   {Path: "m.go", Status: StatusMissingApprovers},
	}}
	var order []string
	for _, fs := range rs.SortedFiles() {
		order = append(order, fs.Path)
	}
	if want := []string{"err.go", "m.go", "a.go", "b.go"}; !reflect.DeepEqual(order, want) {
		t.Errorf("SortedFiles order = %v, want %v", order, want)
	}
	targets, fallback := rs.RequiredTargets()
	want := rulesfile.ApproverTargets{{Target: "lead", RequiredCount: 1}, {Target: "team", RequiredCount: 2}}
	if !reflect.DeepEqual(targets, want) || fallback != 1 {
		t.Errorf("RequiredTargets = %v, %d; want %v, 1", targets, fallback, want)
	}
}

// TestBuilderKeepsEventOrderAcrossBlocks: the trace keeps every event in
// order however many blocks it spans, and an empty trace is empty, not nil.
func TestBuilderKeepsEventOrderAcrossBlocks(t *testing.T) {
	if got := NewReviewStateBuilder().Build(nil).Events; got == nil || len(got) != 0 {
		t.Errorf("empty trace = %#v, want an empty, non-nil slice", got)
	}
	b := NewReviewStateBuilder()
	n := 3*eventBlock + 3 // past the doubling blocks and into full-size ones
	for i := range n {
		b.AddEvent("sift", strconv.Itoa(i), "", "", StatusDone, "")
	}
	events := b.Build(nil).Events
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
