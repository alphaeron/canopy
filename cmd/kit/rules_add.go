package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesAddCmd appends a rule to a KIT file. Without --file it edits the
// repository-root KIT. With a TTY and no --pattern it runs the interactive
// form; with --pattern it applies the flags non-interactively.
var rulesAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a rule to a KIT file",
	RunE: func(cmd *cobra.Command, args []string) error {
		targetPath, err := targetKitFile(cmd)
		if err != nil {
			return err
		}

		k, err := loadKitFileContent(targetPath)
		if err != nil {
			return err
		}
		mode, _ := resolveOutputMode(cmd)

		var fields tui.RuleFields
		if mode == modeTUI && !ruleFlagProvided(cmd, "pattern") {
			fields, err = tui.RunForm(tui.RuleFields{Strategy: "union"})
			if err != nil {
				return fmt.Errorf("run form: %w", err)
			}
		} else {
			fields, err = ruleFieldsFromFlags(cmd)
			if err != nil {
				return err
			}
		}

		rule, err := buildRuleFromFields(fields)
		if err != nil {
			return err
		}
		if err := k.Add(rule); err != nil {
			return err
		}
		if err := k.Save(targetPath); err != nil {
			return fmt.Errorf("save %s: %w", targetPath, err)
		}
		if mode == modeJSON {
			return tui.JSONEncoder(os.Stdout, k.Rules)
		}
		fmt.Printf("added rule %q to %s\n", rule.Pattern, targetPath)
		return nil
	},
}

// targetKitFile resolves the --file flag to the KIT file path this command
// mutates, falling back to the repository-root KIT.
func targetKitFile(cmd *cobra.Command) (string, error) {
	filePath, err := cmd.Flags().GetString("file")
	if err != nil {
		return "", err
	}
	return kitFilePathFor(filePath), nil
}

// ruleFlagProvided reports whether the named flag was explicitly set on the
// command line, even if its value equals the flag default. add and edit use
// this to decide which fields the form should own versus override.
func ruleFlagProvided(cmd *cobra.Command, name string) bool {
	return cmd.Flags().Changed(name)
}

// ruleFieldsFromFlags assembles a tui.RuleFields purely from command flags.
// Approver pairs are validated here (not inside the form) so scripted runs
// fail with the precise pair error before any file mutation.
func ruleFieldsFromFlags(cmd *cobra.Command) (tui.RuleFields, error) {
	var f tui.RuleFields
	var err error
	if f.Pattern, err = cmd.Flags().GetString("pattern"); err != nil {
		return f, err
	}
	if f.Name, err = cmd.Flags().GetString("name"); err != nil {
		return f, err
	}
	if f.Strategy, err = cmd.Flags().GetString("strategy"); err != nil {
		return f, err
	}
	if f.Ignore, err = cmd.Flags().GetBool("ignore-parent"); err != nil {
		return f, err
	}
	if f.CurrentDirOnly, err = cmd.Flags().GetBool("current-dir-only"); err != nil {
		return f, err
	}
	if f.SelfApprove, err = cmd.Flags().GetBool("self-approve"); err != nil {
		return f, err
	}
	approvers, err := cmd.Flags().GetStringSlice("approver")
	if err != nil {
		return f, err
	}
	f.ApproverText, err = approverText(approvers)
	return f, err
}

// approverText converts the --approver flag pairs ("target required_count",
// repeatable) into the form text used to build the rule's approver targets.
// An odd pair count or a non-integer count is an error, so a malformed
// scripted invocation never reaches the file writer.
func approverText(pairs []string) (string, error) {
	if len(pairs)%2 != 0 {
		return "", fmt.Errorf("--approver takes 'target required_count' pairs; got %d value(s)", len(pairs))
	}
	targets := make([]kitfile.ApproverTarget, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		count, err := strconv.Atoi(pairs[i+1])
		if err != nil {
			return "", fmt.Errorf("--approver %q: %w", pairs[i+1], err)
		}
		targets = append(targets, kitfile.ApproverTarget{Target: pairs[i], RequiredCount: count})
	}
	return kitfile.ApproversToText(targets), nil
}

// ruleFields returns the union of the form's fields and the explicitly-set
// flag overrides: a flag the user passed on the command line always wins
// over the corresponding form value, while a flag left at its default leaves
// the form's text standing.
func ruleFields(form tui.RuleFields, cmd *cobra.Command) (tui.RuleFields, error) {
	var err error
	if ruleFlagProvided(cmd, "name") {
		if form.Name, err = cmd.Flags().GetString("name"); err != nil {
			return form, err
		}
	}
	if ruleFlagProvided(cmd, "strategy") {
		if form.Strategy, err = cmd.Flags().GetString("strategy"); err != nil {
			return form, err
		}
	}
	if ruleFlagProvided(cmd, "ignore-parent") {
		if form.Ignore, err = cmd.Flags().GetBool("ignore-parent"); err != nil {
			return form, err
		}
	}
	if ruleFlagProvided(cmd, "current-dir-only") {
		if form.CurrentDirOnly, err = cmd.Flags().GetBool("current-dir-only"); err != nil {
			return form, err
		}
	}
	if ruleFlagProvided(cmd, "self-approve") {
		if form.SelfApprove, err = cmd.Flags().GetBool("self-approve"); err != nil {
			return form, err
		}
	}
	if ruleFlagProvided(cmd, "approver") {
		approvers, err := cmd.Flags().GetStringSlice("approver")
		if err != nil {
			return form, err
		}
		if form.ApproverText, err = approverText(approvers); err != nil {
			return form, err
		}
	}
	return form, nil
}

// buildRuleFromFields converts a submitted rule-field set into the KIT file
// rule it appends or replaces. Strategy name and approver text are both
// validated before the rule is returned, so a form submit with an unparseable
// approver line is an error rather than a rule silently losing its approvers.
func buildRuleFromFields(f tui.RuleFields) (kitfile.Rule, error) {
	var rule kitfile.Rule
	strategy := f.Strategy
	if strategy == "" {
		strategy = "union"
	}
	var err error
	rule.CombineStrategy, err = kitfile.ParseRuleCombineStrategy(strategy)
	if err != nil {
		return rule, err
	}
	rule.Approvers, err = kitfile.ParseApprovers(f.ApproverText)
	if err != nil {
		return rule, err
	}
	rule.Pattern = f.Pattern
	rule.Name = f.Name
	rule.IgnoreParentDirs = f.Ignore
	rule.CurrentDirectoryOnly = f.CurrentDirOnly
	rule.SelfApprove = f.SelfApprove
	return rule, nil
}

func init() {
	kitFileFlag(rulesAddCmd)
	rulesAddCmd.Flags().String("pattern", "", "rule pattern (regex); required unless the interactive form is used")
	rulesAddCmd.Flags().String("name", "", "rule name")
	rulesAddCmd.Flags().String("strategy", "union", "combine strategy: union, override, or intersect")
	rulesAddCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	rulesAddCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	rulesAddCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	rulesAddCmd.Flags().StringSlice("approver", nil, `approver pairs 'target required_count', repeatable`)
	rulesCmd.AddCommand(rulesAddCmd)
}
