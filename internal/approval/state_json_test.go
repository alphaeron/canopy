package approval

import (
	"encoding/json"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
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
			{DirectoryPath: "", Active: true, Status: StatusSatisfied,
				Rules:    []RuleState{{Rule: rulesfile.Rule{Pattern: `.*`, Name: "security"}, Approvers: []ApproverTargetState{sec}, Status: StatusSatisfied}},
				Required: RuleTargets{{ApproverTarget: sec.ApproverTarget}}},
			{DirectoryPath: "svc", Active: true, Status: StatusMissingApprovers,
				Rules:    []RuleState{{Rule: rulesfile.Rule{Pattern: `\.go$`}, Approvers: []ApproverTargetState{backend}, Status: StatusMissingApprovers}},
				Required: RuleTargets{{ApproverTarget: sec.ApproverTarget}, {ApproverTarget: backend.ApproverTarget}}},
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
	if js.Overall != "missing_approvers" || len(js.Files) != 1 || js.Errors == nil || js.Events == nil {
		t.Fatalf("json = %s", data)
	}
	f := js.Files[0]
	if len(f.Layers) != 2 || f.Layers[1].RulesFile != rulesfile.PathInDir("svc") || len(f.Layers[1].Requires) != 2 || f.Required[1].Approved != 0 || f.Required[0].ApprovedBy[0] != "org/sec" {
		t.Errorf("file json = %+v", f)
	}
}
