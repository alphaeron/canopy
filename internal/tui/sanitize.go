package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// tabWidth is the number of spaces a tab expands to; a raw tab would break
// the cell-exact layout of panes.
const tabWidth = 4

// clean makes caller text safe to draw. Everything the TUI shows from a
// repository (paths, rule names, diffs) is untrusted: an escape sequence in
// a file could retitle the terminal, write the clipboard, or corrupt the
// screen. clean strips escape sequences and control characters, keeping
// newlines and expanding tabs, so only the TUI's own styling reaches the
// terminal.
func clean(s string) string {
	s = ansi.Strip(s)
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(strings.Repeat(" ", tabWidth))
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func cleanItem(it Item) Item {
	it.Title = clean(it.Title)
	it.Description = clean(it.Description)
	it.Detail = cleanDetail(it.Detail)
	return it
}

func cleanDetail(d Detail) Detail {
	tabs := make([]Tab, len(d.Tabs))
	for i, t := range d.Tabs {
		tabs[i] = Tab{Title: clean(t.Title), Fields: cleanFields(t.Fields), Sections: cleanSections(t.Sections)}
	}
	return Detail{Fields: cleanFields(d.Fields), Sections: cleanSections(d.Sections), Tabs: tabs}
}

func cleanFields(in []Field) []Field {
	fields := make([]Field, len(in))
	for i, f := range in {
		fields[i] = Field{Label: clean(f.Label), Value: clean(f.Value), Badge: f.Badge}
	}
	return fields
}

func cleanSections(in []Section) []Section {
	sections := make([]Section, len(in))
	for i, s := range in {
		lines := make([]string, len(s.Lines))
		for j, l := range s.Lines {
			lines[j] = clean(l)
		}
		rows := make([]Row, len(s.Rows))
		for j, r := range s.Rows {
			r.Text, r.Note = clean(r.Text), clean(r.Note)
			rows[j] = r
		}
		sections[i] = Section{Title: clean(s.Title), Lines: lines, Diff: s.Diff, Rows: rows}
	}
	return sections
}
