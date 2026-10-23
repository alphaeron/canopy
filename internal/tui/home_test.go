package tui

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/brand"
)

// homeRoutes is a small command tree: a page, a group with subcommands, and
// a utility command.
func homeRoutes(loads *int) []Route {
	match := staticRoute("match", filesPage)
	match.Load = func(context.Context, Values) (Page, error) {
		*loads++
		return filesPage(), nil
	}
	rules := staticRoute("rules", filesPage)
	rules.Long = "Operate on\nrules."
	rules.Summary = func(context.Context) (string, error) { return "3 rules in 2 rules files", nil }
	help := staticRoute("help", filesPage)
	help.Utility = true
	return []Route{help, match, rules, staticRoute("rules list", filesPage), staticRoute("rules get", filesPage)}
}

// TestHomeTree pins the home page's shape: commands as a tree in their
// order with utilities last, subcommands under their group, and a group's
// detail listing its subcommands as links.
func TestHomeTree(t *testing.T) {
	loads := 0
	h := newHarness(t, Config{Routes: homeRoutes(&loads)})
	s := h.screen()
	at := func(label string) int { return strings.Index(s, "──"+label) }
	m, r, l, hp := at("match"), at("▼ rules"), at("list"), at("help")
	if m < 0 || r < m || l < r || hp < l {
		t.Fatalf("want match, rules › list, then help last:\n%s", s)
	}
	h.press("down")
	s = h.screen()
	for _, want := range []string{brand.Command + " rules", "Operate on rules.", "Subcommands", "list", "3 rules in 2 rules files"} {
		if !strings.Contains(s, want) {
			t.Errorf("group detail missing %q:\n%s", want, s)
		}
	}
	h.press("tab", "down", "enter")
	if got := h.a.top().route.Command; got != "rules list" {
		t.Fatalf("a subcommand link must open it, top is %q", got)
	}
}

// TestHomeLeafOpens: enter on a command without subcommands opens it.
func TestHomeLeafOpens(t *testing.T) {
	loads := 0
	h := newHarness(t, Config{Routes: homeRoutes(&loads)})
	h.press("enter")
	if got := h.crumbs(); !strings.Contains(got, brand.Command+" › match") {
		t.Fatalf("enter must open the hovered command, header %q", got)
	}
}

// TestHomePreview: resting on a command loads it in the background and
// shows its summary and first rows; the result is cached, and going back
// home loads it afresh because the command may show something else now.
func TestHomePreview(t *testing.T) {
	loads := 0
	h := newHarness(t, Config{Routes: homeRoutes(&loads)})
	if loads != 1 {
		t.Fatalf("the hovered command must be previewed once, loads=%d", loads)
	}
	if s := h.screen(); !strings.Contains(s, "Now") || !strings.Contains(s, "2 items") || !strings.Contains(s, "Preview") {
		t.Fatalf("the preview must show the page's summary and a Preview view:\n%s", s)
	}
	h.press("]")
	if s := h.screen(); !strings.Contains(s, "a.go") || !strings.Contains(s, "b.go") {
		t.Fatalf("the Preview view must list the page's rows:\n%s", s)
	}
	h.press("down", "up")
	if loads != 1 {
		t.Errorf("returning to a previewed command must use the cache, loads=%d", loads)
	}
	h.press("enter")
	loads = 0
	h.press("esc")
	if loads != 1 {
		t.Errorf("going back home must preview the hovered command again, loads=%d", loads)
	}
}

// TestHomeDeepLinkDoesNotPreview: a session that starts on a command's page
// loads nothing for the home page behind it.
func TestHomeDeepLinkDoesNotPreview(t *testing.T) {
	loads := 0
	routes := homeRoutes(&loads)
	h := newHarness(t, Config{Routes: routes, Start: &routes[2], StartPage: filesPage()})
	h.press("down", "down")
	if loads != 0 {
		t.Errorf("home must not preview while hidden, loads=%d", loads)
	}
}

// TestHomeOverview: the root row shows the repository overview.
func TestHomeOverview(t *testing.T) {
	loads := 0
	overview := func(context.Context) (Detail, error) {
		return Detail{Fields: []Field{{Label: "HEAD", Value: "main @ abc1234"}}}, nil
	}
	h := newHarness(t, Config{Routes: homeRoutes(&loads), Overview: overview})
	h.press("up")
	if s := h.screen(); !strings.Contains(s, "main @ abc1234") || !strings.Contains(s, "5 commands") {
		t.Fatalf("the root row must show the overview:\n%s", s)
	}
}

func TestReflow(t *testing.T) {
	got := reflow("The rules command\nallows things.\n\nExamples:\n  app rules get .*\n  app rules list\nDone.")
	want := []string{"The rules command allows things.", "", "Examples:", "  app rules get .*", "  app rules list", "Done."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reflow = %q, want %q", got, want)
	}
}

// TestHomeOverviewIgnoresAStaleLoad: a newer overview load cancels the
// older one, and the older one's result, landing last, is dropped.
func TestHomeOverviewIgnoresAStaleLoad(t *testing.T) {
	calls := 0
	var first context.Context
	overview := func(ctx context.Context) (Detail, error) {
		calls++
		if calls == 1 {
			first = ctx
			return Detail{Fields: []Field{{Label: "HEAD", Value: "stale"}}}, nil
		}
		return Detail{Fields: []Field{{Label: "HEAD", Value: "fresh"}}}, nil
	}
	loads := 0
	h := newHomePage(t.Context(), homeRoutes(&loads), overview)
	older, newer := h.loadOverview(), h.loadOverview()
	olderMsg := older()
	if first.Err() == nil {
		t.Error("the older load's context is not cancelled by the newer load")
	}
	h.update(newer())
	h.update(olderMsg)
	if got := h.rootDetail.Fields[1].Value; got != "fresh" {
		t.Errorf("root HEAD = %q, want the newer load's", got)
	}
}
