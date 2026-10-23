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

// rulesEditCmd edits one rule, found by its pattern, replacing the rule's
// name, strategy, flags, and approver list; the pattern itself is kept.
// When several rules have the pattern, --file and --position choose one; a
// terminal gets a picker and a script must pass them (see ruleTarget).
var rulesEditCmd = &cobra.Command{
	Use:         "edit <pattern>",
	Short:       "Edit a rule in a rules file",
	Long:        "Edit an existing rule in a rules file, replacing its name, strategy, flags, and approver list.",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"arg": "the pattern of the rule to edit"},
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		mode, _ := resolveOutputMode(cmd)
		interactive := mode == modeTUI
		s, err := openWorkspace(cmd.Context(), workspaceLog(interactive))
		if err != nil {
			return err
		}
		if !interactive {
			if err := warnBroken(os.Stderr, s); err != nil {
				return err
			}
		}
		target, err := ruleTarget(s, valuesFrom(cmd, args), pattern, interactive)
		if errors.Is(err, tui.ErrAborted) {
			fmt.Fprintln(os.Stderr, "edit aborted")
			return nil
		}
		if err != nil {
			if interactive {
				// A skipped rules file may be why the rule was not found.
				if werr := warnBroken(os.Stderr, s); werr != nil {
					return werr
				}
			}
			return err
		}

		fields := editSeed(target)
		if interactive {
			fields, err = runRuleForm(fields, nil, brokenNote(s))
			if errors.Is(err, tui.ErrAborted) {
				fmt.Fprintln(os.Stderr, "edit aborted")
				return nil
			}
			if err != nil {
				return fmt.Errorf("run form: %w", err)
			}
		}
		// Flags given on the command line win over the form; the others
		// leave the rule's values standing.
		if fields, err = applyRuleFlags(fields, valuesFrom(cmd, args)); err != nil {
			return err
		}

		newRule, err := s.Edit(target, fields)
		if err != nil {
			return err
		}
		if mode == modeJSON {
			return writeJSON(cmd.OutOrStdout(), newRule)
		}
		fmt.Printf("edited rule %q at %s\n", pattern, target.Location())
		return nil
	},
}

// editRule replaces target with f, keeping target's pattern, as `rules
// edit` does.
func editRule(s *rulesedit.Workspace, target, f rulesfile.Rule) (string, error) {
	if _, err := s.Edit(target, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited rule %q at %s", target.Pattern, target.Location()), nil
}

// rulesEditRoute is `rules edit <pattern>`: flags left unset keep the
// rule's values. A pattern matching several rules opens a picker of them.
func rulesEditRoute(v tui.Values) tui.Route {
	return ruleRoute(rulesEditCmd, v, rulesEditRoute, func(ctx context.Context, v tui.Values) (string, error) {
		s, err := openWorkspace(ctx, quietLog)
		if err != nil {
			return "", err
		}
		return editFromValues(s, v)
	})
}

func editFromValues(s *rulesedit.Workspace, v tui.Values) (string, error) {
	pattern, err := patternArg(v)
	if err != nil {
		return "", err
	}
	target, err := ruleTarget(s, v, pattern, false)
	if err != nil {
		return "", err
	}
	fields, err := applyRuleFlags(editSeed(target), v)
	if err != nil {
		return "", err
	}
	return editRule(s, target, fields)
}

func init() {
	rulesFileFlag(rulesEditCmd, "the rules file holding the rule (default: the one rules file with that pattern)")
	positionFlagFor(rulesEditCmd)
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
