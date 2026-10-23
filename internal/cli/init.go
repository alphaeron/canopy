package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/canopy/internal/rulesedit"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
)

// initCmd scaffolds a new rules file. It accepts an optional [path], where an
// empty or missing argument writes the repository-root rules file and a
// directory argument writes the rules file inside that directory. Without
// --pattern it runs the interactive rule form; with --pattern it scaffolds the
// file and the rule together.
var initCmd = &cobra.Command{
	Use:         "init [path]",
	Short:       "Initialize a rules file in the current repository",
	Long:        "Initialize a rules file in the current repository, with an optional rule pre-filled by the form or by flags.",
	Annotations: map[string]string{"arg": "the rules file to write, or a directory to write it in (default: " + rulesfile.FileName + " in the current directory)"},
	RunE: func(cmd *cobra.Command, args []string) error {
		var arg string
		if len(args) > 0 {
			arg = args[0]
		}
		path := rulesedit.PathFor(arg)
		force, _ := cmd.Flags().GetBool("force")
		if err := rulesedit.CheckCreate(path, force); err != nil {
			return forceHint(err)
		}
		pattern, _ := cmd.Flags().GetString("pattern")
		mode, _ := resolveOutputMode(cmd)

		var fields rulesfile.Rule
		var err error
		if pattern == "" && mode == modeTUI {
			fields, err = runRuleForm(newRuleSeed(), nil, "")
			if errors.Is(err, tui.ErrAborted) {
				fmt.Fprintln(os.Stderr, "init aborted")
				return nil
			}
			if err != nil {
				return fmt.Errorf("run form: %w", err)
			}
		} else if fields, err = applyRuleFlags(newRuleSeed(), valuesFrom(cmd, args)); err != nil {
			return err
		}

		rules, err := createRulesFile(path, force, fields)
		if err != nil {
			return err
		}
		if mode == modeJSON {
			return writeJSON(os.Stdout, rules)
		}
		fmt.Println(initMessage(path, rules))
		return nil
	},
}

// createRulesFile writes the rules file `init` creates (see
// rulesedit.Create): with the rule fields describe, or empty when they have
// no pattern. The CLI and the TUI both create rules files through it.
func createRulesFile(path string, force bool, fields rulesfile.Rule) (rulesfile.Rules, error) {
	var rules []rulesfile.Rule
	if fields.Pattern != "" {
		rules = append(rules, fields)
	}
	written, err := rulesedit.Create(path, force, rules...)
	return written, forceHint(err)
}

// forceHint tells the user how to overwrite an existing rules file.
func forceHint(err error) error {
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w; pass --force to overwrite", err)
	}
	return err
}

// initMessage confirms what createRulesFile wrote.
func initMessage(path string, rules rulesfile.Rules) string {
	if len(rules) > 0 {
		return fmt.Sprintf("initialized %s with rule %q", path, rules[0].Pattern)
	}
	return "initialized empty rules file at " + path
}

// initRoute is `init [path]`.
func initRoute(v tui.Values) tui.Route {
	return runRoute(initCmd, v, func(_ context.Context, v tui.Values) (string, error) { return initFromValues(v) })
}

func initFromValues(v tui.Values) (string, error) {
	var arg string
	if len(v.Args) > 0 {
		arg = v.Args[0]
	}
	fields, err := applyRuleFlags(newRuleSeed(), v)
	if err != nil {
		return "", err
	}
	path := rulesedit.PathFor(arg)
	rules, err := createRulesFile(path, v.Bool("force"), fields)
	if err != nil {
		return "", err
	}
	return initMessage(path, rules), nil
}

func init() {
	initCmd.Flags().Bool("force", false, "overwrite an existing rules file")
	initCmd.Flags().String("pattern", "", "optional rule pattern (regex) to register at init time")
	initCmd.Flags().String("name", "", "optional rule name")
	initCmd.Flags().String("strategy", "union", "combine strategy: union, override, or intersect")
	initCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	initCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	initCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	initCmd.Flags().StringSlice("approver", nil, `optional rule approver pairs 'target required_count', repeatable`)
	rootCmd.AddCommand(initCmd)
}
