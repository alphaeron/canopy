package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alphaeron/kit/internal/brand"
)

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// harness drives the app synchronously: every command is executed and its
// message fed back, the way the Bubble Tea runtime would.
type harness struct {
	t     *testing.T
	a     *app
	quit  bool
	depth int
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	cfg.previewDelay = time.Nanosecond
	h := &harness{t: t, a: newApp(context.Background(), cfg)}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.a.Init())
	return h
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	_, cmd := h.a.Update(msg)
	h.run(cmd)
}

func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.send(keyPress(k))
	}
}

// run executes cmd and delivers its messages. Spinner ticks and cursor
// blinks are dropped: each re-arms a timer forever. The app's own inputs use
// the terminal cursor and never blink, but huh's text fields (in the rule
// and options forms) keep a drawn cursor, so a test that focuses one waits
// out a single blink interval before its blink message is dropped here.
func (h *harness) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	h.depth++
	defer func() { h.depth-- }()
	if h.depth > 64 {
		h.t.Fatal("command chain did not settle")
	}
	msg := cmd()
	if strings.HasPrefix(fmt.Sprintf("%T", msg), "cursor.") {
		return
	}
	switch msg := msg.(type) {
	case nil, spinner.TickMsg:
	case tea.QuitMsg:
		h.quit = true
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		h.send(msg)
	}
}

func (h *harness) screen() string { return ansi.Strip(h.a.View().Content) }

func (h *harness) crumbs() string { return strings.SplitN(h.screen(), "\n", 2)[0] }

func staticRoute(name string, page func() Page) Route {
	return Route{Command: name, Description: name + " page", Load: func(context.Context, Values) (Page, error) {
		return page(), nil
	}}
}

func filesPage() Page {
	return NewListPage("Files", []Item{
		{Title: "a.go", Detail: Detail{Fields: []Field{{Label: "File", Value: "detail-a"}}}},
		{Title: "b.go", Detail: Detail{Fields: []Field{{Label: "File", Value: "detail-b"}}}},
	})
}

// TestDeepLinkStartsAboveHome is the subcommand contract: `match` opens
// on the match page, and back goes to the home page rather than quitting.
func TestDeepLinkStartsAboveHome(t *testing.T) {
	r := staticRoute("match", filesPage)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r, StartPage: filesPage()})
	if got := h.crumbs(); !strings.Contains(got, brand.Command+" › match") {
		t.Fatalf("header must show the deep-linked page, got %q", got)
	}
	h.press("esc")
	if len(h.a.stack) != 1 || h.quit {
		t.Fatalf("esc must return home without quitting (stack %d, quit %v)", len(h.a.stack), h.quit)
	}
	if !strings.Contains(h.screen(), "Commands") {
		t.Errorf("home page must list the commands:\n%s", h.screen())
	}
	h.press("esc")
	if h.quit || len(h.a.stack) != 1 {
		t.Errorf("esc at home must be a no-op")
	}
}

// TestStartWithoutPageLoadsInBackground covers a deep link with no
// preloaded page: the route loads after Init.
func TestStartWithoutPageLoadsInBackground(t *testing.T) {
	r := staticRoute("coverage", filesPage)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r})
	if len(h.a.stack) != 2 || h.a.top().route.Command != "coverage" {
		t.Fatalf("start route was not loaded: stack %d", len(h.a.stack))
	}
}

// TestHomeNavigatesToRoute is the "run the TUI, then go to a command's page"
// path: enter on a home row loads and pushes that route.
func TestHomeNavigatesToRoute(t *testing.T) {
	h := newHarness(t, Config{Routes: []Route{staticRoute("match", filesPage), staticRoute("coverage", filesPage)}})
	h.press("down", "enter")
	if got := h.crumbs(); !strings.Contains(got, brand.Command+" › coverage") {
		t.Fatalf("enter must open the hovered command, header %q", got)
	}
	if !strings.Contains(h.screen(), "a.go") {
		t.Errorf("pushed page must be sized and rendered:\n%s", h.screen())
	}
}

