package approval

import (
	"reflect"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

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

func TestReviewStateCountsAndEvents(t *testing.T) {
	rs := &ReviewState{
		Files: map[string]FileState{
			"a": {Status: StatusSatisfied}, "b": {Status: StatusSatisfied}, "c": {Status: StatusError},
		},
		Events: []Event{{Path: "a", Note: "1"}, {Path: "b"}, {Note: "global"}, {Path: "a", Note: "2"}},
	}
	if got, want := rs.StatusCounts(), map[Status]int{StatusSatisfied: 2, StatusError: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("StatusCounts = %v, want %v", got, want)
	}
	if got := rs.FileEvents("a"); len(got) != 2 || got[0].Note != "1" || got[1].Note != "2" {
		t.Errorf("FileEvents(a) = %+v, want a's two events in order", got)
	}
}
