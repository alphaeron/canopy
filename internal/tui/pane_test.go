package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestPaneGeometry pins the contract every page relies on: a pane is exactly
// w cells by h rows whatever its content, with the title embedded in the top
// rule and the info in the bottom rule.
func TestPaneGeometry(t *testing.T) {
	st := newStyles(true)
	tests := []struct {
		name         string
		title, info  string
		content      string
		w, h         int
		wantTop      string
		wantBottom   string
		wantLineFrom string
	}{
		{name: "fits", title: "Files", info: "3/12", content: "a\nb", w: 30, h: 6, wantTop: "Files", wantBottom: "3/12"},
		{name: "long content clipped", title: "T", content: strings.Repeat("x", 100) + "\n" + strings.Repeat("y\n", 50), w: 20, h: 5, wantTop: "T"},
		{name: "long title truncated", title: strings.Repeat("t", 80), info: "1/1", w: 24, h: 3, wantBottom: "1/1"},
		{name: "minimum", w: 4, h: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := ansi.Strip(pane(st, tc.title, tc.info, tc.content, tc.w, tc.h, true))
			lines := strings.Split(out, "\n")
			if len(lines) != tc.h {
				t.Fatalf("got %d rows, want %d:\n%s", len(lines), tc.h, out)
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got != tc.w {
					t.Errorf("row %d is %d cells, want %d: %q", i, got, tc.w, l)
				}
			}
			if !strings.HasPrefix(lines[0], "╭") || !strings.HasSuffix(lines[0], "╮") {
				t.Errorf("top rule must have both corners: %q", lines[0])
			}
			if !strings.Contains(lines[0], tc.wantTop) {
				t.Errorf("top rule %q must embed %q", lines[0], tc.wantTop)
			}
			if !strings.Contains(lines[tc.h-1], tc.wantBottom) {
				t.Errorf("bottom rule %q must embed %q", lines[tc.h-1], tc.wantBottom)
			}
		})
	}
}

func TestComputeSplit(t *testing.T) {
	tests := []struct {
		name           string
		w, h           int
		show, aux      bool
		wantDetail     bool
		wantSideBySide bool
		wantAux        bool
	}{
		{name: "wide", w: 120, h: 30, show: true, wantDetail: true, wantSideBySide: true},
		{name: "narrow but tall stacks", w: 70, h: 30, show: true, wantDetail: true},
		{name: "small hides detail", w: 70, h: 10, show: true},
		{name: "toggled off", w: 120, h: 30, show: false},
		{name: "very wide with tabs has three columns", w: 180, h: 30, show: true, aux: true, wantDetail: true, wantSideBySide: true, wantAux: true},
		{name: "very wide without tabs keeps two", w: 180, h: 30, show: true, wantDetail: true, wantSideBySide: true},
		{name: "wide with tabs keeps two", w: 120, h: 30, show: true, aux: true, wantDetail: true, wantSideBySide: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := computeSplit(tc.w, tc.h, tc.show, tc.aux)
			if l.detail != tc.wantDetail || l.sideBySide != tc.wantSideBySide || (l.auxW > 0) != tc.wantAux {
				t.Fatalf("got detail=%v sideBySide=%v aux=%d, want %v/%v/%v", l.detail, l.sideBySide, l.auxW, tc.wantDetail, tc.wantSideBySide, tc.wantAux)
			}
			switch {
			case !l.detail:
				if l.mainW != tc.w || l.mainH != tc.h {
					t.Errorf("main pane must fill the body, got %dx%d", l.mainW, l.mainH)
				}
			case l.sideBySide:
				if l.mainW+l.detailW+l.auxW != tc.w {
					t.Errorf("widths %d+%d+%d must sum to %d", l.mainW, l.detailW, l.auxW, tc.w)
				}
			default:
				if l.mainH+l.detailH != tc.h {
					t.Errorf("heights %d+%d must sum to %d", l.mainH, l.detailH, tc.h)
				}
			}
		})
	}
}

func TestRenderDetail(t *testing.T) {
	st := newStyles(true)
	render := func(d Detail, width int) string { return renderView(st, d.views()[0], width, -1).content }
	if got := ansi.Strip(render(Detail{}, 40)); got != "No details" {
		t.Errorf("empty detail = %q", got)
	}
	d := Detail{
		Fields:   []Field{{Label: "File", Value: "a.go"}, {Label: "Scope", Value: ""}},
		Sections: []Section{{Title: "Diff", Lines: []string{"@@ -1 +1 @@", "-old", "+new"}, Diff: true}},
	}
	out := ansi.Strip(render(d, 40))
	for _, want := range []string{"File", "a.go", "Scope", "—", "Diff", "-old", "+new"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q:\n%s", want, out)
		}
	}
}
