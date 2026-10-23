package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesedit"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesListCmd lists the rules of every rules file in the repository, or of
// one rules file with --file. Narrowing never changes the output's shape: the
// JSON is always one flat list of rules, each naming its rules file.
var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the rules in the repository's rules files",
	Long: `List the rules of every rules file in the repository's working tree, the
repository-root rules file first. --file narrows the list to one rules file.`,
}

// runRulesList is rulesListCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runRulesList(cmd *cobra.Command, args []string) error {
	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, rulesListRoute(valuesFrom(cmd, args)))
	}
	filePath, _ := cmd.Flags().GetString("file")
	s, err := openWorkspace(cmd.Context(), cliLog())
	if err != nil {
		return err
	}
	if err := warnBroken(os.Stderr, s); err != nil {
		return err
	}
	only, err := resolveFile(s, filePath)
	if err != nil {
		return err
	}
	entries := s.Entries(only)
	if mode == modeJSON {
		return writeJSON(os.Stdout, entries)
	}
	if err := writeTable(os.Stdout, []string{"Rules File", "Position", "Pattern", "Name", "Strategy", "Approvers"}, ruleRows(entries)); err != nil {
		return err
	}
	return narrowedNotice(os.Stderr, only, len(s.Rules))
}

// narrowedNotice tells the user, on stderr so stdout stays parseable, that
// --file hid the other rules files.
func narrowedNotice(w io.Writer, only string, total int) error {
	if only == "" || total <= 1 {
		return nil
	}
	_, err := fmt.Fprintf(w, "showing %s, 1 of %d rules files; omit --file to list all\n", only, total)
	return err
}

// ruleRows are the table rows for entries.
func ruleRows(rules rulesfile.Rules) [][]string {
	rows := make([][]string, 0, len(rules))
	for _, r := range rules {
		strategy := strategyName(r)
		if strategy == "" {
			strategy = "(default)"
		}
		rows = append(rows, []string{r.RulesFile, strconv.Itoa(r.Position), r.Pattern, r.Name, strategy, approversLabel(r.Approvers)})
	}
	return rows
}

// strategyName is the rule's combine_strategy as written in the rules file,
// or "" when it is omitted (the approval package then applies its default).
func strategyName(r rulesfile.Rule) string {
	if !r.CombineStrategy.IsValid() {
		return ""
	}
	return r.CombineStrategy.String()
}

// rulesListRoute lists the rules of every rules file, or of the one --file
// names, with add, edit, and remove.
func rulesListRoute(v tui.Values) tui.Route {
	return commandRoute(rulesListCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		// The rules and the match share one read of the repository.
		repo, engine, err := openLocal(quietLog)
		if err != nil {
			return nil, err
		}
		s, err := loadWorkspace(ctx, repo, engine)
		if err != nil {
			return nil, err
		}
		only, err := resolveFile(s, v.Flag("file"))
		if err != nil {
			return nil, err
		}
		match, err := matchWith(ctx, engine, review.Options{})
		return rulesPage(s, only, ruleMatches{match: match, err: err}), nil
	})
}

// ruleMatches is one match over the repository, from which the rules page
// shows each rule's reach and its overlaps; err is why it is unavailable.
// The zero value has no files.
type ruleMatches struct {
	match *review.Result
	err   error
}

// files are the files the rule with key matched.
func (m ruleMatches) files(key rulesfile.RuleKey) []string {
	if m.match == nil {
		return nil
	}
	return m.match.FilesOf(key)
}

// overlaps are the other rules matching any of the rule's files, with how
// many files each shares, ordered by that count.
func (m ruleMatches) overlaps(key rulesfile.RuleKey) []tui.Row {
	if m.match == nil {
		return nil
	}
	overlaps := m.match.Overlaps(key)
	rows := make([]tui.Row, 0, len(overlaps))
	for _, o := range overlaps {
		rows = append(rows, tui.Row{Text: ruleLabel(o.Name, o.Pattern) + " in " + o.Location(), Note: countOf(o.Shared, "shared file"), Route: ruleFilesRoute(o.Rule, m.files(o.Key()))})
	}
	return rows
}

// rulesPage lists the rules of every rules file, or only only's. The status
// line says when other rules files are hidden and how to see them.
func rulesPage(s *rulesedit.Workspace, only string, matches ruleMatches) tui.Page {
	entries := s.Entries(only)
	items := make([]tui.Item, len(entries))
	for i, e := range entries {
		items[i] = ruleItem(e, matches)
	}
	title, summary := "Rules", countOf(len(entries), "rule")+" in "+countOf(len(s.Rules), "rules file")
	if only != "" {
		title += " · " + only
		summary = fmt.Sprintf("%s · showing %s, 1 of %s · o to change", countOf(len(entries), "rule"), only, countOf(len(s.Rules), "rules file"))
	}
	summary = joinNotes(summary, brokenNote(s))
	if matches.err != nil {
		summary += " · file matches unavailable: " + matches.err.Error()
	}
	return tui.NewListPage(title, items).WithSummary(summary).WithActions(ruleActions(s)...)
}

