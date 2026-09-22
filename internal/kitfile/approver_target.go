package kitfile

import (
	"fmt"
)

type ApproverTarget struct {
	Target string `yaml:"target"`
	RequiredCount int `yaml:"required_count"`
}

type ApproverTargets []ApproverTarget

func (at ApproverTarget) String() string {
	return fmt.Sprintf("ApproverTarget{Target: \"%v\", RequiredCount: %v}", at.Target, at.RequiredCount)
}
