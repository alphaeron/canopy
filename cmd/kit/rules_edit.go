package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesEditCmd edits one rule by its pattern in a KIT file, replacing the
// rule's name, strategy, flags, and approver list. The pattern itself is
// positional and immutable here, since the pattern is the rule's key.
var rulesEditCmd = &cobra.Command{
	Use:   "edit <pattern>",
	Short: "Edit a rule in a KIT file",
	Long:  "Edit an existing rule in a KIT file, replacing its name, strategy, flags, and approver list.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		targetPath, err := targetKitFile(cmd)
		if err != nil {
			return err
		}

		k, err := loadKitFileContent(targetPath)
		if err != nil {
			return err
		}
		rule, ok := k.RuleByPattern(pattern)
		if !ok {
			return fmt.Errorf("no rule with pattern %q in %s: %w", pattern, targetPath, kitfile.ErrPatternNotFound)
		}

		mode, _ := resolveOutputMode(cmd)
		fields := tui.RuleFields{
			Name:           rule.Name,
			Strategy:       rule.CombineStrategy.String(),
			Ignore:         rule.IgnoreParentDirs,
			CurrentDirOnly: rule.CurrentDirectoryOnly,
			SelfApprove:    rule.SelfApprove,
			ApproverText:   kitfile.ApproversToText(rule.Approvers),
		}
		if mode == modeTUI {
			f, ferr := tui.RunForm(fields)
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
		if err := k.Edit(pattern, func(r *kitfile.Rule) { *r = newRule }); err != nil {
			return err
		}
		if err := k.Save(targetPath); err != nil {
			return fmt.Errorf("save %s: %w", targetPath, err)
		}
		if mode == modeJSON {
			return tui.JSONEncoder(cmd.OutOrStdout(), newRule)
		}
		fmt.Printf("edited rule %q in %s\n", pattern, targetPath)
		return nil
	},
}

func init() {
	kitFileFlag(rulesEditCmd)
	rulesEditCmd.Flags().String("name", "", "the rule's display name")
	rulesEditCmd.Flags().String("strategy", "", "combine strategy: union, override, or intersect")
	rulesEditCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	rulesEditCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	rulesEditCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	rulesEditCmd.Flags().StringSlice("approver", nil, `replace the rule's approver list with these 'target required_count' pairs`)
	rulesCmd.AddCommand(rulesEditCmd)
}
