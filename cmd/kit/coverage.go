package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kenin"
	"github.com/alphaeron/kit/internal/tui"
)

// coverageCmd computes the fraction of the file universe every rule covers,
// the overall match percentage, and the files that match no rule — the
// reverse of match, reporting gaps rather than hits. --scope narrows the
// coverage table to that one rule, but the overall percentage still
// reflects the full set.
var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Coverage report of KIT file rules",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := getRepo()
		if err != nil {
			return err
		}
		engine, err := newLocalEngine(repo)
		if err != nil {
			return err
		}

		changed, _ := cmd.Flags().GetBool("changed")
		scopeName, _ := cmd.Flags().GetString("scope")

		report, err := engine.Coverage(kenin.Options{ChangedOnly: changed})
		if err != nil {
			return fmt.Errorf("run coverage: %w", err)
		}

		if scopeName != "" {
			report = filterCoverageByScope(report, scopeName)
		}

		mode, _ := resolveOutputMode(cmd)
		return renderCoverage(mode, report)
	},
}

// renderCoverage renders the engine's coverage report in any of the three
// CLI output modes. TUI mode lists the per-scope breakdown rows; the
// uncovered files are a plain list underneath, and the overall percentage
// is always the last row.
func renderCoverage(mode outputMode, report *kenin.CoverageReport) error {
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, report)
	case modeTUI:
		rows := make([]tui.Row, 0, len(report.PerScope))
		for _, sc := range report.PerScope {
			title := sc.Scope.Name
			if title == "" {
				title = sc.Scope.Pattern
			}
			rows = append(rows, tui.Row{
				Title:    title + " in " + sc.Scope.KITFile,
				Subtitle: fmt.Sprintf("%d file(s), %.1f%%", sc.Files, sc.Percent),
				Data:     sc,
			})
		}
		if _, err := tui.RunListView(rows); err != nil {
			return fmt.Errorf("render coverage: %w", err)
		}
		fmt.Printf("Total coverage: %.2f%% of %d file(s)\n", report.Percent, report.Universe)
		if len(report.Uncovered) > 0 {
			fmt.Println("\nUncovered files:")
			for _, f := range report.Uncovered {
				fmt.Println("  " + f)
			}
		}
	default:
		fmt.Printf("Total coverage: %.2f%% of %d file(s)\n", report.Percent, report.Universe)
		var header = []string{"Scope", "Pattern", "KIT File", "Files", "Percent"}
		rows := make([][]string, 0, len(report.PerScope))
		for _, sc := range report.PerScope {
			name := sc.Scope.Name
			if name == "" {
				name = sc.Scope.Pattern
			}
			rows = append(rows, []string{name, sc.Scope.Pattern, sc.Scope.KITFile,
				fmt.Sprintf("%d", sc.Files), fmt.Sprintf("%.2f%%", sc.Percent)})
		}
		if err := tui.TablePrinter(os.Stdout, header, rows); err != nil {
			return err
		}
		if len(report.Uncovered) > 0 {
			fmt.Println("\nUncovered files: " + strings.Join(report.Uncovered, "\n  ") + "  ")
		}
	}
	return nil
}

// filterCoverageByScope returns a copy of the report that retains only the
// scope whose name equals name, leaving the overall universe / covered
// numbers untouched — this is the projection --scope <name> asks for.
func filterCoverageByScope(report *kenin.CoverageReport, name string) *kenin.CoverageReport {
	per := make([]kenin.ScopeCoverage, 0, len(report.PerScope))
	for _, sc := range report.PerScope {
		if sc.Scope.Name != name {
			continue
		}
		per = append(per, sc)
	}
	return &kenin.CoverageReport{
		Universe:  report.Universe,
		Covered:   report.Covered,
		Percent:   report.Percent,
		PerScope:  per,
		Uncovered: report.Uncovered,
	}
}

func init() {
	coverageCmd.Flags().String("scope", "", "restrict the coverage report to this scope name")
	coverageCmd.Flags().Bool("changed", false, "report only over the changed files in the working tree")
	rootCmd.AddCommand(coverageCmd)
}
