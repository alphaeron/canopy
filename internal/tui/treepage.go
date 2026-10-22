package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/tree"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// TreeGroup is one expandable branch of a tree page, such as a rule and the
// files it applies to.
type TreeGroup struct {
	Title  string
	Detail Detail
	Items  []Item
	// Route, when set, is opened by enter on a group without items (a
	// command with no subcommands on the home page).
	Route *Route
	// Collapsed starts the group closed.
	Collapsed bool
	// Badge is a status glyph shown before the title.
	Badge Badge
}

// treeRef identifies a node: the root (group < 0), a group, or an item
// within a group (item < 0 for the group itself). It is the node's value, so the tree renders its
// String and the page recovers the selection from GivenValue instead of
// from the styled display text.
type treeRef struct {
	group, item int
	label       string
}

func (r treeRef) String() string { return r.label }

// TreePage shows groups of items as an expandable tree with a detail pane
// that follows the cursor. Enter (or l/→, h/←) expands and collapses a
// group; enter on an item opens its Route or focuses the detail pane.
type TreePage struct {
	name       string
	summary    string
	st         *styles
	groups     []TreeGroup
	tree       tree.Model
	rootDetail *Detail
	paneSplit
}

// NewTreePage builds a tree page titled name with every group expanded
// unless it is Collapsed.
func NewTreePage(name string, groups []TreeGroup) *TreePage {
	return newTreePage(name, "all "+strings.ToLower(clean(name)), groups)
}

// newTreePage builds a tree page whose root row reads rootLabel.
func newTreePage(name, rootLabel string, groups []TreeGroup) *TreePage {
	st := newStyles(true)
	name = clean(name)
	groups = slices.Clone(groups)
	for gi := range groups {
		g := &groups[gi]
		g.Title = clean(g.Title)
		g.Detail = cleanDetail(g.Detail)
		g.Items = slices.Clone(g.Items)
		for ii := range g.Items {
			g.Items[ii] = cleanItem(g.Items[ii])
		}
	}
	// The bubbles tree always renders its root row, so the root is a real
	// node summarizing the page rather than a blank line.
	root := tree.Root(treeRef{group: -1, item: -1, label: rootLabel})
	wantAux := false
	for gi, g := range groups {
		node := tree.Root(treeRef{group: gi, item: -1, label: withGlyph(g.Badge, g.Title)})
		wantAux = wantAux || detailHasTabs(g.Detail)
		for ii, it := range g.Items {
			node.Child(treeRef{group: gi, item: ii, label: withGlyph(it.Badge, it.Title)})
			wantAux = wantAux || detailHasTabs(it.Detail)
		}
		switch {
		case len(g.Items) == 0:
			// A leaf, so it draws no open/closed indicator.
			root.Child(treeRef{group: gi, item: -1, label: withGlyph(g.Badge, g.Title)})
		case g.Collapsed:
			root.Child(node.Close())
		default:
			root.Child(node.Open())
		}
	}
	t := tree.New(root, 0, 0)
	t.SetShowHelp(false)
	// The app owns "?" and ctrl+p.
	t.KeyMap.ShowFullHelp.SetEnabled(false)
	t.KeyMap.CloseFullHelp.SetEnabled(false)
	t.KeyMap.Up.SetKeys("up", "k")
	if len(groups) > 0 {
		t.Down()
	}
	p := &TreePage{name: name, groups: groups, tree: t, paneSplit: newPaneSplit(st)}
	p.wantAux = wantAux
	p.setStyles(st)
	return p
}

// withRootDetail replaces the root row's summary with d.
func (p *TreePage) withRootDetail(d Detail) *TreePage {
	d = cleanDetail(d)
	p.rootDetail = &d
	p.detail.refresh("root", p.rootLabel(), d)
	return p
}

