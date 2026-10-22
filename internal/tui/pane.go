package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Pane geometry: one border cell plus one padding cell on each side, and one
// border row top and bottom.
const (
	paneFrameWidth  = 4
	paneFrameHeight = 2
)

// pane draws content inside a rounded border exactly w cells wide and h rows
// tall. The title is embedded in the top rule and info is right-aligned in
// the bottom rule (pug's border-text convention); either may be empty. The
// border takes the accent color when the pane is focused. Content is
// clipped, never wrapped, so the box geometry is exact regardless of what
// the caller renders inside it.
func pane(st *styles, title, info, content string, w, h int, focused bool) string {
	if w < paneFrameWidth || h < paneFrameHeight {
		return strings.Join(fitLines("", w, h), "\n")
	}
	b := lipgloss.RoundedBorder()
	rule := lipgloss.NewStyle().Foreground(st.paneBorderColor(focused))
	titleStyle := st.paneTitle
	if focused {
		titleStyle = st.paneTitleFocused
	}

	var out strings.Builder
	out.WriteString(borderRule(rule, b.TopLeft, b.Top, b.TopRight, title, titleStyle, "", st.paneInfo, w))
	side := rule.Render(b.Left)
	for _, line := range fitLines(content, w-paneFrameWidth, h-paneFrameHeight) {
		out.WriteString("\n" + side + " " + line + " " + rule.Render(b.Right))
	}
	out.WriteString("\n" + borderRule(rule, b.BottomLeft, b.Bottom, b.BottomRight, "", titleStyle, info, st.paneInfo, w))
	return out.String()
}

// borderRule renders one horizontal border row of width w with optional text
// embedded after the left corner and before the right corner, e.g.
// "╭─ Files ─────────── 3/12 ─╮". Text that does not fit is truncated so the
// row is always exactly w cells.
func borderRule(rule lipgloss.Style, left, fill, right, leftText string, leftStyle lipgloss.Style, rightText string, rightStyle lipgloss.Style, w int) string {
	// One fill cell always separates the corners from embedded text.
	const lead, trail = 1, 1
	avail := max(0, w-2-lead-trail)
	pad := func(s string) string {
		if s == "" {
			return ""
		}
		return " " + s + " "
	}
	r := pad(rightText)
	if ansi.StringWidth(r) > avail {
		r = ""
	}
	l := pad(leftText)
	if room := avail - ansi.StringWidth(r); ansi.StringWidth(l) > room {
		// Truncate the text itself, keeping the trailing space; text that
		// cannot show at least one cell is dropped.
		if room < 3 {
			l = ""
		} else {
			l = ansi.Truncate(l, room-1, "…") + " "
		}
	}
	middle := avail - ansi.StringWidth(l) - ansi.StringWidth(r)

	var sb strings.Builder
	sb.WriteString(rule.Render(left + strings.Repeat(fill, lead)))
	sb.WriteString(leftStyle.Render(l))
	sb.WriteString(rule.Render(strings.Repeat(fill, middle)))
	sb.WriteString(rightStyle.Render(r))
	sb.WriteString(rule.Render(strings.Repeat(fill, trail) + right))
	return sb.String()
}

// fitLines clips or pads s to exactly h lines of exactly w cells. It is
// ANSI-aware: styled text is truncated with x/ansi rather than at byte
// offsets, so escape sequences are never split.
func fitLines(s string, w, h int) []string {
	if h <= 0 {
		return nil
	}
	w = max(0, w)
	src := strings.Split(s, "\n")
	out := make([]string, h)
	for i := range out {
		var line string
		if i < len(src) {
			line = ansi.Truncate(src[i], w, "…")
		}
		if gap := w - ansi.StringWidth(line); gap > 0 {
			line += strings.Repeat(" ", gap)
		}
		out[i] = line
	}
	return out
}

// Split thresholds. Below sideBySideMinWidth the detail pane stacks under
// the main pane; below stackedMinHeight there is no room for both and the
// detail pane is hidden. From threeColumnMinWidth, a page whose rows have
// tabs gets a third column: the summary and the selected tab side by side.
const (
	sideBySideMinWidth  = 90
	stackedMinHeight    = 16
	mainPanePercent     = 45
	threeColumnMinWidth = 160
	threeMainPercent    = 30
	threeDetailPercent  = 30
)

