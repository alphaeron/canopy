package tui

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// TestStandaloneKeyMapEscCancels pins that the CLI's prompts (RunForm,
// Pick) cancel on esc like the TUI's forms; huh's default key map quits on
// ctrl+c only, so esc used to do nothing.
func TestStandaloneKeyMapEscCancels(t *testing.T) {
	quit := standaloneKeyMap().Quit
	for _, msg := range []tea.KeyPressMsg{
		{Code: tea.KeyEscape},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		if !key.Matches(msg, quit) {
			t.Errorf("%q must cancel a standalone prompt", msg.String())
		}
	}
}
