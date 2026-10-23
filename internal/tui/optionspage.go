package tui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/alphaeron/canopy/internal/brand"
)

// allChoice is the label for the empty value of a choice parameter: no
// narrowing, e.g. every rules file or every scope.
const allChoice = "all"

// optionsPage edits a route's arguments and flags: the TUI form of typing
// them on the command line. Submitting reopens the route with the new
// values; esc closes it unchanged. A live preview shows the equivalent CLI
// command.
//
// The fields form one huh group, except that a clearable flag is split
// across two: its "unchanged or set" choice ends one group, and its input
// is a group of its own, hidden (and skipped) unless the flag is set.
// huh hides groups, not fields, so the input could not otherwise be hidden.
type optionsPage struct {
	route   Route
	replace bool
	// texts, bools, and set hold each param's field value, indexed like
	// route.Params; huh binds to them in place. set[i] reports that a
	// clearable flag is given, its value (possibly empty) in texts[i].
	texts []string
	bools []bool
	set   []bool
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
		set:     make([]bool, len(r.Params)),
		st:      newStyles(true),
	}
	var groups []*huh.Group
	fields := make([]huh.Field, 0, len(r.Params))
	for i, prm := range r.Params {
		title, help := paramUsage(prm), Clean(prm.Help)
		switch {
		case prm.Kind == ParamChoice:
			if prm.Arg {
				p.texts[i] = strings.Join(r.Values.Args, " ")
			} else {
				p.texts[i] = r.Values.Flag(prm.Name)
			}
			fields = append(fields, huh.NewSelect[string]().Title(title).Description(help).
				Options(choiceOptions(prm, choices[prm.Name], p.texts[i])...).Value(&p.texts[i]))
		case prm.Arg && prm.Variadic:
			p.texts[i] = strings.Join(r.Values.Args, "\n")
			f := huh.NewText().Title(title).Description(help + " (one per line)").Value(&p.texts[i])
			if prm.Required {
				f.Validate(required(prm))
			}
			fields = append(fields, f)
		case prm.clearable():
			p.texts[i], p.set[i] = r.Values.Flags[prm.Name]
			fields = append(fields, huh.NewSelect[bool]().Title(title).Description(help).
				Options(huh.NewOption(Clean(prm.Unset), false), huh.NewOption("set", true)).Value(&p.set[i]))
			groups = append(groups, huh.NewGroup(fields...))
			fields = nil
			set := &p.set[i]
			groups = append(groups, huh.NewGroup(
				huh.NewInput().Title(title).Description("the value to set; empty sets it empty").Value(&p.texts[i]),
			).WithHideFunc(func() bool { return !*set }))
		case prm.Kind == ParamBool:
			p.bools[i] = r.Values.Bool(prm.Name)
			fields = append(fields, huh.NewConfirm().Title(title).Description(help).Value(&p.bools[i]))
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
	if len(fields) > 0 || len(groups) == 0 {
		groups = append(groups, huh.NewGroup(fields...))
	}
	p.form = huh.NewForm(groups...).WithShowHelp(true)
	return p
}

// choiceOptions offers every choice, after the empty value (labeled
// p.Unset, or "all") unless p is required, keeping the current value
// selectable even if it is no longer among the choices.
func choiceOptions(p Param, choices []string, current string) []huh.Option[string] {
	var opts []huh.Option[string]
	if !p.Required {
		unset := p.Unset
		if unset == "" {
			unset = allChoice
		}
		opts = append(opts, huh.NewOption(Clean(unset), ""))
	}
	found := current == ""
	for _, c := range choices {
		opts = append(opts, huh.NewOption(Clean(c), c))
		found = found || c == current
	}
	if !found {
		opts = append(opts, huh.NewOption(Clean(current), current))
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
		case prm.Arg && prm.Variadic && prm.Kind == ParamChoice:
			v.Args = strings.Fields(p.texts[i])
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
		case prm.clearable():
			if p.set[i] {
				v.Flags[prm.Name] = strings.TrimSpace(p.texts[i])
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

func (p *optionsPage) status() string { return "options for " + brand.Command + " " + p.route.Command }

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
	preview := p.st.label.Render("Runs  ") + p.st.value.Render(Clean(p.edited().CommandLine()))
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