// splitLayout is the geometry of a page that pairs a main pane with a
// detail pane, which on very wide terminals is two columns.
type splitLayout struct {
	mainW, mainH     int
	detailW, detailH int
	// auxW is the third column's width, 0 when there is none.
	auxW int
	// sideBySide is true when the panes sit left and right, false when
	// they are stacked.
	sideBySide bool
	// detail reports whether the detail pane is visible at all.
	detail bool
}

// computeSplit divides a w×h body between the main and detail panes,
// mirroring crush's sidebar on wide terminals and pug's stacked preview on
// narrow ones. wantAux asks for the third column when there is room.
func computeSplit(w, h int, showDetail, wantAux bool) splitLayout {
	switch {
	case !showDetail:
		return splitLayout{mainW: w, mainH: h}
	case wantAux && w >= threeColumnMinWidth:
		mw := w * threeMainPercent / 100
		dw := w * threeDetailPercent / 100
		return splitLayout{mainW: mw, mainH: h, detailW: dw, detailH: h, auxW: w - mw - dw, sideBySide: true, detail: true}
	case w >= sideBySideMinWidth:
		mw := w * mainPanePercent / 100
		return splitLayout{mainW: mw, mainH: h, detailW: w - mw, detailH: h, sideBySide: true, detail: true}
	case h >= stackedMinHeight:
		mh := h / 2
		return splitLayout{mainW: w, mainH: mh, detailW: w, detailH: h - mh, detail: true}
	default:
		return splitLayout{mainW: w, mainH: h}
	}
}

// hit reports which pane contains body cell (x, y) and the position relative
// to that pane's content area, which starts inside the border and padding.
// The content position is negative or past the end over the border itself;
// callers bound it against their own content.
func (l splitLayout) hit(x, y int) (area focusArea, cx, cy int, ok bool) {
	content := func(px, py int) (int, int) {
		return x - px - paneFrameWidth/2, y - py - paneFrameHeight/2
	}
	if x >= 0 && y >= 0 && x < l.mainW && y < l.mainH {
		cx, cy = content(0, 0)
		return focusMain, cx, cy, true
	}
	if !l.detail {
		return focusMain, 0, 0, false
	}
	dx, dy := 0, l.mainH
	if l.sideBySide {
		dx, dy = l.mainW, 0
	}
	if x >= dx && y >= dy && x < dx+l.detailW && y < dy+l.detailH {
		cx, cy = content(dx, dy)
		return focusDetail, cx, cy, true
	}
	if ax := dx + l.detailW; l.auxW > 0 && x >= ax && y >= dy && x < ax+l.auxW && y < dy+l.detailH {
		cx, cy = content(ax, dy)
		return focusAux, cx, cy, true
	}
	return focusMain, 0, 0, false
}

// nextFocus is the pane tab moves focus to: main, detail, then the third
// column when there is one.
func (l splitLayout) nextFocus(f focusArea) focusArea {
	switch {
	case !l.detail:
		return focusMain
	case f == focusMain:
		return focusDetail
	case f == focusDetail && l.auxW > 0:
		return focusAux
	}
	return focusMain
}

// wheelDelta is -1 for wheel up, +1 for wheel down, and 0 otherwise.
func wheelDelta(b tea.MouseButton) int {
	switch b {
	case tea.MouseWheelUp:
		return -1
	case tea.MouseWheelDown:
		return 1
	}
	return 0
}

// detailWheelLines is how far one wheel notch scrolls the detail pane.
const detailWheelLines = 3

// join composes the two rendered panes according to the layout.
func (l splitLayout) join(main, detail string) string {
	if !l.detail {
		return main
	}
	if l.sideBySide {
		return lipgloss.JoinHorizontal(lipgloss.Top, main, detail)
	}
	return lipgloss.JoinVertical(lipgloss.Left, main, detail)
}
