package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/canopy/internal/rulesedit"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
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
		mode, _ := resolveOutputMode(cmd)
		interactive := mode == modeTUI && !cmd.Flags().Changed("pattern")
		s, err := openWorkspace(cmd.Context(), workspaceLog(interactive))
		if err != nil {
			return err
		}
		if !interactive {
			if err := warnBroken(os.Stderr, s); err != nil {
				return err
			}
		}
		fileFlag, _ := cmd.Flags().GetString("file")
		target, err := resolveFile(s, fileFlag)
		if err != nil {
			return err
		}

		var fields rulesfile.Rule
		if interactive {
			seed := newRuleSeed()
			var choices []string
			if target == "" {
				if choices = s.Rules.Paths(); len(choices) == 0 {
					return rulesedit.ErrNoRulesFiles
				}
				seed.RulesFile = nearestRulesFile(s)
			}
			fields, err = runRuleForm(seed, choices, brokenNote(s))
			if errors.Is(err, tui.ErrAborted) {
				fmt.Fprintln(os.Stderr, "add aborted")
				return nil
			}
			if err != nil {
				return fmt.Errorf("run form: %w", err)
			}
			if target == "" {
				target = fields.RulesFile
			}
		} else {
			fields, err = applyRuleFlags(newRuleSeed(), valuesFrom(cmd, args))
			if err != nil {
				return err
			}
			if target == "" {
				if target, err = resolveFile(s, rulesfile.FileName); err != nil {
					return err
				}
			}
		}

		rules, err := s.Add(target, fields)
		if err != nil {
			return err
		}
		if mode == modeJSON {
			return writeJSON(os.Stdout, rules)
		}
		fmt.Printf("added rule %q to %s\n", fields.Pattern, target)
		return nil
	},
}

// applyRuleFlags applies the rule flags set in v over seed: a flag given on
// the command line (see valuesFrom) or in the TUI's options form always
// wins, while an unset one leaves seed's value standing, so a single-field
// rules edit does not zero out the other fields. init and add seed the
// flags' defaults. The strategy and approver pairs are parsed here, so a
// malformed value fails with a precise error before any file is written.
func applyRuleFlags(seed rulesfile.Rule, v tui.Values) (rulesfile.Rule, error) {
	f := seed
	if s, ok := v.Flags["pattern"]; ok {
		f.Pattern = s
	}
	if s, ok := v.Flags["name"]; ok {
		f.Name = s
	}
	if s, ok := v.Flags["strategy"]; ok {
		strategy, err := rulesfile.ParseRuleCombineStrategy(s)
		if err != nil {
			return f, fmt.Errorf("--strategy: %w", err)
		}
		f.CombineStrategy = strategy
	}
	for name, field := range map[string]*bool{
		"ignore-parent":    &f.IgnoreParentDirs,
		"current-dir-only": &f.CurrentDirectoryOnly,
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
		targets, err := rulesedit.ParseApproverPairs(pairs)
		if err != nil {
			return f, fmt.Errorf("--approver: %w", err)
		}
		f.Approvers = targets
	}
	return f, nil
}

func addRule(s *rulesedit.Workspace, rulesFile string, f rulesfile.Rule) (string, error) {
	if _, err := s.Add(rulesFile, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("added rule %q to %s", f.Pattern, rulesFile), nil
}

// rulesAddRoute is `rules add`. The options form is the interactive
// form, so --pattern is required in it; --file defaults to the
// repository-root rules file, as in a scripted run.
func rulesAddRoute(v tui.Values) tui.Route {
	r := runRoute(rulesAddCmd, v, func(ctx context.Context, v tui.Values) (string, error) {
		s, err := openWorkspace(ctx, quietLog)
		if err != nil {
			return "", err
		}
		return addFromValues(s, v)
	})
	return withParam(r, func(p *tui.Param) bool { return p.Name == "pattern" }, func(p *tui.Param) { p.Required = true })
}

func addFromValues(s *rulesedit.Workspace, v tui.Values) (string, error) {
	file := v.Flag("file")
	if file == "" {
		file = rulesfile.FileName
	}
	target, err := resolveFile(s, file)
	if err != nil {
		return "", err
	}
	fields, err := applyRuleFlags(newRuleSeed(), v)
	if err != nil {
		return "", err
	}
	return addRule(s, target, fields)
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
