package approval

import (
	"encoding/json"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// TestReviewStateMarshalJSON pins the wire form: snake_case statuses,
// layers named by rules file, and lists that are never null.
func TestReviewStateMarshalJSON(t *testing.T) {
	sec := ApproverTargetState{ApproverTarget: rulesfile.ApproverTarget{Target: "org/sec", RequiredCount: 1}, SatisfiedCount: 1, Status: StatusSatisfied, ResolvedBy: []string{"org/sec"}}
	backend := ApproverTargetState{ApproverTarget: rulesfile.ApproverTarget{Target: "org/backend", RequiredCount: 2}, Status: StatusMissingApprovers}
	file := FileState{
		Path:   "svc/a.go",
		Status: StatusMissingApprovers,
		Layers: []LayerState{
			{DirectoryPath: "", Status: StatusSatisfied,
				Rules:    []RuleState{{Rule: rulesfile.Rule{Pattern: `.*`, Name: "security"}, Approvers: []ApproverTargetState{sec}, Status: StatusSatisfied}},
				Required: ExecutableTargets{{ApproverTarget: sec.ApproverTarget}}},
			{DirectoryPath: "svc", Status: StatusMissingApprovers,
				Rules:    []RuleState{{Rule: rulesfile.Rule{Pattern: `\.go$`}, Approvers: []ApproverTargetState{backend}, Status: StatusMissingApprovers}},
				Required: ExecutableTargets{{ApproverTarget: sec.ApproverTarget}, {ApproverTarget: backend.ApproverTarget}}},
		},
		Required: []ApproverTargetState{sec, backend},
	}
	state := &ReviewState{Overall: StatusMissingApprovers, Files: map[string]FileState{file.Path: file}}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var js reviewJSON
	if err := json.Unmarshal(data, &js); err != nil {
		t.Fatalf("Unmarshal %s: %v", data, err)
	}
	if js.Overall != "missing_approvers" || len(js.Files) != 1 || js.Errors == nil || js.Warnings == nil || js.Events == nil || js.Outcome != nil {
		t.Fatalf("json = %s", data)
	}
	f := js.Files[0]
	if len(f.Layers) != 2 || f.Layers[1].RulesFile != rulesfile.PathInDir("svc") || len(f.Layers[1].Requires) != 2 || len(f.Required[1].ApprovedBy) != 0 || f.Required[0].ApprovedBy[0] != "org/sec" {
		t.Errorf("file json = %+v", f)
	}
}

// TestReviewReportMarshalJSON: a report is the review's JSON with its
// outcome, whose lists are never null.
func TestReviewReportMarshalJSON(t *testing.T) {
	state := &ReviewState{Overall: StatusSatisfied, Files: map[string]FileState{}}
	data, err := json.Marshal(ReviewReport{State: state, Outcome: Outcome{Verdict: Failed, Summary: "1 invalid proposed rules file"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var js reviewJSON
	if err := json.Unmarshal(data, &js); err != nil {
		t.Fatalf("Unmarshal %s: %v", data, err)
	}
	if js.Overall != "satisfied" || js.Outcome == nil || js.Outcome.Verdict != Failed || js.Outcome.Items == nil || js.Outcome.Warnings == nil {
		t.Errorf("json = %s", data)
	}
}
