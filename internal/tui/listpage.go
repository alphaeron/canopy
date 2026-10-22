package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// Item is one row of a list page, or one leaf of a tree page.
type Item struct {
	Title       string
	Description string
	// Detail is shown in the detail pane while the row is hovered.
	Detail Detail
	// Route, when set, is opened by enter: the drill-down from one
	// command's page into another's (a rule into the files it matches,
	// a rules file into its rules). Without a route, enter focuses the
	// detail pane so it can be scrolled.
	Route *Route
	// Value is opaque caller data handed back to actions.
	Value any
	// Badge is a status glyph shown before the title.
	Badge Badge
}

// Action is a page-specific key bound to an operation on the hovered row,
// such as editing or removing a rule.
type Action struct {
	// Key is the key that triggers the action, e.g. "e".
	Key string
	// Help is the short description shown in the help line.
	Help string
	// RequiresItem disables the action when no row is selected.
	RequiresItem bool
	// Confirm, when non-nil, returns a question the user must answer
	// "y" to before Run is called.
	Confirm func(Item) string
	// Run performs the action off the UI loop.
	Run func(ctx context.Context, it Item) (Outcome, error)
}

func (a Action) binding() key.Binding {
	return key.NewBinding(key.WithKeys(a.Key), key.WithHelp(a.Key, a.Help))
}

// listRow adapts an Item to the bubbles list, remembering its index so the
// detail pane can key on identity rather than title.
type listRow struct {
	Item
	index int
}

func (r listRow) FilterValue() string { return r.Item.Title + " " + r.Item.Description }
func (r listRow) Title() string       { return withGlyph(r.Badge, r.Item.Title) }
func (r listRow) Description() string { return r.Item.Description }

type focusArea int

const (
	focusMain focusArea = iota
	focusDetail
	// focusAux is the third column on very wide terminals.
	focusAux
)

// ListPage is a filterable list with a detail pane that follows the cursor:
// the list on the left (or top) and the hovered row's Detail on the right
// (or bottom).
type ListPage struct {
	name     string
	summary  string
	st       *styles
	items    []Item
	actions  []Action
	list     list.Model
	delegate list.DefaultDelegate
	paneSplit
}

