package tui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
)

// allChoice is the label for the empty value of a choice parameter: no
// narrowing, e.g. every KIT file or every scope.
const allChoice = "all"

// optionsPage edits a route's arguments and flags: the TUI form of typing
// them on the command line. Submitting reopens the route with the new
// values; esc closes it unchanged. A live preview shows the equivalent CLI
// command.
type optionsPage struct {
	route   Route
	replace bool
	// texts and bools hold each param's field value, indexed like
	// route.Params; huh binds to them in place.
	texts []string
	bools []bool
	form  *huh.Form
	st    *styles
	w, h  int
	done  bool
}

func newOptionsPage(r Route, choices map[string][]string, replace bool) *optionsPage {
	p := &optionsPage{
		route:   r,
		replace: replace,
		texts:   make([]string, len(r.Params)),
		bools:   make([]bool, len(r.Params)),
		st:      newStyles(true),
	}
	fields := make([]huh.Field, 0, len(r.Params))
	for i, prm := range r.Params {
		title, help := paramUsage(prm), clean(prm.Help)
		switch {
		case prm.Arg && prm.Variadic:
			p.texts[i] = strings.Join(r.Values.Args, "\n")
			f := huh.NewText().Title(title).Description(help + " (one per line)").Value(&p.texts[i])
			if prm.Required {
				f.Validate(required(prm))
			}
			fields = append(fields, f)
		case prm.Kind == ParamBool:
			p.bools[i] = r.Values.Bool(prm.Name)
			fields = append(fields, huh.NewConfirm().Title(title).Description(help).Value(&p.bools[i]))
		case prm.Kind == ParamChoice:
			p.texts[i] = r.Values.Flag(prm.Name)
			fields = append(fields, huh.NewSelect[string]().Title(title).Description(help).
				Options(choiceOptions(choices[prm.Name], p.texts[i])...).Value(&p.texts[i]))
		default:
			if prm.Arg {
				p.texts[i] = strings.Join(r.Values.Args, " ")
			} else {
				p.texts[i] = r.Values.Flag(prm.Name)
			}
			f := huh.NewInput().Title(title).Description(help).Value(&p.texts[i])
			if prm.Required {
				f.Validate(required(prm))
			}
			fields = append(fields, f)
		}
	}
	p.form = huh.NewForm(huh.NewGroup(fields...)).WithShowHelp(true)
	return p
}

// choiceOptions offers "all" (the empty value) and every choice, keeping the
// current value selectable even if it is no longer among the choices.
func choiceOptions(choices []string, current string) []huh.Option[string] {
	opts := []huh.Option[string]{huh.NewOption(allChoice, "")}
	found := current == ""
	for _, c := range choices {
		opts = append(opts, huh.NewOption(clean(c), c))
		found = found || c == current
	}
	if !found {
		opts = append(opts, huh.NewOption(clean(current), current))
	}
	return opts
}

func required(p Param) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New(paramUsage(p) + " is required")
		}
		return nil
	}
}

// values reads the form back into Values, as the CLI would parse them.
func (p *optionsPage) values() Values {
	v := Values{Flags: map[string]string{}}
	for i, prm := range p.route.Params {
		switch {
		case prm.Arg && prm.Variadic:
			for _, line := range strings.Split(p.texts[i], "\n") {
				if line = strings.TrimSpace(line); line != "" {
					v.Args = append(v.Args, line)
				}
			}
		case prm.Arg:
			if s := strings.TrimSpace(p.texts[i]); s != "" {
				v.Args = []string{s}
			}
		case prm.Kind == ParamBool:
			if p.bools[i] {
				v.Flags[prm.Name] = "true"
			}
		default:
			if s := strings.TrimSpace(p.texts[i]); s != "" {
				v.Flags[prm.Name] = s
			}
		}
	}
	return v
}

func (p *optionsPage) edited() Route {
	r := p.route
	r.Values = p.values()
	return r
}

func (p *optionsPage) title() string { return "options" }

func (p *optionsPage) status() string { return "options for kit " + p.route.Command }

func (p *optionsPage) init() tea.Cmd { return p.form.Init() }

func (p *optionsPage) setStyles(st *styles) {
	p.st = st
	p.form = themeForm(p.form, st)
}

func (p *optionsPage) setSize(w, h int) {
	p.w, p.h = w, h
	// Two rows above the form hold the command preview.
	p.form = p.form.WithWidth(max(0, min(w-paneFrameWidth, 80))).WithHeight(max(0, h-paneFrameHeight-2))
}

// capturing is always true: every key belongs to the form's fields.
func (p *optionsPage) capturing() bool { return true }

func (p *optionsPage) back() bool { return false }

func (p *optionsPage) cursor() *tea.Cursor { return nil }

func (p *optionsPage) update(msg tea.Msg) tea.Cmd {
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
		r, replace := p.edited(), p.replace
		return func() tea.Msg { return reopenMsg{route: r, replace: replace} }
	case huh.StateAborted:
		p.done = true
		return func() tea.Msg { return outcomeMsg{pop: true} }
	}
	return cmd
}

func (p *optionsPage) view() string {
	preview := p.st.label.Render("Runs  ") + p.st.value.Render(clean(p.edited().CommandLine()))
	return pane(p.st, "options · "+p.route.Command, "", preview+"\n\n"+p.form.View(), p.w, p.h, true)
}

func (p *optionsPage) bindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next field")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next/apply")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
	}
}

var _ Page = (*optionsPage)(nil)
