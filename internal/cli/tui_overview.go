package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/tui"
)

// repoOverview is the home page's root row: the repository at a glance,
// and links to the pages that act on what it shows. A part that cannot be
// computed is reported in its own field rather than hiding the rest.
//
// Every part reads one snapshot of the repository through one engine, so
// the rules are fetched, and the working tree and HEAD listed, once.
func repoOverview(ctx context.Context) (tui.Detail, error) {
	repo, engine, err := openLocal(quietLog)
	if err != nil {
		return tui.Detail{}, err
	}
	s, err := loadWorkspace(ctx, repo, engine)
	if err != nil {
		return tui.Detail{}, err
	}
	d := tui.Detail{Fields: []tui.Field{{Label: "Repository", Value: s.Root}}}
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

	d.Fields = append(d.Fields, tui.Field{Label: "rules files", Value: joinNotes(countOf(len(s.Rules), "rules file")+" · "+countOf(len(s.Entries("")), "rule"), brokenNote(s))})
	if n := violationCount(s.Validate("")); n > 0 {
		d.Fields = append(d.Fields, tui.Field{Label: "Validation", Value: countOf(n, "violation"), Badge: tui.BadgeFail})
	} else {
		d.Fields = append(d.Fields, tui.Field{Label: "Validation", Value: "valid", Badge: tui.BadgeOK})
	}

	if report, _, err := coverageWith(ctx, engine, review.Options{}); err != nil {
		d.Fields = append(d.Fields, failed("Coverage", err))
	} else {
		badge := tui.BadgeOK
		if len(report.Uncovered) > 0 {
			badge = tui.BadgePending
		}
		d.Fields = append(d.Fields, tui.Field{Label: "Coverage", Value: fmt.Sprintf("%.1f%% of %d files", report.Percent, report.Universe), Badge: badge})
	}

	if res, err := reviewWith(ctx, engine, reviewOptions{changed: true}); err != nil {
		d.Fields = append(d.Fields, failed("Changed", err))
	} else {
		d.Fields = append(d.Fields,
			tui.Field{Label: "Changed", Value: countOf(len(res.state.Files), "file")},
			tui.Field{Label: "They need", Value: changedNeeds(res.state)},
		)
	}

	link := func(text, note string, r tui.Route) tui.Row { return tui.Row{Text: text, Note: note, Route: &r} }
	d.Sections = []tui.Section{{Title: "Go to", Rows: []tui.Row{
		link("Review changed files", brand.Command+" review --changed", reviewRoute(tui.Values{Flags: map[string]string{"changed": "true"}})),
		link("Match changed files", brand.Command+" match --changed --diff", matchRoute(tui.Values{Flags: map[string]string{"changed": "true", "diff": "true"}})),
		link("Coverage", brand.Command+" coverage", coverageRoute(tui.Values{})),
		link("Validate rules files", brand.Command+" validate", validateRoute(tui.Values{})),
		link("Rules", brand.Command+" rules list", rulesListRoute(tui.Values{})),
	}}}
	return d, nil
}

// changedNeeds labels who the changed files need: each approver target
// with the most approvals any file needs from it, and how many files fall
// back to the repository owners.
func changedNeeds(state *approval.ReviewState) string {
	targets, fallback := state.RequiredTargets()
	var parts []string
	if label := approversLabel(targets); label != "" {
		parts = append(parts, label)
	}
	if fallback > 0 {
		parts = append(parts, countOf(fallback, "file")+" for the repository owners")
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}
