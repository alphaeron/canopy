package tui

import "charm.land/lipgloss/v2"

// A shared surface palette. Every view renders through frame, so the
// header strip, the content panel, and the status strip carry one visual
// contract: a bold light-on-dark title strip across the top, a rounded
// bordered panel filling the middle, and a status strip across the bottom
// with faint context on the left and bold hotkeys on the right.
var (
	darkColor   = lipgloss.Color("235") // header strip background
	titleColor  = lipgloss.Color("255") // title on the dark strip
	lightColor  = lipgloss.Color("236") // status strip background
	dimColor    = lipgloss.Color("250") // faint context text on the light strip
	hotkeyColor = lipgloss.Color("248") // bold hotkey text on the light strip
	borderColor = lipgloss.Color("240") // panel rules
)

// frame renders the complete application chrome around content: a header
// strip carrying title, the content inside a rounded panel with one cell of
// vertical and two cells of horizontal padding, and a status strip carrying
// the left context and the right hotkeys. Both strips are lipgloss-clamped
// to width, so they span the terminal edge to edge and no hand-rolled
// indentation is possible on either end. The frame claims exactly height
// rows when height >= 2: header, then panel (border rows included), then
// status. width <= 0 or height < 2 leaves the corresponding constraint
// unset, which fits WindowSizeMsg arriving after the first View call.
// Content rendered into the pane must fit within contentHeight(height).
func frame(title, context, hotkeys string, width, height int, content string) string {
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(titleColor).
		Background(darkColor).
		Width(width).
		Render("  " + title)

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(width).
		Height(panelHeight(height)).
		Render(content)

	if width > 0 {
		// The hotkeys always stay whole: the context is clipped to the
		// remaining cells and then padded. v2 lipgloss wraps overflowing
		// content rather than truncating it, so the clip is what keeps the
		// status strip exactly one line tall.
		available := width - lipgloss.Width(hotkeys) - 2
		if available < 1 {
			available = 1
		}
		left := lipgloss.NewStyle().
			Width(available).
			Foreground(dimColor).Render(clipLine("  "+context, available))
		right := lipgloss.NewStyle().Bold(true).Foreground(hotkeyColor).Render(hotkeys + "  ")
		status := lipgloss.NewStyle().Width(width).Background(lightColor).
			Render(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
		return header + "\n" + panel + "\n" + status
	}
	left := lipgloss.NewStyle().Foreground(dimColor).Render("  " + context)
	right := lipgloss.NewStyle().Bold(true).Foreground(hotkeyColor).Render(hotkeys + "  ")
	return header + "\n" + panel + "\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// panelHeight is the panel's own rows, border rules included: lipgloss's
// Height counts the top and bottom rules in, so it is height-2. Non-positive
// heights leave the constraint unset (0).
func panelHeight(height int) int {
	if height < 2 {
		return 0
	}
	return height - 2
}

// contentHeight is the rows the pane's content may occupy: the terminal
// height minus the two strips, the panel's two border rules, and the
// panel's two padding rows. Non-positive results leave the constraint
// unset (0).
func contentHeight(height int) int {
	return clamped(height, 6)
}

// contentWidth is the pane's content columns: the terminal width minus the
// panel's two border columns and the two padding columns on each side.
func contentWidth(width int) int {
	return clamped(width, 6)
}

// clipLine shortens s to at most width display cells, splitting only on
// rune boundaries. The frame passes plain caller strings (titles and
// contexts) with no escapes, so lipgloss.Width measures them accurately and
// the clip never lands mid-rune.
func clipLine(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	end := 0
	for _, r := range s {
		if lipgloss.Width(s[:end])+lipgloss.Width(string(r)) > width {
			break
		}
		end++
	}
	return s[:end]
}

// clamped returns v-amount when the result is non-negative, otherwise 0,
// which for a bubbles model means "no constraint until a WindowSizeMsg
// arrives".
func clamped(v, amount int) int {
	if v <= amount {
		return 0
	}
	return v - amount
}
