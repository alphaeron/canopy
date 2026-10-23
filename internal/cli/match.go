package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
)

// matchCmd runs the match engine's match phase over the selected file
// universe and renders per-file scopes, per-scope groups, or the diff
// content. The engine runs once per invocation. --scope narrows the result
// after matching (review.Result.FilterScope): matching always uses every
// rule, so it changes what is reported, never what matches. --by-scope
// regroups that one result per scope.
var matchCmd = &cobra.Command{
	Use:   "match",
	Short: "Match files against rules file rules",
}

// runMatchCmd is matchCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runMatchCmd(cmd *cobra.Command, args []string) error {
	changed, _ := cmd.Flags().GetBool("changed")
	scopeName, _ := cmd.Flags().GetString("scope")
	includeDiff, _ := cmd.Flags().GetBool("diff")
	byScope, _ := cmd.Flags().GetBool("by-scope")
	opts := review.Options{
		ChangedOnly: changed,
		ScopeName:   scopeName,
		IncludeDiff: includeDiff,
	}

	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, matchRoute(valuesFrom(cmd, args)))
	}
	result, err := runMatch(cmd.Context(), opts, cliLog())
	if err != nil {
		return err
	}
	if byScope {
		return renderMatchByScope(mode, result.GroupByRule())
	}
	return renderMatchByFile(mode, result, includeDiff)
}

// runMatch runs the engine's match phase against the local repository. log
// is as for openEngine.
func runMatch(ctx context.Context, opts review.Options, log *slog.Logger) (*review.Result, error) {
	engine, err := openEngine(log)
	if err != nil {
		return nil, err
	}
	return matchWith(ctx, engine, opts)
}

// matchWith is runMatch on an open engine.
func matchWith(ctx context.Context, engine *review.Engine, opts review.Options) (*review.Result, error) {
	result, err := engine.Match(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("run match: %w", err)
	}
	return result, nil
}

// renderMatchByFile renders one row per file, optionally followed by each
// file's matched rules and the matched diff. The TUI form is matchPage.
func renderMatchByFile(mode outputMode, result *review.Result, includeDiff bool) error {
	switch mode {
	case modeJSON:
		return writeJSON(os.Stdout, result)
	default:
		var header = []string{"File", "Scope"}
		rows := make([][]string, 0, len(result.Files))
		for _, fm := range result.Files {
			rows = append(rows, []string{fm.Path, ruleTitles(fm.Rules)})
		}
		if err := writeTable(os.Stdout, header, rows); err != nil {
			return err
		}
		if includeDiff {
			for _, fm := range result.Files {
				if fm.Diff == "" {
					continue
				}
				fmt.Printf("\n=== diff: %s ===\n%s\n", fm.Path, fm.Diff)
			}
		}
	}
	return nil
}

// renderMatchByScope renders one row per scope, then the files it matched.
// The TUI form is matchByScopePage.
func renderMatchByScope(mode outputMode, groups []approval.RuleFiles) error {
	switch mode {
	case modeJSON:
		return writeJSON(os.Stdout, groups)
	default:
		var header = []string{"Scope", "Pattern", "Rules File", "Position", "Files"}
		rows := make([][]string, 0, len(groups))
		for _, g := range groups {
			files := strings.Join(g.Files, "\n")
			rows = append(rows, []string{g.Name, g.Pattern, g.RulesFile, strconv.Itoa(g.Position), files})
		}
		if err := writeTable(os.Stdout, header, rows); err != nil {
			return err
		}
	}
	return nil
}

// ruleTitles names the rules matching a file (see ruleTitle), or "none" if
// no rule matched.
func ruleTitles(rules rulesfile.Rules) string {
	if len(rules) == 0 {
		return "none"
	}
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = ruleTitle(r)
	}
	return strings.Join(out, ", ")
}

// matchRoute shows the match result per file, or per scope with --by-scope.
// Each file is also evaluated for who must approve it.
func matchRoute(v tui.Values) tui.Route {
	return commandRoute(matchCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		engine, err := openEngine(quietLog)
		if err != nil {
			return nil, err
		}
		result, err := matchWith(ctx, engine, review.Options{
			ChangedOnly: v.Bool("changed"),
			IncludeDiff: v.Bool("diff"),
			ScopeName:   v.Flag("scope"),
		})
		if err != nil {
			return nil, err
		}
		own := ownershipOf(ctx, engine, result.Paths())
		if v.Bool("by-scope") {
			return matchByScopePage(result.GroupByRule(), own), nil
		}
		return matchPage(result, own), nil
	})
}

// matchPage lists files; the drill-down shows which scopes own the file
// (name, pattern, and the rules file that declared it), who must approve it
// and how its chain produced that, and, when the match ran with --diff, the
// file's diff. Enter opens the file's full rule chain.
func matchPage(result *review.Result, own ownership) tui.Page {
	items := make([]tui.Item, 0, len(result.Files))
	for _, fm := range result.Files {
		var d tui.Detail
		d.Fields = []tui.Field{{Label: "File", Value: fm.Path}, {Label: "Scopes", Value: strconv.Itoa(len(fm.Rules))}}
		for _, r := range fm.Rules {
			d.Sections = append(d.Sections, tui.Section{Title: "Scope " + ruleTitle(r), Lines: ruleLines(r)})
		}
		if fm.Diff != "" {
			d.Tabs = append(d.Tabs, tui.Tab{Title: "Diff", Sections: []tui.Section{{Title: "Diff", Lines: strings.Split(strings.TrimRight(fm.Diff, "\n"), "\n"), Diff: true}}})
		}
		r := fileRulesRoute(fm.Path)
		it := tui.Item{Title: fm.Path, Description: ruleTitles(fm.Rules), Detail: d, Route: &r}
		own.annotate(&it, fm.Path)
		items = append(items, it)
	}
	owned := result.Covered()
	return tui.NewListPage("Files", items).
		WithSummary(fmt.Sprintf("%d files · %d owned · %d unowned · %s", len(items), owned, len(items)-owned, own.summary()))
}

func matchByScopePage(groups []approval.RuleFiles, own ownership) tui.Page {
	tg := make([]tui.TreeGroup, 0, len(groups))
	for _, g := range groups {
		files := slices.Sorted(slices.Values(g.Files))
		items := make([]tui.Item, 0, len(files))
		for _, f := range files {
			r := fileRulesRoute(f)
			it := tui.Item{
				Title:  f,
				Detail: tui.Detail{Fields: []tui.Field{{Label: "File", Value: f}, {Label: "Scope", Value: ruleTitle(g.Rule)}}},
				Route:  &r,
			}
			own.annotate(&it, f)
			items = append(items, it)
		}
		d := tui.Detail{Fields: append(ruleFieldsDetail(g.Rule), tui.Field{Label: "Files", Value: strconv.Itoa(len(files))})}
		tg = append(tg, tui.TreeGroup{Title: ruleTitle(g.Rule), Detail: d, Items: items})
	}
	return tui.NewTreePage("Scopes", tg).WithSummary(countOf(len(groups), "scope") + " · " + own.summary())
}

func init() {
	matchCmd.RunE = runMatchCmd
	matchCmd.Flags().Bool("changed", false, "match only files changed in the working tree")
	matchCmd.Flags().Bool("diff", false, "attach the diff content of each file to its match row")
	matchCmd.Flags().Bool("by-scope", false, "render the result as a scope-keyed group tree")
	matchCmd.Flags().String("scope", "", "filter the match to this scope name (a rule name)")
	rootCmd.AddCommand(matchCmd)
}
