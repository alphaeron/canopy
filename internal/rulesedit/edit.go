package rulesedit

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// ErrRuleNotFound reports that no rule matches what identifies the rule to
// change. Callers identify it with errors.Is.
var ErrRuleNotFound = errors.New("no such rule")

// ErrRulesFileChanged reports that a rules file no longer holds the rule an
// edit or removal was aimed at, because the file changed since it was
// read. Callers identify it with errors.Is.
var ErrRulesFileChanged = errors.New("rules file changed since it was read; reload and try again")

// add appends rule to rules. A pattern the rules already declare is allowed
// (a layer combines its rules in order); a rule with a violation
// (rulesfile.Rules.Validate) is rejected, so a change never makes valid
// rules invalid.
func add(rules rulesfile.Rules, rule rulesfile.Rule) (rulesfile.Rules, error) {
	if err := check(rule); err != nil {
		return nil, fmt.Errorf("add rule: %w", err)
	}
	return append(rules, rule), nil
}

// check is rule's violations as one error, or nil when it has none.
func check(rule rulesfile.Rule) error {
	return errors.Join(rulesfile.Rules{rule}.Validate()...)
}

// at checks that rules still hold target, unchanged, at target's position:
// a pattern may repeat, so another rule with the same pattern that moved
// into the position is a change too.
func at(rules rulesfile.Rules, target rulesfile.Rule) error {
	if target.Position < 0 || target.Position >= len(rules) || !sameRule(rules[target.Position], target) {
		return fmt.Errorf("rule %q at %s: %w", target.Pattern, target.Location(), ErrRulesFileChanged)
	}
	return nil
}

// sameRule reports whether a and b are the same rule, apart from where
// each was read from. No approvers and an empty approver list are the same
// (a rule saved without approvers reads back with an empty list).
func sameRule(a, b rulesfile.Rule) bool {
	for _, r := range []*rulesfile.Rule{&a, &b} {
		r.RulesFile, r.Position = "", 0
		if len(r.Approvers) == 0 {
			r.Approvers = nil
		}
	}
	return reflect.DeepEqual(a, b)
}

// replace replaces the rule at target's position with rule (see at),
// rejecting a rule with a violation as add does.
func replace(rules rulesfile.Rules, target, rule rulesfile.Rule) (rulesfile.Rules, error) {
	if err := at(rules, target); err != nil {
		return nil, err
	}
	if err := check(rule); err != nil {
		return nil, fmt.Errorf("edit rule: %w", err)
	}
	rules[target.Position] = rule
	return rules, nil
}

// remove deletes the rule at target's position (see at).
func remove(rules rulesfile.Rules, target rulesfile.Rule) (rulesfile.Rules, error) {
	if err := at(rules, target); err != nil {
		return nil, err
	}
	return slices.Delete(rules, target.Position, target.Position+1), nil
}

// DirPattern is a rule pattern matching every file under the
// repository-relative directory dir, or "" for the repository root, whose
// rules already apply to every file.
func DirPattern(dir string) string {
	if dir = path.Clean(dir); dir == "." {
		return ""
	}
	return "^" + regexp.QuoteMeta(dir+"/")
}
