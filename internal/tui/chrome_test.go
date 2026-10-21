package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestFrameSpansFullWidth pins the chrome contract both the tree and the
// list rely on: the frame is exactly height rows when sized, the header and
// status strips span the requested width edge to edge, and the hotkeys are
// always whole — a long context is clipped in the left segment rather than
// wrapping the hotkeys past the terminal, which is the regression the old
// hand-indented chrome could not guarantee.
func TestFrameSpansFullWidth(t *testing.T) {
	const width, height = 40, 15
	out := frame("kit · match", strings.Repeat("x", 64), "enter select · q quit", width, height, "content")
	lines := strings.Split(out, "\n")
	if len(lines) != height {
		t.Fatalf("frame must be exactly %d rows, got %d:\n%s", height, len(lines), out)
	}
	if got := lipgloss.Width(lines[0]); got != width {
		t.Errorf("header is %d cells wide, want %d:\n%s", got, width, lines[0])
	}
	if !strings.Contains(lines[0], "kit · match") {
		t.Errorf("header must carry the title:\n%q", lines[0])
	}
	if got := lipgloss.Width(lines[height-1]); got != width {
		t.Errorf("status strip is %d cells wide, want %d:\n%s", got, width, lines[height-1])
	}
	// A 64-cell context against 40 total cells must be clipped: the
	// hotkeys survive whole and nothing wraps to a fourth line.
	if !strings.Contains(lines[height-1], "enter select · q quit") {
		t.Errorf("hotkeys must survive a long context:\n%q", lines[height-1])
	}
	panel := lines[height-2]
	if !strings.Contains(panel, "╰") {
		t.Errorf("the row above the status strip must end the panel's bottom rule:\n%q", panel)
	}
	if !strings.Contains(lines[1], "╭") {
		t.Errorf("the row below the header must open the panel's top rule:\n%q", lines[1])
	}
}

// TestFrameContextStillShrinks reports that a context shorter than the
// available width is not visually stretched into the hotkeys: the status
// strip carries the full hotkey segment and no stray context padding.
func TestFrameContextStillShrinks(t *testing.T) {
	out := frame("title", "few items", "q quit", 40, 8, "x")
	status := strings.Split(out, "\n")[7] // header + 6 panel rows above the strip
	if !strings.Contains(status, "few items") || !strings.Contains(status, "q quit") {
		t.Errorf("status must carry both context and hotkeys:\n%q", status)
	}
}

// TestFrameUnclampedWhenSized reports that a zero width and height leave the
// frame unconstrained (a list or tree built without knowing the terminal
// dimensions must not render width-zero strips until the first WindowSizeMsg
// arrives), while still carrying title, panel and context.
func TestFrameUnclampedWhenSized(t *testing.T) {
	out := frame("title", "ctx", "q quit", 0, 0, "x")
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], "title") {
		t.Errorf("header must carry the title:\n%q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "ctx") || !strings.Contains(lines[len(lines)-1], "q quit") {
		t.Errorf("status row must carry the context and hotkeys:\n%q", lines[len(lines)-1])
	}
	foundContent := false
	for _, l := range lines {
		if strings.Contains(l, "x") {
			foundContent = true
		}
	}
	if !foundContent {
		t.Errorf("the panel row must carry the content in:\n%s", out)
	}
}

// TestPanelHeightAndClamped pins the frame's size helpers, which encode the
// layout the views depend on: the panel's rendered rows are height-2 (its
// two rules included), the pane's content area is 6 cells inside in both
// dimensions (2 strips + 2 rules + 2 padding rows; 2 rules + 4 padding
// columns), and small terminals degrade to an unset constraint rather than
// a negative size.
func TestPanelHeightAndClamped(t *testing.T) {
	if got := panelHeight(15); got != 13 {
		t.Errorf("panelHeight(15) = %d, want 13", got)
	}
	if got := panelHeight(2); got != 0 {
		t.Errorf("panelHeight(2) = %d, want 0", got)
	}
	if got := contentWidth(80); got != 74 {
		t.Errorf("contentWidth(80) = %d, want 74", got)
	}
	if got := contentHeight(40); got != 34 {
		t.Errorf("contentHeight(40) = %d, want 34", got)
	}
	if got := contentWidth(6); got != 0 {
		t.Errorf("contentWidth(6) = %d, want 0", got)
	}
	if got := contentHeight(5); got != 0 {
		t.Errorf("contentHeight(5) = %d, want 0", got)
	}
}
