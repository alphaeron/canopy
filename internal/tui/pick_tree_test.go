package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func pickerFixture() *treePicker {
	return newTreePicker("Which rule?", []PickGroup{
		{Title: "RULES", Choices: []Choice{{Label: "[0] base", Value: "a"}, {Label: "[2] override", Value: "b"}}},
		{Title: "svc/RULES", Choices: []Choice{{Label: "[1] core", Value: "c"}}},
	})
}

func pressKeys(p *treePicker, codes ...rune) {
	for _, c := range codes {
		p.Update(tea.KeyPressMsg{Code: c})
	}
}

// TestTreePickerPicksOnlyChoices pins that the cursor starts on the first
// choice, moves between choices skipping the group rows, stops at the ends,
// and that enter returns the choice under it.
func TestTreePickerPicksOnlyChoices(t *testing.T) {
	p := pickerFixture()
	if view := p.View().Content; !strings.Contains(view, "svc/RULES") || !strings.Contains(view, "[2] override") {
		t.Errorf("the tree must show every group and choice:\n%s", view)
	}
	pressKeys(p, tea.KeyDown, tea.KeyDown, tea.KeyDown, tea.KeyEnter)
	if p.value == nil || *p.value != "c" {
		t.Fatalf("down past the RULES group must reach svc/RULES's rule and stop there; picked %v", p.value)
	}

	p = pickerFixture()
	pressKeys(p, tea.KeyUp, tea.KeyEnter)
	if p.value == nil || *p.value != "a" {
		t.Errorf("up from the first choice must stay on it; picked %v", p.value)
	}
}

func TestTreePickerCancels(t *testing.T) {
	p := pickerFixture()
	pressKeys(p, tea.KeyEscape)
	if !p.aborted || p.value != nil {
		t.Errorf("esc must cancel without a pick: aborted %v, value %v", p.aborted, p.value)
	}
	if _, err := PickTree("none", []PickGroup{{Title: "RULES"}}); err == nil {
		t.Error("a tree with no choices must be an error, not a prompt")
	}
}
