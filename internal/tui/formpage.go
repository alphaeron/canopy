package tui

import (
	"context"
	"image/color"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
)

// formPage hosts a huh form inside the app. Submitting runs the caller's
// submit function and closes the page; esc closes it without saving.
type formPage struct {
	name    string
	summary string
	st      *styles
	form    *huh.Form
	submit  func(ctx context.Context) (string, error)
	w, h    int
	done    bool
}

// NewFormPage hosts form as a page titled name, with summary on the status
// line. The caller binds form's fields to its own values; once the form
// completes, submit persists them and returns a confirmation message, and
// the page closes and the page beneath it is reloaded once it succeeds.
func NewFormPage(name, summary string, form *huh.Form, submit func(ctx context.Context) (string, error)) Page {
	return &formPage{name: name, summary: summary, form: form.WithShowHelp(true), submit: submit, st: newStyles(true)}
}

func (p *formPage) title() string { return p.name }

func (p *formPage) status() string { return p.summary }

func (p *formPage) init() tea.Cmd { return p.form.Init() }

func (p *formPage) setStyles(st *styles) {
	p.st = st
	p.form = themeForm(p.form, st)
}

// themeForm tells a huh form whether the terminal is dark, which is how
// huh picks its theme's light or dark variant.
func themeForm(form *huh.Form, st *styles) *huh.Form {
	var bg color.Color = color.White
	if st.isDark {
		bg = color.Black
	}
	// Handling the background message only records the flag; it issues no
	// command.
	m, _ := form.Update(tea.BackgroundColorMsg{Color: bg})
	if f, ok := m.(*huh.Form); ok {
		return f
	}
	return form
}

func (p *formPage) setSize(w, h int) {
	p.w, p.h = w, h
	p.form = p.form.WithWidth(max(0, min(w-paneFrameWidth, 80))).WithHeight(max(0, h-paneFrameHeight))
}

// capturing is always true: every key belongs to the form's fields.
func (p *formPage) capturing() bool { return true }

// cursor is nil: huh draws its own cursor inside the fields.
func (p *formPage) cursor() *tea.Cursor { return nil }

func (p *formPage) back() bool { return false }

func (p *formPage) update(msg tea.Msg) tea.Cmd {
	if p.done {
		return nil
	}
	if k, ok := msg.(tea.KeyPressMsg); ok && key.Matches(k, globalKeys.Back) {
		p.done = true
		return func() tea.Msg { return outcomeMsg{pop: true} }
	}
	m, cmd := p.form.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		p.form = f
	}
	switch p.form.State {
	case huh.StateCompleted:
		p.done = true
		submit := p.submit
		return runCmd(func(ctx context.Context) (Outcome, error) {
			info, err := submit(ctx)
			return Outcome{Info: info, Reload: true}, err
		}, true)
	case huh.StateAborted:
		p.done = true
		return func() tea.Msg { return outcomeMsg{pop: true} }
	}
	return cmd
}

func (p *formPage) view() string {
	return pane(p.st, p.name, "", p.form.View(), p.w, p.h, true)
}

func (p *formPage) bindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next field")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next/submit")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
}
