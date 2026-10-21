package tui

import (
	"fmt"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// listRow is a list.Item that carries the caller's opaque data.
type listRow struct {
	title    string
	subtitle string
	data     any
}

func (r listRow) FilterValue() string { return r.title }
func (r listRow) Title() string       { return r.title }
func (r listRow) Description() string { return r.subtitle }

// Row is one selectable item: title line, subtitle line, and opaque data
// returned on selection.
type Row struct {
	Title    string
	Subtitle string
	Data     any
}

// ListView is a scrollable, selectable list rendered through the shared
// frame: a bold header strip, the list inside a rounded padded panel, and a
// status strip listing the driving keys. The list carries no border of its
// own — the frame supplies every edge. The selection is captured internally
// and read after the program ends; "enter" selects the hovered row, "q"/"esc"
// quits without selection.
type ListView struct {
	model    list.Model
	selected any
	quit     bool
	title    string
	count    int
	width    int
	height   int
}

// NewListView builds a ListView from the rows. The width and height are the
// terminal dimensions the list should be constrained to; the frame strips
// and rules claim two columns and four rows of them. A non-positive size is
// "no constraint": the first WindowSizeMsg after Init refits the list and
// the frame to the terminal's real dimensions.
func (v *ListView) Init() tea.Cmd { return nil }

func NewListView(rows []Row, width, height int) *ListView {
	v := &ListView{}
	items := make([]list.Item, 0, len(rows))
	for i := range rows {
		items = append(items, listRow{rows[i].Title, rows[i].Subtitle, rows[i].Data})
	}
	v.model = list.New(items, list.NewDefaultDelegate(), contentWidth(width), contentHeight(height))
	v.model.SetFilteringEnabled(false)
	v.title = "kit · list"
	v.count = len(rows)
	v.width = width
	v.height = height
	return v
}

// Selected returns the data of the chosen row, or nil.
func (v *ListView) Selected() any { return v.selected }

// View draws the list inside the shared frame: the frame supplies every
// edge and the inner padding, so the strips always span the terminal.
func (v *ListView) View() tea.View {
	context := fmt.Sprintf("%d item(s)", v.count)
	return tea.NewView(frame(v.title, context, "↑↓/jk move · enter select · q quit", v.width, v.height, v.model.View()))
}

// Update handles keys. "enter" locks in the hovered row's data; "q" and
// "esc" quit. All other keys go to the bubbles list (up/down, j/k, page).
// WindowSizeMsg refits the list to the terminal.
func (v *ListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.KeyPressMsg:
		switch k.String() {
		case "enter":
			if item := v.model.SelectedItem(); item != nil {
				if row, ok := item.(listRow); ok {
					v.selected = row.data
				}
			}
			v.quit = true
			return v, tea.Quit
		case "q", "esc":
			v.quit = true
			return v, tea.Quit
		}
	case tea.WindowSizeMsg:
		v.width = k.Width
		v.height = k.Height
		v.model.SetWidth(contentWidth(k.Width))
		v.model.SetHeight(contentHeight(k.Height))
	}
	v.model, _ = v.model.Update(msg)
	return v, nil
}

// RunListView runs the list and returns the selected payload (nil if the
// user quit). A negative width/height means "no constraint".
func RunListView(rows []Row) (any, error) {
	v := NewListView(rows, 0, 0)
	p := tea.NewProgram(v)
	if _, err := p.Run(); err != nil {
		return nil, err
	}
	return v.selected, nil
}
