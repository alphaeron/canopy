package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/alphaeron/kit/internal/kenin"
	"github.com/alphaeron/kit/internal/shonin"
	"github.com/alphaeron/kit/internal/tui"
)

// repoOverview is the home page's root row: the repository at a glance,
// and links to the pages that act on what it shows. A part that cannot be
// computed is reported in its own field rather than hiding the rest.
func repoOverview(context.Context) (tui.Detail, error) {
	repo, err := getRepo()
	if err != nil {
		return tui.Detail{}, err
	}
	s, err := openKitSet()
	if err != nil {
		return tui.Detail{}, err
	}
	d := tui.Detail{Fields: []tui.Field{{Label: "Repository", Value: s.root}}}
	failed := func(label string, err error) tui.Field {
		return tui.Field{Label: label, Value: err.Error(), Badge: tui.BadgeFail}
	}

	switch ref, err := repo.Head(); {
	case err == nil:
		d.Fields = append(d.Fields, tui.Field{Label: "HEAD", Value: ref.Name().Short() + " @ " + ref.Hash().String()[:7]})
	case !repo.HasCommits():
		d.Fields = append(d.Fields, tui.Field{Label: "HEAD", Value: "no commits yet"})
	default:
		d.Fields = append(d.Fields, failed("HEAD", err))
	}

	d.Fields = append(d.Fields, tui.Field{Label: "KIT files", Value: countOf(len(s.rules), "KIT file") + " · " + countOf(len(s.entries("")), "rule")})
	if _, errs := validateGrouped(s.rules); len(errs) > 0 {
		d.Fields = append(d.Fields, tui.Field{Label: "Validation", Value: countOf(len(errs), "violation"), Badge: tui.BadgeFail})
	} else {
		d.Fields = append(d.Fields, tui.Field{Label: "Validation", Value: "valid", Badge: tui.BadgeOK})
	}

	if report, err := runCoverage(kenin.Options{}); err != nil {
		d.Fields = append(d.Fields, failed("Coverage", err))
	} else {
		badge := tui.BadgeOK
		if len(report.Uncovered) > 0 {
			badge = tui.BadgePending
		}
		d.Fields = append(d.Fields, tui.Field{Label: "Coverage", Value: fmt.Sprintf("%.1f%% of %d files", report.Percent, report.Universe), Badge: badge})
	}

	if state, err := runReview(reviewOptions{changed: true}, quietLog); err != nil {
		d.Fields = append(d.Fields, failed("Changed", err))
	} else {
		d.Fields = append(d.Fields,
			tui.Field{Label: "Changed", Value: countOf(len(state.Files), "file")},
			tui.Field{Label: "They need", Value: changedNeeds(state.Files)},
		)
	}

	link := func(text, note string, r tui.Route) tui.Row { return tui.Row{Text: text, Note: note, Route: &r} }
	d.Sections = []tui.Section{{Title: "Go to", Rows: []tui.Row{
		link("Review changed files", "kit review --changed", reviewRoute(tui.Values{Flags: map[string]string{"changed": "true"}})),
		link("Match changed files", "kit match --changed --diff", matchRoute(tui.Values{Flags: map[string]string{"changed": "true", "diff": "true"}})),
		link("Coverage", "kit coverage", coverageRoute(tui.Values{})),
		link("Validate KIT files", "kit validate", validateRoute(tui.Values{})),
		link("Rules", "kit rules list", rulesListRoute(tui.Values{})),
	}}}
	return d, nil
}

// changedNeeds is every approver target the changed files require, each
// with the most approvals any file needs from it, and how many files fall
// back to the repository owners.
func changedNeeds(files map[string]shonin.FileState) string {
	need := map[string]int{}
	fallback := 0
	for _, fs := range files {
		if fs.UsedFallback {
			fallback++
			continue
		}
		for _, r := range fs.Required {
			need[r.Target] = max(need[r.Target], r.RequiredCount)
		}
	}
	var parts []string
	for _, t := range slices.Sorted(maps.Keys(need)) {
		parts = append(parts, fmt.Sprintf("%s (%d)", t, need[t]))
	}
	if fallback > 0 {
		parts = append(parts, countOf(fallback, "file")+" for the repository owners")
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}