// NewListPage builds a list page titled name over items.
func NewListPage(name string, items []Item) *ListPage {
	st := newStyles(true)
	name = Clean(name)
	items = slices.Clone(items)
	rows := make([]list.Item, len(items))
	for i := range items {
		items[i] = cleanItem(items[i])
		rows[i] = listRow{Item: items[i], index: i}
	}
	d := list.NewDefaultDelegate()
	l := list.New(rows, d, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()
	// The terminal's own cursor blinks natively; a drawn cursor would
	// re-render the screen on a timer.
	l.FilterInput.SetVirtualCursor(false)
	// The app owns "?" for its own help.
	l.KeyMap.ShowFullHelp.SetEnabled(false)
	l.KeyMap.CloseFullHelp.SetEnabled(false)
	p := &ListPage{
		name:      name,
		items:     items,
		list:      l,
		delegate:  d,
		paneSplit: newPaneSplit(st),
	}
	for _, it := range items {
		p.wantAux = p.wantAux || detailHasTabs(it.Detail)
	}
	p.setStyles(st)
	return p
}

// WithActions adds row actions to the page.
func (p *ListPage) WithActions(actions ...Action) *ListPage {
	p.actions = append(p.actions, actions...)
	return p
}

// WithSummary sets the status-line summary; the default is the item count.
func (p *ListPage) WithSummary(s string) *ListPage {
	p.summary = s
	return p
}

func (p *ListPage) title() string { return p.name }

func (p *ListPage) status() string {
	if p.summary != "" {
		return p.summary
	}
	return countLabel(len(p.items), "item")
}

func (p *ListPage) init() tea.Cmd {
	p.syncDetail()
	return nil
}

func (p *ListPage) setStyles(st *styles) {
	p.st = st
	p.delegate.Styles = st.listItem
	p.list.SetDelegate(p.delegate)
	p.list.Styles = st.list
	p.detail.setStyles(st)
}

func (p *ListPage) setSize(w, h int) {
	p.list.SetSize(p.resize(w, h))
}

func (p *ListPage) capturing() bool { return p.list.SettingFilter() }

// cursor places the terminal cursor in the filter input, which is the first
// content row of the list pane.
func (p *ListPage) cursor() *tea.Cursor {
	if !p.list.SettingFilter() {
		return nil
	}
	c := p.list.FilterInput.Cursor()
	if c != nil {
		c.X += paneFrameWidth / 2
		c.Y += paneFrameHeight / 2
	}
	return c
}

func (p *ListPage) back() bool {
	if p.paneSplit.back() {
		return true
	}
	if p.list.IsFiltered() {
		p.list.ResetFilter()
		p.syncDetail()
		return true
	}
	return false
}

func (p *ListPage) update(msg tea.Msg) tea.Cmd {
	if m, ok := msg.(tea.MouseMsg); ok {
		return p.mouse(m)
	}
	if k, ok := msg.(tea.KeyPressMsg); ok && !p.list.SettingFilter() {
		if cmd, handled := p.handleKey(k); handled {
			return cmd
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	p.syncDetail()
	return cmd
}

func (p *ListPage) handleKey(k tea.KeyPressMsg) (tea.Cmd, bool) {
	if cmd, handled, relayout := p.paneSplit.handleKey(k); handled {
		if relayout {
			p.setSize(p.w, p.h)
		}
		return cmd, true
	}
	if key.Matches(k, paneKeys.Open) {
		return p.open(), true
	}
	for _, a := range p.actions {
		if key.Matches(k, a.binding()) {
			return p.runAction(a), true
		}
	}
	return nil, false
}

// open drills into the selected row: its route, or else the detail pane.
func (p *ListPage) open() tea.Cmd {
	it, ok := p.selected()
	switch {
	case !ok:
	case it.Route != nil:
		return navigate(*it.Route)
	default:
		p.focusDetailPane()
	}
	return nil
}

// mouse handles the wheel (move the selection, or scroll the detail column
// under the pointer) and left clicks (focus the pane; select a row, or open
// it when it is already selected; open a link in the detail pane).
func (p *ListPage) mouse(msg tea.MouseMsg) tea.Cmd {
	m := msg.Mouse()
	area, _, cy, ok := p.layout.hit(m.X, m.Y)
	if !ok || p.list.SettingFilter() {
		return nil
	}
	if area != focusMain {
		return p.paneSplit.mouse(msg, area, cy)
	}
	if _, wheel := msg.(tea.MouseWheelMsg); wheel {
		switch d := wheelDelta(m.Button); {
		case d < 0:
			p.list.CursorUp()
		case d > 0:
			p.list.CursorDown()
		}
		p.syncDetail()
		return nil
	}
	if m.Button != tea.MouseLeft {
		return nil
	}
	p.focus = area
	idx, ok := p.rowAt(cy)
	if !ok {
		return nil
	}
	if idx == p.list.Index() {
		return p.open()
	}
	p.list.Select(idx)
	p.syncDetail()
	return nil
}

// rowAt maps a content line of the list pane to a visible item index. The
// first line is the filter row; items follow, each the delegate's height
// plus its spacing, and the gaps between items map to nothing.
func (p *ListPage) rowAt(line int) (int, bool) {
	line--
	stride := p.delegate.Height() + p.delegate.Spacing()
	if line < 0 || line%stride >= p.delegate.Height() {
		return 0, false
	}
	start, end := p.list.Paginator.GetSliceBounds(len(p.list.VisibleItems()))
	idx := start + line/stride
	return idx, idx < end
}

func (p *ListPage) runAction(a Action) tea.Cmd {
	it, ok := p.selected()
	if a.RequiresItem && !ok {
		return func() tea.Msg { return outcomeMsg{err: errNoItem} }
	}
	return actionCmd(a, it)
}

// actionCmd runs an action, behind a confirm prompt when it asks for one.
func actionCmd(a Action, it Item) tea.Cmd {
	run := runCmd(func(ctx context.Context) (Outcome, error) { return a.Run(ctx, it) }, false)
	if a.Confirm != nil {
		return confirm(a.Confirm(it), run)
	}
	return run
}

func (p *ListPage) selected() (Item, bool) {
	row, ok := p.list.SelectedItem().(listRow)
	return row.Item, ok
}

func (p *ListPage) syncDetail() {
	row, ok := p.list.SelectedItem().(listRow)
	if !ok {
		p.detail.show("", "", Detail{})
		return
	}
	p.detail.show(strconv.Itoa(row.index), row.Item.Title, row.Detail)
}

func (p *ListPage) view() string {
	info := ""
	if n := len(p.list.VisibleItems()); n > 0 {
		info = fmt.Sprintf("%d/%d", p.list.Index()+1, n)
	}
	title := p.name
	if p.list.IsFiltered() && !p.list.SettingFilter() {
		title += " · filter: " + p.list.FilterValue()
	}
	return p.join(pane(p.st, title, info, p.list.View(), p.layout.mainW, p.layout.mainH, p.focus == focusMain))
}

// previewRows are the page's first n rows as detail rows, links included,
// for the home page's preview of a command.
func (p *ListPage) previewRows(n int) []Row {
	rows := make([]Row, 0, min(n, len(p.items)))
	for _, it := range p.items[:min(n, len(p.items))] {
		rows = append(rows, Row{Text: it.Title, Note: it.Description, Badge: it.Badge, Route: it.Route})
	}
	return rows
}

func (p *ListPage) bindings() []key.Binding {
	if p.list.SettingFilter() {
		return []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply filter")),
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		}
	}
	if p.focus != focusMain {
		return p.detailBindings()
	}
	b := []key.Binding{paneKeys.Up, paneKeys.Down, paneKeys.Open, paneKeys.Filter}
	for _, a := range p.actions {
		b = append(b, a.binding())
	}
	return append(b, p.paneBindings()...)
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

var _ Page = (*ListPage)(nil)
