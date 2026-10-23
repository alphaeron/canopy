package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
)

// chainTabs are a file's Chain and Trace views.
func chainTabs(fs approval.FileState, state *approval.ReviewState) []tui.Tab {
	return []tui.Tab{
		{Title: "Chain", Sections: []tui.Section{chainSection(fs)}},
		{Title: "Trace", Sections: []tui.Section{{Title: "Events for " + fs.Path, Rows: eventRows(state.FileEvents(fs.Path), false)}}},
	}
}

// chainSection is the cascade of rule layers from the repository root down to
// the file: each layer's status, the rules it applied with their strategy
// and approver progress, and the approvers it requires of the layer below.
// A layer links to its rules file's rules.
func chainSection(fs approval.FileState) tui.Section {
	s := tui.Section{Title: "Layers · root → file"}
	if len(fs.Layers) == 0 {
		s.Lines = []string{"No rule matches this file; the repository owners decide."}
		return s
	}
	for i, l := range fs.Layers {
		rulesPath := rulesfile.PathInDir(l.DirectoryPath)
		r := rulesListRoute(tui.Values{Flags: map[string]string{"file": rulesPath}})
		s.Rows = append(s.Rows, tui.Row{Text: rulesPath, Badge: statusBadge(l.Status), Note: statusLabel(l.Status), Route: &r})
		for j, rule := range l.Rules {
			s.Rows = append(s.Rows, tui.Row{
				Indent: 1,
				Text:   ruleLabel(rule.Name, rule.Pattern),
				Badge:  statusBadge(rule.Status),
				Note:   joinNotes(rule.CombineStrategy.String()+": "+approval.CombineStrategyEffect(rule.CombineStrategy, i == 0 && j == 0), rule.Reason),
			})
			s.Rows = append(s.Rows, targetRows(rule.Approvers, 2)...)
		}
		s.Rows = append(s.Rows, tui.Row{Indent: 1, Text: "→ requires " + orNoneLabel(ruleTargetsLabel(l.Required))})
	}
	return s
}

// targetRows are approver targets with their approval meters, indented.
func targetRows(states []approval.ApproverTargetState, indent int) []tui.Row {
	rows := make([]tui.Row, 0, len(states))
	for _, t := range states {
		note := t.Reason
		if len(t.ResolvedBy) > 0 {
			note = joinNotes("approved by "+strings.Join(t.ResolvedBy, ", "), note)
		}
		rows = append(rows, tui.Row{
			Indent: indent,
			Text:   t.Target,
			Badge:  statusBadge(t.Status),
			Meter:  &tui.Meter{Have: t.SatisfiedCount, Need: t.RequiredCount},
			Note:   note,
		})
	}
	return rows
}

// eventRows are the algorithm's events, naming each one's file when
// withPath is set.
func eventRows(events []approval.Event, withPath bool) []tui.Row {
	var rows []tui.Row
	for _, e := range events {
		where := e.Phase
		if e.Layer != "" {
			where += " " + rulesfile.PathInDir(e.Layer)
		}
		if e.Rule != "" {
			where += " · " + e.Rule
		}
		if withPath && e.Path != "" {
			where += " · " + e.Path
		}
		rows = append(rows, tui.Row{Text: where, Badge: statusBadge(e.Status), Note: e.Note})
	}
	return rows
}

// statusBadge maps an algorithm status to its glyph.
func statusBadge(s approval.Status) tui.Badge {
	switch s {
	case approval.StatusSatisfied, approval.StatusDone:
		return tui.BadgeOK
	case approval.StatusMissingApprovers, approval.StatusActive:
		return tui.BadgePending
	case approval.StatusError, approval.StatusUnresolved:
		return tui.BadgeFail
	case approval.StatusInactive:
		return tui.BadgeOff
	}
	return tui.BadgeNone
}

// requiredOnlyLabel is who must approve a file, without progress: what
// match and rules get show, where no approvals are simulated.
func requiredOnlyLabel(fs approval.FileState) string {
	if fs.UsedFallback {
		return "repository owners (no rule requires approvers)"
	}
	parts := make([]string, len(fs.Required))
	for i, r := range fs.Required {
		parts[i] = fmt.Sprintf("%s (%d)", r.Target, r.RequiredCount)
	}
	return orNoneLabel(strings.Join(parts, ", "))
}

// ownershipBadge marks a file's ownership where no approvals are simulated
// (see approval.FileState.Ownership).
func ownershipBadge(fs approval.FileState) tui.Badge {
	switch fs.Ownership() {
	case approval.OwnershipBroken:
		return tui.BadgeFail
	case approval.OwnedByFallback:
		return tui.BadgePending
	}
	return tui.BadgeOK
}

func ruleTargetsLabel(targets approval.ExecutableTargets) string {
	parts := make([]string, len(targets))
	for i, t := range targets {
		parts[i] = t.Target + " (" + strconv.Itoa(t.RequiredCount) + ")"
	}
	return strings.Join(parts, ", ")
}

func ruleLabel(name, pattern string) string {
	if name == "" {
		return pattern
	}
	return name + ": " + pattern
}

// verdictBadge is the badge for a review's verdict.
func verdictBadge(v approval.Verdict) tui.Badge {
	switch v {
	case approval.Passed:
		return tui.BadgeOK
	case approval.Pending:
		return tui.BadgePending
	}
	return tui.BadgeFail
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " · " + b
}

func orNoneLabel(s string) string {
	if s == "" {
		return "nobody"
	}
	return s
}
