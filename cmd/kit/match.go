package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kenin"
	"github.com/alphaeron/kit/internal/tui"
)

// matchCmd runs the kenin engine's match phase over the selected file
// universe and renders per-file scopes, per-scope groups, or the diff
// content. The engine runs once per invocation. --scope narrows the result
// after matching (kenin.MatchResult.FilterScope): matching always uses every
// rule, so it changes what is reported, never what matches. --by-scope
// regroups that one result per scope.
var matchCmd = &cobra.Command{
	Use:   "match",
	Short: "Match files against KIT file rules",
}

// runMatchCmd is matchCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runMatchCmd(cmd *cobra.Command, args []string) error {
	changed, _ := cmd.Flags().GetBool("changed")
	scopeName, _ := cmd.Flags().GetString("scope")
	includeDiff, _ := cmd.Flags().GetBool("diff")
	byScope, _ := cmd.Flags().GetBool("by-scope")
	opts := kenin.Options{
		ChangedOnly: changed,
		ScopeName:   scopeName,
		IncludeDiff: includeDiff,
	}

	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, matchRoute(valuesFrom(cmd, args)))
	}
	result, err := runMatch(opts)
	if err != nil {
		return err
	}
	if byScope {
		return renderMatchByScope(mode, result.GroupByScope())
	}
	return renderMatchByFile(mode, result, includeDiff)
}

// runMatch runs the engine's match phase against the local repository.
func runMatch(opts kenin.Options) (*kenin.MatchResult, error) {
	repo, err := getRepo()
	if err != nil {
		return nil, err
	}
	engine, err := newLocalEngine(repo)
	if err != nil {
		return nil, err
	}
	result, err := engine.Match(opts)
	if err != nil {
		return nil, fmt.Errorf("run match: %w", err)
	}
	return result, nil
}

// renderMatchByFile renders one row per file, optionally followed by each
// file's matched rules and the matched diff. The TUI form is matchPage.
func renderMatchByFile(mode outputMode, result *kenin.MatchResult, includeDiff bool) error {
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, result)
	default:
		var header = []string{"File", "Scope"}
		rows := make([][]string, 0, len(result.Files))
		for _, fm := range result.Files {
			rows = append(rows, []string{fm.Path, scopeTitles(fm.Scopes)})
		}
		if err := tui.TablePrinter(os.Stdout, header, rows); err != nil {
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
func renderMatchByScope(mode outputMode, groups []kenin.ScopeGroup) error {
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, groups)
	default:
		var header = []string{"Scope", "Pattern", "KIT File", "Files"}
		rows := make([][]string, 0, len(groups))
		for _, g := range groups {
			files := strings.Join(g.Files, "\n")
			rows = append(rows, []string{g.Scope.Name, g.Scope.Pattern, g.Scope.KITFile, files})
		}
		if err := tui.TablePrinter(os.Stdout, header, rows); err != nil {
			return err
		}
	}
	return nil
}

// scopeTitles returns the scope names for a file's scope set, or "none" if
// no scope matched.
func scopeTitles(scopes []kenin.Scope) string {
	if len(scopes) == 0 {
		return "none"
	}
	var out []string
	for _, s := range scopes {
		if s.Name != "" {
			out = append(out, s.Name)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, " ")
}

func init() {
	matchCmd.RunE = runMatchCmd
	matchCmd.Flags().Bool("changed", false, "match only files changed in the working tree")
	matchCmd.Flags().Bool("diff", false, "attach the diff content of each file to its match row")
	matchCmd.Flags().Bool("by-scope", false, "render the result as a scope-keyed group tree")
	matchCmd.Flags().String("scope", "", "filter the match to this scope name (a rule name)")
	rootCmd.AddCommand(matchCmd)
}
