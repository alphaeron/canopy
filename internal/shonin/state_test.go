package shonin

import "testing"

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
