package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Detail is the drill-down content shown beside the hovered row of a list or
// tree: a block of labelled fields followed by titled sections, and
// optionally further views of the same row (Tabs). Callers describe what to
// show; the TUI owns how it is styled.
type Detail struct {
	Fields   []Field
	Sections []Section
	// Tabs are further views of the row, after the implicit "Summary" view
	// made of Fields and Sections: the rule chain, the event trace, a diff.
	// [ and ] switch between them; on very wide terminals the summary stays
	// in its own column and the selected tab fills a third one.
	Tabs []Tab
}

// Tab is one named view of a Detail beyond its summary.
type Tab struct {
	Title    string
	Fields   []Field
	Sections []Section
}

// Field is one "label  value" line in a Detail. A Badge, when set, precedes
// the value.
type Field struct {
	Label string
	Value string
	Badge Badge
}

// Section is a titled block in a Detail: plain Lines, then structured Rows.
// A Diff section colors unified-diff lines (additions, deletions, hunk
// headers).
type Section struct {
	Title string
	Lines []string
	Diff  bool
	Rows  []Row
}

// Row is one structured line of a Section: indented text with an optional
// status badge, approval meter, and muted note. A Row with a Route is a link:
// when the detail pane is focused, ↑/↓ move between links and enter opens
// the selected one.
type Row struct {
	Text   string
	Note   string
	Indent int
	Badge  Badge
	Meter  *Meter
	Route  *Route
}

// Meter is an approval count against its requirement, drawn as "▓▓░ 2/3".
type Meter struct {
	Have, Need int
}

// Badge is a status glyph shown before a row, field value, or list title.
type Badge int

const (
	BadgeNone Badge = iota
	// BadgeOK is a satisfied or healthy state.
	BadgeOK
	// BadgePending is a state waiting on something, such as approvals.
	BadgePending
	// BadgeFail is an error or an unsatisfiable state.
	BadgeFail
	// BadgeOff is an inactive or skipped state.
	BadgeOff
)

// Glyph is the badge's character, or "" for BadgeNone.
func (b Badge) Glyph() string {
	switch b {
	case BadgeOK:
		return "✓"
	case BadgePending:
		return "●"
	case BadgeFail:
		return "✗"
	case BadgeOff:
		return "○"
	}
	return ""
}

// withGlyph prefixes s with b's glyph, uncolored, for places whose style is
// applied by a bubbles component (list and tree rows).
func withGlyph(b Badge, s string) string {
	if g := b.Glyph(); g != "" {
		return g + " " + s
	}
	return s
}

// IsZero reports whether the detail has nothing to show.
func (d Detail) IsZero() bool {
	return len(d.Fields) == 0 && len(d.Sections) == 0 && len(d.Tabs) == 0
}

// views are the detail's tabs, the summary first.
func (d Detail) views() []Tab {
	return append([]Tab{{Title: "Summary", Fields: d.Fields, Sections: d.Sections}}, d.Tabs...)
}

// link is a Row with a Route at a position in rendered detail content.
type link struct {
	line, height int
	route        Route
}

// rendered is a detail view drawn for one width: its content and where its
// links landed, so the pane can select, scroll to, and open them.
type rendered struct {
	content string
	links   []link
}

// renderView draws v for a pane whose content area is width cells wide,
// highlighting link sel (-1 for none). Every line is wrapped here, never by
// the viewport, so link positions are exact rendered line numbers.
func renderView(st *styles, v Tab, width, sel int) rendered {
	width = max(1, width)
	if len(v.Fields) == 0 && len(v.Sections) == 0 {
		return rendered{content: st.empty.Render("No details")}
	}
	var out rendered
	var blocks []string
	line := 0
	add := func(block string) {
		blocks = append(blocks, block)
		line += lipgloss.Height(block) + 1 // the blank line between blocks
	}
	if len(v.Fields) > 0 {
		add(renderFields(st, v.Fields, width))
	}
	for _, s := range v.Sections {
		var b strings.Builder
		b.WriteString(st.sectionTitle.Render(ansi.Truncate(s.Title, width, "…")))
		at := line + 1
		if len(s.Lines) == 0 && len(s.Rows) == 0 {
			b.WriteString("\n" + st.empty.Render("none"))
		}
		for _, l := range s.Lines {
			var text string
			if s.Diff {
				text = styleDiffLine(st, ansi.Hardwrap(l, width, true))
			} else {
				text = st.value.Render(ansi.Wrap(l, width, ""))
			}
			b.WriteString("\n" + text)
			at += lipgloss.Height(text)
		}
		for _, r := range s.Rows {
			selected := r.Route != nil && len(out.links) == sel
			text := renderRow(st, r, width, selected)
			h := lipgloss.Height(text)
			if r.Route != nil {
				out.links = append(out.links, link{line: at, height: h, route: *r.Route})
			}
			b.WriteString("\n" + text)
			at += h
		}
		add(b.String())
	}
	out.content = strings.Join(blocks, "\n\n")
	return out
}

