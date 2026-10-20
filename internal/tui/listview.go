package tui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
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

// ListView is a scrollable, selectable list. The selection is captured
// internally and read after the program ends; "enter" selects the hovered
// row, "q"/"esc" quits without selection.
type ListView struct {
	model    list.Model
	selected any
	quit     bool
}

// NewListView builds a ListView from the rows. The width and height are the
// terminal dimensions the list should be constrained to.
// Init returns no commands: the list starts unfiltered and un-scrolled.
func (v *ListView) Init() tea.Cmd { return nil }

func NewListView(rows []Row, width, height int) *ListView {
	v := &ListView{}
	items := make([]list.Item, 0, len(rows))
	for i := range rows {
		items = append(items, listRow{rows[i].Title, rows[i].Subtitle, rows[i].Data})
	}
	v.model = list.New(items, list.NewDefaultDelegate(), width, height)
	v.model.SetFilteringEnabled(false)
	return v
}

// Selected returns the data of the chosen row, or nil.
func (v *ListView) Selected() any { return v.selected }

// View renders the list.
func (v *ListView) View() string { return v.model.View() }

// Update handles keys. "enter" locks in the hovered row's data; "q" and
// "esc" quit. All other keys go to the bubbles list (up/down, j/k, page).
func (v *ListView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
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
