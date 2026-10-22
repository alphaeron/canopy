package tui

import (
	"errors"
	"fmt"

	"charm.land/bubbles/v2/key"
	huh "charm.land/huh/v2"
)

// RunForm runs form on the terminal until the user submits or cancels,
// with esc cancelling as it does in the TUI's forms. It reports whether the
// form was submitted; after a cancel the values bound to the form must be
// ignored.
func RunForm(form *huh.Form) (bool, error) {
	err := form.WithKeyMap(standaloneKeyMap()).Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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
// the TUI resolves by row, such as which rules file holds the rule to edit.
func Pick(title string, choices []Choice) (string, error) {
	var value string
	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(Clean(c.Label), c.Value)
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title(Clean(title)).Options(opts...).Value(&value),
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