// renderFields aligns labels and wraps each value under itself.
func renderFields(st *styles, fields []Field, width int) string {
	labelW := 0
	for _, f := range fields {
		labelW = max(labelW, ansi.StringWidth(f.Label))
	}
	labelW = min(labelW, max(1, width/2))
	valueW := max(1, width-labelW-2)
	rows := make([]string, 0, len(fields))
	for _, f := range fields {
		label := st.label.Width(labelW + 2).Render(ansi.Truncate(f.Label, labelW, "…"))
		value := st.value.Width(valueW).Render(orDash(f.Value))
		if g := f.Badge.Glyph(); g != "" {
			value = st.value.Width(valueW).Render(st.badge(f.Badge).Render(g) + " " + orDash(f.Value))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, label, value))
	}
	return strings.Join(rows, "\n")
}

// renderRow draws a row: a two-cell gutter that marks the selected link,
// its indent, badge, text, meter, and note, wrapped to width with
// continuation lines indented under the text.
func renderRow(st *styles, r Row, width int, selected bool) string {
	gutter := "  "
	if selected {
		gutter = st.linkSelected.Render("▸ ")
	}
	lead := strings.Repeat("  ", r.Indent)
	var parts []string
	if g := r.Badge.Glyph(); g != "" {
		parts = append(parts, st.badge(r.Badge).Render(g))
	}
	text := st.value
	switch {
	case selected:
		text = st.linkSelected
	case r.Route != nil:
		text = st.link
	}
	parts = append(parts, text.Render(r.Text))
	if r.Meter != nil {
		parts = append(parts, renderMeter(st, *r.Meter))
	}
	body := strings.Join(parts, " ")
	if r.Note != "" {
		body += "  " + st.note.Render(r.Note)
	}
	hang := 2 + ansi.StringWidth(lead)
	if r.Badge != BadgeNone {
		hang += 2 // continuation lines align with the text, not the badge
	}
	body = ansi.Wrap(body, max(1, width-hang), "")
	pad := strings.Repeat(" ", hang)
	lines := strings.Split(body, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = gutter + lead + lines[i]
		} else {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// meterCells caps a meter's width so a large requirement stays one line.
const meterCells = 10

func renderMeter(st *styles, m Meter) string {
	need := max(1, m.Need)
	cells := min(need, meterCells)
	filled := min(cells, m.Have*cells/need)
	style := st.badge(BadgePending)
	if m.Have >= m.Need {
		style = st.badge(BadgeOK)
	}
	bar := style.Render(strings.Repeat("▓", filled)) + st.note.Render(strings.Repeat("░", cells-filled))
	return bar + " " + style.Render(strconv.Itoa(m.Have)+"/"+strconv.Itoa(m.Need))
}

func styleDiffLine(st *styles, line string) string {
	switch {
	case strings.HasPrefix(line, "@@"):
		return st.diffHunk.Render(line)
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return st.diffAdd.Render(line)
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return st.diffDel.Render(line)
	default:
		return st.value.Render(line)
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// detailView is one scrollable column of the detail pane: a viewport over a
// rendered Tab and the selected link within it.
type detailView struct {
	vp    viewport.Model
	view  Tab
	links []link
	sel   int // selected link, -1 for none
}

func newDetailView() detailView {
	return detailView{vp: viewport.New(), sel: -1}
}

// set replaces the view's content and scrolls back to the top.
func (v *detailView) set(st *styles, t Tab) {
	v.view = t
	v.sel = -1
	v.render(st)
	v.vp.GotoTop()
}

func (v *detailView) render(st *styles) {
	r := renderView(st, v.view, v.vp.Width(), v.sel)
	v.links = r.links
	if v.sel >= len(v.links) {
		v.sel = -1
	}
	v.vp.SetContent(r.content)
}

func (v *detailView) setSize(st *styles, w, h int) {
	v.vp.SetWidth(max(0, w))
	v.vp.SetHeight(max(0, h))
	v.render(st)
}

// step moves the link selection by delta (±1), scrolling to keep it in
// view. Past the first or last link it scrolls instead, so content beyond
// the links stays reachable.
func (v *detailView) step(st *styles, delta int) {
	next := v.sel + delta
	if v.sel < 0 && delta > 0 {
		next = v.firstVisibleLink()
	}
	if next < 0 || next >= len(v.links) {
		if delta < 0 {
			v.vp.ScrollUp(1)
		} else {
			v.vp.ScrollDown(1)
		}
		return
	}
	v.sel = next
	v.render(st)
	l := v.links[next]
	switch {
	case l.line < v.vp.YOffset():
		v.vp.SetYOffset(l.line)
	case l.line+l.height > v.vp.YOffset()+v.vp.Height():
		v.vp.SetYOffset(l.line + l.height - v.vp.Height())
	}
}

// firstVisibleLink is the first link at or below the top of the viewport,
// so the first ↓ selects what the user is looking at.
func (v *detailView) firstVisibleLink() int {
	for i, l := range v.links {
		if l.line >= v.vp.YOffset() {
			return i
		}
	}
	return len(v.links)
}

// linkAt is the link on content line y of the viewport, if any.
func (v *detailView) linkAt(y int) (int, bool) {
	line := y + v.vp.YOffset()
	for i, l := range v.links {
		if line >= l.line && line < l.line+l.height {
			return i, true
		}
	}
	return 0, false
}

func (v *detailView) selected() (Route, bool) {
	if v.sel < 0 || v.sel >= len(v.links) {
		return Route{}, false
	}
	return v.links[v.sel].route, true
}

// detailPane is the right (or lower) pane that previews the hovered row. It
// follows the cursor of its owning page and takes keys only when focused.
// On very wide terminals it splits into two columns: the summary, and the
// selected tab beside it.
type detailPane struct {
	st     *styles
	main   detailView
	aux    detailView
	key    string
	title  string
	detail Detail
	// tab is the selected index into detail.views(). tabTitle remembers
	// it by name so moving to another row keeps the same view open.
	tab      int
	tabTitle string
	// split shows the summary in main and the selected tab in aux.
	split bool
	w, h  int
	auxW  int
}

func newDetailPane(st *styles) detailPane {
	return detailPane{st: st, main: newDetailView(), aux: newDetailView(), key: "\x00"}
}

// show replaces the pane's content with the row identified by key and
// scrolls back to the top. Re-showing the same key is a no-op, so pages can
// call show after every update to follow the cursor without re-rendering or
// resetting the scroll position.
func (d *detailPane) show(key, title string, detail Detail) {
	if key == d.key {
		return
	}
	d.key = key
	d.replace(title, detail)
}

// refresh replaces the content of the row identified by key, keeping the
// selected tab, when that row is still shown: content that arrived later,
// such as a home-page preview.
func (d *detailPane) refresh(key, title string, detail Detail) {
	if key == d.key {
		d.replace(title, detail)
	}
}

func (d *detailPane) replace(title string, detail Detail) {
	d.title = title
	d.detail = detail
	d.tab = 0
	for i, v := range detail.views() {
		if v.Title == d.tabTitle {
			d.tab = i
		}
	}
	d.apply()
}

// apply renders the selected views into the columns.
func (d *detailPane) apply() {
	views := d.detail.views()
	if d.split && d.spreadSummary() {
		// No tabs to fill the third column: the summary's fields stay
		// beside the list and its sections move over.
		d.main.set(d.st, Tab{Fields: views[0].Fields})
		d.aux.set(d.st, Tab{Sections: views[0].Sections})
		return
	}
	if d.split {
		d.main.set(d.st, views[0])
		if t := d.auxTab(); t > 0 {
			d.aux.set(d.st, views[t])
		} else {
			d.aux.set(d.st, Tab{})
		}
		return
	}
	d.main.set(d.st, views[min(d.tab, len(views)-1)])
}

// spreadSummary reports whether a detail without tabs is spread over both
// columns: its fields in the first and its sections in the second.
func (d *detailPane) spreadSummary() bool {
	return len(d.detail.Tabs) == 0 && len(d.detail.Fields) > 0 && len(d.detail.Sections) > 0
}

// auxTab is the view shown in the third column: the selected tab, or the
// first one when the summary is selected; 0 when there is none.
func (d *detailPane) auxTab() int {
	n := len(d.detail.views())
	switch {
	case n < 2:
		return 0
	case d.tab >= 1 && d.tab < n:
		return d.tab
	default:
		return 1
	}
}

// cycleTab selects the next (delta > 0) or previous view. In split mode the
// summary is always visible, so only the tabs cycle.
func (d *detailPane) cycleTab(delta int) {
	n := len(d.detail.views())
	first := 0
	if d.split {
		first = 1
		d.tab = d.auxTab()
	}
	if n-first < 2 {
		return
	}
	d.tab = first + ((d.tab-first+delta)%(n-first)+(n-first))%(n-first)
	d.tabTitle = d.detail.views()[d.tab].Title
	if d.split {
		d.aux.set(d.st, d.detail.views()[d.tab])
	} else {
		d.main.set(d.st, d.detail.views()[d.tab])
	}
}

// canCycle reports whether [ and ] would change the view.
func (d *detailPane) canCycle() bool {
	n := len(d.detail.views())
	if d.split {
		return n > 2
	}
	return n > 1
}

// setSize lays the pane out as one column w wide, or, with auxW > 0, as two
// columns: the summary w wide and the tab column auxW wide.
func (d *detailPane) setSize(w, h, auxW int) {
	d.w, d.h, d.auxW = w, h, auxW
	split := auxW > 0
	d.main.setSize(d.st, w-paneFrameWidth, h-paneFrameHeight)
	d.aux.setSize(d.st, auxW-paneFrameWidth, h-paneFrameHeight)
	if split != d.split {
		d.split = split
		d.apply()
	}
}

func (d *detailPane) setStyles(st *styles) {
	d.st = st
	d.main.render(st)
	d.aux.render(st)
}

func (d *detailPane) column(area focusArea) *detailView {
	if area == focusAux {
		return &d.aux
	}
	return &d.main
}

// update handles a key while the pane (column area) is focused: ↑/↓ move
// between links or scroll, enter opens the selected link, and the rest
// scrolls the viewport.
func (d *detailPane) update(area focusArea, msg tea.Msg) tea.Cmd {
	v := d.column(area)
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(k, paneKeys.Up):
			v.step(d.st, -1)
			return nil
		case key.Matches(k, paneKeys.Down):
			v.step(d.st, 1)
			return nil
		case key.Matches(k, paneKeys.Open):
			if r, ok := v.selected(); ok {
				return navigate(r)
			}
			return nil
		}
	}
	var cmd tea.Cmd
	v.vp, cmd = v.vp.Update(msg)
	return cmd
}

// click opens the link on content line y of a column, if there is one.
func (d *detailPane) click(area focusArea, y int) tea.Cmd {
	v := d.column(area)
	i, ok := v.linkAt(y)
	if !ok {
		return nil
	}
	v.sel = i
	v.render(d.st)
	return navigate(v.links[i].route)
}

func (d *detailPane) scroll(area focusArea, delta int) {
	v := d.column(area)
	if delta < 0 {
		v.vp.ScrollUp(detailWheelLines)
	} else {
		v.vp.ScrollDown(detailWheelLines)
	}
}

// hasLinks reports whether the focused column has links to move between.
func (d *detailPane) hasLinks(area focusArea) bool { return len(d.column(area).links) > 0 }

// detailHasTabs reports whether any of a page's details has tabs, which is
// what earns the page a third column on a very wide terminal.
func detailHasTabs(details ...Detail) bool {
	for _, d := range details {
		if len(d.Tabs) > 0 {
			return true
		}
	}
	return false
}

// tabStrip names views[from:] for a pane title, the selected one
// bracketed: "[Summary] Chain Trace". It is empty when there is nothing to
// choose between in a single column.
func (d *detailPane) tabStrip(from, sel int) string {
	views := d.detail.views()
	if len(views) <= from || from == 0 && len(views) < 2 {
		return ""
	}
	var parts []string
	for i := from; i < len(views); i++ {
		if i == sel {
			parts = append(parts, "["+views[i].Title+"]")
		} else {
			parts = append(parts, views[i].Title)
		}
	}
	return strings.Join(parts, " ")
}

func (d *detailPane) view(focus focusArea) string {
	title := "Details"
	if d.title != "" {
		title = d.title
	}
	if !d.split {
		if strip := d.tabStrip(0, d.tab); strip != "" {
			title += " · " + strip
		}
		return pane(d.st, title, scrollInfo(&d.main.vp), d.main.vp.View(), d.w, d.h, focus == focusDetail)
	}
	left := pane(d.st, title, scrollInfo(&d.main.vp), d.main.vp.View(), d.w, d.h, focus == focusDetail)
	auxTitle := d.tabStrip(1, d.auxTab())
	if auxTitle == "" {
		auxTitle = "Details"
	}
	right := pane(d.st, auxTitle, scrollInfo(&d.aux.vp), d.aux.vp.View(), d.auxW, d.h, focus == focusAux)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func scrollInfo(vp *viewport.Model) string {
	if vp.TotalLineCount() > vp.Height() {
		return fmt.Sprintf("%3.f%%", vp.ScrollPercent()*100)
	}
	return ""
}
