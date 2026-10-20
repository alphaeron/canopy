package tui

import (
	"fmt"
	"regexp"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// RuleFields is the submitted rule-field set.
type RuleFields struct {
	Pattern        string
	Name           string
	Strategy       string
	Ignore         bool
	CurrentDirOnly bool
	SelfApprove    bool
	ApproverText   string
}

// RuleFormModel is the interactive rule editor: three focusable text inputs
// (pattern, name, approver list) plus single-key toggles. The cursor moves
// across the text inputs with the number keys 1-3; submit is Ctrl+S, cancel
// is Esc. On submit the form validates and reports the fields it saw.
type RuleFormModel struct {
	pattern   textinput.Model
	name      textinput.Model
	approvers textinput.Model
	focused   int
	strategy  string
	ignore    bool
	cdirOnly  bool
	selfApply bool
	err       string
	fields    RuleFields
	submitted bool
	quit      bool
}

// NewRuleFormModel builds a form pre-filled with the given fields. An empty
// RuleFields yields a blank new-rule form.
// Init focuses the pattern field so typing starts in the first input.
func (m *RuleFormModel) Init() tea.Cmd { return m.pattern.Focus() }

func NewRuleFormModel(f RuleFields) *RuleFormModel {
	m := &RuleFormModel{strategy: f.Strategy, ignore: f.Ignore, cdirOnly: f.CurrentDirOnly,
		selfApply: f.SelfApprove, fields: f}
	m.pattern = textinput.New()
	m.pattern.Placeholder = "pattern (regex)"
	m.pattern.SetValue(f.Pattern)
	m.name = textinput.New()
	m.name.Placeholder = "rule name"
	m.name.SetValue(f.Name)
	m.approvers = textinput.New()
	m.approvers.SetValue(f.ApproverText)
	return m
}

// View renders the form with the focused input's cursor.
func (m *RuleFormModel) View() string {
	return fmt.Sprintf(`  add rule

  %sPattern: %s
  Name:      %s
  Strategy:  %s
  Ignore:    %s
  Current-dir-only: %s
  Self-approve: %s
  Approvers: %s%s%s (one per line, tab-separated)

  i=ignore  c=current-dir-only  a=self-approve  s=next strategy  1/2/3=field
`,
		cursorTag(m.focused == 0), m.pattern.View(),
		cursorTag(m.focused == 1), m.name.View(),
		m.strategy,
		yesNo(m.ignore),
		yesNo(m.cdirOnly),
		yesNo(m.selfApply),
		cursorTag(m.focused == 2), m.approvers.View(),
	)
}

// cursorTag marks the focused row.
func cursorTag(on bool) string {
	if on {
		return "> "
	}
	return "  "
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Update routes keys to the focused input, single-key toggles, focus switch,
// and submit on Ctrl+S.
func (m *RuleFormModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			m.quit = true
			return m, tea.Quit
		case "ctrl+s":
			if err := m.validate(); err != nil {
				m.err = err.Error()
			} else {
				m.fields = RuleFields{Pattern: m.pattern.Value(), Name: m.name.Value(),
					Strategy: m.strategy, Ignore: m.ignore, CurrentDirOnly: m.cdirOnly,
					SelfApprove: m.selfApply, ApproverText: m.approvers.Value()}
				m.submitted = true
			}
			return m, tea.Quit
		case "i":
			m.ignore = !m.ignore
			return m, nil
		case "c":
			m.cdirOnly = !m.cdirOnly
			return m, nil
		case "a":
			m.selfApply = !m.selfApply
			return m, nil
		case "s":
			m.strategy = nextStrategy(m.strategy)
			return m, nil
		case "1":
			m.focused = 0
			return m, m.pattern.Focus()
		case "2":
			m.focused = 1
			return m, m.name.Focus()
		case "3":
			m.focused = 2
			return m, m.approvers.Focus()
		}
	}
	var cmd tea.Cmd
	switch m.focused {
	case 1:
		m.name, cmd = m.name.Update(msg)
	case 2:
		m.approvers, cmd = m.approvers.Update(msg)
	default:
		m.pattern, cmd = m.pattern.Update(msg)
	}
	return m, cmd
}

// nextStrategy cycles the three choices in a stable order (union → override
// → intersect → union).
func nextStrategy(s string) string {
	switch s {
	case "union":
		return "override"
	case "override":
		return "intersect"
	default:
		return "union"
	}
}

// validate checks required fields and a compilable pattern regex. A missing
// pattern or name, or a pattern with a parse error, is an error.
func (m *RuleFormModel) validate() error {
	if m.pattern.Value() == "" {
		return errMissing{"pattern"}
	}
	if m.name.Value() == "" {
		return errMissing{"name"}
	}
	if _, err := regexp.Compile(m.pattern.Value()); err != nil {
		return errBadPattern{m.pattern.Value()}
	}
	return nil
}

type errMissing struct{ field string }

func (e errMissing) Error() string { return "missing required field: " + e.field }

type errBadPattern struct{ pattern string }

func (e errBadPattern) Error() string { return "invalid pattern regex: " + e.pattern }

// RunForm runs the rule form and returns the fields on successful submit,
// or (empty fields, nil) on cancel.
func RunForm(f RuleFields) (RuleFields, error) {
	m := NewRuleFormModel(f)
	_, err := tea.NewProgram(m).Run()
	if err != nil {
		return f, err
	}
	return m.fields, nil
}