func TestLoadErrorStaysAndReports(t *testing.T) {
	bad := Route{Command: "bad", Load: func(context.Context, Values) (Page, error) { return nil, errors.New("boom") }}
	h := newHarness(t, Config{Routes: []Route{bad}})
	h.press("enter")
	if len(h.a.stack) != 1 {
		t.Fatalf("a failed load must not push a page")
	}
	if !strings.Contains(h.screen(), "boom") {
		t.Errorf("load error must be shown in the status line:\n%s", h.screen())
	}
}

// TestDetailFollowsCursor is the drill-down pane contract: the detail pane
// shows the hovered row and updates as the cursor moves.
func TestDetailFollowsCursor(t *testing.T) {
	r := staticRoute("match", filesPage)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r, StartPage: filesPage()})
	if !strings.Contains(h.screen(), "detail-a") {
		t.Fatalf("detail pane must show the first row:\n%s", h.screen())
	}
	h.press("down")
	if s := h.screen(); !strings.Contains(s, "detail-b") || strings.Contains(s, "detail-a") {
		t.Errorf("detail pane must follow the cursor:\n%s", s)
	}
}

// TestTypingInFilterDoesNotQuit guards the bug where "q" typed into a
// filter quit the program; ctrl+c must still quit from anywhere.
func TestTypingInFilterDoesNotQuit(t *testing.T) {
	r := staticRoute("match", filesPage)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r, StartPage: filesPage()})
	h.press("/", "q", "?")
	if h.quit {
		t.Fatal("q typed into the filter quit the program")
	}
	if !h.a.top().page.capturing() || h.a.showHelp {
		t.Fatal("the filter must capture typed keys")
	}
	h.press("esc")
	if len(h.a.stack) != 2 || h.a.top().page.capturing() {
		t.Fatalf("esc while typing must cancel the filter, not leave the page")
	}
	// An applied filter is cleared by esc before esc means "back".
	h.press("/", "b", "enter")
	if !strings.Contains(h.screen(), "filter: b") {
		t.Fatalf("applied filter must show in the pane title:\n%s", h.screen())
	}
	h.press("esc")
	if len(h.a.stack) != 2 || strings.Contains(h.screen(), "filter: b") {
		t.Fatalf("esc must clear an applied filter first")
	}
	h.press("esc")
	if len(h.a.stack) != 1 {
		t.Fatalf("esc with no filter must go back")
	}
	h.press("ctrl+c")
	if !h.quit {
		t.Error("ctrl+c must always quit")
	}
}

func TestQuitKey(t *testing.T) {
	h := newHarness(t, Config{Routes: []Route{staticRoute("match", filesPage)}})
	h.press("q")
	if !h.quit {
		t.Error("q must quit when not typing")
	}
}

// TestActionConfirmRunsAndReloads covers the remove-rule flow: the action
// asks first, "n" cancels, "y" runs it and reloads the page.
func TestActionConfirmRunsAndReloads(t *testing.T) {
	loads, runs := 0, 0
	r := Route{Command: "rules", Load: func(context.Context, Values) (Page, error) {
		loads++
		return NewListPage("Rules", []Item{{Title: "*.go"}}).WithActions(Action{
			Key: "x", Help: "remove", RequiresItem: true,
			Confirm: func(it Item) string { return "Remove " + it.Title + "?" },
			Run: func(context.Context, Item) (Outcome, error) {
				runs++
				return Outcome{Info: "removed", Reload: true}, nil
			},
		}), nil
	}}
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r})
	h.press("x")
	if !strings.Contains(h.screen(), "Remove *.go? [y/N]") {
		t.Fatalf("action must prompt first:\n%s", h.screen())
	}
	h.press("n")
	if runs != 0 || h.a.prompt != nil {
		t.Fatalf("n must cancel")
	}
	h.press("x", "y")
	if runs != 1 {
		t.Fatalf("y must run the action, runs=%d", runs)
	}
	if loads != 2 || len(h.a.stack) != 2 {
		t.Errorf("the page must be reloaded in place (loads=%d stack=%d)", loads, len(h.a.stack))
	}
	if !strings.Contains(h.screen(), "removed") {
		t.Errorf("outcome info must show:\n%s", h.screen())
	}
}

