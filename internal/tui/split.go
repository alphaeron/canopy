package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// paneSplit is the detail half of a list or tree page: the layout, which
// pane has focus, and the keys and mouse events that act on the detail
// columns. Pages embed it and handle their own main pane.
type paneSplit struct {
	detail     detailPane
	focus      focusArea
	hideDetail bool
	// wantAux asks for the third column on very wide terminals; pages set
	// it when any of their rows has tabs.
	wantAux bool
	layout  splitLayout
	w, h    int
}

func newPaneSplit(st *styles) paneSplit {
	return paneSplit{detail: newDetailPane(st)}
}

// resize lays the panes out for a w×h body and returns the main pane's
// content size.
func (s *paneSplit) resize(w, h int) (int, int) {
	s.w, s.h = w, h
	s.layout = computeSplit(w, h, !s.hideDetail, s.wantAux)
	if !s.layout.detail || s.focus == focusAux && s.layout.auxW == 0 {
		s.focus = focusMain
	}
	s.detail.setSize(s.layout.detailW, s.layout.detailH, s.layout.auxW)
	return max(0, s.layout.mainW-paneFrameWidth), max(0, s.layout.mainH-paneFrameHeight)
}

// handleKey takes the keys that belong to the split: switching focus and
// views, toggling the detail pane (relayout tells the page to resize its
// main pane), and every key while a detail column is focused.
func (s *paneSplit) handleKey(k tea.KeyPressMsg) (cmd tea.Cmd, handled, relayout bool) {
	switch {
	case key.Matches(k, paneKeys.SwitchFocus):
		s.focus = s.layout.nextFocus(s.focus)
		return nil, true, false
	case key.Matches(k, paneKeys.TogglePane):
		s.hideDetail = !s.hideDetail
		return nil, true, true
	case key.Matches(k, paneKeys.NextView):
		s.detail.cycleTab(1)
		return nil, true, false
	case key.Matches(k, paneKeys.PrevView):
		s.detail.cycleTab(-1)
		return nil, true, false
	case s.focus != focusMain:
		return s.detail.update(s.focus, k), true, false
	}
	return nil, false, false
}

// mouse handles a wheel or click over a detail column (area): the wheel
// scrolls it, and a click focuses it and opens the link under the pointer.
func (s *paneSplit) mouse(msg tea.MouseMsg, area focusArea, cy int) tea.Cmd {
	m := msg.Mouse()
	if _, wheel := msg.(tea.MouseWheelMsg); wheel {
		if d := wheelDelta(m.Button); d != 0 {
			s.detail.scroll(area, d)
		}
		return nil
	}
	if m.Button != tea.MouseLeft {
		return nil
	}
	s.focus = area
	return s.detail.click(area, cy)
}

// back returns focus to the main pane, reporting whether it moved.
func (s *paneSplit) back() bool {
	if s.focus != focusMain {
		s.focus = focusMain
		return true
	}
	return false
}

// focusDetail moves focus into the detail pane, as enter on a row without a
// route does.
func (s *paneSplit) focusDetailPane() {
	if s.layout.detail {
		s.focus = focusDetail
	}
}

func (s *paneSplit) join(main string) string {
	if !s.layout.detail {
		return main
	}
	return s.layout.join(main, s.detail.view(s.focus))
}

// detailBindings are the help-line keys while a detail column is focused.
func (s *paneSplit) detailBindings() []key.Binding {
	b := []key.Binding{key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "scroll"))}
	if s.detail.hasLinks(s.focus) {
		b = []key.Binding{
			key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "links")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open link")),
		}
	}
	return append(b, s.viewBindings()...)
}

// paneBindings are the split's help-line keys while the main pane is
// focused.
func (s *paneSplit) paneBindings() []key.Binding {
	b := s.viewBindings()
	return append(b, paneKeys.TogglePane)
}

func (s *paneSplit) viewBindings() []key.Binding {
	var b []key.Binding
	if s.layout.detail {
		if s.detail.canCycle() {
			b = append(b, paneKeys.PrevView)
		}
		b = append(b, paneKeys.SwitchFocus)
	}
	return b
}
