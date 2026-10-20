package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// initCmd scaffolds a new KIT file. It accepts an optional [path], where an
// empty or missing argument writes the repository-root KIT and a directory
// argument writes the KIT file inside that directory. Without --pattern it
// runs the interactive rule form; with --pattern it scaffolds the file and
// the rule together.
var initCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Initialize a KIT file in the current repository",
	Long:  "Initialize a KIT file in the current repository, with an optional rule pre-filled by the form or by flags.",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "KIT"
		if len(args) > 0 {
			if isDir(args[0]) {
				path = filepath.Join(args[0], "KIT")
			} else {
				path = args[0]
			}
		}
		force, _ := cmd.Flags().GetBool("force")

		if _, statErr := os.Stat(path); statErr == nil && !force {
			return fmt.Errorf("KIT file %q already exists; pass --force to overwrite", path)
		}

		ruleName, _ := cmd.Flags().GetString("name")
		pattern, _ := cmd.Flags().GetString("pattern")
		strategy, _ := cmd.Flags().GetString("strategy")
		ignoreParent, _ := cmd.Flags().GetBool("ignore-parent")
		currentDirOnly, _ := cmd.Flags().GetBool("current-dir-only")
		selfApprove, _ := cmd.Flags().GetBool("self-approve")
		approvers, _ := cmd.Flags().GetStringSlice("approver")

		mode, _ := resolveOutputMode(cmd)

		var fields tui.RuleFields
		var err error
		if pattern == "" && mode == modeTUI {
			fields, err = tui.RunForm(tui.RuleFields{Strategy: "union"})
			if err != nil {
				return fmt.Errorf("run form: %w", err)
			}
		} else {
			fields = tui.RuleFields{
				Pattern: pattern, Name: ruleName, Strategy: strategy,
				Ignore: ignoreParent, CurrentDirOnly: currentDirOnly,
				SelfApprove: selfApprove,
			}
			approverText, aErr := approverText(approvers)
			if aErr != nil {
				return aErr
			}
			fields.ApproverText = approverText
		}

		k := &kitfile.KitFile{Path: path}
		if fields.Pattern != "" {
			rule, err := buildRuleFromFields(fields)
			if err != nil {
				return err
			}
			if err := k.Add(rule); err != nil {
				return err
			}
		}
		if err := k.Save(path); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		if mode == modeJSON {
			return tui.JSONEncoder(os.Stdout, k.Rules)
		}
		if len(k.Rules) > 0 {
			fmt.Printf("initialized %s with rule %q\n", path, k.Rules[0].Pattern)
		} else {
			fmt.Printf("initialized empty KIT file at %s\n", path)
		}
		return nil
	},
}

// isDir reports whether the path is a directory; os.Stat errors are treated
// as "not-a-directory" so the caller falls through to the plain-file path.
func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func init() {
	initCmd.Flags().Bool("force", false, "overwrite an existing KIT file")
	initCmd.Flags().String("pattern", "", "optional rule pattern (regex) to register at init time")
	initCmd.Flags().String("name", "", "optional rule name")
	initCmd.Flags().String("strategy", "union", "combine strategy: union, override, or intersect")
	initCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	initCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	initCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	initCmd.Flags().StringSlice("approver", nil, `optional rule approver pairs 'target required_count', repeatable`)
	rootCmd.AddCommand(initCmd)
}
