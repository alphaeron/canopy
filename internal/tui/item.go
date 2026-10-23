package tui

import (
	"context"

	"charm.land/bubbles/v2/key"
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