// setDetail replaces a group's (item < 0) or item's detail, refreshing the
// pane when that node is shown: content that arrived after the page was
// built.
func (p *TreePage) setDetail(group, item int, d Detail) {
	d = cleanDetail(d)
	if item < 0 {
		p.groups[group].Detail = d
	} else {
		p.groups[group].Items[item].Detail = d
	}
	ref := treeRef{group: group, item: item}
	p.detail.refresh(ref.key(), p.refTitle(ref), d)
}

func (p *TreePage) rootLabel() string {
	return p.tree.Root().GivenValue().(treeRef).label
}

// key identifies a node for the detail pane.
func (r treeRef) key() string {
	switch {
	case r.group < 0:
		return "root"
	case r.item < 0:
		return fmt.Sprintf("g%d", r.group)
	}
	return fmt.Sprintf("g%d/%d", r.group, r.item)
}

func (p *TreePage) refTitle(r treeRef) string {
	switch {
	case r.group < 0:
		return p.rootLabel()
	case r.item < 0:
		return p.groups[r.group].Title
	}
	return p.groups[r.group].Items[r.item].Title
}

// WithSummary sets the status-line summary.
func (p *TreePage) WithSummary(s string) *TreePage {
	p.summary = s
	return p
}

func (p *TreePage) title() string { return p.name }

func (p *TreePage) status() string {
	if p.summary != "" {
		return p.summary
	}
	n := 0
	for _, g := range p.groups {
		n += len(g.Items)
	}
	return countLabel(len(p.groups), "group") + " · " + countLabel(n, "item")
}

func (p *TreePage) init() tea.Cmd {
	p.syncDetail()
	return nil
}

func (p *TreePage) setStyles(st *styles) {
	p.st = st
	ts := tree.DefaultStyles(st.isDark)
	ts.NodeStyle = lipgloss.NewStyle().Foreground(st.text)
	ts.ParentNodeStyle = lipgloss.NewStyle().Bold(true).Foreground(st.text)
	ts.RootNodeStyle = ts.ParentNodeStyle
	ts.SelectedNodeStyle = lipgloss.NewStyle().Bold(true).Foreground(st.accent)
	ts.CursorStyle = lipgloss.NewStyle().PaddingRight(1).Bold(true).Foreground(st.accent)
	ts.EnumeratorStyle = lipgloss.NewStyle().Foreground(st.subtle)
	ts.SelectedEnumeratorStyle = ts.EnumeratorStyle
	ts.IndenterStyle = ts.EnumeratorStyle
	ts.OpenIndicatorStyle = lipgloss.NewStyle().Foreground(st.muted)
	p.tree.SetStyles(ts)
	p.detail.setStyles(st)
}

func (p *TreePage) setSize(w, h int) {
	p.tree.SetSize(p.resize(w, h))
	// Re-apply the tree's scroll-off against the new height: positioning
	// the cursor before the first size scrolled a zero-height viewport,
	// which would otherwise hide the root row until the cursor moves.
	p.tree.SetYOffset(p.tree.YOffset())
}

func (p *TreePage) capturing() bool { return false }

func (p *TreePage) cursor() *tea.Cursor { return nil }

func (p *TreePage) back() bool { return p.paneSplit.back() }

func (p *TreePage) update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(tea.MouseMsg); ok {
		return p.mouse(m)
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}
	if cmd, handled, relayout := p.handleKey(k); handled {
		if relayout {
			p.setSize(p.w, p.h)
		}
		return cmd
	}
	if key.Matches(k, paneKeys.Open) {
		return p.open()
	}
	var cmd tea.Cmd
	p.tree, cmd = p.tree.Update(msg)
	p.syncDetail()
	return cmd
}

// open acts on the selected node: an item opens its route or focuses the
// detail pane, a group without items opens its route, and any other group
// expands or collapses.
func (p *TreePage) open() tea.Cmd {
	if it, ok := p.selectedItem(); ok {
		if it.Route != nil {
			return navigate(*it.Route)
		}
		p.focusDetailPane()
		return nil
	}
	if ref, ok := p.selectedRef(); ok && ref.group >= 0 {
		if g := p.groups[ref.group]; len(g.Items) == 0 {
			if g.Route != nil {
				return navigate(*g.Route)
			}
			p.focusDetailPane()
			return nil
		}
	}
	p.tree.ToggleCurrentNode()
	p.syncDetail()
	return nil
}

