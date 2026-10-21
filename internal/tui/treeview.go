package tui

import (
	"fmt"
	"sort"

	tree "charm.land/bubbles/v2/tree"
	tea "charm.land/bubbletea/v2"
)

// TreeView renders a scope → files hierarchy through the shared frame: a bold
// header strip, the tree inside a rounded padded panel, and a status strip
// whose hotkeys drive the tree. The tree itself has no border of its own —
// the frame supplies it, so the ruled edges always span the terminal and
// track resizes. The title is the owning command's name.
type TreeView struct {
	model  tree.Model
	title  string
	scopes int
	files  int
	width  int
	height int
}

// Init returns no commands: the tree needs none until a key arrives.
func (t *TreeView) Init() tea.Cmd { return nil }

// NewTreeFromScopes builds the two-level tree the commands need: at the top
// are scopes (rules), and under each scope is the list of files it matched.
// Scopes are expanded by default so the full match picture is visible, and
// both scopes and files are sorted so the layout is deterministic.
func NewTreeFromScopes(scopes map[string][]string) *TreeView {
	root := tree.Root("kit · rules")
	files := 0
	scopeKeys := make([]string, 0, len(scopes))
	for k := range scopes {
		scopeKeys = append(scopeKeys, k)
	}
	sort.Strings(scopeKeys)
	for _, scope := range scopeKeys {
		node := tree.Root(scope)
		matched := append([]string(nil), scopes[scope]...)
		sort.Strings(matched)
		for _, f := range matched {
			node.Child(f)
		}
		node.Open() // expand each scope on first render
		root.Child(node)
		files += len(matched)
	}
	return &TreeView{
		model:  tree.New(root, contentWidth(60), contentHeight(25)),
		title:  "kit · rules",
		scopes: len(scopes),
		files:  files,
	}
}

// RunTree runs the tree view and returns on quit.
func RunTree(tv *TreeView) error {
	_, err := tea.NewProgram(tv).Run()
	return err
}

// View draws the tree inside the shared frame: the frame's strips and
// rounded panel supply every edge, and the inner padding keeps the nodes off
// the rules.
func (t *TreeView) View() tea.View {
	context := fmt.Sprintf("%d scope(s) · %d file(s)", t.scopes, t.files)
	return tea.NewView(frame(t.title, context, "enter expand · q quit", t.width, t.height, t.model.View()))
}

// Update handles keys: "q" and "esc" quit; the other keys are routed to the
// tree model for navigation. WindowSizeMsg refits the tree to the panel: the
// frame claims two columns and four rows for its own strips and rules.
func (t *TreeView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.KeyPressMsg:
		if k.String() == "q" || k.String() == "esc" {
			return t, tea.Quit
		}
	case tea.WindowSizeMsg:
		t.width = k.Width
		t.height = k.Height
		t.model.SetSize(contentWidth(k.Width), contentHeight(k.Height))
	}
	model, cmd := t.model.Update(msg)
	t.model = model
	return t, cmd
}
