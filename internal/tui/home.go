package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alphaeron/kit/internal/brand"
)

// Home-page preview tuning. Previews wait for the cursor to settle so
// holding j does not load every command, and show at most previewRowsMax
// rows of the page they preview.
const (
	previewDelay   = 150 * time.Millisecond
	previewRowsMax = 12
)

// homeTarget marks messages for the home page, which the app delivers to it
// even when another page is on top: a preview that finishes after the user
// navigated away still lands in the home page's cache.
type homeTarget interface{ forHome() }

type (
	// previewDueMsg fires when the cursor has rested on node key; seq
	// tells a stale timer from the current one.
	previewDueMsg struct {
		key string
		seq int
	}
	// previewMsg carries a loaded preview for node key.
	previewMsg struct {
		key     string
		preview preview
	}
	// overviewMsg carries the repository overview for the root row.
	overviewMsg struct {
		detail Detail
		err    error
	}
)

func (previewDueMsg) forHome() {}
func (previewMsg) forHome()    {}
func (overviewMsg) forHome()   {}

// preview is what the home page learned by loading a command: its one-line
// summary and its first rows.
type preview struct {
	summary string
	rows    []Row
	err     error
}

// previewer is a page that can describe its first rows for a preview.
type previewer interface {
	previewRows(n int) []Row
}

// homeNode is a command on the home tree.
type homeNode struct {
	group, item int
	route       *Route
	base        Detail
}

// homePage is the TUI equivalent of `--help`, as a tree of the command
// hierarchy: a top-level command is a group whose items are its
// subcommands. The detail pane describes the hovered command (usage, flags,
// subcommands as links) and, once the cursor rests, previews it live: the
// summary of the page it would open and that page's first rows. The root
// row is the repository overview.
type homePage struct {
	*TreePage
	ctx      context.Context
	delay    time.Duration
	commands int
	nodes    map[string]homeNode
	overview func(context.Context) (Detail, error)
	cache    map[string]preview
	selKey   string
	seq      int
	// active is set once home has been shown (init or revealed): a
	// deep-linked session never previews commands behind its start page.
	active bool
	cancel context.CancelFunc
}

// newHomePage builds the home tree from routes, which are listed in
// `--help` order: "rules list" is an item of the "rules" group. Utility
// commands (help) come last, their groups collapsed.
func newHomePage(ctx context.Context, routes []Route, overview func(context.Context) (Detail, error)) *homePage {
	type group struct {
		route    *Route
		children []*Route
		utility  bool
	}
	var order []string
	groups := map[string]*group{}
	for i := range routes {
		r := &routes[i]
		top, _, _ := strings.Cut(r.Command, " ")
		g, ok := groups[top]
		if !ok {
			g = &group{}
			groups[top] = g
			order = append(order, top)
		}
		g.utility = g.utility || r.Utility
		if r.Command == top {
			g.route = r
		} else {
			g.children = append(g.children, r)
		}
	}
	// A subcommand whose parent is not a route stands on its own.
	for i := 0; i < len(order); i++ {
		g := groups[order[i]]
		if g.route != nil {
			continue
		}
		var own []string
		for _, c := range g.children {
			groups[c.Command] = &group{route: c, utility: c.Utility}
			own = append(own, c.Command)
		}
		delete(groups, order[i])
		order = slices.Replace(order, i, i+1, own...)
		i += len(own) - 1
	}
	slices.SortStableFunc(order, func(a, b string) int {
		ua, ub := groups[a].utility, groups[b].utility
		switch {
		case ua == ub:
			return 0
		case ub:
			return -1
		}
		return 1
	})

	h := &homePage{ctx: ctx, delay: previewDelay, commands: len(routes), nodes: map[string]homeNode{}, overview: overview, cache: map[string]preview{}}
	tgs := make([]TreeGroup, 0, len(order))
	for gi, top := range order {
		g := groups[top]
		tg := TreeGroup{Title: top, Route: g.route, Collapsed: g.utility && len(g.children) > 0}
		tg.Detail = commandDetail(g.route, g.children)
		h.nodes[treeRef{group: gi, item: -1}.key()] = homeNode{group: gi, item: -1, route: g.route, base: tg.Detail}
		for ii, c := range g.children {
			d := commandDetail(c, nil)
			tg.Items = append(tg.Items, Item{Title: strings.TrimPrefix(c.Command, top+" "), Description: c.Description, Detail: d, Route: c})
			h.nodes[treeRef{group: gi, item: ii}.key()] = homeNode{group: gi, item: ii, route: c, base: d}
		}
		tgs = append(tgs, tg)
	}
	h.TreePage = newTreePage("Commands", brand.Command, tgs).WithSummary(countLabel(len(routes), "command"))
	// Every command can grow a Preview tab, so the third column is wanted
	// even though no row has a tab yet.
	h.wantAux = true
	root := Detail{Fields: []Field{{Label: "Commands", Value: countLabel(len(routes), "command")}}}
	if overview != nil {
		root.Sections = []Section{{Title: "Repository", Lines: []string{"loading…"}}}
	}
	h.withRootDetail(root)
	return h
}

