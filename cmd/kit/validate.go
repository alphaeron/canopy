package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// validateCmd reports integrity violations in one or every KIT file in the
// repository. The output is the validation error table: every violation,
// with the KIT file that carries it. A clean run reports every file valid.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate KIT file integrity",
	Long:  "Validate KIT file structure, rule patterns, and required fields.",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := getRepo()
		if err != nil {
			return err
		}
		all, _ := cmd.Flags().GetBool("all")
		filePath, _ := cmd.Flags().GetString("file")

		var grouped map[string]kitfile.Rules
		if all {
			grouped, err = allKitContent(repo)
			if err != nil {
				return err
			}
		} else {
			path := kitFilePathFor(filePath)
			k, err := loadKitFileContent(path)
			if err != nil {
				return err
			}
			grouped = map[string]kitfile.Rules{path: k.Rules}
		}

		violations, errList := validateGrouped(grouped)

		mode, _ := resolveOutputMode(cmd)
		if mode == modeJSON {
			return tui.JSONEncoder(os.Stdout, violations)
		}
		var header = []string{"KIT File", "Violation"}
		rows := make([][]string, 0, len(errList))
		for path, errs := range violations {
			for _, e := range errs {
				rows = append(rows, []string{path, e})
			}
		}
		if err := tui.TablePrinter(os.Stdout, header, rows); err != nil {
			return err
		}
		if len(violations) == 0 {
			fmt.Printf("All %d KIT file(s) are valid.\n", len(grouped))
		} else {
			return fmt.Errorf("%d violation(s) in %d KIT file(s)", len(errList), len(violations))
		}
		return nil
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
	validateCmd.Flags().String("file", "", "validate only this specific KIT file (default is repo-root KIT)")
	validateCmd.Flags().Bool("all", false, "validate every KIT file in the repository")
	rootCmd.AddCommand(validateCmd)
}
