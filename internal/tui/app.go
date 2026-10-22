package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Page is one screen of the TUI. Pages are built by the constructors in this
// package (NewListPage, NewTreePage, NewRuleFormPage) and are driven only by
// the app, which owns the chrome, navigation, and global keys.
type Page interface {
	// title is the pane title for pages not opened through a route.
	title() string
	// status is the page's summary for the status line.
	status() string
	init() tea.Cmd
	setSize(w, h int)
	setStyles(st *styles)
	update(msg tea.Msg) tea.Cmd
	view() string
	// bindings are the page's own keys, shown in the help line.
	bindings() []key.Binding
	// capturing reports that the page is taking text input, so global
	// single-key bindings must pass through to it.
	capturing() bool
	// cursor is the terminal cursor for the page's focused text input,
	// relative to the page body, or nil when nothing takes text.
	cursor() *tea.Cursor
	// back performs a page-local "back" (clear a filter, return focus to
	// the main pane) and reports whether it did; if not, the app pops the
	// page.
	back() bool
}

// Outcome is the result of an Action or a submitted form.
type Outcome struct {
	// Info is shown in the status line.
	Info string
	// Page, when non-nil, is pushed onto the navigation stack.
	Page Page
	// Reload re-runs the current page's route so it reflects the change.
	Reload bool
}

// Config configures Run.
type Config struct {
	// Routes populate the home page and the command palette.
	Routes []Route
	// Start, when non-nil, is opened on top of the home page: this is how
	// a subcommand deep-links into its own page. Back returns home.
	Start *Route
	// StartPage is Start's already-loaded page. Subcommands load their
	// data before starting the TUI so load errors surface as ordinary CLI
	// errors; when nil, Start is loaded in the background instead.
	StartPage Page
}

// Run starts the full-screen TUI and blocks until the user quits.
func Run(ctx context.Context, cfg Config) error {
	_, err := tea.NewProgram(newApp(ctx, cfg), tea.WithContext(ctx)).Run()
	if err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}

// Messages pages and actions use to drive the app (pug's NavigateTo /
// ReportError pattern): state changes happen in app.Update, never inside
// commands.
type (
	navigateMsg struct{ route Route }
	homeMsg     struct{}
	loadedMsg   struct {
		route Route
		page  Page
		err   error
		// replace is the page a reload replaces; the result is dropped if
		// the user has navigated away from it meanwhile.
		replace Page
	}
	outcomeMsg struct {
		outcome Outcome
		err     error
		// pop closes the top page (a submitted form) before applying the
		// outcome.
		pop bool
	}
	confirmMsg struct {
		text string
		then tea.Cmd
	}
	// optionsMsg asks the app to open the options form for route. With
	// replace, submitting reloads the current page in place; otherwise the
	// route opens as a new page (a command opened without a required
	// argument).
	optionsMsg struct {
		route   Route
		replace bool
	}
	// optionsReadyMsg carries the choice lists loaded for the form.
	optionsReadyMsg struct {
		route   Route
		replace bool
		choices map[string][]string
		err     error
	}
	// reopenMsg closes the options form and opens route with its new values.
	reopenMsg struct {
		route   Route
		replace bool
	}
	// runMsg asks the app to run a side-effecting operation (an action or a
	// form submission) in a command with the app's context.
	runMsg struct {
		run func(ctx context.Context) (Outcome, error)
		pop bool
	}
)

func runCmd(run func(ctx context.Context) (Outcome, error), pop bool) tea.Cmd {
	return func() tea.Msg { return runMsg{run: run, pop: pop} }
}

func navigate(r Route) tea.Cmd { return func() tea.Msg { return navigateMsg{route: r} } }
func goHome() tea.Msg          { return homeMsg{} }

func confirm(text string, then tea.Cmd) tea.Cmd {
	return func() tea.Msg { return confirmMsg{text: text, then: then} }
}

// entry is one level of the navigation stack.
type entry struct {
	route *Route // nil for the home page and pages pushed by actions
	page  Page
}

// app is the single top-level Bubble Tea model (crush's architecture):
// pages are components it calls into, not independent programs.
type app struct {
	ctx    context.Context
	routes []Route
	st     *styles
	stack  []entry

	width, height int

	help     help.Model
	showHelp bool
	palette  *palette
	prompt   *confirmMsg

	statusText string
	statusErr  bool
	loading    string
	spinner    spinner.Model

	pendingStart *Route
}

