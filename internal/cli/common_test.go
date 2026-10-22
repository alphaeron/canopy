package cli

import (
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/match"
)

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
		fields  ruleFields
		wantErr bool
	}{
		{
			"defaults to union with no approvers",
			ruleFields{Pattern: `.*\.go`, Name: "core"},
			false,
		},
		{
			"parses approver text",
			ruleFields{Pattern: `.*`, Name: "n", ApproverText: "team 2"},
			false,
		},
		{
			"unknown strategy name",
			ruleFields{Pattern: `.*`, Name: "n", Strategy: "bogus"},
			true,
		},
		{
			"approver text without a count",
			ruleFields{Pattern: `.*`, Name: "n", ApproverText: "team"},
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
	fields := ruleFields{Pattern: `.*`, Name: "n", ApproverText: kitfile.ApproversToText(targets)}
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
	s := []match.Scope{{Name: "core"}, {Name: "sec"}}
	if got := scopeTitles(s); got != "core sec" {
		t.Errorf("got %q, want 'core sec'", got)
	}
}
