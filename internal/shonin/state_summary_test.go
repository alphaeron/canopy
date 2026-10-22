package shonin

import (
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
)

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
