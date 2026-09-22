package kitfile

import (
	"testing"
	"reflect"

	"gopkg.in/yaml.v3"
)

var rule1 = Rule{
	Pattern: ".*\\.go",
	Name: "Core Backend Review",
	IgnoreParentDirs: false,
	CombineStrategy: RuleCombineStrategyUnion,
	CurrentDirectoryOnly: false,
	SelfApprove: true,
	Approvers: []ApproverTarget{
		ApproverTarget{Target: "org/backend-team", RequiredCount: 2},
		ApproverTarget{Target: "org/security-leads", RequiredCount: 1},
	},
}

var rule2 = Rule{
  Pattern: ".*\\.tf",
  Name: "Infrastructure Review",
  IgnoreParentDirs: false,
	CombineStrategy: RuleCombineStrategyIntersect,
  CurrentDirectoryOnly: false,
  SelfApprove: false,
	Approvers: []ApproverTarget{
		ApproverTarget{Target: "org/cloud-team", RequiredCount: 1},
	},
}

func TestRuleParsing(t *testing.T) {
	yamlData := []byte(`
".*\\.go":
  name: "Core Backend Review"
  ignore_parent_dirs: false
  combine_strategy: union
  current_directory_only: false
  self_approve: true
  approvers:
    - target: "org/backend-team"
      required_count: 2
    - target: "org/security-leads"
      required_count: 1
`)

	var actual Rule
	err := yaml.Unmarshal(yamlData, &actual)
	if err != nil {
		t.Errorf("error: %v", err)
	}
	if !reflect.DeepEqual(rule1, actual) {
		t.Errorf("parsed rule does not match expected value")
	}
}

func TestRulesParsing(t *testing.T) {
	expected := []Rule{rule1, rule2}
	yamlData := []byte(`
- ".*\\.go":
  name: "Core Backend Review"
  ignore_parent_dirs: false
  combine_strategy: union
  current_directory_only: false
  self_approve: true
  approvers:
    - target: "org/backend-team"
      required_count: 2
    - target: "org/security-leads"
      required_count: 1
- ".*\\.tf":
  name: "Infrastructure Review"
  ignore_parent_dirs: false
  combine_strategy: intersect
  current_directory_only: false
  self_approve: false
  approvers:
    - target: "org/cloud-team"
      required_count: 1
`)

	var rules Rules
	err := yaml.Unmarshal(yamlData, &rules)
	if err != nil {
		t.Errorf("error: %v", err)
	}

	if len(rules) != len(expected) {
		t.Errorf(`Different number of actual vs. expected rules, (%d vs. %d)`, len(rules), len(expected))
	}

	for i := range rules {
		if !reflect.DeepEqual(rules[i], expected[i]) {
			t.Errorf("parsed rule does not match expected value")
		}
	}
}
