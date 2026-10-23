package cli

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesGetCmd resolves the review rule chain for the files the regex
// arguments select, matched against the working tree's files exactly as the
// match/coverage engine consumes them: the pure "which rules apply where"
// projection.
var rulesGetCmd = &cobra.Command{
	Use:   "get [pattern...]",
	Short: "Get review rules for a file/regex",
	Long: `The get subcommand shows the review rules that apply to each file the
regular expressions select (a file matching any of them), from the
repository-root rules file down.

This way, review requirements can be visualized locally without opening a
pull or merge request.`,
	Annotations: map[string]string{"arg": "regular expression of the files to check (default: every file)"},
}

// runRulesGet is rulesGetCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle. --json is one object: each selected file's rules,
// in chain order, each naming its rules file and position.
func runRulesGet(cmd *cobra.Command, args []string) error {
	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, rulesGetRoute(valuesFrom(cmd, args)))
	}
	engine, err := openEngine(cliLog())
	if err != nil {
		return err
	}
	paths, chain, err := ruleChainFor(cmd.Context(), engine, args)
	if err != nil {
		return err
	}
	if mode == modeJSON {
		byFile := chain.RulesByFile()
		out := make(map[string]rulesfile.Rules, len(paths))
		for _, p := range paths {
			out[p] = byFile[p]
		}
		return writeJSON(os.Stdout, out)
	}
	return printRuleChain(os.Stdout, chain)
}

// ruleChainFor is the files patterns select (every file when there are
// none) with their rule chain.
func ruleChainFor(ctx context.Context, engine *review.Engine, patterns []string) ([]string, approval.RuleChain, error) {
	paths, err := engine.Files(ctx, review.Options{Patterns: patterns})
	if err != nil {
		return nil, approval.RuleChain{}, fmt.Errorf("find files: %w", err)
	}
	chain, err := engine.RuleChain(ctx, paths)
	if err != nil {
		return nil, approval.RuleChain{}, fmt.Errorf("fetch rules: %w", err)
	}
	return paths, chain, nil
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

func printRule(b *strings.Builder, rule approval.ExecutableRule, indent string) {
	fmt.Fprintf(b, "%sName:                 %s\n", indent, rule.Name)
	fmt.Fprintf(b, "%sIgnoreParentDirs:     %t\n", indent, rule.IgnoreParentDirs)
	fmt.Fprintf(b, "%sCombineStrategy:      %s\n", indent, rule.CombineStrategy)
	fmt.Fprintf(b, "%sCurrentDirectoryOnly: %t\n", indent, rule.CurrentDirectoryOnly)
	fmt.Fprintf(b, "%sSelfApprove:          %t\n", indent, rule.SelfApprove)
	fmt.Fprintf(b, "%sApprovers:\n", indent)
	for _, approver := range rule.Approvers {
		fmt.Fprintf(b, "%s  %s (%d)\n", indent, approver.Target, approver.RequiredCount)
	}
	b.WriteString("\n")
}

// rulesGetRoute resolves the rule chain for the files matching the pattern
// arguments (every file when there are none) and shows it as rule → files.
// The CLI argument is optional, but opening the route without one asks for
// the regex first, as `rules files` does, rather than loading every file.
func rulesGetRoute(v tui.Values) tui.Route {
	r := commandRoute(rulesGetCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		engine, err := openEngine(quietLog)
		if err != nil {
			return nil, err
		}
		_, chain, err := ruleChainFor(ctx, engine, v.Args)
		if err != nil {
			return nil, err
		}
		return rulesGetPage(chain, ownership{state: engine.Requirements(ctx, chain)}), nil
	})
	return requirePattern(r)
}

// requirePattern makes r's optional pattern argument required in the TUI,
// so opening the route asks for the regex first instead of loading every
// file; the CLI argument stays optional.
func requirePattern(r tui.Route) tui.Route {
	return withParam(r, func(p *tui.Param) bool { return p.Arg }, func(p *tui.Param) { p.Required = true })
}

// rulesGetPage groups files under the rules that apply to them. A rule's
// detail is its definition; a file's detail is its provenance: every layer
// of its chain, from the nearest rules file up, and the rules each declares.
func rulesGetPage(chain approval.RuleChain, own ownership) tui.Page {
	byRule := chain.ByRule()
	groups := make([]tui.TreeGroup, 0, len(byRule))
	for _, rf := range byRule {
		items := make([]tui.Item, 0, len(rf.Files))
		for _, f := range rf.Files {
			it := tui.Item{Title: f, Detail: provenance(f, chain.FileChains[f])}
			own.annotate(&it, f)
			items = append(items, it)
		}
		groups = append(groups, tui.TreeGroup{
			Title:  rf.String(),
			Detail: tui.Detail{Fields: append(ruleFieldsDetail(rf.Rule), tui.Field{Label: "Files", Value: strconv.Itoa(len(rf.Files))})},
			Items:  items,
		})
	}
	return tui.NewTreePage("Rules", groups).
		WithSummary(countOf(len(groups), "rule") + " across " + countOf(len(chain.FileChains), "file"))
}

func provenance(file string, layers []approval.RuleLayer) tui.Detail {
	d := tui.Detail{Fields: []tui.Field{{Label: "File", Value: file}, {Label: "Layers", Value: strconv.Itoa(len(layers))}}}
	for _, layer := range layers {
		var lines []string
		for _, r := range layer.Rules {
			line := r.Pattern + "  " + r.CombineStrategy.String()
			if r.Name != "" {
				line = r.Name + ": " + line
			}
			if a := approversLabel(r.Approvers); a != "" {
				line += "  [" + a + "]"
			}
			lines = append(lines, line)
		}
		d.Sections = append(d.Sections, tui.Section{Title: layer.RulesFile(), Lines: lines})
	}
	return d
}

func init() {
	rulesGetCmd.RunE = runRulesGet
	rulesCmd.AddCommand(rulesGetCmd)
}
