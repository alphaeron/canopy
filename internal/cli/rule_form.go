package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	huh "charm.land/huh/v2"

	"github.com/alphaeron/kit/internal/tui"
)

// ruleFields is the rule editor's field set, as the CLI prompt and the
// TUI's rule form edit it.
type ruleFields struct {
	// KitFile is the KIT file the rule is written to, when the form offered
	// a choice of KIT files.
	KitFile        string
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

// newRuleForm builds the huh form for the rule editor over the fields the
// caller owns. Each field binds directly to that ruleFields value through
// a pointer accessor, so the library writes results in place on submit;
// the caller passes the same pointer runRuleForm or newRuleFormPage reads after
// the form completes. Every accessor is pre-set from the form's current
// values to seed it. The pattern validates that it compiles, the name must
// be non-empty, and the approver list is a free-text field. When kitFiles
// is non-empty the form starts with a KIT file picker bound to f.KitFile
// (which seeds the selection). Esc or the cancel binding aborts the form
// without touching the caller's value.
func newRuleForm(f *ruleFields, kitFiles []string) *huh.Form {
	fields := []huh.Field{}
	if len(kitFiles) > 0 {
		// The target KIT file comes first: it decides where the rule lives.
		opts := make([]huh.Option[string], len(kitFiles))
		for i, k := range kitFiles {
			opts[i] = huh.NewOption(tui.Clean(k), k)
		}
		fields = append(fields, huh.NewSelect[string]().
			Title("KIT file").
			Options(opts...).
			Value(&f.KitFile))
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
	)...))
}

// runRuleForm runs the rule form on the terminal pre-filled from f and
// returns the edited set, or f unchanged if the user cancels. The form's
// validation blocks submission with an empty pattern, an empty name, or a
// pattern that does not compile, so on a nil error the pattern is compiled
// and the name is non-empty. A non-empty kitFiles adds a KIT file picker,
// and the chosen file is returned in KitFile.
func runRuleForm(f ruleFields, kitFiles []string) (ruleFields, error) {
	fields := f
	if fields.Strategy == "" {
		fields.Strategy = "union"
	}
	submitted, err := tui.RunForm(newRuleForm(&fields, kitFiles))
	if err != nil || !submitted {
		return f, err
	}
	return fields, nil
}

// newRuleFormPage is the rule form as a TUI page pre-filled from seed. A
// non-empty kitFiles starts the form with a KIT file picker (seeded from
// seed.KitFile), as in rules add. submit persists the edited fields and
// returns a confirmation message.
func newRuleFormPage(name string, seed ruleFields, kitFiles []string, submit func(ctx context.Context, f ruleFields) (string, error)) tui.Page {
	if seed.Strategy == "" {
		seed.Strategy = "union"
	}
	summary := "new rule"
	if seed.Name != "" {
		summary = "rule " + seed.Name
	}
	fields := &seed
	return tui.NewFormPage(name, summary, newRuleForm(fields, kitFiles), func(ctx context.Context) (string, error) {
		return submit(ctx, *fields)
	})
}
