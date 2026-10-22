package tui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"
)

// styles is the single source of every color and text style the TUI draws
// with. Pages never pick raw colors; they read the semantic field they need,
// so a light or dark terminal only changes this one constructor.
type styles struct {
	isDark bool

	// Semantic colors.
	accent  color.Color
	text    color.Color
	muted   color.Color
	subtle  color.Color
	border  color.Color
	success color.Color
	danger  color.Color
	warning color.Color

	// Chrome.
	app          lipgloss.Style
	brand        lipgloss.Style
	crumb        lipgloss.Style
	crumbCurrent lipgloss.Style
	crumbSep     lipgloss.Style
	statusText   lipgloss.Style
	statusInfo   lipgloss.Style
	statusError  lipgloss.Style
	statusPrompt lipgloss.Style
	hint         lipgloss.Style

	// Panes.
	paneTitle        lipgloss.Style
	paneTitleFocused lipgloss.Style
	paneInfo         lipgloss.Style

	// Detail bodies.
	label        lipgloss.Style
	value        lipgloss.Style
	sectionTitle lipgloss.Style
	empty        lipgloss.Style
	diffAdd      lipgloss.Style
	diffDel      lipgloss.Style
	diffHunk     lipgloss.Style
	note         lipgloss.Style
	link         lipgloss.Style
	linkSelected lipgloss.Style
	badgeOK      lipgloss.Style
	badgePending lipgloss.Style
	badgeFail    lipgloss.Style
	badgeOff     lipgloss.Style

	// Command palette.
	paletteBox      lipgloss.Style
	paletteTitle    lipgloss.Style
	paletteItem     lipgloss.Style
	paletteSelected lipgloss.Style
	paletteDesc     lipgloss.Style

	help     help.Styles
	listItem list.DefaultItemStyles
	list     list.Styles
}

// newStyles builds the palette for a dark or light terminal background.
func newStyles(isDark bool) *styles {
	ld := lipgloss.LightDark(isDark)
	s := &styles{
		isDark:  isDark,
		accent:  ld(lipgloss.Color("#5A3FD1"), lipgloss.Color("#9D8CFF")),
		text:    ld(lipgloss.Color("#1F1F24"), lipgloss.Color("#E6E6EA")),
		muted:   ld(lipgloss.Color("#6B6B76"), lipgloss.Color("#9A9AA5")),
		subtle:  ld(lipgloss.Color("#A0A0AA"), lipgloss.Color("#5C5C66")),
		border:  ld(lipgloss.Color("#C4C4CC"), lipgloss.Color("#45454F")),
		success: ld(lipgloss.Color("#1E7F45"), lipgloss.Color("#5BD68A")),
		danger:  ld(lipgloss.Color("#B3261E"), lipgloss.Color("#FF6B6B")),
		warning: ld(lipgloss.Color("#8A5A00"), lipgloss.Color("#F2C14E")),
	}

	s.app = lipgloss.NewStyle().Foreground(s.text)
	s.brand = lipgloss.NewStyle().Bold(true).Foreground(s.accent)
	s.crumb = lipgloss.NewStyle().Foreground(s.muted)
	s.crumbCurrent = lipgloss.NewStyle().Bold(true).Foreground(s.text)
	s.crumbSep = lipgloss.NewStyle().Foreground(s.subtle)
	s.statusText = lipgloss.NewStyle().Foreground(s.muted)
	s.statusInfo = lipgloss.NewStyle().Foreground(s.success)
	s.statusError = lipgloss.NewStyle().Bold(true).Foreground(s.danger)
	s.statusPrompt = lipgloss.NewStyle().Bold(true).Foreground(s.warning)
	s.hint = lipgloss.NewStyle().Foreground(s.subtle)

	s.paneTitle = lipgloss.NewStyle().Foreground(s.muted)
	s.paneTitleFocused = lipgloss.NewStyle().Bold(true).Foreground(s.accent)
	s.paneInfo = lipgloss.NewStyle().Foreground(s.subtle)

	s.label = lipgloss.NewStyle().Foreground(s.muted)
	s.value = lipgloss.NewStyle().Foreground(s.text)
	s.sectionTitle = lipgloss.NewStyle().Bold(true).Foreground(s.accent)
	s.empty = lipgloss.NewStyle().Italic(true).Foreground(s.subtle)
	s.diffAdd = lipgloss.NewStyle().Foreground(s.success)
	s.diffDel = lipgloss.NewStyle().Foreground(s.danger)
	s.diffHunk = lipgloss.NewStyle().Foreground(s.accent)
	s.note = lipgloss.NewStyle().Foreground(s.muted)
	s.link = lipgloss.NewStyle().Foreground(s.text).Underline(true)
	s.linkSelected = lipgloss.NewStyle().Bold(true).Foreground(s.accent)
	s.badgeOK = lipgloss.NewStyle().Foreground(s.success)
	s.badgePending = lipgloss.NewStyle().Foreground(s.warning)
	s.badgeFail = lipgloss.NewStyle().Bold(true).Foreground(s.danger)
	s.badgeOff = lipgloss.NewStyle().Foreground(s.subtle)

	s.paletteBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(s.accent).
		Padding(0, 1)
	s.paletteTitle = lipgloss.NewStyle().Bold(true).Foreground(s.accent)
	s.paletteItem = lipgloss.NewStyle().Foreground(s.text).PaddingLeft(1)
	s.paletteSelected = lipgloss.NewStyle().Bold(true).Foreground(s.accent).
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(s.accent)
	s.paletteDesc = lipgloss.NewStyle().Foreground(s.muted)

	s.help = help.DefaultStyles(isDark)
	s.help.ShortKey = lipgloss.NewStyle().Foreground(s.muted)
	s.help.ShortDesc = lipgloss.NewStyle().Foreground(s.subtle)
	s.help.ShortSeparator = lipgloss.NewStyle().Foreground(s.subtle)
	s.help.FullKey = s.help.ShortKey
	s.help.FullDesc = s.help.ShortDesc
	s.help.FullSeparator = s.help.ShortSeparator

	s.listItem = list.NewDefaultItemStyles(isDark)
	s.listItem.SelectedTitle = s.listItem.SelectedTitle.
		Foreground(s.accent).BorderForeground(s.accent)
	s.listItem.SelectedDesc = s.listItem.SelectedDesc.
		Foreground(s.muted).BorderForeground(s.accent)
	s.listItem.NormalTitle = s.listItem.NormalTitle.Foreground(s.text)
	s.listItem.NormalDesc = s.listItem.NormalDesc.Foreground(s.muted)
	s.listItem.FilterMatch = lipgloss.NewStyle().Underline(true).Bold(true)

	s.list = list.DefaultStyles(isDark)
	s.list.TitleBar = lipgloss.NewStyle()
	s.list.NoItems = s.empty.PaddingLeft(2)
	return s
}

// paneBorderColor is the rule color for a pane: the accent when focused, so
// the active pane is identifiable at a glance (pug's convention).
func (s *styles) paneBorderColor(focused bool) color.Color {
	if focused {
		return s.accent
	}
	return s.border
}

// badge is the style a status badge's glyph is drawn in.
func (s *styles) badge(b Badge) lipgloss.Style {
	switch b {
	case BadgeOK:
		return s.badgeOK
	case BadgePending:
		return s.badgePending
	case BadgeFail:
		return s.badgeFail
	case BadgeOff:
		return s.badgeOff
	}
	return s.value
}
