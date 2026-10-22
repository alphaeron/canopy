package tui

import "charm.land/bubbles/v2/key"

// globalKeyMap holds the bindings the app handles on every page. Single
// character bindings (q, ?, :) are suspended while a page is capturing text
// input, so typing into a filter or form never quits or opens dialogs.
type globalKeyMap struct {
	Quit      key.Binding
	ForceQuit key.Binding
	Back      key.Binding
	Help      key.Binding
	Palette   key.Binding
	Reload    key.Binding
	Options   key.Binding
	Suspend   key.Binding
}

var globalKeys = globalKeyMap{
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
	Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Palette:   key.NewBinding(key.WithKeys("ctrl+p", ":"), key.WithHelp("ctrl+p", "commands")),
	Reload:    key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "reload")),
	Options:   key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "options")),
	Suspend:   key.NewBinding(key.WithKeys("ctrl+z"), key.WithHelp("ctrl+z", "suspend")),
}

// paneKeyMap holds the bindings shared by pages with a detail pane.
type paneKeyMap struct {
	Open        key.Binding
	SwitchFocus key.Binding
	TogglePane  key.Binding
	Filter      key.Binding
	Up          key.Binding
	Down        key.Binding
	NextView    key.Binding
	PrevView    key.Binding
}

var paneKeys = paneKeyMap{
	Open:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
	SwitchFocus: key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "switch pane")),
	TogglePane:  key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "toggle details")),
	Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	NextView:    key.NewBinding(key.WithKeys("]"), key.WithHelp("[/]", "views")),
	PrevView:    key.NewBinding(key.WithKeys("["), key.WithHelp("[/]", "views")),
}

// confirmKeys answer a y/n prompt.
var confirmKeys = struct {
	Yes key.Binding
	No  key.Binding
}{
	Yes: key.NewBinding(key.WithKeys("y", "Y", "enter"), key.WithHelp("y", "confirm")),
	No:  key.NewBinding(key.WithKeys("n", "N", "esc"), key.WithHelp("n", "cancel")),
}
