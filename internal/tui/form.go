package tui

import (
	"errors"

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

// Choice is one option of PickTree: the label shown and the value returned.
type Choice struct {
	Label string
	Value string
}

// standaloneKeyMap is huh's key map with esc added to quit, so the CLI's
// prompts cancel the same way the TUI's forms do.
func standaloneKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"), key.WithHelp("esc", "cancel"))
	return km
}