// mouse handles the wheel (move the selection, or scroll the detail pane
// under the pointer) and left clicks (focus the pane; select a node, or
// open it when it is already selected). Every node is one line, so a
// content line plus the viewport offset is the node's offset.
func (p *TreePage) mouse(msg tea.MouseMsg) tea.Cmd {
	m := msg.Mouse()
	area, _, cy, ok := p.layout.hit(m.X, m.Y)
	if !ok {
		return nil
	}
	if area != focusMain {
		return p.paneSplit.mouse(msg, area, cy)
	}
	if _, wheel := msg.(tea.MouseWheelMsg); wheel {
		switch d := wheelDelta(m.Button); {
		case d < 0:
			p.tree.Up()
		case d > 0:
			p.tree.Down()
		}
		p.syncDetail()
		return nil
	}
	if m.Button != tea.MouseLeft {
		return nil
	}
	p.focus = area
	if cy < 0 {
		return nil
	}
	line := cy + p.tree.ViewportYOffset()
	if p.tree.Node(line) == nil {
		return nil
	}
	if line == p.tree.YOffset() {
		return p.open()
	}
	p.tree.SetYOffset(line)
	p.syncDetail()
	return nil
}

// selectedRef is the hovered node's identity, false for the invisible root.
func (p *TreePage) selectedRef() (treeRef, bool) {
	node := p.tree.NodeAtCurrentOffset()
	if node == nil {
		return treeRef{}, false
	}
	ref, ok := node.GivenValue().(treeRef)
	return ref, ok
}

func (p *TreePage) selectedItem() (Item, bool) {
	ref, ok := p.selectedRef()
	if !ok || ref.group < 0 || ref.item < 0 {
		return Item{}, false
	}
	return p.groups[ref.group].Items[ref.item], true
}

func (p *TreePage) syncDetail() {
	ref, ok := p.selectedRef()
	switch {
	case !ok:
		p.detail.show("", "", Detail{})
	case ref.group < 0 && p.rootDetail != nil:
		p.detail.show(ref.key(), ref.label, *p.rootDetail)
	case ref.group < 0:
		p.detail.show(ref.key(), ref.label, Detail{Fields: []Field{{Label: "Summary", Value: p.status()}}})
	case ref.item < 0:
		p.detail.show(ref.key(), p.groups[ref.group].Title, p.groups[ref.group].Detail)
	default:
		it := p.groups[ref.group].Items[ref.item]
		p.detail.show(ref.key(), it.Title, it.Detail)
	}
}

// previewRows are the page's first n groups as detail rows, for the home
// page's preview of a command.
func (p *TreePage) previewRows(n int) []Row {
	rows := make([]Row, 0, min(n, len(p.groups)))
	for _, g := range p.groups[:min(n, len(p.groups))] {
		r := Row{Text: g.Title, Badge: g.Badge, Route: g.Route}
		if len(g.Items) > 0 {
			r.Note = countLabel(len(g.Items), "item")
		}
		rows = append(rows, r)
	}
	return rows
}

func (p *TreePage) view() string {
	content := p.tree.View()
	if len(p.groups) == 0 {
		content = p.st.empty.Render("Nothing to show")
	}
	return p.join(pane(p.st, p.name, "", content, p.layout.mainW, p.layout.mainH, p.focus == focusMain))
}

func (p *TreePage) bindings() []key.Binding {
	if p.focus != focusMain {
		return p.detailBindings()
	}
	b := []key.Binding{
		paneKeys.Up, paneKeys.Down,
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "expand/open")),
		key.NewBinding(key.WithKeys("h", "l"), key.WithHelp("←/→", "collapse/expand")),
	}
	return append(b, p.paneBindings()...)
}

var _ Page = (*TreePage)(nil)
