package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesEditCmd edits one rule by its pattern, replacing the rule's name,
// strategy, flags, and approver list. The pattern is positional and
// immutable, since it is the rule's key within its KIT file. Without --file
// the KIT file is the one declaring the pattern; when several do, a
// terminal gets a picker and a script must pass --file (see
// kitSet.ruleTarget).
var rulesEditCmd = &cobra.Command{
	Use:   "edit <pattern>",
	Short: "Edit a rule in a KIT file",
	Long:  "Edit an existing rule in a KIT file, replacing its name, strategy, flags, and approver list.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		mode, _ := resolveOutputMode(cmd)
		s, err := openKitSet()
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
		i := slices.IndexFunc(s.rules[target], func(r kitfile.Rule) bool { return r.Pattern == pattern })
		if i < 0 {
			return fmt.Errorf("no rule with pattern %q in %s: %w", pattern, target, kitfile.ErrPatternNotFound)
		}

		fields := fieldsFromRule(s.rules[target][i])
		if mode == modeTUI {
			f, ferr := tui.RunForm(fields, nil)
			if ferr != nil {
				return fmt.Errorf("run form: %w", ferr)
			}
			fields, err = ruleFields(f, cmd)
			if err != nil {
				return err
			}
		} else {
			// Scripted mode: only flag-provided fields mutate; empty flags
			// leave the pre-filled values standing so a single-field edit
			// (e.g., --self-approve) does not zero out the other fields.
			fields, err = ruleFields(fields, cmd)
			if err != nil {
				return err
			}
		}

		newRule, err := buildRuleFromFields(fields)
		if err != nil {
			return err
		}
		newRule.Pattern = pattern
		if _, err := s.mutate(target, func(k *kitfile.KitFile) error {
			return k.Edit(pattern, func(r *kitfile.Rule) { *r = newRule })
		}); err != nil {
			return err
		}
		if mode == modeJSON {
			return tui.JSONEncoder(cmd.OutOrStdout(), newRule)
		}
		fmt.Printf("edited rule %q in %s\n", pattern, target)
		return nil
	},
}

func init() {
	kitFileFlag(rulesEditCmd, "the KIT file holding the rule (default: the one KIT file with that pattern)")
	rulesEditCmd.Flags().String("name", "", "the rule's display name")
	rulesEditCmd.Flags().String("strategy", "", "combine strategy: union, override, or intersect")
	rulesEditCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	rulesEditCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	rulesEditCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	rulesEditCmd.Flags().StringSlice("approver", nil, `replace the rule's approver list with these 'target required_count' pairs`)
	rulesCmd.AddCommand(rulesEditCmd)
}
