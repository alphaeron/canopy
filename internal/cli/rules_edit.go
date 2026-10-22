package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesEditCmd edits one rule by its pattern, replacing the rule's name,
// strategy, flags, and approver list. The pattern is positional and
// immutable, since it is the rule's key within its rules file. Without --file
// the rules file is the one declaring the pattern; when several do, a
// terminal gets a picker and a script must pass --file (see
// rulesSet.ruleTarget).
var rulesEditCmd = &cobra.Command{
	Use:         "edit <pattern>",
	Short:       "Edit a rule in a rules file",
	Long:        "Edit an existing rule in a rules file, replacing its name, strategy, flags, and approver list.",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"arg": "the pattern of the rule to edit"},
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		mode, _ := resolveOutputMode(cmd)
		s, err := openRulesSet(cmd.Context())
		if err != nil {
			return err
		}
		fileFlag, _ := cmd.Flags().GetString("file")
		target, err := s.ruleTarget(fileFlag, pattern, mode == modeTUI)
		if errors.Is(err, tui.ErrAborted) {
			fmt.Fprintln(os.Stderr, "edit aborted")
			return nil
		}
		if err != nil {
			return err
		}
		rule, err := s.rule(target, pattern)
		if err != nil {
			return err
		}

		fields := fieldsFromRule(rule)
		if mode == modeTUI {
			if fields, err = runRuleForm(fields, nil); err != nil {
				return fmt.Errorf("run form: %w", err)
			}
		}
		// Flags given on the command line win over the form; the others
		// leave the rule's values standing.
		if fields, err = ruleFieldsFrom(fields, valuesFrom(cmd, args)); err != nil {
			return err
		}

		newRule, err := buildRuleFromFields(fields)
		if err != nil {
			return err
		}
		newRule.Pattern = pattern
		if _, err := s.mutate(target, func(k *rulesfile.File) error {
			return k.Edit(pattern, func(r *rulesfile.Rule) { *r = newRule })
		}); err != nil {
			return err
		}
		if mode == modeJSON {
			return writeJSON(cmd.OutOrStdout(), newRule)
		}
		fmt.Printf("edited rule %q in %s\n", pattern, target)
		return nil
	},
}

func init() {
	rulesFileFlag(rulesEditCmd, "the rules file holding the rule (default: the one rules file with that pattern)")
	rulesEditCmd.Flags().String("name", "", "the rule's display name")
	rulesEditCmd.Flags().String("strategy", "", "combine strategy: union, override, or intersect")
	rulesEditCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	rulesEditCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	rulesEditCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	rulesEditCmd.Flags().StringSlice("approver", nil, `replace the rule's approver list with these 'target required_count' pairs`)
	tuiUnset(rulesEditCmd, "file", "the rules file declaring the pattern")
	// Unset keeps the rule's value; --name '' and --approver '' clear it.
	for _, f := range []string{"name", "strategy", "ignore-parent", "current-dir-only", "self-approve", "approver"} {
		tuiUnset(rulesEditCmd, f, "unchanged")
	}
	rulesCmd.AddCommand(rulesEditCmd)
}
