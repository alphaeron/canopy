package tui

// Detail is the drill-down content shown beside the hovered row of a list or
// tree: a block of labelled fields followed by titled sections, and
// optionally further views of the same row (Tabs). Callers describe what to
// show; the TUI owns how it is styled.
type Detail struct {
	Fields   []Field
	Sections []Section
	// Tabs are further views of the row, after the implicit "Summary" view
	// made of Fields and Sections: the rule chain, the event trace, a diff.
	// [ and ] switch between them; on very wide terminals the summary stays
	// in its own column and the selected tab fills a third one.
	Tabs []Tab
}

// Tab is one named view of a Detail beyond its summary.
type Tab struct {
	Title    string
	Fields   []Field
	Sections []Section
}

// Field is one "label  value" line in a Detail. A Badge, when set, precedes
// the value.
type Field struct {
	Label string
	Value string
	Badge Badge
}

// Section is a titled block in a Detail: plain Lines, then structured Rows.
// A Diff section colors unified-diff lines (additions, deletions, hunk
// headers).
type Section struct {
	Title string
	Lines []string
	Diff  bool
	Rows  []Row
}

// Row is one structured line of a Section: indented text with an optional
// status badge, approval meter, and muted note. A Row with a Route is a link:
// when the detail pane is focused, ↑/↓ move between links and enter opens
// the selected one.
type Row struct {
	Text   string
	Note   string
	Indent int
	Badge  Badge
	Meter  *Meter
	Route  *Route
}

// Meter is an approval count against its requirement, drawn as "▓▓░ 2/3".
type Meter struct {
	Have, Need int
}

// Badge is a status glyph shown before a row, field value, or list title.
type Badge int

const (
	// BadgeNone draws no glyph.
	BadgeNone Badge = iota
	// BadgeOK is a satisfied or healthy state.
	BadgeOK
	// BadgePending is a state waiting on something, such as approvals.
	BadgePending
	// BadgeFail is an error or an unsatisfiable state.
	BadgeFail
	// BadgeOff is an inactive or skipped state.
	BadgeOff
)

// Glyph is the badge's character, or "" for BadgeNone.
func (b Badge) Glyph() string {
	switch b {
	case BadgeOK:
		return "✓"
	case BadgePending:
		return "●"
	case BadgeFail:
		return "✗"
	case BadgeOff:
		return "○"
	}
	return ""
}

// withGlyph prefixes s with b's glyph, uncolored, for places whose style is
// applied by a bubbles component (list and tree rows).
func withGlyph(b Badge, s string) string {
	if g := b.Glyph(); g != "" {
		return g + " " + s
	}
	return s
}

// views are the detail's tabs, the summary first.
func (d Detail) views() []Tab {
	return append([]Tab{{Title: "Summary", Fields: d.Fields, Sections: d.Sections}}, d.Tabs...)
}
