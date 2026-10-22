package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/match"
)

// coverageCmd computes the fraction of the file universe every rule covers,
// the overall match percentage, and the files that match no rule — the
// reverse of match, reporting gaps rather than hits. --scope narrows the
// coverage table to that one rule, but the overall percentage still
// reflects the full set.
var coverageCmd = &cobra.Command{
	Use:   "coverage",
	Short: "Coverage report of KIT file rules",
}

// runCoverageCmd is coverageCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runCoverageCmd(cmd *cobra.Command, args []string) error {
	changed, _ := cmd.Flags().GetBool("changed")
	scopeName, _ := cmd.Flags().GetString("scope")
	opts := match.Options{ChangedOnly: changed, ScopeName: scopeName}

	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, coverageRoute(valuesFrom(cmd, args)))
	}
	report, _, err := runCoverage(cmd.Context(), opts)
	if err != nil {
		return err
	}
	return renderCoverage(mode, report)
}

// runCoverage computes the coverage report against the local repository,
// with the unfiltered match it was computed from. opts.ScopeName narrows
// the per-scope breakdown in the engine.
func runCoverage(ctx context.Context, opts match.Options) (*match.CoverageReport, *match.Result, error) {
	repo, err := getRepo()
	if err != nil {
		return nil, nil, err
	}
	report, match, err := newLocalEngine(repo, nil).Coverage(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("run coverage: %w", err)
	}
	return report, match, nil
}

// renderCoverage renders the engine's coverage report as JSON or a table.
// The TUI form is coveragePage.
func renderCoverage(mode outputMode, report *match.CoverageReport) error {
	switch mode {
	case modeJSON:
		return writeJSON(os.Stdout, report)
	default:
		fmt.Printf("Total coverage: %.2f%% of %d file(s)\n", report.Percent, report.Universe)
		var header = []string{"Scope", "Pattern", "KIT File", "Files", "Percent"}
		rows := make([][]string, 0, len(report.PerScope))
		for _, sc := range report.PerScope {
			name := sc.Scope.Name
			if name == "" {
				name = sc.Scope.Pattern
			}
			rows = append(rows, []string{name, sc.Scope.Pattern, sc.Scope.KitFile,
				fmt.Sprintf("%d", sc.Files), fmt.Sprintf("%.2f%%", sc.Percent)})
		}
		if err := writeTable(os.Stdout, header, rows); err != nil {
			return err
		}
		if len(report.Uncovered) > 0 {
			fmt.Println("\nUncovered files: " + strings.Join(report.Uncovered, "\n  ") + "  ")
		}
	}
	return nil
}

func init() {
	coverageCmd.RunE = runCoverageCmd
	coverageCmd.Flags().String("scope", "", "restrict the coverage report to this scope name")
	coverageCmd.Flags().Bool("changed", false, "report only over the changed files in the working tree")
	rootCmd.AddCommand(coverageCmd)
}
