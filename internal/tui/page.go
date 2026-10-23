package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Page is one screen of the TUI. Pages are built by the constructors in this
// package (NewListPage, NewTreePage, NewFormPage) and are driven only by
// the app, which owns the chrome, navigation, and global keys.
type Page interface {
	// title is the pane title for pages not opened through a route.
	title() string
	// status is the page's summary for the status line.
	status() string
	init() tea.Cmd
	setSize(w, h int)
	setStyles(st *styles)
	update(msg tea.Msg) tea.Cmd
	view() string
	// bindings are the page's own keys, shown in the help line.
	bindings() []key.Binding
	// capturing reports that the page is taking text input, so global
	// single-key bindings must pass through to it.
	capturing() bool
	// cursor is the terminal cursor for the page's focused text input,
	// relative to the page body, or nil when nothing takes text.
	cursor() *tea.Cursor
	// back performs a page-local "back" (clear a filter, return focus to
	// the main pane) and reports whether it did; if not, the app pops the
	// page.
	back() bool
}

// Outcome is the result of an Action or a submitted form.
type Outcome struct {
	// Info is shown in the status line.
	Info string
	// Page, when non-nil, is pushed onto the navigation stack.
	Page Page
	// Reload re-runs the current page's route so it reflects the change.
	Reload bool
}
