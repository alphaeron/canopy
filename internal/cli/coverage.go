package cli

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/rulesedit"
	"github.com/alphaeron/canopy/internal/tui"
)

// coverageCmd computes the fraction of the file universe every rule covers,
// the overall match percentage, and the files that match no rule — the
// reverse of match, reporting gaps rather than hits. --scope narrows the
// coverage table to that one rule, but the overall percentage still
// reflects the full set.
var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Coverage report of rules file rules",
}

// runCoverageCmd is coverageCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runCoverageCmd(cmd *cobra.Command, args []string) error {
	changed, _ := cmd.Flags().GetBool("changed")
	scopeName, _ := cmd.Flags().GetString("scope")
	opts := review.Options{ChangedOnly: changed, ScopeName: scopeName}

	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, coverageRoute(valuesFrom(cmd, args)))
	}
	report, _, err := runCoverage(cmd.Context(), opts, cliLog())
	if err != nil {
		return err
	}
	return renderCoverage(mode, report)
}

// runCoverage computes the coverage report against the local repository,
// with the unfiltered match it was computed from. opts.ScopeName narrows
// the per-scope breakdown in the engine. log is as for openEngine.
func runCoverage(ctx context.Context, opts review.Options, log *slog.Logger) (*review.CoverageReport, *review.Result, error) {
	engine, err := openEngine(log)
	if err != nil {
		return nil, nil, err
	}
	return coverageWith(ctx, engine, opts)
}

// coverageWith is runCoverage on an open engine.
func coverageWith(ctx context.Context, engine *review.Engine, opts review.Options) (*review.CoverageReport, *review.Result, error) {
	report, match, err := engine.Coverage(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("run coverage: %w", err)
	}
	return report, match, nil
}

// renderCoverage renders the engine's coverage report as JSON or a table.
// The TUI form is coveragePage.
func renderCoverage(mode outputMode, report *review.CoverageReport) error {
	switch mode {
	case modeJSON:
		return writeJSON(os.Stdout, report)
	default:
		fmt.Printf("Total coverage: %.2f%% of %d file(s)\n", report.Percent, report.Universe)
		var header = []string{"Scope", "Pattern", "Rules File", "Position", "Files", "Percent"}
		rows := make([][]string, 0, len(report.PerRule))
		for _, rc := range report.PerRule {
			rows = append(rows, []string{ruleTitle(rc.Rule), rc.Pattern, rc.RulesFile, strconv.Itoa(rc.Position),
				fmt.Sprintf("%d", rc.Files), fmt.Sprintf("%.2f%%", rc.Percent)})
		}
		if err := writeTable(os.Stdout, header, rows); err != nil {
			return err
		}
		if len(report.Uncovered) > 0 {
			fmt.Println("\nUncovered files:\n  " + strings.Join(report.Uncovered, "\n  "))
		}
	}
	return nil
}

func coverageRoute(v tui.Values) tui.Route {
	return commandRoute(coverageCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		opts := review.Options{ChangedOnly: v.Bool("changed"), ScopeName: v.Flag("scope")}
		repo, engine, err := openLocal(quietLog)
		if err != nil {
			return nil, err
		}
		report, match, err := coverageWith(ctx, engine, opts)
		if err != nil {
			return nil, err
		}
		s, err := loadWorkspace(ctx, repo, engine)
		if err != nil {
			return nil, err
		}
		return coveragePage(report, match, s), nil
	})
}

// detailFilesMax caps the file rows a detail lists inline; the drill-down
// has them all.
const detailFilesMax = 50

