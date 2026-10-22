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
	fields := make([]Field, len(d.Fields))
	for i, f := range d.Fields {
		fields[i] = Field{Label: clean(f.Label), Value: clean(f.Value)}
	}
	sections := make([]Section, len(d.Sections))
	for i, s := range d.Sections {
		lines := make([]string, len(s.Lines))
		for j, l := range s.Lines {
			lines[j] = clean(l)
		}
		sections[i] = Section{Title: clean(s.Title), Lines: lines, Diff: s.Diff}
	}
	return Detail{Fields: fields, Sections: sections}
}
