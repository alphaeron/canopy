package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// validateCmd reports integrity violations in every rules file in the
// repository, or in one with --file. The output is the validation error
// table: every violation, with the rules file that carries it. A clean run
// reports every file valid; any violation exits non-zero.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate rules file integrity",
	Long: `Validate rules file structure, rule patterns, and required fields in every rules
file in the repository's working tree. --file narrows the check to one rules file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		s, err := openRulesSet(cmd.Context())
		if err != nil {
			return err
		}
		only, err := s.resolve(filePath)
		if err != nil {
			return err
		}
		grouped := s.rules
		if only != "" {
			grouped = rulesfile.Set{only: s.rules[only]}
		}

		violations := grouped.Validate()
		total := violationCount(violations)

		mode, _ := resolveOutputMode(cmd)
		if mode == modeJSON {
			return writeJSON(os.Stdout, violationText(violations))
		}
		var header = []string{"Rules File", "Violation"}
		rows := make([][]string, 0, total)
		for _, path := range s.rules.Paths() {
			for _, e := range violations[path] {
				rows = append(rows, []string{path, e.Error()})
			}
		}
		if err := writeTable(os.Stdout, header, rows); err != nil {
			return err
		}
		if err := narrowedNotice(os.Stderr, only, len(s.rules)); err != nil {
			return err
		}
		if len(violations) == 0 {
			fmt.Printf("All %d rules file(s) are valid.\n", len(grouped))
			return nil
		}
		return fmt.Errorf("%d violation(s) in %d rules file(s)", total, len(violations))
	},
}

// violationCount is the number of violations across every rules file.
func violationCount(violations map[string][]error) int {
	n := 0
	for _, errs := range violations {
		n += len(errs)
	}
	return n
}

// violationText is violations as message strings, the validate JSON form.
func violationText(violations map[string][]error) map[string][]string {
	out := make(map[string][]string, len(violations))
	for p, errs := range violations {
		for _, e := range errs {
			out[p] = append(out[p], e.Error())
		}
	}
	return out
}

func init() {
	validateCmd.Flags().String("file", "", "validate only this rules file (path from the repository root, or its directory)")
	rootCmd.AddCommand(validateCmd)
}