// coveragePage lists each scope's coverage with the files it matched; a
// scope with a name drills into `match --scope <name>`. The uncovered
// files are the last row, grouped by directory, each directory linking to
// `rules add` for its nearest rules file.
func coveragePage(report *review.CoverageReport, match *review.Result, s *rulesedit.Workspace) tui.Page {
	items := make([]tui.Item, 0, len(report.PerRule)+1)
	for _, sc := range report.PerRule {
		d := tui.Detail{
			Fields: append(ruleFieldsDetail(sc.Rule),
				tui.Field{Label: "Files", Value: strconv.Itoa(sc.Files)},
				tui.Field{Label: "Coverage", Value: fmt.Sprintf("%.2f%%", sc.Percent)},
			),
			Sections: []tui.Section{fileLinks("Files", match.FilesOf(sc.Key()))},
		}
		it := tui.Item{
			Title:       ruleTitle(sc.Rule) + " in " + sc.Location(),
			Description: fmt.Sprintf("%d file(s), %.1f%%", sc.Files, sc.Percent),
			Detail:      d,
		}
		if sc.Name != "" {
			r := matchRoute(tui.Values{Flags: map[string]string{"scope": sc.Name}})
			it.Route = &r
		}
		items = append(items, it)
	}
	uncovered := tui.Item{
		Title:       "Uncovered files",
		Description: fmt.Sprintf("%d file(s) match no rule", len(report.Uncovered)),
		Detail:      tui.Detail{Sections: []tui.Section{uncoveredSection(report.Uncovered, s)}},
	}
	if len(report.Uncovered) > 0 {
		uncovered.Badge = tui.BadgePending
	}
	items = append(items, uncovered)
	return tui.NewListPage("Coverage", items).
		WithSummary(fmt.Sprintf("Total coverage: %.2f%% of %d file(s)", report.Percent, report.Universe))
}

// fileLinks lists files as links to their rule chains, at most
// detailFilesMax of them.
func fileLinks(title string, files []string) tui.Section {
	sec := tui.Section{Title: fmt.Sprintf("%s (%d)", title, len(files))}
	for i, f := range files {
		if i == detailFilesMax {
			sec.Rows = append(sec.Rows, tui.Row{Text: fmt.Sprintf("+%d more", len(files)-i), Note: "enter on the row lists them all"})
			break
		}
		r := fileRulesRoute(f)
		sec.Rows = append(sec.Rows, tui.Row{Text: f, Route: &r})
	}
	return sec
}

// dirFiles is a directory with the files directly in it.
type dirFiles struct {
	// Dir is repository-relative and slash-separated; "." is the root, as
	// rulesedit.Workspace.NearestTo takes it.
	Dir   string
	Files []string
}

// groupByDir groups files by the directory each is directly in, the
// directories sorted and each keeping its files' order.
func groupByDir(files []string) []dirFiles {
	byDir := map[string][]string{}
	for _, f := range files {
		d := path.Dir(f)
		byDir[d] = append(byDir[d], f)
	}
	out := make([]dirFiles, 0, len(byDir))
	for _, d := range slices.Sorted(maps.Keys(byDir)) {
		out = append(out, dirFiles{Dir: d, Files: byDir[d]})
	}
	return out
}

// uncoveredSection groups uncovered files by directory. Each directory
// links to adding a rule for it to its nearest rules file, or to `init`
// when the repository has none.
func uncoveredSection(files []string, s *rulesedit.Workspace) tui.Section {
	sec := tui.Section{Title: "By directory · enter adds a rule"}
	for _, g := range groupByDir(files) {
		d := g.Dir
		nearest := s.NearestTo(d)
		var r tui.Route
		note := countOf(len(g.Files), "file")
		if nearest == "" {
			r = initRoute(tui.Values{})
			note += " · no rules file yet"
		} else {
			flags := map[string]string{"file": nearest}
			if pattern := rulesedit.DirPattern(d); pattern != "" {
				flags["pattern"] = pattern
			}
			r = rulesAddRoute(tui.Values{Flags: flags})
			note += " · nearest rules file: " + nearest
		}
		label := d + "/"
		if d == "." {
			label = "repository root"
		}
		sec.Rows = append(sec.Rows, tui.Row{Text: label, Note: note, Route: &r})
		for i, f := range g.Files {
			if i == detailFilesMax {
				sec.Rows = append(sec.Rows, tui.Row{Indent: 1, Text: fmt.Sprintf("+%d more", len(g.Files)-i)})
				break
			}
			sec.Rows = append(sec.Rows, tui.Row{Indent: 1, Text: path.Base(f)})
		}
	}
	return sec
}

func init() {
	coverageCmd.RunE = runCoverageCmd
	coverageCmd.Flags().String("scope", "", "restrict the coverage report to this scope name")
	coverageCmd.Flags().Bool("changed", false, "report only over the changed files in the working tree")
	rootCmd.AddCommand(coverageCmd)
}