// commandDetail describes a command for the home page: its invocation, what
// opening it does, its long help, its subcommands as links, and the
// arguments and flags the options form (o) edits.
func commandDetail(r *Route, children []*Route) Detail {
	d := Detail{Fields: []Field{
		{Label: "Command", Value: r.CommandLine()},
		{Label: "About", Value: r.Description},
		{Label: "Opens", Value: opensLabel(r, len(children) > 0)},
	}}
	if r.Long != "" && strings.TrimSpace(r.Long) != r.Description {
		d.Sections = append(d.Sections, Section{Title: "Help", Lines: reflow(r.Long)})
	}
	if len(children) > 0 {
		rows := make([]Row, len(children))
		for i, c := range children {
			rows[i] = Row{Text: strings.TrimPrefix(c.Command, r.Command+" "), Note: c.Description, Route: c}
		}
		d.Sections = append(d.Sections, Section{Title: "Subcommands", Rows: rows})
	}
	var opts []string
	for _, p := range r.Params {
		opts = append(opts, paramUsage(p)+"  "+p.Help)
	}
	if len(opts) > 0 {
		title := "Options (o on the page)"
		if r.Run != nil {
			title = "Form fields"
		}
		d.Sections = append(d.Sections, Section{Title: title, Lines: opts})
	}
	return d
}

// reflow joins help text's hard-wrapped lines into paragraphs, so the pane
// wraps them to its own width. Blank lines separate paragraphs, and
// indented lines (examples, flag tables) are kept as they are.
func reflow(text string) []string {
	var out []string
	para := ""
	flush := func() {
		if para != "" {
			out = append(out, para)
			para = ""
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		switch {
		case strings.TrimSpace(line) == "":
			flush()
			out = append(out, "")
		case line[0] == ' ' || line[0] == '\t':
			flush()
			out = append(out, line)
		case para == "":
			para = line
		default:
			para += " " + line
		}
	}
	flush()
	return out
}

// opensLabel says what enter on a command does.
func opensLabel(r *Route, group bool) string {
	switch {
	case r.Run != nil:
		return "a form; submitting it changes files"
	case r.missingRequired():
		for _, p := range r.Params {
			if p.Required {
				return "asks for " + paramUsage(p) + " first"
			}
		}
	case group:
		return "its subcommands (below)"
	}
	return "a page"
}

// previewable reports whether the home page loads r to preview it: a page
// that needs no input, or a command with its own Summary.
func previewable(r *Route) bool {
	return r.Summary != nil || r.Load != nil && r.Run == nil && !r.missingRequired()
}

func (h *homePage) init() tea.Cmd {
	h.active = true
	return tea.Batch(h.TreePage.init(), h.loadOverview(), h.selectionChanged())
}

func (h *homePage) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case previewDueMsg:
		if msg.seq != h.seq {
			return nil
		}
		return h.loadPreview(msg.key)
	case previewMsg:
		if errors.Is(msg.preview.err, context.Canceled) {
			return nil
		}
		h.cache[msg.key] = msg.preview
		h.applyPreview(msg.key, msg.preview)
		return nil
	case overviewMsg:
		d := msg.detail
		if msg.err != nil {
			d = Detail{Fields: []Field{{Label: "Repository", Value: msg.err.Error(), Badge: BadgeFail}}}
		}
		d.Fields = append([]Field{{Label: "Commands", Value: countLabel(h.commands, "command")}}, d.Fields...)
		h.withRootDetail(d)
		return nil
	}
	return tea.Batch(h.TreePage.update(msg), h.selectionChanged())
}

