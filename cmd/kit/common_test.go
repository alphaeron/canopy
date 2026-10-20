package main

import (
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/kenin"
	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
	"github.com/alphaeron/kit/internal/tui"
)

func TestKitFilePathFor(t *testing.T) {
	if got := kitFilePathFor(""); got != "KIT" {
		t.Errorf("empty flag should default to KIT, got %q", got)
	}
	if got := kitFilePathFor("sub/KIT"); got != "sub/KIT" {
		t.Errorf("explicit path should pass through, got %q", got)
	}
}

func TestApproverTextTable(t *testing.T) {
	cases := []struct {
		name    string
		pairs   []string
		want    string
		wantErr bool
	}{
		{"none", nil, "", false},
		{"one pair", []string{"team-a", "2"}, "team-a\t2", false},
		{"two pairs", []string{"a", "1", "b", "3"}, "a\t1\nb\t3", false},
		{"odd count", []string{"a"}, "", true},
		{"bad count", []string{"a", "abc"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := approverText(c.pairs)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestBuildRuleFromFieldsTable(t *testing.T) {
	cases := []struct {
		name    string
		fields  tui.RuleFields
		wantErr bool
	}{
		{
			"defaults to union with no approvers",
			tui.RuleFields{Pattern: `.*\.go`, Name: "core"},
			false,
		},
		{
			"parses approver text",
			tui.RuleFields{Pattern: `.*`, Name: "n", ApproverText: "team 2"},
			false,
		},
		{
			"unknown strategy name",
			tui.RuleFields{Pattern: `.*`, Name: "n", Strategy: "bogus"},
			true,
		},
		{
			"approver text without a count",
			tui.RuleFields{Pattern: `.*`, Name: "n", ApproverText: "team"},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rule, err := buildRuleFromFields(c.fields)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", rule)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rule.Pattern != c.fields.Pattern || rule.Name != c.fields.Name {
				t.Errorf("rule did not carry pattern/name: %+v", rule)
			}
		})
	}
}

func TestBuildRuleFromFieldsRoundTripApprovers(t *testing.T) {
	targets := kitfile.ApproverTargets{{Target: "a", RequiredCount: 1}, {Target: "b", RequiredCount: 4}}
	fields := tui.RuleFields{Pattern: `.*`, Name: "n", ApproverText: kitfile.ApproversToText(targets)}
	rule, err := buildRuleFromFields(fields)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !reflect.DeepEqual(rule.Approvers, targets) {
		t.Errorf("approvers mismatch: %+v", rule.Approvers)
	}
}

func TestScopeTitles(t *testing.T) {
	if got := scopeTitles(nil); got != "none" {
		t.Errorf("empty set should read 'none', got %q", got)
	}
	s := []kenin.Scope{{Name: "core"}, {Name: "sec"}}
	if got := scopeTitles(s); got != "core sec" {
		t.Errorf("got %q, want 'core sec'", got)
	}
}

func TestFilterCoverageByScope(t *testing.T) {
	report := &kenin.CoverageReport{
		Universe: 10,
		Covered:  7,
		Percent:  70.0,
		PerScope: []kenin.ScopeCoverage{
			{Scope: kenin.Scope{Name: "core"}, Files: 5, Percent: 50},
			{Scope: kenin.Scope{Name: "sec"}, Files: 2, Percent: 20},
		},
		Uncovered: []string{"x.go"},
	}
	filtered := filterCoverageByScope(report, "sec")
	if len(filtered.PerScope) != 1 || filtered.PerScope[0].Scope.Name != "sec" {
		t.Fatalf("did not filter to the named scope: %+v", filtered.PerScope)
	}
	// Overall universe/covered numbers are preserved by the projection.
	if filtered.Universe != report.Universe || filtered.Covered != report.Covered || filtered.Percent != report.Percent {
		t.Errorf("projection changed overall numbers: %+v", filtered)
	}
	if !reflect.DeepEqual(filtered.Uncovered, report.Uncovered) {
		t.Errorf("projection changed uncovered list: %v", filtered.Uncovered)
	}
}

func TestScopesByRuleShapes(t *testing.T) {
	chain := shonin.RuleChain{FileChains: map[string][]shonin.RuleLayer{
		"s/one.go": {{DirectoryPath: "s", Rules: []shonin.Rule{{Rule: kitfile.Rule{Pattern: `.*\.go`, Name: "core"}}}}},
		"two.go":   nil, // a file with no matching scope is absent from FileChains
	}}
	got := scopesByRule(chain)
	key := `.*\.go in s/KIT (core)`
	if !reflect.DeepEqual(got[key], []string{"s/one.go"}) {
		t.Errorf("expected %s to cover s/one.go only; got %v", key, got)
	}
	if len(got) != 1 {
		t.Errorf("expected one scope key, got %d: %v", len(got), got)
	}
}