func TestPaletteNavigates(t *testing.T) {
	h := newHarness(t, Config{Routes: []Route{staticRoute("match", filesPage), staticRoute("coverage", filesPage)}})
	h.press("ctrl+p")
	screen := h.screen()
	if h.a.palette == nil || !strings.Contains(screen, "Go to") {
		t.Fatalf("ctrl+p must open the palette:\n%s", screen)
	}
	// The palette overlays the page; it must not blank it.
	if !strings.Contains(screen, brand.Command+" ") || !strings.Contains(screen, "commands ·") {
		t.Errorf("the page must stay visible around the palette:\n%s", screen)
	}
	for _, k := range "cov" {
		h.press(string(k))
	}
	if n := len(h.a.palette.filtered); n != 1 {
		t.Fatalf("filter must narrow to one entry, got %d", n)
	}
	h.press("enter")
	if h.a.palette != nil || h.a.top().route == nil || h.a.top().route.Command != "coverage" {
		t.Fatalf("enter must close the palette and open the route")
	}
	h.press(":")
	h.press("enter") // first entry is home
	if len(h.a.stack) != 1 {
		t.Errorf("palette home entry must return to the home page")
	}
}

func TestTreePageDrillsAndToggles(t *testing.T) {
	target := staticRoute("rules for a.go", filesPage)
	tree := func() Page {
		return NewTreePage("Rules", []TreeGroup{{
			Title:  "*.go in rules",
			Detail: Detail{Fields: []Field{{Label: "Pattern", Value: "group-detail"}}},
			Items:  []Item{{Title: "a.go", Detail: Detail{Fields: []Field{{Label: "File", Value: "leaf-detail"}}}, Route: &target}},
		}})
	}
	r := staticRoute("rules get", tree)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r, StartPage: tree()})
	if !strings.Contains(h.screen(), "group-detail") {
		t.Fatalf("the cursor must start on the first group and show its detail:\n%s", h.screen())
	}
	if !strings.Contains(h.screen(), "all rules") {
		t.Errorf("the root row must be visible on first render:\n%s", h.screen())
	}
	h.press("down")
	if !strings.Contains(h.screen(), "leaf-detail") {
		t.Fatalf("hovering a file must show its detail:\n%s", h.screen())
	}
	h.press("enter")
	if got := h.a.top().route.Command; got != "rules for a.go" {
		t.Errorf("enter on a leaf must open its route, top is %q", got)
	}
}

func TestHelpToggleResizesBody(t *testing.T) {
	h := newHarness(t, Config{Routes: []Route{staticRoute("match", filesPage)}})
	_, before := h.a.bodySize()
	h.press("?")
	_, after := h.a.bodySize()
	if !h.a.showHelp || after >= before {
		t.Errorf("full help must take rows from the body (%d -> %d)", before, after)
	}
	if got := len(strings.Split(h.screen(), "\n")); got != 30 {
		t.Errorf("screen must stay exactly the terminal height, got %d rows", got)
	}
}

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheel(x, y int, b tea.MouseButton) tea.MouseWheelMsg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: b}
}

// Screen coordinates of list rows in the harness's 120x30 terminal: the
// header row, the pane's top border, and the list's filter row precede the
// first item, and each item is two lines plus one of spacing.
const (
	firstRowY = 3
	rowStride = 3
	listX     = 5
	detailX   = 80
)

// TestMouseSelectsAndOpensRows covers clicking: a click selects a row and
// focuses its pane, and clicking the selected row opens it.
func TestMouseSelectsAndOpensRows(t *testing.T) {
	match, coverage := staticRoute("match", filesPage), staticRoute("coverage", filesPage)
	commands := func() Page {
		return NewListPage("Commands", []Item{
			{Title: "match", Detail: Detail{Fields: []Field{{Label: "Command", Value: brand.Command + " match"}}}, Route: &match},
			{Title: "coverage", Detail: Detail{Fields: []Field{{Label: "Command", Value: brand.Command + " coverage"}}}, Route: &coverage},
		})
	}
	start := staticRoute("commands", commands)
	h := newHarness(t, Config{Routes: []Route{match, coverage}, Start: &start, StartPage: commands()})
	h.send(click(listX, firstRowY+rowStride))
	if len(h.a.stack) != 2 || !strings.Contains(h.screen(), brand.Command+" coverage") {
		t.Fatalf("a click must select the second row without opening it:\n%s", h.screen())
	}
	h.send(click(listX, firstRowY+rowStride+1)) // description line of the same row
	if got := h.crumbs(); !strings.Contains(got, "commands › coverage") {
		t.Fatalf("clicking the selected row must open it, header %q", got)
	}
	// Spacing rows and the border map to no row.
	h.send(click(listX, firstRowY+2))
	h.send(click(listX, 1))
	if !strings.Contains(h.screen(), "detail-a") {
		t.Errorf("clicks between rows must not move the selection:\n%s", h.screen())
	}
	h.send(click(detailX, 5))
	if p := h.a.top().page.(*ListPage); p.focus != focusDetail {
		t.Errorf("a click in the detail pane must focus it")
	}
}