func newApp(ctx context.Context, cfg Config) *app {
	st := newStyles(true)
	a := &app{
		ctx:     ctx,
		routes:  cfg.Routes,
		st:      st,
		help:    help.New(),
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
	a.help.Styles = st.help
	a.stack = []entry{{page: newHomePage(cfg.Routes)}}
	switch {
	case cfg.Start != nil && cfg.StartPage != nil:
		r := *cfg.Start
		a.stack = append(a.stack, entry{route: &r, page: cfg.StartPage})
	case cfg.Start != nil:
		r := *cfg.Start
		a.pendingStart = &r
	}
	for _, e := range a.stack {
		e.page.setStyles(st)
	}
	return a
}

func (a *app) top() entry { return a.stack[len(a.stack)-1] }

func (a *app) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, a.top().page.init()}
	if a.pendingStart != nil {
		cmds = append(cmds, navigate(*a.pendingStart))
		a.pendingStart = nil
	}
	return tea.Batch(cmds...)
}

func (a *app) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resize()
		return a, nil
	case tea.BackgroundColorMsg:
		a.st = newStyles(msg.IsDark())
		a.help.Styles = a.st.help
		for _, e := range a.stack {
			e.page.setStyles(a.st)
		}
		if a.palette != nil {
			a.palette.st = a.st
		}
		return a, nil
	case spinner.TickMsg:
		if a.loading == "" {
			return a, nil
		}
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd
	case navigateMsg:
		if msg.route.missingRequired() {
			a.palette = nil
			return a, a.openOptions(msg.route, false)
		}
		return a, a.load(msg.route, nil)
	case optionsMsg:
		return a, a.openOptions(msg.route, msg.replace)
	case optionsReadyMsg:
		if msg.err != nil {
			a.setStatus(msg.err.Error(), true)
			return a, nil
		}
		return a, a.push(newOptionsPage(msg.route, msg.choices, msg.replace))
	case reopenMsg:
		a.pop()
		var replace Page
		if msg.replace {
			replace = a.top().page
		}
		return a, a.load(msg.route, replace)
	case homeMsg:
		a.palette = nil
		a.stack = a.stack[:1]
		return a, nil
	case loadedMsg:
		return a, a.loaded(msg)
	case outcomeMsg:
		return a, a.applyOutcome(msg)
	case confirmMsg:
		a.prompt = &msg
		return a, nil
	case runMsg:
		ctx := a.ctx
		return a, func() tea.Msg {
			o, err := msg.run(ctx)
			return outcomeMsg{outcome: o, err: err, pop: msg.pop}
		}
	case tea.KeyPressMsg:
		return a, a.handleKey(msg)
	case tea.MouseWheelMsg, tea.MouseClickMsg:
		return a, a.handleMouse(msg.(tea.MouseMsg))
	}
	return a, a.top().page.update(msg)
}

// handleKey routes a key press: dialogs first, then global bindings, then
// the top page. Any key clears a transient status message (pug).
func (a *app) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if key.Matches(msg, globalKeys.ForceQuit) {
		return tea.Quit
	}
	a.statusText, a.statusErr = "", false

	if a.prompt != nil {
		switch {
		case key.Matches(msg, confirmKeys.Yes):
			then := a.prompt.then
			a.prompt = nil
			return then
		case key.Matches(msg, confirmKeys.No):
			a.prompt = nil
		}
		return nil
	}
	if a.palette != nil {
		return a.palette.update(msg, a)
	}

	page := a.top().page
	if key.Matches(msg, globalKeys.Suspend) {
		return tea.Suspend
	}
	if page.capturing() {
		return page.update(msg)
	}
	switch {
	case key.Matches(msg, globalKeys.Quit):
		return tea.Quit
	case key.Matches(msg, globalKeys.Help):
		a.showHelp = !a.showHelp
		a.resize()
		return nil
	case key.Matches(msg, globalKeys.Palette):
		a.palette = newPalette(a.st, a.routes)
		return a.palette.init()
	case key.Matches(msg, globalKeys.Reload):
		return a.reload()
	case key.Matches(msg, globalKeys.Options):
		if r := a.top().route; r != nil && len(r.Params) > 0 {
			return a.openOptions(*r, true)
		}
	case key.Matches(msg, globalKeys.Back):
		if page.back() {
			return nil
		}
		a.pop()
		return nil
	}
	return page.update(msg)
}

