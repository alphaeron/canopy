package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// tabbedPage is a list whose rows have Chain tabs and links: the shape of
// the review and match pages.
func tabbedPage(target *Route) func() Page {
	return func() Page {
		return NewListPage("Files", []Item{
			{Title: "a.go", Detail: Detail{
				Fields: []Field{{Label: "File", Value: "summary-a"}},
				Tabs: []Tab{
					{Title: "Chain", Sections: []Section{{Title: "Layers", Rows: []Row{{Text: "chain-a", Route: target}}}}},
					{Title: "Trace", Sections: []Section{{Title: "Events", Lines: []string{"trace-a"}}}},
				},
			}},
			{Title: "b.go", Detail: Detail{
				Fields: []Field{{Label: "File", Value: "summary-b"}},
				Tabs:   []Tab{{Title: "Chain", Sections: []Section{{Title: "Layers", Lines: []string{"chain-b"}}}}},
			}},
		})
	}
}

func startOn(t *testing.T, page func() Page, w, h int) *harness {
	t.Helper()
	r := staticRoute("files", page)
	hs := newHarness(t, Options{Routes: []Route{r}, Start: &r, StartPage: page()})
	hs.send(tea.WindowSizeMsg{Width: w, Height: h})
	return hs
}

// TestDetailTabs covers [ and ]: the pane title names the views, ] shows
// the next one, and moving to another row keeps the same view when that
// row has it.
func TestDetailTabs(t *testing.T) {
	target := staticRoute("rules for a.go", filesPage)
	h := startOn(t, tabbedPage(&target), 120, 30)
	if s := h.screen(); !strings.Contains(s, "[Summary] Chain Trace") || !strings.Contains(s, "summary-a") {
		t.Fatalf("the summary must show first, with the views named:\n%s", s)
	}
	h.press("]")
	if s := h.screen(); !strings.Contains(s, "Summary [Chain] Trace") || !strings.Contains(s, "chain-a") || strings.Contains(s, "summary-a") {
		t.Fatalf("] must show the Chain view:\n%s", s)
	}
	h.press("down")
	if s := h.screen(); !strings.Contains(s, "chain-b") {
		t.Fatalf("the next row must open on the same view:\n%s", s)
	}
	h.press("[")
	if s := h.screen(); !strings.Contains(s, "summary-b") {
		t.Fatalf("[ must go back to the summary:\n%s", s)
	}
}

// TestDetailLinks covers rows with routes: with the detail focused, down
// selects the first link and enter opens it.
func TestDetailLinks(t *testing.T) {
	target := staticRoute("rules for a.go", filesPage)
	h := startOn(t, tabbedPage(&target), 120, 30)
	h.press("]", "tab", "down")
	if s := h.screen(); !strings.Contains(s, "▸ chain-a") {
		t.Fatalf("down in the detail pane must select the link:\n%s", s)
	}
	h.press("enter")
	if got := h.a.top().route.Command; got != "rules for a.go" {
		t.Fatalf("enter on a link must open its route, top is %q", got)
	}
}

// TestThreeColumns: a very wide terminal shows the summary and the selected
// view side by side, and tab reaches the third column.
func TestThreeColumns(t *testing.T) {
	target := staticRoute("rules for a.go", filesPage)
	h := startOn(t, tabbedPage(&target), 180, 30)
	s := h.screen()
	if !strings.Contains(s, "summary-a") || !strings.Contains(s, "chain-a") || !strings.Contains(s, "[Chain] Trace") {
		t.Fatalf("both columns must show:\n%s", s)
	}
	h.press("]")
	if s := h.screen(); !strings.Contains(s, "trace-a") || !strings.Contains(s, "summary-a") {
		t.Fatalf("] must change only the third column:\n%s", s)
	}
	p := h.a.top().page.(*ListPage)
	h.press("tab", "tab")
	if p.focus != focusAux {
		t.Fatalf("tab must reach the third column, focus %v", p.focus)
	}
	h.press("tab")
	if p.focus != focusMain {
		t.Errorf("tab must wrap to the list, focus %v", p.focus)
	}
}

