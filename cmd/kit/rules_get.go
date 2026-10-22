package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/shonin"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesGetCmd resolves the review rule chain for every file/regex argument.
// Each arg is the regular expression of the files to analyze, matched
// against the repository at HEAD exactly as the match/coverage engine
// consumes it — this is the pure "which rules apply where" projection.
var rulesGetCmd = &cobra.Command{
	Use:   "get [pattern...]",
	Short: "Get review rules for a file/regex",
	Long: `The get subcommand allows for checking applicable review rules for
a file/pattern matching a set of files.

This way, review requirements can be visualized locally without opening a
pull request.`,
	Annotations: map[string]string{"arg": "regular expression of the files to check (default: every file)"},
}

// runRulesGet is rulesGetCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runRulesGet(cmd *cobra.Command, args []string) error {
	if mode, _ := resolveOutputMode(cmd); mode == modeTUI {
		return openTUI(cmd, rulesGetRoute(valuesFrom(cmd, args)))
	}
	if len(args) == 0 {
		args = []string{`.*`}
	}
	repo, err := getRepo()
	if err != nil {
		return err
	}
	ref, err := repo.Head()
	if err != nil {
		if repo.HasCommits() {
			return fmt.Errorf("read HEAD: %w", err)
		}
		// No commit yet: no tracked files exist, so the rule chain over
		// any file set is simply empty.
		return renderEmptyMatch(cmd)
	}
	fetch := shonin.LocalFetcher{Repo: repo}
	s := shonin.Shonin{
		Fetch:    &fetch,
		Resolver: &shonin.DefaultApproverResolver{},
	}

	mode, _ := resolveOutputMode(cmd)
	for _, arg := range args {
		paths, err := repo.FindFiles(arg, ref, false)
		if err != nil {
			return fmt.Errorf("find files for %q: %w", arg, err)
		}
		ruleChain, err := s.GetRuleChainForFiles(paths)
		if err != nil {
			return fmt.Errorf("fetch rules for %q: %w", arg, err)
		}
		switch mode {
		case modeJSON:
			m := map[string][]string{}
			for _, file := range paths {
				for _, layer := range ruleChain.FileChains[file] {
					for _, rule := range layer.Rules {
						m[file] = append(m[file], rule.Pattern)
					}
				}
			}
			if err := tui.JSONEncoder(os.Stdout, m); err != nil {
				return err
			}
		default:
			shonin.PrintRuleChain(ruleChain)
		}
	}
	return nil
}

func init() {
	rulesGetCmd.RunE = runRulesGet
	rulesCmd.AddCommand(rulesGetCmd)
}

// renderEmptyMatch outputs the current command's canonical "nothing matched"
// payload for a repository that has no commit yet, where no file exists to
// carry a rule. The JSON form is an empty mapping and the table form is no
// rows, matching what shonin produces for a rule-less tree.
func renderEmptyMatch(cmd *cobra.Command) error {
	if m, _ := resolveOutputMode(cmd); m == modeJSON {
		return tui.JSONEncoder(os.Stdout, map[string][]string{})
	}
	return nil
}
