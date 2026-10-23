package rulesfile

import (
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

var rule1 = Rule{
	Pattern:              ".*\\.go",
	Name:                 "Core Backend Review",
	IgnoreParentDirs:     false,
	CombineStrategy:      RuleCombineStrategyUnion,
	CurrentDirectoryOnly: false,
	SelfApprove:          true,
	Approvers: []ApproverTarget{
		ApproverTarget{Target: "org/backend-team", RequiredCount: 2},
		ApproverTarget{Target: "org/security-leads", RequiredCount: 1},
	},
}

var rule2 = Rule{
	Pattern:              ".*\\.tf",
	Name:                 "Infrastructure Review",
	IgnoreParentDirs:     false,
	CombineStrategy:      RuleCombineStrategyIntersect,
	CurrentDirectoryOnly: false,
	SelfApprove:          false,
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

func TestRuleMarshalRoundTripsPattern(t *testing.T) {
	rule := fixtureRules[0]
	encoded, err := rule.MarshalYAML()
	if err != nil {
		t.Fatalf("marshal rule: %v", err)
	}
	// MarshalYAML returns an interface{} of yaml.Node; re-encode to text
	// and back through UnmarshalYAML to prove the pattern survives.
	dat, err := yaml.Marshal(encoded)
	if err != nil {
		t.Fatalf("encode yaml: %v", err)
	}
	var decoded Rule
	if err := yaml.Unmarshal(dat, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Pattern != rule.Pattern {
		t.Errorf("pattern = %q, want %q", decoded.Pattern, rule.Pattern)
	}
	if decoded.Name != rule.Name {
		t.Errorf("name = %q, want %q", decoded.Name, rule.Name)
	}
	if decoded.CombineStrategy != rule.CombineStrategy {
		t.Errorf("strategy = %v, want %v", decoded.CombineStrategy, rule.CombineStrategy)
	}
	if len(decoded.Approvers) != 1 || decoded.Approvers[0].Target != "org/backend-team" {
		t.Errorf("approvers = %+v, want one target org/backend-team", decoded.Approvers)
	}
}

// TestRuleKeyIsTheLocation pins that a rule is identified by where it is
// declared: the same pattern twice in one rules file is two rules, and a
// rule's name does not change its key.
func TestRuleKeyIsTheLocation(t *testing.T) {
	first := Rule{RulesFile: PathInDir("svc"), Position: 0, Pattern: "x", Name: "one"}
	again := Rule{RulesFile: PathInDir("svc"), Position: 1, Pattern: "x", Name: "one"}
	renamed := first
	renamed.Name = "two"
	if first.Key() == again.Key() || first.Key() != renamed.Key() {
		t.Errorf("keys: first %v, again %v, renamed %v", first.Key(), again.Key(), renamed.Key())
	}
}

func TestRuleString(t *testing.T) {
	tests := []struct {
		r    Rule
		want string
	}{
		{Rule{RulesFile: FileName, Pattern: `.*`}, `.* in ` + FileName + `[0]`},
		{Rule{RulesFile: PathInDir("svc"), Position: 2, Pattern: `\.go$`, Name: "go"}, `\.go$ in ` + PathInDir("svc") + `[2] (go)`},
		{Rule{Pattern: `.*`, Name: "loose"}, `.* (loose)`},
	}
	for _, tc := range tests {
		if got := tc.r.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}

var fixtureRules = Rules{
	{
		Pattern:         ".*\\.go$",
		Name:            "backend review",
		CombineStrategy: RuleCombineStrategyUnion,
		SelfApprove:     true,
		Approvers: []ApproverTarget{
			{Target: "org/backend-team", RequiredCount: 2},
		},
	},
	{
		Pattern:         ".*\\.tf$",
		Name:            "infra review",
		CombineStrategy: RuleCombineStrategyIntersect,
		Approvers: []ApproverTarget{
			{Target: "org/cloud-team", RequiredCount: 1},
		},
	},
}

// TestRulesValidateAllowsDuplicatesRejectsBrokenPattern pins that a
// repeated pattern is valid (a layer combines its rules in order) while an
// invalid one is not.
func TestRulesValidateAllowsDuplicatesRejectsBrokenPattern(t *testing.T) {
	dup := Rules{{Pattern: ".*.go$"}, {Pattern: ".*.go$", CombineStrategy: RuleCombineStrategyOverride}}
	if errs := dup.Validate(); len(errs) != 0 {
		t.Errorf("a repeated pattern must be valid: %v", errs)
	}
	bad := Rules{{Pattern: "("}}
	if errs := bad.Validate(); len(errs) == 0 {
		t.Errorf("Validate missed invalid pattern")
	}
	if errs := fixtureRules.Validate(); len(errs) != 0 {
		t.Errorf("Validate over a clean file: %v", errs)
	}
}

func TestRulesValidateRejectsMissingApproverFields(t *testing.T) {
	fixture := Rules{{Pattern: "x", Approvers: []ApproverTarget{{}}}}
	if errs := fixture.Validate(); len(errs) == 0 {
		t.Errorf("Validate missed empty approver")
	}
}

// TestValidateNamesTheRule pins that every violation of a rule carries
// its pattern, so callers never parse it out of the message.
func TestValidateNamesTheRule(t *testing.T) {
	rules := Rules{{Pattern: "("}, {Pattern: "ok", Approvers: ApproverTargets{{}}}, {Pattern: "ok"}}
	var got []string
	for _, err := range rules.Validate() {
		v, ok := errors.AsType[*RuleViolation](err)
		if !ok {
			t.Fatalf("violation %v is not a *RuleViolation", err)
		}
		got = append(got, v.Pattern)
	}
	if want := []string{"(", "ok", "ok"}; !reflect.DeepEqual(got, want) {
		t.Errorf("violation patterns = %v, want %v", got, want)
	}
}

func TestApproverTargetRequiredCountValidation(t *testing.T) {
	r := Rule{Pattern: `.*`, Approvers: ApproverTargets{{Target: "a", RequiredCount: 0}}}
	if len(Rules{r}.Validate()) == 0 {
		t.Fatal("expected a required_count validation error")
	}
}
