package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/match"
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
	engine := newLocalEngine(repo, nil)

	mode, _ := resolveOutputMode(cmd)
	for _, arg := range args {
		paths, err := engine.Files(cmd.Context(), match.Options{Patterns: []string{arg}})
		if err != nil {
			return fmt.Errorf("find files for %q: %w", arg, err)
		}
		ruleChain, err := engine.RuleChain(cmd.Context(), paths)
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
			if err := writeJSON(os.Stdout, m); err != nil {
				return err
			}
		default:
			if err := printRuleChain(os.Stdout, ruleChain); err != nil {
				return err
			}
		}
	}
	return nil
}

// printRuleChain writes each file's chain as an indented tree: the file,
// each layer's directory, and every rule in it with its settings. Files are
// in path order.
func printRuleChain(w io.Writer, chain approval.RuleChain) error {
	var b strings.Builder
	for _, file := range slices.Sorted(maps.Keys(chain.FileChains)) {
		fmt.Fprintf(&b, "%s\n", file)
		for _, layer := range chain.FileChains[file] {
			indentCount := strings.Count(layer.DirectoryPath, "/")
			fmt.Fprintf(&b, "%s%s/\n", strings.Repeat("  ", indentCount), layer.DirectoryPath)
			patternIndent := strings.Repeat("  ", indentCount+1)
			ruleIndent := strings.Repeat("  ", indentCount+2)
			for _, rule := range layer.Rules {
				fmt.Fprintf(&b, "%s%s:\n", patternIndent, rule.Pattern)
				printRule(&b, rule, ruleIndent)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func printRule(b *strings.Builder, rule approval.Rule, indent string) {
	fmt.Fprintf(b, "%sName:                 %s\n", indent, rule.Name)
	fmt.Fprintf(b, "%sIgnoreParentDirs:     %t\n", indent, rule.IgnoreParentDirs)
	fmt.Fprintf(b, "%sCombineStrategy:      %s\n", indent, rule.CombineStrategy)
	fmt.Fprintf(b, "%sCurrentDirectoryOnly: %t\n", indent, rule.CurrentDirectoryOnly)
	fmt.Fprintf(b, "%sSelfApprove:          %t\n", indent, rule.SelfApprove)
	fmt.Fprintf(b, "%sApprovers:\n", indent)
	for _, approver := range rule.Approvers {
		fmt.Fprintf(b, "%s  %s\n", indent, approver)
	}
	b.WriteString("\n")
}

func init() {
	rulesGetCmd.RunE = runRulesGet
	rulesCmd.AddCommand(rulesGetCmd)
}