// TestThreeColumnsSpreadSummary: a row without tabs uses the third column
// for its sections rather than leaving it empty.
func TestThreeColumnsSpreadSummary(t *testing.T) {
	page := func() Page {
		return NewListPage("Rules", []Item{
			{Title: "r", Detail: Detail{
				Fields:   []Field{{Label: "Pattern", Value: "field-value"}},
				Sections: []Section{{Title: "Overlaps", Lines: []string{"section-line"}}},
				Tabs:     []Tab{{Title: "Files"}},
			}},
			{Title: "plain", Detail: Detail{
				Fields:   []Field{{Label: "Pattern", Value: "plain-field"}},
				Sections: []Section{{Title: "Overlaps", Lines: []string{"plain-section"}}},
			}},
		})
	}
	h := startOn(t, page, 180, 30)
	h.press("down")
	var fieldCol, sectionCol int
	for _, line := range strings.Split(h.screen(), "\n") {
		if i := strings.Index(line, "plain-field"); i >= 0 {
			fieldCol = ansi.StringWidth(line[:i])
		}
		if i := strings.Index(line, "plain-section"); i >= 0 {
			sectionCol = ansi.StringWidth(line[:i])
		}
	}
	if fieldCol == 0 || sectionCol <= fieldCol+20 {
		t.Fatalf("sections must move to the third column (field at %d, section at %d):\n%s", fieldCol, sectionCol, h.screen())
	}
}

func TestRenderRows(t *testing.T) {
	st := newStyles(true)
	r := &Route{Command: "x"}
	v := Tab{Sections: []Section{{Title: "Approvers", Rows: []Row{
		{Text: "org/backend", Badge: BadgePending, Meter: &Meter{Have: 1, Need: 2}, Note: "approved by alice"},
		{Text: "lead", Indent: 1, Badge: BadgeOK, Meter: &Meter{Have: 1, Need: 1}},
		{Text: "open me", Route: r},
	}}}}
	out := renderView(st, v, 60, 0)
	got := ansi.Strip(out.content)
	for _, want := range []string{"● org/backend ▓░ 1/2  approved by alice", "    ✓ lead ▓ 1/1", "▸ open me"} {
		if !strings.Contains(got, want) {
			t.Errorf("rows missing %q:\n%s", want, got)
		}
	}
	if len(out.links) != 1 || out.links[0].route.Command != "x" {
		t.Fatalf("links = %+v", out.links)
	}
	// The link's recorded line is where it is drawn.
	if line := strings.Split(got, "\n")[out.links[0].line]; !strings.Contains(line, "open me") {
		t.Errorf("link line %d is %q", out.links[0].line, line)
	}
}

// TestLinkLinesAfterWrapping pins link positions when earlier text wraps,
// which is why the detail wraps lines itself instead of the viewport.
func TestLinkLinesAfterWrapping(t *testing.T) {
	st := newStyles(true)
	r := &Route{Command: "x"}
	v := Tab{
		Fields: []Field{{Label: "About", Value: strings.Repeat("word ", 30)}},
		Sections: []Section{{Title: "S", Lines: []string{strings.Repeat("long ", 30)}, Rows: []Row{
			{Text: strings.Repeat("wrapped ", 10), Route: r},
			{Text: "second", Route: r},
		}}},
	}
	out := renderView(st, v, 30, -1)
	lines := strings.Split(ansi.Strip(out.content), "\n")
	for i, l := range out.links {
		if l.line+l.height > len(lines) {
			t.Fatalf("link %d past the content", i)
		}
	}
	if !strings.Contains(lines[out.links[1].line], "second") {
		t.Errorf("second link at line %d is %q", out.links[1].line, lines[out.links[1].line])
	}
	if out.links[0].height < 2 {
		t.Errorf("a wrapped link must span its lines, height %d", out.links[0].height)
	}
}

func TestBadgeInTitles(t *testing.T) {
	page := func() Page {
		return NewListPage("Files", []Item{{Title: "a.go", Badge: BadgeFail}})
	}
	h := startOn(t, page, 120, 30)
	if !strings.Contains(h.screen(), "✗ a.go") {
		t.Errorf("a row's badge must precede its title:\n%s", h.screen())
	}
}
