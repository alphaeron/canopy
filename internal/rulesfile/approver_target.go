package rulesfile

import (
	"fmt"
)

// ApproverTarget is a user or group whose approvals count toward a rule,
// along with how many of its approvals the rule requires.
type ApproverTarget struct {
	Target        string `yaml:"target"`
	RequiredCount int    `yaml:"required_count"`
}

// ApproverTargets is the list of targets a rule requires approval from.
type ApproverTargets []ApproverTarget

// String returns a debug representation of the target.
func (at ApproverTarget) String() string {
	return fmt.Sprintf("ApproverTarget{Target: \"%v\", RequiredCount: %v}", at.Target, at.RequiredCount)
}
