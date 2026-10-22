package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesListCmd lists the rules of every KIT file in the repository, or of
// one KIT file with --file. Narrowing never changes the output's shape: the
// JSON is always one flat list of rules, each naming its KIT file.
var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the rules in the repository's KIT files",
	Long: `List the rules of every KIT file in the repository's working tree, the
repository-root KIT first. --file narrows the list to one KIT file.`,
}

// runRulesList is rulesListCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runRulesList(cmd *cobra.Command, args []string) error {
	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, rulesListRoute(valuesFrom(cmd, args)))
	}
	filePath, _ := cmd.Flags().GetString("file")
	s, err := openKitSet()
	if err != nil {
		return err
	}
	only, err := s.resolve(filePath)
	if err != nil {
		return err
	}
	entries := s.entries(only)
	if mode == modeJSON {
		return tui.JSONEncoder(os.Stdout, rulesJSON(entries))
	}
	if err := tui.TablePrinter(os.Stdout, []string{"KIT File", "Pattern", "Name", "Strategy", "Approvers"}, ruleRows(entries)); err != nil {
		return err
	}
	return narrowedNotice(os.Stderr, only, len(s.rules))
}

// narrowedNotice tells the user, on stderr so stdout stays parseable, that
// --file hid the other KIT files.
func narrowedNotice(w io.Writer, only string, total int) error {
	if only == "" || total <= 1 {
		return nil
	}
	_, err := fmt.Fprintf(w, "showing %s, 1 of %d KIT files; omit --file to list all\n", only, total)
	return err
}

// ruleJSON is a rule in the rules list JSON: the KIT file fields under
// their KIT file (YAML) names, plus the KIT file that declares it.
type ruleJSON struct {
	KitFile              string         `json:"kit_file"`
	Pattern              string         `json:"pattern"`
	Name                 string         `json:"name,omitempty"`
	CombineStrategy      string         `json:"combine_strategy,omitempty"`
	IgnoreParentDirs     bool           `json:"ignore_parent_dirs"`
	CurrentDirectoryOnly bool           `json:"current_directory_only"`
	SelfApprove          bool           `json:"self_approve"`
	Approvers            []approverJSON `json:"approvers"`
}

type approverJSON struct {
	Target        string `json:"target"`
	RequiredCount int    `json:"required_count"`
}

// rulesJSON converts entries to the JSON list; it is never null.
func rulesJSON(entries []kitfile.DeclaredRule) []ruleJSON {
	out := make([]ruleJSON, 0, len(entries))
	for _, e := range entries {
		approvers := make([]approverJSON, 0, len(e.Rule.Approvers))
		for _, a := range e.Rule.Approvers {
			approvers = append(approvers, approverJSON{Target: a.Target, RequiredCount: a.RequiredCount})
		}
		out = append(out, ruleJSON{
			KitFile:              e.KitFile,
			Pattern:              e.Rule.Pattern,
			Name:                 e.Rule.Name,
			CombineStrategy:      strategyName(e.Rule),
			IgnoreParentDirs:     e.Rule.IgnoreParentDirs,
			CurrentDirectoryOnly: e.Rule.CurrentDirectoryOnly,
			SelfApprove:          e.Rule.SelfApprove,
			Approvers:            approvers,
		})
	}
	return out
}

// ruleRows are the table rows for entries.
func ruleRows(entries []kitfile.DeclaredRule) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		strategy := strategyName(e.Rule)
		if strategy == "" {
			strategy = "(default)"
		}
		rows = append(rows, []string{e.KitFile, e.Rule.Pattern, e.Rule.Name, strategy, approversLabel(e.Rule.Approvers)})
	}
	return rows
}

// strategyName is the rule's combine_strategy as written in the KIT file,
// or "" when it is omitted (shonin then applies its default).
func strategyName(r kitfile.Rule) string {
	if _, err := kitfile.ParseRuleCombineStrategy(r.CombineStrategy.String()); err != nil {
		return ""
	}
	return r.CombineStrategy.String()
}

func init() {
	rulesListCmd.RunE = runRulesList
	rulesListCmd.Flags().String("file", "", "list only this KIT file (path from the repository root, or its directory)")
	rulesListCmd.Flags().Bool("all", false, "list every KIT file")
	cobra.CheckErr(rulesListCmd.Flags().MarkDeprecated("all", "every KIT file is listed by default; use --file to narrow"))
	rulesCmd.AddCommand(rulesListCmd)
}
