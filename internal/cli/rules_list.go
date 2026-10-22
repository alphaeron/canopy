package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// rulesListCmd lists the rules of every rules file in the repository, or of
// one rules file with --file. Narrowing never changes the output's shape: the
// JSON is always one flat list of rules, each naming its rules file.
var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the rules in the repository's rules files",
	Long: `List the rules of every rules file in the repository's working tree, the
repository-root rules file first. --file narrows the list to one rules file.`,
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
	s, err := openRulesSet(cmd.Context())
	if err != nil {
		return err
	}
	only, err := s.resolve(filePath)
	if err != nil {
		return err
	}
	entries := s.entries(only)
	if mode == modeJSON {
		return writeJSON(os.Stdout, rulesJSON(entries))
	}
	if err := writeTable(os.Stdout, []string{"Rules File", "Pattern", "Name", "Strategy", "Approvers"}, ruleRows(entries)); err != nil {
		return err
	}
	return narrowedNotice(os.Stderr, only, len(s.rules))
}

// narrowedNotice tells the user, on stderr so stdout stays parseable, that
// --file hid the other rules files.
func narrowedNotice(w io.Writer, only string, total int) error {
	if only == "" || total <= 1 {
		return nil
	}
	_, err := fmt.Fprintf(w, "showing %s, 1 of %d rules files; omit --file to list all\n", only, total)
	return err
}

// ruleJSON is a rule in the rules list JSON: the rules file fields under
// their rules file (YAML) names, plus the rules file that declares it.
type ruleJSON struct {
	RulesFile            string         `json:"rules_file"`
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
func rulesJSON(entries []rulesfile.DeclaredRule) []ruleJSON {
	out := make([]ruleJSON, 0, len(entries))
	for _, e := range entries {
		approvers := make([]approverJSON, 0, len(e.Rule.Approvers))
		for _, a := range e.Rule.Approvers {
			approvers = append(approvers, approverJSON{Target: a.Target, RequiredCount: a.RequiredCount})
		}
		out = append(out, ruleJSON{
			RulesFile:            e.RulesFile,
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
func ruleRows(entries []rulesfile.DeclaredRule) [][]string {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		strategy := strategyName(e.Rule)
		if strategy == "" {
			strategy = "(default)"
		}
		rows = append(rows, []string{e.RulesFile, e.Rule.Pattern, e.Rule.Name, strategy, approversLabel(e.Rule.Approvers)})
	}
	return rows
}

// strategyName is the rule's combine_strategy as written in the rules file,
// or "" when it is omitted (the approval package then applies its default).
func strategyName(r rulesfile.Rule) string {
	if _, err := rulesfile.ParseRuleCombineStrategy(r.CombineStrategy.String()); err != nil {
		return ""
	}
	return r.CombineStrategy.String()
}

func init() {
	rulesListCmd.RunE = runRulesList
	rulesListCmd.Flags().String("file", "", "list only this rules file (path from the repository root, or its directory)")
	rulesCmd.AddCommand(rulesListCmd)
}
