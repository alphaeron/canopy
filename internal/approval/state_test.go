package approval

import (
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
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
		return ApproverTargetState{ApproverTarget: kitfile.ApproverTarget{Target: name, RequiredCount: n}}
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
	want := kitfile.ApproverTargets{{Target: "lead", RequiredCount: 1}, {Target: "team", RequiredCount: 2}}
	if !reflect.DeepEqual(targets, want) || fallback != 1 {
		t.Errorf("RequiredTargets = %v, %d; want %v, 1", targets, fallback, want)
	}
}
