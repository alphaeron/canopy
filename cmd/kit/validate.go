package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// validateCmd reports integrity violations in every KIT file in the
// repository, or in one with --file. The output is the validation error
// table: every violation, with the KIT file that carries it. A clean run
// reports every file valid; any violation exits non-zero.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate KIT file integrity",
	Long: `Validate KIT file structure, rule patterns, and required fields in every KIT
file in the repository's working tree. --file narrows the check to one KIT file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		s, err := openKitSet()
		if err != nil {
			return err
		}
		only, err := s.resolve(filePath)
		if err != nil {
			return err
		}
		grouped := s.rules
		if only != "" {
			grouped = map[string]kitfile.Rules{only: s.rules[only]}
		}

		violations, errList := validateGrouped(grouped)

		mode, _ := resolveOutputMode(cmd)
		if mode == modeJSON {
			return tui.JSONEncoder(os.Stdout, violations)
		}
		var header = []string{"KIT File", "Violation"}
		rows := make([][]string, 0, len(errList))
		for _, path := range s.paths() {
			for _, e := range violations[path] {
				rows = append(rows, []string{path, e})
			}
		}
		if err := tui.TablePrinter(os.Stdout, header, rows); err != nil {
			return err
		}
		if err := narrowedNotice(os.Stderr, only, len(s.rules)); err != nil {
			return err
		}
		if len(violations) == 0 {
			fmt.Printf("All %d KIT file(s) are valid.\n", len(grouped))
			return nil
		}
		return fmt.Errorf("%d violation(s) in %d KIT file(s)", len(errList), len(violations))
	},
}

// validateGrouped runs KitFile.Validate on every KIT file and splits each
// combined error back into its individual violation lines. A file that
// produces no violations does not appear in the result map, so an empty map
// means every file is clean.
func validateGrouped(grouped map[string]kitfile.Rules) (map[string][]string, []string) {
	violations := map[string][]string{}
	var errList []string
	for path, rules := range grouped {
		kf := kitfile.KitFile{Path: path, Rules: rules}
		if err := kf.Validate(); err == nil {
			continue
		} else {
			var errs []string
			var multi interface{ Unwrap() []error }
			if errors.As(err, &multi) {
				for _, e := range multi.Unwrap() {
					errs = append(errs, e.Error())
				}
			} else {
				errs = []string{err.Error()}
			}
			violations[path] = errs
			errList = append(errList, errs...)
		}
	}
	return violations, errList
}

func init() {
	validateCmd.Flags().String("file", "", "validate only this KIT file (path from the repository root, or its directory)")
	validateCmd.Flags().Bool("all", false, "validate every KIT file")
	cobra.CheckErr(validateCmd.Flags().MarkDeprecated("all", "every KIT file is validated by default; use --file to narrow"))
	rootCmd.AddCommand(validateCmd)
}
