package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Palette geometry.
const (
	paletteMaxWidth = 64
	paletteMaxRows  = 10
)

var paletteKeys = struct {
	Select key.Binding
	Up     key.Binding
	Down   key.Binding
	Close  key.Binding
}{
	Select: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "go")),
	Up:     key.NewBinding(key.WithKeys("up", "ctrl+k"), key.WithHelp("↑", "up")),
	Down:   key.NewBinding(key.WithKeys("down", "ctrl+j"), key.WithHelp("↓", "down")),
	Close:  key.NewBinding(key.WithKeys("esc", "ctrl+p"), key.WithHelp("esc", "close")),
}

func paletteBindings() []key.Binding {
	return []key.Binding{paletteKeys.Up, paletteKeys.Down, paletteKeys.Select, paletteKeys.Close}
}

// paletteEntry is one destination: a route, or home when route is nil.
type paletteEntry struct {
	name, hint string
	route      *Route
}

// palette is crush's command palette: a centered dialog with a filter input
// over every page in the app, reachable from anywhere with ctrl+p.
type palette struct {
	st       *styles
	input    textinput.Model
	entries  []paletteEntry
	filtered []paletteEntry
	cursor   int
}

func newPalette(st *styles, routes []Route) *palette {
	in := textinput.New()
	in.Placeholder = "Type to filter commands"
	in.Prompt = "> "
	// Use the terminal's own cursor rather than a drawn one, which blinks
	// natively instead of re-rendering on a timer (crush does the same).
	in.SetVirtualCursor(false)
	entries := []paletteEntry{{name: "home", hint: "all commands"}}
	for i := range routes {
		entries = append(entries, paletteEntry{name: Clean(routes[i].Command), hint: Clean(routes[i].Description), route: &routes[i]})
	}
	p := &palette{st: st, input: in, entries: entries}
	p.filter()
	return p
}

func (p *palette) init() tea.Cmd { return p.input.Focus() }

// update handles a key while the palette is open. Choosing an entry closes
// the palette and navigates; esc closes it.
func (p *palette) update(msg tea.KeyPressMsg, a *app) tea.Cmd {
	switch {
	case key.Matches(msg, paletteKeys.Close):
		a.palette = nil
		return nil
	case key.Matches(msg, paletteKeys.Up):
		if p.cursor > 0 {
			p.cursor--
		}
		return nil
	case key.Matches(msg, paletteKeys.Down):
		if p.cursor < len(p.filtered)-1 {
			p.cursor++
		}
		return nil
	case key.Matches(msg, paletteKeys.Select):
		if len(p.filtered) == 0 {
			return nil
		}
		e := p.filtered[p.cursor]
		a.palette = nil
		if e.route == nil {
			return goHome
		}
		return navigate(*e.route)
	}
	var cmd tea.Cmd
	before := p.input.Value()
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
		p.filter()
	}
	return cmd
}

// scroll moves the selection with the mouse wheel.
func (p *palette) scroll(b tea.MouseButton) {
	switch b {
	case tea.MouseWheelUp:
		p.cursor = max(0, p.cursor-1)
	case tea.MouseWheelDown:
		p.cursor = max(0, min(len(p.filtered)-1, p.cursor+1))
	}
}

// filter ranks entries with the same fuzzy matcher the bubbles list uses.
func (p *palette) filter() {
	p.cursor = 0
	term := strings.TrimSpace(p.input.Value())
	if term == "" {
		p.filtered = p.entries
		return
	}
	targets := make([]string, len(p.entries))
	for i, e := range p.entries {
		targets[i] = e.name + " " + e.hint
	}
	ranks := list.DefaultFilter(term, targets)
	p.filtered = make([]paletteEntry, 0, len(ranks))
	for _, r := range ranks {
		p.filtered = append(p.filtered, p.entries[r.Index])
	}
}

// view renders the dialog box.
func (p *palette) view(screenW int) string {
	w := min(paletteMaxWidth, max(20, screenW-4))
	inner := w - p.st.paletteBox.GetHorizontalFrameSize()
	p.input.SetWidth(max(1, inner-ansi.StringWidth(p.input.Prompt)-1))

	rows := []string{p.st.paletteTitle.Render("Go to"), p.input.View(), ""}
	start := max(0, p.cursor-paletteMaxRows+1)
	end := min(len(p.filtered), start+paletteMaxRows)
	for i := start; i < end; i++ {
		e := p.filtered[i]
		style := p.st.paletteItem
		if i == p.cursor {
			style = p.st.paletteSelected
		}
		name := style.Render(e.name)
		hint := p.st.paletteDesc.Render(e.hint)
		rows = append(rows, ansi.Truncate(spread(name, hint, inner), inner, "…"))
	}
	if len(p.filtered) == 0 {
		rows = append(rows, p.st.empty.Render("No matching commands"))
	}
	return p.st.paletteBox.Width(w).Render(strings.Join(rows, "\n"))
}

// overlay composites the palette over the screen, horizontally centered and
// a little below the header, using lipgloss's layer compositor. It returns
// the composed screen and the terminal cursor for the filter input.
func (p *palette) overlay(screen string, w, h int) (string, *tea.Cursor) {
	box := p.view(w)
	x := max(0, (w-lipgloss.Width(box))/2)
	y := min(max(0, h/6), max(0, h-lipgloss.Height(box)))
	out := lipgloss.NewCompositor(
		lipgloss.NewLayer(screen),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
	c := p.input.Cursor()
	if c != nil {
		// The input sits on the box's second content row (below the
		// title), inside the border and the left padding.
		c.X += x + p.st.paletteBox.GetBorderLeftSize() + p.st.paletteBox.GetPaddingLeft()
		c.Y += y + p.st.paletteBox.GetBorderTopSize() + 1
	}
	return out, c
}
