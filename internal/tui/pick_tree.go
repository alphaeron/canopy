package tui

import (
	"errors"
	"fmt"
	"slices"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/tree"
	tea "charm.land/bubbletea/v2"
)

// PickGroup is one branch of PickTree: a heading, such as a rules file, and
// the choices under it.
type PickGroup struct {
	Title   string
	Choices []Choice
}

// PickTree asks the user to choose one of the choices on the terminal,
// shown as a tree of groups, and returns its value, or ErrAborted if they
// cancel. It is Pick for choices that belong to groups, such as the rules
// of several rules files. Only a choice can be picked, not a group.
func PickTree(title string, groups []PickGroup) (string, error) {
	if !slices.ContainsFunc(groups, func(g PickGroup) bool { return len(g.Choices) > 0 }) {
		return "", errNoChoices
	}
	m := newTreePicker(title, groups)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", fmt.Errorf("pick: %w", err)
	}
	picked := final.(*treePicker)
	if picked.aborted || picked.value == nil {
		return "", ErrAborted
	}
	return *picked.value, nil
}

// pickRef is a picker row: a group's heading, or a choice with its value.
type pickRef struct {
	label  string
	value  string
	choice bool
}

func (r pickRef) String() string { return r.label }

// treePicker is PickTree's program: the tree, and the value picked.
type treePicker struct {
	tree    tree.Model
	keys    pickKeys
	value   *string
	aborted bool
}

type pickKeys struct {
	pick, cancel key.Binding
}

func newTreePicker(title string, groups []PickGroup) *treePicker {
	root := tree.Root(pickRef{label: Clean(title)})
	rows := 1
	for _, g := range groups {
		node := tree.Root(pickRef{label: Clean(g.Title)})
		for _, c := range g.Choices {
			node.Child(pickRef{label: Clean(c.Label), value: c.Value, choice: true})
		}
		root.Child(node.Open())
		rows += 1 + len(g.Choices)
	}
	// The tree draws nothing at width 0; the terminal's size replaces this
	// default as soon as it is known.
	t := tree.New(root, 80, rows)
	t.SetShowHelp(false)
	p := &treePicker{
		tree: t,
		keys: pickKeys{
			pick:   key.NewBinding(key.WithKeys("enter")),
			cancel: key.NewBinding(key.WithKeys("esc", "ctrl+c")),
		},
	}
	p.next(1)
	return p
}

// next moves the cursor from the current row to the nearest choice in dir
// (1 down, -1 up), staying put when there is none that way.
func (p *treePicker) next(dir int) {
	for off := p.tree.YOffset() + dir; p.tree.Node(off) != nil; off += dir {
		if ref, ok := p.tree.Node(off).GivenValue().(pickRef); ok && ref.choice {
			p.tree.SetYOffset(off)
			return
		}
	}
}

func (p *treePicker) Init() tea.Cmd { return nil }

func (p *treePicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.tree.SetWidth(msg.Width)
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, p.keys.cancel):
			p.aborted = true
			return p, tea.Quit
		case key.Matches(msg, p.keys.pick):
			if node := p.tree.NodeAtCurrentOffset(); node != nil {
				if ref, ok := node.GivenValue().(pickRef); ok && ref.choice {
					p.value = &ref.value
					return p, tea.Quit
				}
			}
			return p, nil
		case key.Matches(msg, p.tree.KeyMap.Down):
			p.next(1)
			return p, nil
		case key.Matches(msg, p.tree.KeyMap.Up):
			p.next(-1)
			return p, nil
		}
	}
	return p, nil
}

func (p *treePicker) View() tea.View {
	if p.value != nil || p.aborted {
		return tea.NewView("")
	}
	return tea.NewView(p.tree.View() + "\n↑/↓ move · enter pick · esc cancel\n")
}

// errNoChoices is PickTree's error when there is nothing to pick.
var errNoChoices = errors.New("pick: no choices")