// ruleItem is a rule row: the detail shows the full rule, what its
// strategy does, how many files it matches, and the other rules matching
// the same files, with the files themselves in a tab. Enter drills into
// the rule chains of the files this rule applies to (see ruleFilesRoute).
func ruleItem(rule rulesfile.Rule, matches ruleMatches) tui.Item {
	fields := append(ruleFieldsDetail(rule),
		tui.Field{Label: "Effect", Value: approval.CombineStrategyEffect(rule.CombineStrategy, false)},
	)
	desc := orNone(rule.Name) + " · " + rule.Location()
	d := tui.Detail{Fields: fields}
	it := tui.Item{Title: rule.Pattern, Value: rule}
	if matches.err == nil {
		files := matches.files(rule.Key())
		it.Route = ruleFilesRoute(rule, files)
		d.Fields = append(d.Fields, tui.Field{Label: "Matches", Value: countOf(len(files), "file")})
		d.Sections = []tui.Section{{Title: "Overlaps · rules matching the same files", Rows: matches.overlaps(rule.Key())}}
		d.Tabs = []tui.Tab{{Title: "Files", Sections: []tui.Section{fileLinks("Files", files)}}}
		desc += " · " + countOf(len(files), "file")
	}
	it.Description, it.Detail = desc, d
	return it
}

// ruleFilesRoute is `rules get` for exactly the files rule applies to, so
// opening a rule shows its own files' chains, not those of every file its
// pattern matches (a pattern alone names neither where the rule applies nor
// which of the rules sharing it this is). nil when the rule applies to no
// file.
func ruleFilesRoute(rule rulesfile.Rule, files []string) *tui.Route {
	if len(files) == 0 {
		return nil
	}
	r := rulesGetRoute(tui.Values{Args: []string{review.ExactPattern(files...)}})
	r.Title = "files of " + rule.String()
	return &r
}

// ruleFieldsDetail is r's definition as detail fields, with the rules file
// declaring it.
func ruleFieldsDetail(r rulesfile.Rule) []tui.Field {
	strategy := strategyName(r)
	if strategy == "" {
		strategy = "(default)"
	}
	return []tui.Field{
		{Label: "Pattern", Value: r.Pattern},
		{Label: "Name", Value: r.Name},
		{Label: "rules file", Value: r.RulesFile},
		{Label: "Position", Value: strconv.Itoa(r.Position)},
		{Label: "Strategy", Value: strategy},
		{Label: "Ignore parent", Value: strconv.FormatBool(r.IgnoreParentDirs)},
		{Label: "Current dir only", Value: strconv.FormatBool(r.CurrentDirectoryOnly)},
		{Label: "Self approve", Value: strconv.FormatBool(r.SelfApprove)},
		{Label: "Approvers", Value: approversLabel(r.Approvers)},
	}
}

// ruleActions are the TUI forms of rules add, edit, and remove. Each row
// knows its rules file, so edit and remove are never ambiguous; add offers
// every rules file, preselecting the hovered row's, as `rules add` does
// on a terminal.
func ruleActions(s *rulesedit.Workspace) []tui.Action {
	return []tui.Action{
		{
			Key: "a", Help: "add rule",
			Run: func(ctx context.Context, it tui.Item) (tui.Outcome, error) {
				choices := s.Rules.Paths()
				if len(choices) == 0 {
					return tui.Outcome{}, rulesedit.ErrNoRulesFiles
				}
				seed := newRuleSeed()
				seed.RulesFile = nearestRulesFile(s)
				if e, err := ruleOf(it); err == nil {
					seed.RulesFile = e.RulesFile
				}
				page := newRuleFormPage("add rule", seed, choices, func(ctx context.Context, f rulesfile.Rule) (string, error) {
					return addRule(s, f.RulesFile, f)
				})
				return tui.Outcome{Page: page}, nil
			},
		},
		{
			Key: "e", Help: "edit rule", RequiresItem: true,
			Run: func(ctx context.Context, it tui.Item) (tui.Outcome, error) {
				e, err := ruleOf(it)
				if err != nil {
					return tui.Outcome{}, err
				}
				page := newRuleFormPage("edit rule", editSeed(e), nil, func(ctx context.Context, f rulesfile.Rule) (string, error) {
					return editRule(s, e, f)
				})
				return tui.Outcome{Page: page}, nil
			},
		},
		{
			Key: "x", Help: "remove rule", RequiresItem: true,
			Confirm: func(it tui.Item) string {
				if e, err := ruleOf(it); err == nil {
					return fmt.Sprintf("Remove rule %q at %s?", e.Pattern, e.Location())
				}
				return fmt.Sprintf("Remove rule %q?", it.Title)
			},
			Run: func(ctx context.Context, it tui.Item) (tui.Outcome, error) {
				e, err := ruleOf(it)
				if err != nil {
					return tui.Outcome{}, err
				}
				info, err := removeRule(s, e)
				return tui.Outcome{Info: info, Reload: true}, err
			},
		},
	}
}

// ruleOf recovers the rule a rules-page row was built from (see ruleItem).
func ruleOf(it tui.Item) (rulesfile.Rule, error) {
	r, ok := it.Value.(rulesfile.Rule)
	if !ok {
		return rulesfile.Rule{}, fmt.Errorf("row %q does not hold a rule", it.Title)
	}
	return r, nil
}

func init() {
	rulesListCmd.RunE = runRulesList
	rulesListCmd.Flags().String("file", "", "list only this rules file (path from the repository root, or its directory)")
	rulesCmd.AddCommand(rulesListCmd)
}
