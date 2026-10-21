package tui

import (
	"errors"
	"fmt"
	"regexp"

	huh "charm.land/huh/v2"
)

// RuleFields is the submitted rule-field set.
type RuleFields struct {
	Pattern        string
	Name           string
	Strategy       string
	Ignore         bool
	CurrentDirOnly bool
	SelfApprove    bool
	ApproverText   string
}

// strategies is the fixed set of matching strategies a rule may use.
var strategies = []string{"union", "override", "intersect"}

// newRuleForm builds the huh form for the rule editor. Each field binds
// directly to a RuleFields value via a pointer accessor, so the library
// writes results in place on submit; every accessor is pre-set from f to
// seed the form. The pattern validates that it compiles, the name must be
// non-empty, and the approver list is a free-text field. Cancel (Esc or the
// cancel binding) aborts the form and RunForm returns the original values.
func newRuleForm(f RuleFields) *huh.Form {
	return huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Pattern").
			Placeholder("pattern (regex)").
			Value(&f.Pattern).
			Validate(func(s string) error {
				if s == "" {
					return errors.New("pattern is required")
				}
				if _, err := regexp.Compile(s); err != nil {
					return fmt.Errorf("invalid pattern regex: %s", s)
				}
				return nil
			}),
		huh.NewInput().
			Title("Name").
			Value(&f.Name).
			Validate(huh.ValidateNotEmpty()),
		huh.NewSelect[string]().
			Title("Strategy").
			Options(huh.NewOptions(strategies...)...).
			Value(&f.Strategy),
		huh.NewConfirm().
			Title("Ignore").
			Value(&f.Ignore),
		huh.NewConfirm().
			Title("Current-dir-only").
			Value(&f.CurrentDirOnly),
		huh.NewConfirm().
			Title("Self-approve").
			Value(&f.SelfApprove),
		huh.NewInput().
			Title("Approvers").
			Placeholder("one per line, tab-separated").
			Value(&f.ApproverText),
	))
}

// RunForm launches the interactive rule form pre-filled from f and blocks
// until the user submits or cancels. It returns the fields as edited — or
// f unchanged if the form was cancelled — and any transport error. A
// validation failure prevents submission, so on a nil error the pattern is
// compiled and the name is non-empty.
func RunForm(f RuleFields) (RuleFields, error) {
	if f.Strategy == "" {
		f.Strategy = "union"
	}
	form := newRuleForm(f)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return f, nil
		}
		return f, err
	}
	return f, nil
}
