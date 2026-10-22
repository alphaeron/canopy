package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
	"github.com/alphaeron/kit/internal/tui"
)

// quietLog discards the review algorithm's structured output in the TUI,
// where a line on stderr would tear the screen. Everything it logs is also
// in the ReviewState the pages render.
var quietLog = slog.New(slog.DiscardHandler)

// reviewRoute is `kit review`: one row per file, most in need of attention
// first, after a summary row for the whole review.
func reviewRoute(v tui.Values) tui.Route {
	return commandRoute(reviewCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		opts, err := reviewOptionsFrom(v)
		if err != nil {
			return nil, err
		}
		state, err := runReview(opts, quietLog)
		if err != nil {
			return nil, err
		}
		return reviewPage(state, opts), nil
	})
}

func reviewPage(state *shonin.ReviewState, opts reviewOptions) tui.Page {
	files := state.SortedFiles()
	counts := map[shonin.Status]int{}
	for _, fs := range files {
		counts[fs.Status]++
	}
	items := make([]tui.Item, 0, len(files)+1)
	items = append(items, tui.Item{
		Title:       "Review",
		Description: statusLabel(state.Overall) + " · " + countOf(len(files), "file"),
		Badge:       statusBadge(state.Overall),
		Detail:      reviewSummaryDetail(state, opts, counts),
	})
	for _, fs := range files {
		r := fileRulesRoute(fs.Path)
		items = append(items, tui.Item{
			Title:       fs.Path,
			Description: statusLabel(fs.Status) + " · " + requiredLabel(fs),
			Badge:       statusBadge(fs.Status),
			Detail:      fileReviewDetail(fs, state.Events),
			Route:       &r,
		})
	}
	var parts []string
	for _, s := range []shonin.Status{shonin.StatusSatisfied, shonin.StatusMissingApprovers, shonin.StatusUnresolved, shonin.StatusError} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, statusLabel(s)))
		}
	}
	summary := "overall " + statusLabel(state.Overall) + " · " + countOf(len(files), "file")
	if len(parts) > 0 {
		summary += ": " + strings.Join(parts, ", ")
	}
	return tui.NewListPage("Review", items).WithSummary(summary)
}

// reviewSummaryDetail is the review as a whole: the inputs it ran with, the
// file counts by status, and every error it collected.
func reviewSummaryDetail(state *shonin.ReviewState, opts reviewOptions, counts map[shonin.Status]int) tui.Detail {
	d := tui.Detail{Fields: []tui.Field{
		{Label: "Overall", Value: statusLabel(state.Overall), Badge: statusBadge(state.Overall)},
		{Label: "Files", Value: filesLabel(opts)},
		{Label: "Author", Value: opts.author},
		{Label: "Approvals", Value: strings.Join(opts.approvers, ", ")},
		{Label: "Owners", Value: strings.Join(opts.owners, ", ")},
		{Label: "Inactive", Value: strings.Join(opts.inactive, ", ")},
	}}
	var rows []tui.Row
	for _, s := range []shonin.Status{shonin.StatusError, shonin.StatusUnresolved, shonin.StatusMissingApprovers, shonin.StatusSatisfied} {
		if n := counts[s]; n > 0 {
			rows = append(rows, tui.Row{Text: statusLabel(s), Badge: statusBadge(s), Note: countOf(n, "file")})
		}
	}
	d.Sections = append(d.Sections, tui.Section{Title: "Files by status", Rows: rows})
	if len(state.Errors) > 0 {
		errs := make([]tui.Row, len(state.Errors))
		for i, e := range state.Errors {
			errs[i] = tui.Row{Text: e, Badge: tui.BadgeFail}
		}
		d.Sections = append(d.Sections, tui.Section{Title: "Errors", Rows: errs})
	}
	d.Tabs = []tui.Tab{{Title: "Trace", Sections: []tui.Section{{Title: "Events", Rows: eventRows(state.Events, nil)}}}}
	return d
}

func filesLabel(opts reviewOptions) string {
	label := "every file"
	if opts.changed {
		label = "changed files"
	}
	if len(opts.patterns) > 0 {
		label += " matching " + strings.Join(opts.patterns, " or ")
	}
	return label
}

// fileReviewDetail is one file's evaluation: its requirement and approval
// progress, then tabs for the chain of KIT layers that produced it and the
// events the algorithm recorded for the file.
func fileReviewDetail(fs shonin.FileState, events []shonin.Event) tui.Detail {
	fallback := "no"
	if fs.UsedFallback {
		fallback = "yes: no rule requires approvers, so the repository owners do"
	}
	d := tui.Detail{
		Fields: []tui.Field{
			{Label: "File", Value: fs.Path},
			{Label: "Status", Value: statusLabel(fs.Status), Badge: statusBadge(fs.Status)},
			{Label: "Requires", Value: requiredLabel(fs)},
			{Label: "Fallback", Value: fallback},
		},
		Sections: []tui.Section{{Title: "Approvers", Rows: targetRows(fs.Required, 0)}},
		Tabs:     chainTabs(fs, events),
	}
	return d
}

