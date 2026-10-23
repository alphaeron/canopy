package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	huh "charm.land/huh/v2"

	"github.com/alphaeron/canopy/internal/rulesedit"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
)

// newRuleSeed is the rule editor's seed for a new rule, which is a union
// rule unless the user picks another strategy. The editor (the CLI prompt
// and the TUI's rule form) edits a rulesfile.Rule directly; its RulesFile is
// the rules file the rule is written to when the form offers a choice.
func newRuleSeed() rulesfile.Rule {
	return rulesfile.Rule{CombineStrategy: rulesfile.RuleCombineStrategyUnion}
}

// editSeed is the rule editor's seed for editing r. It shows the strategy
// the approval algorithm applies (RuleCombineStrategy.Effective), so
// re-saving a rule whose combine_strategy is omitted or corrupt never
// changes who has to approve.
func editSeed(r rulesfile.Rule) rulesfile.Rule {
	r.CombineStrategy = r.CombineStrategy.Effective()
	return r
}

// approversText lets the form edit approvers as text (see
// rulesedit.ParseApprovers). It keeps the text as typed and updates the
// targets whenever the text parses; the field's validation blocks
// submitting text that does not, so a submitted form's targets match it.
type approversText struct {
	text    string
	targets *rulesfile.ApproverTargets
}

func (a *approversText) Get() string { return a.text }

func (a *approversText) Set(text string) {
	a.text = text
	if t, err := rulesedit.ParseApprovers(text); err == nil {
		*a.targets = t
	}
}

// newRuleForm builds the huh form for the rule editor over the fields the
// caller owns. Each field binds directly to that rule through
// a pointer accessor, so the library writes results in place on submit;
// the caller passes the same pointer runRuleForm or newRuleFormPage reads after
// the form completes. Every accessor is pre-set from the form's current
// values to seed it. The pattern validates that it compiles, the name must
// be non-empty, and the approver list is a free-text field. When rulesFiles
// is non-empty the form starts with a rules file picker bound to f.RulesFile
// (which seeds the selection). A non-empty note, such as brokenNote, heads
// the form so a warning is read in the form rather than printed above it.
// Esc or the cancel binding aborts the form without touching the caller's
// value.
func newRuleForm(f *rulesfile.Rule, rulesFiles []string, note string) *huh.Form {
	strategies := make([]huh.Option[rulesfile.RuleCombineStrategy], 0, 3)
	for _, s := range rulesfile.RuleCombineStrategies() {
		strategies = append(strategies, huh.NewOption(s.String(), s))
	}
	fields := []huh.Field{}
	if note != "" {
		fields = append(fields, huh.NewNote().Title("warning").Description(tui.Clean(note)))
	}
	if len(rulesFiles) > 0 {
		// The target rules file comes first: it decides where the rule lives.
		opts := make([]huh.Option[string], len(rulesFiles))
		for i, k := range rulesFiles {
			opts[i] = huh.NewOption(tui.Clean(k), k)
		}
		fields = append(fields, huh.NewSelect[string]().
			Title("rules file").
			Options(opts...).
			Value(&f.RulesFile))
	}
	return huh.NewForm(huh.NewGroup(append(fields,
		huh.NewInput().
			Title("Pattern").
			Placeholder("pattern (regex)").
			Value(&f.Pattern).
			Validate(func(s string) error {
				if s == "" {
					return errors.New("pattern is required")
				}
				if _, err := regexp.Compile(s); err != nil {
					return fmt.Errorf("invalid pattern regex: %s", err)
				}
				return nil
			}),
		huh.NewInput().
			Title("Name").
			Value(&f.Name).
			Validate(huh.ValidateNotEmpty()),
		huh.NewSelect[rulesfile.RuleCombineStrategy]().
			Title("Strategy").
			Options(strategies...).
			Value(&f.CombineStrategy),
		huh.NewConfirm().
			Title("Ignore").
			Value(&f.IgnoreParentDirs),
		huh.NewConfirm().
			Title("Current-dir-only").
			Value(&f.CurrentDirectoryOnly),
		huh.NewConfirm().
			Title("Self-approve").
			Value(&f.SelfApprove),
		huh.NewInput().
			Title("Approvers").
			Placeholder("one per line: target required_count").
			Accessor(&approversText{text: rulesedit.ApproversToText(f.Approvers), targets: &f.Approvers}).
			Validate(func(s string) error {
				_, err := rulesedit.ParseApprovers(s)
				return err
			}),
	)...))
}

// runRuleForm runs the rule form on the terminal pre-filled from f and
// returns the edited rule, or tui.ErrAborted if the user cancels. The form's
// validation blocks submission with an empty pattern, an empty name, or a
// pattern that does not compile, so on a nil error the pattern is compiled
// and the name is non-empty. A non-empty rulesFiles adds a rules file picker,
// and the chosen file is returned in RulesFile; a non-empty note heads the
// form (see newRuleForm).
func runRuleForm(f rulesfile.Rule, rulesFiles []string, note string) (rulesfile.Rule, error) {
	fields := f
	submitted, err := tui.RunForm(newRuleForm(&fields, rulesFiles, note))
	if err != nil {
		return f, err
	}
	if !submitted {
		return f, tui.ErrAborted
	}
	return fields, nil
}

// newRuleFormPage is the rule form as a TUI page pre-filled from seed. A
// non-empty rulesFiles starts the form with a rules file picker (seeded from
// seed.RulesFile), as in rules add. submit persists the edited fields and
// returns a confirmation message.
func newRuleFormPage(name string, seed rulesfile.Rule, rulesFiles []string, submit func(ctx context.Context, f rulesfile.Rule) (string, error)) tui.Page {
	summary := "new rule"
	if seed.Name != "" {
		summary = "rule " + seed.Name
	}
	fields := &seed
	return tui.NewFormPage(name, summary, newRuleForm(fields, rulesFiles, ""), func(ctx context.Context) (string, error) {
		return submit(ctx, *fields)
	})
}
