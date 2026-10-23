package rulesedit

import (
	"errors"
	"slices"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// TestAddAllowsRepeatedPatterns pins that add appends a pattern the rules
// already declare (a layer combines its rules in order) and rejects only an
// invalid one.
func TestAddAllowsRepeatedPatterns(t *testing.T) {
	rules, err := add(nil, fixtureRules[0])
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if rules, err = add(rules, fixtureRules[0]); err != nil || len(rules) != 2 {
		t.Errorf("adding the same pattern again = %+v, %v; want it appended", rules, err)
	}
	if _, err := add(rules, rulesfile.Rule{Pattern: "("}); err == nil {
		t.Error("add accepted the invalid pattern \"(\"")
	}
}

// TestReplaceAndRemoveByLocation pins that a rule is changed by its
// position, and that a position no longer holding the target's pattern is
// ErrRulesFileChanged rather than a change to another rule.
func TestReplaceAndRemoveByLocation(t *testing.T) {
	rules := rulesfile.Rules{{Pattern: "x", Name: "base"}, {Pattern: "x", Name: "override", Position: 1}}
	second := rules[1]
	renamed := second
	renamed.Name = "renamed"
	rules, err := replace(slices.Clone(rules), second, renamed)
	if err != nil || rules[0].Name != "base" || rules[1].Name != "renamed" {
		t.Fatalf("replace = %+v, %v; want only the second rule renamed", rules, err)
	}
	if _, err := remove(slices.Clone(rules), second); !errors.Is(err, ErrRulesFileChanged) {
		t.Errorf("remove of the rule as it was before the rename error = %v, want ErrRulesFileChanged", err)
	}
	renamed.Position = 1
	if rules, err = remove(rules, renamed); err != nil || len(rules) != 1 || rules[0].Name != "base" {
		t.Fatalf("remove = %+v, %v; want only the first rule left", rules, err)
	}
	if _, err := remove(rules, renamed); !errors.Is(err, ErrRulesFileChanged) {
		t.Errorf("remove past the end error = %v, want ErrRulesFileChanged", err)
	}
	moved := rulesfile.Rule{Pattern: "y", Position: 0}
	if _, err := replace(rules, moved, renamed); !errors.Is(err, ErrRulesFileChanged) {
		t.Errorf("replace of a changed position error = %v, want ErrRulesFileChanged", err)
	}
}

func TestDirPattern(t *testing.T) {
	for dir, want := range map[string]string{".": "", "": "", "svc/a.b": `^svc/a\.b/`} {
		if got := DirPattern(dir); got != want {
			t.Errorf("DirPattern(%q) = %q, want %q", dir, got, want)
		}
	}
}

// TestChangedPositionWithTheSamePattern pins that a rule is matched as a
// whole, not by pattern: after the rule before it is removed, another rule
// with the same pattern at the old position is not the target.
func TestChangedPositionWithTheSamePattern(t *testing.T) {
	rules := rulesfile.Rules{{Pattern: "x", Name: "base"}, {Pattern: "x", Name: "override", Position: 1}, {Pattern: "x", Name: "extra", Position: 2}}
	override := rules[1]
	shifted := slices.Delete(slices.Clone(rules), 0, 1) // someone removed base
	if _, err := remove(shifted, override); !errors.Is(err, ErrRulesFileChanged) {
		t.Errorf("remove of a rule whose position now holds another rule with its pattern = %v, want ErrRulesFileChanged", err)
	}
}