// chainTabs are a file's Chain and Trace views.
func chainTabs(fs shonin.FileState, events []shonin.Event) []tui.Tab {
	return []tui.Tab{
		{Title: "Chain", Sections: []tui.Section{chainSection(fs)}},
		{Title: "Trace", Sections: []tui.Section{{Title: "Events for " + fs.Path, Rows: eventRows(events, &fs.Path)}}},
	}
}

// chainSection is the cascade of KIT layers from the repository root down to
// the file: each layer's status, the rules it applied with their strategy
// and approver progress, and the approvers it requires of the layer below.
// A layer links to its KIT file's rules.
func chainSection(fs shonin.FileState) tui.Section {
	s := tui.Section{Title: "Layers · root → file"}
	if len(fs.Layers) == 0 {
		s.Lines = []string{"No rule matches this file; the repository owners decide."}
		return s
	}
	for i, l := range fs.Layers {
		kit := kitfile.PathInDir(l.DirectoryPath)
		r := rulesListRoute(tui.Values{Flags: map[string]string{"file": kit}})
		note := "active"
		if !l.Active {
			note = "inactive"
		}
		if l.Reason != "" {
			note += " · " + l.Reason
		}
		s.Rows = append(s.Rows, tui.Row{Text: kit, Badge: statusBadge(l.Status), Note: note, Route: &r})
		for j, rule := range l.Rules {
			s.Rows = append(s.Rows, tui.Row{
				Indent: 1,
				Text:   ruleLabel(rule.Name, rule.Pattern),
				Badge:  statusBadge(rule.Status),
				Note:   joinNotes(rule.CombineStrategy.String()+": "+strategyEffect(rule.CombineStrategy, i == 0 && j == 0), rule.Reason),
			})
			s.Rows = append(s.Rows, targetRows(rule.Approvers, 2)...)
		}
		s.Rows = append(s.Rows, tui.Row{Indent: 1, Text: "→ requires " + orNoneLabel(ruleTargetsLabel(l.Required))})
	}
	return s
}

// strategyEffect says what a rule's combine strategy does to the approvers
// required by the layers above; the first rule of a chain establishes them.
func strategyEffect(s kitfile.RuleCombineStrategy, first bool) string {
	if first {
		return "establishes the required approvers"
	}
	switch s {
	case kitfile.RuleCombineStrategyUnion:
		return "adds its approvers to those required above"
	case kitfile.RuleCombineStrategyIntersect:
		return "keeps only approvers also required above"
	}
	return "replaces the approvers required above"
}

// targetRows are approver targets with their approval meters, indented.
func targetRows(states []shonin.ApproverTargetState, indent int) []tui.Row {
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

// eventRows are the algorithm's events, only path's when path is non-nil.
func eventRows(events []shonin.Event, path *string) []tui.Row {
	var rows []tui.Row
	for _, e := range events {
		if path != nil && e.Path != *path {
			continue
		}
		where := e.Phase
		if e.Layer != "" {
			where += " " + kitfile.PathInDir(e.Layer)
		}
		if e.Rule != "" {
			where += " · " + e.Rule
		}
		if path == nil && e.Path != "" {
			where += " · " + e.Path
		}
		rows = append(rows, tui.Row{Text: where, Badge: statusBadge(e.Status), Note: e.Note})
	}
	return rows
}

// statusBadge maps an algorithm status to its glyph.
func statusBadge(s shonin.Status) tui.Badge {
	switch s {
	case shonin.StatusSatisfied, shonin.StatusDone:
		return tui.BadgeOK
	case shonin.StatusMissingApprovers, shonin.StatusActive:
		return tui.BadgePending
	case shonin.StatusError, shonin.StatusUnresolved:
		return tui.BadgeFail
	case shonin.StatusInactive:
		return tui.BadgeOff
	}
	return tui.BadgeNone
}

// requiredOnlyLabel is who must approve a file, without progress: what
// match and rules get show, where no approvals are simulated.
func requiredOnlyLabel(fs shonin.FileState) string {
	if fs.UsedFallback {
		return "repository owners (no rule requires approvers)"
	}
	parts := make([]string, len(fs.Required))
	for i, r := range fs.Required {
		parts[i] = fmt.Sprintf("%s (%d)", r.Target, r.RequiredCount)
	}
	return orNoneLabel(strings.Join(parts, ", "))
}

// ownershipBadge marks a file's ownership where no approvals are simulated:
// owned by a rule, falling back to the repository owners, or broken.
func ownershipBadge(fs shonin.FileState) tui.Badge {
	switch {
	case fs.Status == shonin.StatusUnresolved || fs.Status == shonin.StatusError && !fs.UsedFallback:
		return tui.BadgeFail
	case fs.UsedFallback:
		return tui.BadgePending
	}
	return tui.BadgeOK
}

func ruleTargetsLabel(targets shonin.RuleTargets) string {
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
