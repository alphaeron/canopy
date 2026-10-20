package tui

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TreeView renders a hierarchy as a text tree and wraps it in a scrollable
// viewport. It is the default view for anything that is a tree, such as a
// rule chain (rules get) and the grouped scope tree (match --by-scope).
type TreeView struct {
	vp    viewport.Model
	lines []string
}

// ScopeNode is one node in a tree. Title is the label for that node; Kids is
// a map of child name to node. The map is sorted before rendering so the
// tree is deterministic.
type ScopeNode struct {
	Title string
	Kids  map[string]*ScopeNode
}

// NewTreeFromScopes builds the two-level tree the commands need: at the top
// are scopes (rules), and under each scope is the list of files matched.
// Init returns no commands: the tree is a pure view, scrolled by the viewport.
func (t *TreeView) Init() tea.Cmd { return nil }

func NewTreeFromScopes(scopes map[string][]string) *TreeView {
	tree := &TreeView{}
	rootNode := make(map[string]*ScopeNode)
	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		children := make(map[string]*ScopeNode)
		for _, f := range scopes[k] {
			children[f] = &ScopeNode{Title: f}
		}
		rootNode[k] = &ScopeNode{Title: k, Kids: children}
	}
	tree.lines = buildTreeLines(rootNode)
	tree.vp = viewport.New(60, 25)
	tree.vp.SetContent(styleTree(linesToText(tree.lines)))
	return tree
}

// buildTreeLines walks the map and emits one line per node. The top-level
// scopes use a "├" branch; files under a scope use an indented "│  " gutter
// and a "   └──" bullet, in sorted order.
func buildTreeLines(nodes map[string]*ScopeNode) []string {
	lines := []string{}
	keys := sortedNodeKeys(nodes)
	for i, k := range keys {
		n := nodes[k]
		last := i == len(keys)-1
		branch := "│   "
		if last {
			branch = "    "
		}
		lines = append(lines, branch+styleTree(n.Title)+" "+styleLeaf(len(n.Kids)))
		fileKeys := sortedNodeKeys(n.Kids)
		for j, fk := range fileKeys {
			lastF := j == len(fileKeys)-1
			cbranch := "    ├── "
			if lastF {
				cbranch = "    └── "
			}
			lines = append(lines, cbranch+n.Kids[fk].Title)
		}
	}
	return lines
}

func sortedNodeKeys(nodes map[string]*ScopeNode) []string {
	keys := make([]string, 0, len(nodes))
	for k := range nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func linesToText(lines []string) string {
	b := ""
	for i, l := range lines {
		if i > 0 {
			b += "\n"
		}
		b += l
	}
	return b
}

func (t *TreeView) View() string { return t.vp.View() }
func (t *TreeView) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok && (k.String() == "q" || k.String() == "esc") {
		return t, tea.Quit
	}
	model, _ := t.vp.Update(msg)
	t.vp = model
	return t, nil
}

func styleTree(s string) string { return lipgloss.NewStyle().Bold(true).Render(s) }
func styleLeaf(count int) string {
	if count == 0 {
		return lipgloss.NewStyle().Faint(true).Render("(none)")
	}
	return lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("(%d file(s))", count))
}

// RunTree runs a TreeView and returns on quit.
func RunTree(tree *TreeView) error {
	_, err := tea.NewProgram(tree).Run()
	return err
}