// openOptions loads the route's choice lists off the UI loop, then opens
// the options form.
func (a *app) openOptions(r Route, replace bool) tea.Cmd {
	ctx := a.ctx
	return func() tea.Msg {
		choices := map[string][]string{}
		for _, p := range r.Params {
			if p.Kind != ParamChoice || p.Choices == nil {
				continue
			}
			c, err := p.Choices(ctx)
			if err != nil {
				return optionsReadyMsg{err: fmt.Errorf("load %s choices: %w", p.Name, err)}
			}
			choices[p.Name] = c
		}
		return optionsReadyMsg{route: r, replace: replace, choices: choices}
	}
}

// handleMouse routes wheel and click events. The palette takes the wheel
// while open; otherwise events over the body go to the top page in body
// coordinates (below the header row).
func (a *app) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if a.prompt != nil {
		return nil
	}
	m := msg.Mouse()
	if a.palette != nil {
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			a.palette.scroll(m.Button)
		}
		return nil
	}
	_, bodyH := a.bodySize()
	m.Y -= headerHeight
	if m.Y < 0 || m.Y >= bodyH {
		return nil
	}
	if _, ok := msg.(tea.MouseClickMsg); ok {
		a.statusText, a.statusErr = "", false
		return a.top().page.update(tea.MouseClickMsg(m))
	}
	return a.top().page.update(tea.MouseWheelMsg(m))
}

// load starts loading a route's page in the background. replace is the page
// a reload will swap out, or nil to push a new page.
func (a *app) load(r Route, replace Page) tea.Cmd {
	a.palette = nil
	if a.loading != "" {
		a.setStatus("still loading "+a.loading, true)
		return nil
	}
	if r.Load == nil {
		a.setStatus(r.Command+" has no page", true)
		return nil
	}
	a.loading = r.label()
	ctx := a.ctx
	return tea.Batch(a.spinner.Tick, func() tea.Msg {
		page, err := r.Load(ctx, r.Values)
		return loadedMsg{route: r, page: page, err: err, replace: replace}
	})
}

func (a *app) loaded(msg loadedMsg) tea.Cmd {
	a.loading = ""
	if msg.err != nil {
		a.setStatus(msg.err.Error(), true)
		return nil
	}
	r := msg.route
	e := entry{route: &r, page: msg.page}
	if msg.replace != nil {
		if a.top().page != msg.replace {
			return nil
		}
		a.stack[len(a.stack)-1] = e
	} else {
		a.stack = append(a.stack, e)
	}
	return a.mount(msg.page)
}

// push mounts a page that was not opened through a route (a form).
func (a *app) push(p Page) tea.Cmd {
	a.stack = append(a.stack, entry{page: p})
	return a.mount(p)
}

func (a *app) mount(p Page) tea.Cmd {
	p.setStyles(a.st)
	w, h := a.bodySize()
	p.setSize(w, h)
	return p.init()
}

func (a *app) pop() {
	if len(a.stack) > 1 {
		a.stack = a.stack[:len(a.stack)-1]
	}
}

func (a *app) reload() tea.Cmd {
	top := a.top()
	if top.route == nil {
		return nil
	}
	return a.load(*top.route, top.page)
}

func (a *app) applyOutcome(msg outcomeMsg) tea.Cmd {
	if msg.pop {
		a.pop()
	}
	if msg.err != nil {
		a.setStatus(msg.err.Error(), true)
		return nil
	}
	o := msg.outcome
	if o.Info != "" {
		a.setStatus(o.Info, false)
	}
	var cmds []tea.Cmd
	if o.Reload {
		cmds = append(cmds, a.reload())
	}
	if o.Page != nil {
		cmds = append(cmds, a.push(o.Page))
	}
	return tea.Batch(cmds...)
}

func (a *app) setStatus(text string, isErr bool) {
	// Errors can quote repository content (paths, patterns).
	a.statusText, a.statusErr = clean(text), isErr
}

// resize recomputes the body size and propagates it to every page on the
// stack, so a page revealed by going back is already the right size.
func (a *app) resize() {
	w, h := a.bodySize()
	for _, e := range a.stack {
		e.page.setSize(w, h)
	}
	a.help.SetWidth(a.width)
}

// Chrome rows: the header above the body, and the status line below it
// (the help line's height varies).
const headerHeight, statusHeight = 1, 1

// bodySize is the area between the header row and the status and help rows.
func (a *app) bodySize() (int, int) {
	return a.width, max(0, a.height-headerHeight-statusHeight-a.helpHeight())
}

