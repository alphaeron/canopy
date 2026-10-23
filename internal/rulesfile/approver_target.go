package rulesfile

import "encoding/json"

// ApproverTarget is a user or group whose approvals count toward a rule,
// along with how many of its approvals the rule requires.
type ApproverTarget struct {
	Target        string `yaml:"target" json:"target"`
	RequiredCount int    `yaml:"required_count" json:"required_count"`
}

// ApproverTargets is the list of targets a rule requires approval from.
type ApproverTargets []ApproverTarget

// MarshalJSON encodes the targets as a JSON list, empty rather than null
// when there are none.
func (t ApproverTargets) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]ApproverTarget(t))
}
