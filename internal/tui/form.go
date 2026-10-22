package tui

import (
	"errors"
	"fmt"
	"regexp"

	"charm.land/bubbles/v2/key"
	huh "charm.land/huh/v2"
)

// RuleFields is the submitted rule-field set.
type RuleFields struct {
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
// caller owns. Each field binds directly to that RuleFields value through
// a pointer accessor, so the library writes results in place on submit;
// the caller passes the same pointer the shell or RunForm will read after
// the form completes. Every accessor is pre-set from the form's current
// values to seed it. The pattern validates that it compiles, the name must
// be non-empty, and the approver list is a free-text field. When kitFiles
// is non-empty the form starts with a KIT file picker bound to f.KitFile
// (which seeds the selection). Esc or the cancel binding aborts the form
// without touching the caller's value.
func newRuleForm(f *RuleFields, kitFiles []string) *huh.Form {
	fields := []huh.Field{}
	if len(kitFiles) > 0 {
		// The target KIT file comes first: it decides where the rule lives.
		opts := make([]huh.Option[string], len(kitFiles))
		for i, k := range kitFiles {
			opts[i] = huh.NewOption(clean(k), k)
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

// RunForm launches the interactive rule form pre-filled from f and blocks
// until the user submits or cancels. Submit writes the edited values
// back through f's pointer and RunForm returns the edited set; cancel
// returns f unchanged and errors nil. The form's validation blocks
// submission with an empty pattern, an empty name, or a pattern that
// does not compile, so on a nil error the pattern is compiled and the
// name is non-empty. A non-empty kitFiles adds a KIT file picker, and the
// chosen file is returned in KitFile.
func RunForm(f RuleFields, kitFiles []string) (RuleFields, error) {
	fields := f
	if fields.Strategy == "" {
		fields.Strategy = "union"
	}
	form := newRuleForm(&fields, kitFiles).WithKeyMap(standaloneKeyMap())
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return f, nil
		}
		return f, err
	}
	return fields, nil
}

// ErrAborted reports that the user cancelled an interactive prompt.
var ErrAborted = errors.New("aborted")

// Choice is one option of Pick: the label shown and the value returned.
type Choice struct {
	Label string
	Value string
}

// Pick asks the user to choose one of choices on the terminal and returns
// its value, or ErrAborted if they cancel. The CLI uses it to resolve what
// the TUI resolves by row, such as which KIT file holds the rule to edit.
func Pick(title string, choices []Choice) (string, error) {
	var value string
	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(clean(c.Label), c.Value)
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title(clean(title)).Options(opts...).Value(&value),
	)).WithKeyMap(standaloneKeyMap()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return "", ErrAborted
	}
	if err != nil {
		return "", fmt.Errorf("pick: %w", err)
	}
	return value, nil
}

// standaloneKeyMap is huh's key map with esc added to quit, so the CLI's
// prompts cancel the same way the TUI's forms do.
func standaloneKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	return km
}
