package rulesfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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

// writeFixture saves rules to path as the dynamic-key sequence form used on
// disk, then reloads it through the public Load path.
func writeFixture(t *testing.T, dir string, rules Rules) *File {
	t.Helper()
	path := filepath.Join(dir, FileName)
	kf := &File{Rules: rules}
	if err := kf.Save(path); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return loaded
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

func TestRulesFileSaveThenLoadRoundTrips(t *testing.T) {
	got := writeFixture(t, t.TempDir(), fixtureRules)
	if len(got.Rules) != len(fixtureRules) {
		t.Fatalf("rule count = %d, want %d", len(got.Rules), len(fixtureRules))
	}
	for i, want := range fixtureRules {
		if got.Rules[i].Pattern != want.Pattern || got.Rules[i].Name != want.Name {
			t.Errorf("rule %d = (%q, %q), want (%q, %q)",
				i, got.Rules[i].Pattern, got.Rules[i].Name, want.Pattern, want.Name)
		}
	}
}

func TestRulesFileLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected wrap of os.ErrNotExist, got %v", err)
	}
}

func TestRulesFileRuleByPattern(t *testing.T) {
	kf := writeFixture(t, t.TempDir(), fixtureRules)
	if _, ok := kf.RuleByPattern(".*\\.tf$"); !ok {
		t.Errorf("RuleByPattern: expected to find .*\\.tf$")
	}
	if _, ok := kf.RuleByPattern("nope"); ok {
		t.Errorf("RuleByPattern: did not expect to find 'nope'")
	}
}

func TestRulesFileAddRejectsDuplicatePattern(t *testing.T) {
	var kf File
	if err := kf.Add(fixtureRules[0]); err != nil {
		t.Fatalf("first Add to empty RulesFile: %v", err)
	}
	if err := kf.Add(fixtureRules[0]); err == nil {
		t.Errorf("Add of the same pattern again must fail with a duplicate error")
	}
}
func TestRulesFileAddRejectsInvalidRegex(t *testing.T) {
	var kf File
	if err := kf.Add(Rule{Pattern: "("}); err == nil {
		t.Errorf("Add accepted invalid pattern \"(\"")
	}
}
func TestRulesFileEditMutatesOnlyTargetRule(t *testing.T) {
	kf := writeFixture(t, t.TempDir(), fixtureRules)
	if err := kf.Edit(".*\\.go$", func(r *Rule) { r.Name = "renamed" }); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if kf.Rules[0].Name != "renamed" {
		t.Errorf("Edit did not apply, name = %q", kf.Rules[0].Name)
	}
	if kf.Rules[1].Name != "infra review" {
		t.Errorf("Edit mutated the wrong rule, name = %q", kf.Rules[1].Name)
	}
}

func TestRulesFileEditMissingPattern(t *testing.T) {
	kf := writeFixture(t, t.TempDir(), fixtureRules)
	err := kf.Edit("never", func(_ *Rule) {})
	if !errors.Is(err, ErrPatternNotFound) {
		t.Errorf("expected ErrPatternNotFound, got %v", err)
	}
}

func TestRulesFileRemove(t *testing.T) {
	kf := writeFixture(t, t.TempDir(), fixtureRules)
	if err := kf.Remove(".*\\.go$"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := kf.RuleByPattern(".*\\.go$"); ok {
		t.Errorf("pattern still present after Remove")
	}
	if len(kf.Rules) != 1 {
		t.Errorf("rule count = %d, want 1", len(kf.Rules))
	}
	if err := kf.Remove(".*\\.go$"); !errors.Is(err, ErrPatternNotFound) {
		t.Errorf("second Remove should wrap ErrPatternNotFound, got %v", err)
	}
}

func TestRulesFileViolationsDetectsDuplicateAndBrokenPattern(t *testing.T) {
	dup := Rules{{Pattern: ".*.go$"}, {Pattern: ".*.go$"}}
	if errs := (&File{Rules: dup}).Violations(); len(errs) == 0 {
		t.Errorf("Violations missed duplicate pattern")
	}
	bad := Rules{{Pattern: "("}}
	if errs := (&File{Rules: bad}).Violations(); len(errs) == 0 {
		t.Errorf("Violations missed invalid pattern")
	}
	if errs := (&File{Rules: fixtureRules}).Violations(); len(errs) != 0 {
		t.Errorf("Violations over a clean file: %v", errs)
	}
}

func TestRulesFileViolationsRejectsMissingApproverFields(t *testing.T) {
	fixture := Rules{{Pattern: "x", Approvers: []ApproverTarget{{}}}}
	if errs := (&File{Rules: fixture}).Violations(); len(errs) == 0 {
		t.Errorf("Violations missed empty approver")
	}
}

// TestIsRulesFile pins whole-name matching: the GitHub fetcher used to match
// any path containing FileName, so look-alike files were parsed as rules.
func TestIsRulesFile(t *testing.T) {
	tests := map[string]bool{
		FileName:                           true,
		PathInDir("cmd/app"):               true,
		FileName + "CHEN.md":               false,
		"docs/" + FileName + "CHEN.md":     false,
		FileName + "/readme.md":            false,
		PathInDir("svc") + ".bak":          false,
		"svc/" + strings.ToLower(FileName): false,
		"svc/MY" + FileName:                false,
	}
	for p, want := range tests {
		if got := IsRulesFile(p); got != want {
			t.Errorf("IsRulesFile(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestPathInDir(t *testing.T) {
	for dir, want := range map[string]string{
		"":        FileName,
		".":       FileName,
		"src":     PathInDir("src"),
		"src/api": PathInDir("src/api"),
	} {
		if got := PathInDir(dir); got != want {
			t.Errorf("PathInDir(%q) = %q, want %q", dir, got, want)
		}
	}
}