// revealed runs when the home page is shown again after another page: what
// a command would show may have changed (a rule was added), so previews and
// the overview are loaded afresh.
func (h *homePage) revealed() tea.Cmd {
	h.active = true
	clear(h.cache)
	for _, n := range h.nodes {
		h.setDetail(n.group, n.item, n.base)
	}
	h.selKey = ""
	return tea.Batch(h.loadOverview(), h.selectionChanged())
}

// selectionChanged starts the preview of a newly hovered command: from the
// cache, or after the cursor has rested for previewDelay.
func (h *homePage) selectionChanged() tea.Cmd {
	ref, ok := h.selectedRef()
	if !ok || !h.active {
		return nil
	}
	key := ref.key()
	if key == h.selKey {
		return nil
	}
	h.selKey = key
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	n, ok := h.nodes[key]
	if !ok || !previewable(n.route) {
		return nil
	}
	if p, ok := h.cache[key]; ok {
		h.applyPreview(key, p)
		return nil
	}
	h.seq++
	seq := h.seq
	return tea.Tick(h.delay, func(time.Time) tea.Msg { return previewDueMsg{key: key, seq: seq} })
}

// loadPreview loads node key's command off the UI loop. Moving the cursor
// cancels it.
func (h *homePage) loadPreview(key string) tea.Cmd {
	n, ok := h.nodes[key]
	if !ok {
		return nil
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.cancel = cancel
	r := *n.route
	return func() tea.Msg {
		defer cancel()
		return previewMsg{key: key, preview: loadPreview(ctx, r)}
	}
}

func loadPreview(ctx context.Context, r Route) preview {
	if r.Summary != nil {
		s, err := r.Summary(ctx)
		return preview{summary: s, err: err}
	}
	page, err := r.Load(ctx, r.Values)
	if err != nil {
		return preview{err: err}
	}
	if err := ctx.Err(); err != nil {
		return preview{err: err}
	}
	p := preview{summary: page.status()}
	if pv, ok := page.(previewer); ok {
		p.rows = pv.previewRows(previewRowsMax)
	}
	return p
}

// applyPreview shows a preview in node key's detail: the summary as a "Now"
// field and the rows in a Preview tab.
func (h *homePage) applyPreview(key string, p preview) {
	n, ok := h.nodes[key]
	if !ok {
		return
	}
	d := Detail{Fields: slices.Clone(n.base.Fields), Sections: n.base.Sections, Tabs: slices.Clone(n.base.Tabs)}
	now := Field{Label: "Now", Value: p.summary}
	if p.err != nil {
		now = Field{Label: "Now", Value: "could not load: " + p.err.Error(), Badge: BadgeFail}
	}
	d.Fields = slices.Insert(d.Fields, min(2, len(d.Fields)), now)
	if len(p.rows) > 0 {
		d.Tabs = append(d.Tabs, Tab{Title: "Preview", Sections: []Section{{Title: "First rows · enter opens", Rows: p.rows}}})
	}
	h.setDetail(n.group, n.item, d)
}

func (h *homePage) loadOverview() tea.Cmd {
	if h.overview == nil {
		return nil
	}
	ctx, overview := h.ctx, h.overview
	return func() tea.Msg {
		d, err := overview(ctx)
		return overviewMsg{detail: d, err: err}
	}
}

// paramUsage is how a parameter is written on the command line:
// "<pattern>", "[pattern...]", or "--scope".
func paramUsage(p Param) string {
	if !p.Arg {
		return "--" + p.Name
	}
	name := p.Name
	if p.Variadic {
		name += "..."
	}
	if p.Required {
		return "<" + name + ">"
	}
	return "[" + name + "]"
}
