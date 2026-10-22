package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Detail is the drill-down content shown beside the hovered row of a list or
// tree: a block of labelled fields followed by titled sections. Callers
// describe what to show; the TUI owns how it is styled.
type Detail struct {
	Fields   []Field
	Sections []Section
}

// Field is one "label  value" line in a Detail.
type Field struct {
	Label string
	Value string
}

// Section is a titled block of lines in a Detail. A Diff section colors
// unified-diff lines (additions, deletions, hunk headers).
type Section struct {
	Title string
	Lines []string
	Diff  bool
}

// IsZero reports whether the detail has nothing to show.
func (d Detail) IsZero() bool { return len(d.Fields) == 0 && len(d.Sections) == 0 }

// renderDetail draws d for a pane whose content area is width cells wide.
// Field values wrap under themselves so labels stay aligned; section lines
// are left as-is for the viewport to soft-wrap.
func renderDetail(st *styles, d Detail, width int) string {
	if d.IsZero() {
		return st.empty.Render("No details")
	}
	var blocks []string
	if len(d.Fields) > 0 {
		labelW := 0
		for _, f := range d.Fields {
			labelW = max(labelW, ansi.StringWidth(f.Label))
		}
		valueW := max(1, width-labelW-2)
		var rows []string
		for _, f := range d.Fields {
			label := st.label.Width(labelW + 2).Render(f.Label)
			value := st.value.Width(valueW).Render(orDash(f.Value))
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, label, value))
		}
		blocks = append(blocks, strings.Join(rows, "\n"))
	}
	for _, s := range d.Sections {
		var b strings.Builder
		b.WriteString(st.sectionTitle.Render(s.Title))
		if len(s.Lines) == 0 {
			b.WriteString("\n" + st.empty.Render("none"))
		}
		for _, line := range s.Lines {
			b.WriteString("\n")
			if s.Diff {
				b.WriteString(styleDiffLine(st, line))
			} else {
				b.WriteString(st.value.Render(line))
			}
		}
		blocks = append(blocks, b.String())
	}
	return strings.Join(blocks, "\n\n")
}

func styleDiffLine(st *styles, line string) string {
	switch {
	case strings.HasPrefix(line, "@@"):
		return st.diffHunk.Render(line)
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return st.diffAdd.Render(line)
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return st.diffDel.Render(line)
	default:
		return st.value.Render(line)
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// detailPane is the scrollable right (or lower) pane that previews the
// hovered row. It follows the cursor of its owning page and takes keys only
// when focused, at which point the viewport scrolls it.
type detailPane struct {
	st     *styles
	vp     viewport.Model
	key    string
	title  string
	detail Detail
	w, h   int
}

func newDetailPane(st *styles) detailPane {
	vp := viewport.New()
	vp.SoftWrap = true
	return detailPane{st: st, vp: vp, key: "\x00"}
}

// show replaces the pane's content with the row identified by key and
// scrolls back to the top. Re-showing the same key is a no-op, so pages can
// call show after every update to follow the cursor without re-rendering or
// resetting the scroll position.
func (d *detailPane) show(key, title string, detail Detail) {
	if key == d.key {
		return
	}
	d.key = key
	d.title = title
	d.detail = detail
	d.refresh()
	d.vp.GotoTop()
}

func (d *detailPane) setSize(w, h int) {
	d.w, d.h = w, h
	d.vp.SetWidth(max(0, w-paneFrameWidth))
	d.vp.SetHeight(max(0, h-paneFrameHeight))
	d.refresh()
}

func (d *detailPane) setStyles(st *styles) {
	d.st = st
	d.refresh()
}

func (d *detailPane) refresh() {
	d.vp.SetContent(renderDetail(d.st, d.detail, d.vp.Width()))
}

func (d *detailPane) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return cmd
}

func (d *detailPane) view(focused bool) string {
	title := "Details"
	if d.title != "" {
		title = d.title
	}
	info := ""
	if d.vp.TotalLineCount() > d.vp.Height() {
		info = fmt.Sprintf("%3.f%%", d.vp.ScrollPercent()*100)
	}
	return pane(d.st, title, info, d.vp.View(), d.w, d.h, focused)
}