func (a *app) helpHeight() int {
	return lipgloss.Height(a.helpView())
}

func (a *app) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "kit · " + a.top().name()
	if a.width == 0 || a.height == 0 {
		return v
	}
	_, bodyH := a.bodySize()
	body := strings.Join(fitLines(a.top().page.view(), a.width, bodyH), "\n")
	screen := strings.Join([]string{a.headerView(), body, a.statusView(), a.helpView()}, "\n")
	if a.palette != nil {
		screen, v.Cursor = a.palette.overlay(screen, a.width, a.height)
	} else if c := a.top().page.cursor(); c != nil {
		c.Y += headerHeight
		v.Cursor = c
	}
	v.SetContent(a.st.app.Render(screen))
	return v
}

// name is the breadcrumb label for a stack entry.
func (e entry) name() string {
	if e.route != nil {
		return clean(e.route.label())
	}
	return clean(e.page.title())
}

// headerView is the breadcrumb trail on the left and the palette hint on the
// right: "kit › rules list › edit rule".
func (a *app) headerView() string {
	sep := a.st.crumbSep.Render(" › ")
	parts := []string{a.st.brand.Render("kit")}
	for i, e := range a.stack[1:] {
		style := a.st.crumb
		if i == len(a.stack)-2 {
			style = a.st.crumbCurrent
		}
		parts = append(parts, style.Render(e.name()))
	}
	left := " " + strings.Join(parts, sep)
	right := a.st.hint.Render("ctrl+p commands · ? help ")
	return spread(left, right, a.width)
}

// statusView shows, in priority order: a confirm prompt, a loading spinner,
// a transient message, or the page's own summary.
func (a *app) statusView() string {
	var left string
	switch {
	case a.prompt != nil:
		left = a.st.statusPrompt.Render(a.prompt.text + " [y/N]")
	case a.loading != "":
		left = a.spinner.View() + " " + a.st.statusText.Render("loading "+a.loading+"…")
	case a.statusText != "" && a.statusErr:
		left = a.st.statusError.Render("✗ " + a.statusText)
	case a.statusText != "":
		left = a.st.statusInfo.Render("✓ " + a.statusText)
	default:
		left = a.st.statusText.Render(a.top().page.status())
	}
	return " " + ansi.Truncate(left, max(0, a.width-2), "…")
}

// helpView is the short help line, or the full help grid when toggled.
func (a *app) helpView() string {
	var view string
	switch {
	case a.prompt != nil:
		view = a.help.ShortHelpView([]key.Binding{confirmKeys.Yes, confirmKeys.No})
	case a.palette != nil:
		view = a.help.ShortHelpView(paletteBindings())
	case a.showHelp:
		view = a.help.FullHelpView(a.fullHelp())
	default:
		short := a.top().page.bindings()
		if !a.top().page.capturing() {
			short = slices.Concat(short, a.routeBindings(), []key.Binding{globalKeys.Back, globalKeys.Quit})
		}
		view = a.help.ShortHelpView(short)
	}
	return " " + view
}

// routeBindings are the keys that act on the current route: options, when
// its command takes any.
func (a *app) routeBindings() []key.Binding {
	if r := a.top().route; r != nil && len(r.Params) > 0 {
		return []key.Binding{globalKeys.Options}
	}
	return nil
}

func (a *app) fullHelp() [][]key.Binding {
	page := a.top().page.bindings()
	global := []key.Binding{
		globalKeys.Back, globalKeys.Palette, globalKeys.Reload, globalKeys.Options,
		globalKeys.Help, globalKeys.Suspend, globalKeys.Quit,
	}
	// Columns of at most 6 keep the grid short.
	var groups [][]key.Binding
	for len(page) > 0 {
		n := min(6, len(page))
		groups = append(groups, page[:n])
		page = page[n:]
	}
	return append(groups, global)
}

// spread places left and right on one line of width w, truncating left
// first so the right side stays whole.
func spread(left, right string, w int) string {
	rw := ansi.StringWidth(right)
	if rw >= w {
		return ansi.Truncate(left, w, "…")
	}
	left = ansi.Truncate(left, w-rw-1, "…")
	gap := max(1, w-ansi.StringWidth(left)-rw)
	return left + strings.Repeat(" ", gap) + right
}

// errNoItem is reported when an action needs a selected row and there is none.
var errNoItem = errors.New("nothing selected")
