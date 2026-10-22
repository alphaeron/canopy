package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesAddCmd appends a rule to a rules file. --file names the rules file. On a
// terminal without --pattern it runs the interactive form, which starts with a
// rules file picker (preselecting the rules file nearest the current directory)
// unless --file chose one; with --pattern it applies the flags
// non-interactively, writing to the repository-root rules file unless --file
// says otherwise.
var rulesAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a rule to a rules file",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := openRulesSet(cmd.Context())
		if err != nil {
			return err
		}
		fileFlag, _ := cmd.Flags().GetString("file")
		target, err := s.resolve(fileFlag)
		if err != nil {
			return err
		}
		mode, _ := resolveOutputMode(cmd)

		var fields ruleFields
		if mode == modeTUI && !cmd.Flags().Changed("pattern") {
			seed := ruleFields{Strategy: "union"}
			var choices []string
			if target == "" {
				if choices = s.rules.Paths(); len(choices) == 0 {
					return errNoRulesFiles
				}
				seed.RulesFile = s.nearest()
			}
			fields, err = runRuleForm(seed, choices)
			if err != nil {
				return fmt.Errorf("run form: %w", err)
			}
			// The form requires a pattern, so an empty one means it was
			// cancelled.
			if fields.Pattern == "" {
				fmt.Fprintln(os.Stderr, "add aborted")
				return nil
			}
			if target == "" {
				target = fields.RulesFile
			}
		} else {
			fields, err = ruleFieldsFrom(ruleFields{Strategy: "union"}, valuesFrom(cmd, args))
			if err != nil {
				return err
			}
			if target == "" {
				if target, err = s.resolve(rulesfile.FileName); err != nil {
					return err
				}
			}
		}

		rule, err := buildRuleFromFields(fields)
		if err != nil {
			return err
		}
		k, err := s.mutate(target, func(k *rulesfile.File) error { return k.Add(rule) })
		if err != nil {
			return err
		}
		if mode == modeJSON {
			return writeJSON(os.Stdout, k.Rules)
		}
		fmt.Printf("added rule %q to %s\n", rule.Pattern, target)
		return nil
	},
}

// approverText converts the --approver flag pairs ("target required_count",
// repeatable) into the form text used to build the rule's approver targets.
// An odd pair count or a non-integer count is an error, so a malformed
// scripted invocation never reaches the file writer.
func approverText(pairs []string) (string, error) {
	if len(pairs)%2 != 0 {
		return "", fmt.Errorf("--approver takes 'target required_count' pairs; got %d value(s)", len(pairs))
	}
	targets := make([]rulesfile.ApproverTarget, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		count, err := strconv.Atoi(pairs[i+1])
		if err != nil {
			return "", fmt.Errorf("--approver %q: %w", pairs[i+1], err)
		}
		targets = append(targets, rulesfile.ApproverTarget{Target: pairs[i], RequiredCount: count})
	}
	return rulesfile.ApproversToText(targets), nil
}

// ruleFieldsFrom applies the rule flags set in v over seed: a flag given on
// the command line (see valuesFrom) or in the TUI's options form always
// wins, while an unset one leaves seed's value standing, so a single-field
// rules edit does not zero out the other fields. init and add seed the
// flags' defaults. Approver pairs are validated here, so a malformed value
// fails with the precise pair error before any file is written.
func ruleFieldsFrom(seed ruleFields, v tui.Values) (ruleFields, error) {
	f := seed
	if s, ok := v.Flags["pattern"]; ok {
		f.Pattern = s
	}
	if s, ok := v.Flags["name"]; ok {
		f.Name = s
	}
	if s, ok := v.Flags["strategy"]; ok {
		f.Strategy = s
	}
	for name, field := range map[string]*bool{
		"ignore-parent":    &f.Ignore,
		"current-dir-only": &f.CurrentDirOnly,
		"self-approve":     &f.SelfApprove,
	} {
		if _, ok := v.Flags[name]; ok {
			*field = v.Bool(name)
		}
	}
	if s, ok := v.Flags["approver"]; ok {
		pairs, err := splitList(s)
		if err != nil {
			return f, fmt.Errorf("--approver %q: %w", s, err)
		}
		if f.ApproverText, err = approverText(pairs); err != nil {
			return f, err
		}
	}
	return f, nil
}

// buildRuleFromFields converts a submitted rule-field set into the rules file
// rule it appends or replaces. Strategy name and approver text are both
// validated before the rule is returned, so a form submit with an unparseable
// approver line is an error rather than a rule silently losing its approvers.
func buildRuleFromFields(f ruleFields) (rulesfile.Rule, error) {
	var rule rulesfile.Rule
	strategy := f.Strategy
	if strategy == "" {
		strategy = "union"
	}
	var err error
	rule.CombineStrategy, err = rulesfile.ParseRuleCombineStrategy(strategy)
	if err != nil {
		return rule, err
	}
	rule.Approvers, err = rulesfile.ParseApprovers(f.ApproverText)
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
	rulesFileFlag(rulesAddCmd, "the rules file to add the rule to (default: chosen in the form, or the repository-root rules file)")
	rulesAddCmd.Flags().String("pattern", "", "rule pattern (regex); required unless the interactive form is used")
	rulesAddCmd.Flags().String("name", "", "rule name")
	rulesAddCmd.Flags().String("strategy", "union", "combine strategy: union, override, or intersect")
	rulesAddCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	rulesAddCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	rulesAddCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	rulesAddCmd.Flags().StringSlice("approver", nil, `approver pairs 'target required_count', repeatable`)
	tuiUnset(rulesAddCmd, "file", "the repository-root rules file")
	rulesCmd.AddCommand(rulesAddCmd)
}
