package tui

import (
	"strings"
	"testing"
)

func TestClean(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{name: "plain", in: "cmd/app/main.go", want: "cmd/app/main.go"},
		{name: "unicode kept", in: "dír/ファイル.go", want: "dír/ファイル.go"},
		{name: "newline kept", in: "a\nb", want: "a\nb"},
		{name: "tab expanded", in: "a\tb", want: "a    b"},
		{name: "color stripped", in: "\x1b[31mred\x1b[0m", want: "red"},
		{name: "title change stripped", in: "x\x1b]0;pwned\x07y", want: "xy"},
		{name: "clipboard write stripped", in: "x\x1b]52;c;ZXZpbA==\x07y", want: "xy"},
		{name: "bare controls dropped", in: "a\rb\x00c\x7fd", want: "abcd"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clean(tc.in); got != tc.want {
				t.Errorf("clean(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestUntrustedTextNeverReachesTerminal renders a page whose repository
// content carries an escape sequence and checks only the TUI's own styling
// reaches the screen.
func TestUntrustedTextNeverReachesTerminal(t *testing.T) {
	evil := "a.go\x1b]52;c;ZXZpbA==\x07"
	item := Item{
		Title:  evil,
		Detail: Detail{Sections: []Section{{Title: "Diff", Lines: []string{"+" + evil}, Diff: true}}},
	}
	r := staticRoute(evil, func() Page { return NewListPage(evil, []Item{item}) })
	h := newHarness(t, Options{Routes: []Route{r}, Start: &r, StartPage: NewListPage(evil, []Item{item})})
	out := h.a.View().Content
	if strings.Contains(out, "\x1b]") || strings.Contains(out, "\x07") {
		t.Errorf("an OSC sequence from repository content reached the terminal: %q", out)
	}
	if !strings.Contains(h.screen(), "+a.go") {
		t.Errorf("the sanitized text must still render:\n%s", h.screen())
	}
}