func TestMouseWheel(t *testing.T) {
	r := staticRoute("match", filesPage)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r, StartPage: filesPage()})
	h.send(wheel(listX, 10, tea.MouseWheelDown))
	if !strings.Contains(h.screen(), "detail-b") {
		t.Fatalf("the wheel over the list must move the selection:\n%s", h.screen())
	}
	h.send(wheel(listX, 10, tea.MouseWheelUp))
	if !strings.Contains(h.screen(), "detail-a") {
		t.Fatalf("wheel up must move the selection back:\n%s", h.screen())
	}
	// Over the palette the wheel moves the palette selection instead.
	h.press("ctrl+p")
	h.send(wheel(listX, 10, tea.MouseWheelDown))
	if h.a.palette.cursor != 1 {
		t.Errorf("the wheel must move the palette selection, cursor=%d", h.a.palette.cursor)
	}
}

func TestMouseTreeClick(t *testing.T) {
	target := staticRoute("rules for b.go", filesPage)
	tree := func() Page {
		return NewTreePage("Rules", []TreeGroup{{
			Title: "*.go in rules",
			Items: []Item{
				{Title: "a.go", Detail: Detail{Fields: []Field{{Label: "File", Value: "leaf-a"}}}},
				{Title: "b.go", Route: &target},
			},
		}})
	}
	r := staticRoute("rules get", tree)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r, StartPage: tree()})
	// Rows: header, top border, then root, group, a.go, b.go.
	h.send(click(listX, 4))
	if !strings.Contains(h.screen(), "leaf-a") {
		t.Fatalf("a click must select the tree node under it:\n%s", h.screen())
	}
	h.send(click(listX, 5))
	h.send(click(listX, 5))
	if got := h.a.top().route.Command; got != "rules for b.go" {
		t.Errorf("clicking the selected node must open it, top is %q", got)
	}
}

// TestTerminalCursor checks that text inputs use the real terminal cursor
// (no blink timer) placed at the input: the list filter and the palette.
func TestTerminalCursor(t *testing.T) {
	r := staticRoute("match", filesPage)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r, StartPage: filesPage()})
	if h.a.View().Cursor != nil {
		t.Fatal("no cursor while nothing takes text")
	}
	h.press("/", "a")
	c := h.a.View().Cursor
	if c == nil || c.Y != 2 {
		t.Fatalf("filter cursor must sit on the filter row (y=2), got %+v", c)
	}
	line := []rune(strings.Split(h.screen(), "\n")[c.Y])
	if !strings.HasSuffix(string(line[:c.X]), "Filter: a") {
		t.Errorf("filter cursor must follow the typed text, x=%d in %q", c.X, line)
	}
	h.press("esc", "ctrl+p", "c")
	c = h.a.View().Cursor
	if c == nil {
		t.Fatal("the palette input must show a cursor")
	}
	if line := strings.Split(h.screen(), "\n")[c.Y]; !strings.Contains(line, "> c") {
		t.Errorf("palette cursor must sit on the input row, got %q", line)
	}
}

func TestFormFollowsTerminalTheme(t *testing.T) {
	name := "n"
	p := NewFormPage("add rule", "new rule", huh.NewForm(huh.NewGroup(huh.NewInput().Title("Name").Value(&name))), nil).(*formPage)
	dark := p.form.View()
	p.setStyles(newStyles(false))
	if p.form.View() == dark {
		t.Error("the form must re-theme for a light background")
	}
}
