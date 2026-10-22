package cli

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/match"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// The TUI mirrors the command tree: every command `--help` lists, except
// the CLI-only shell completion, is a route (see routeFor) whose options form
// (o) offers exactly the command's arguments and flags, derived from its cobra
// definition. The home page and
// command palette list each command once, with default values; running a
// read-only command on a terminal opens the TUI on its route with the given
// values. A command that changes something (init, rules add/edit/remove) is
// a Run route: its options form is the command line, and submitting it runs
// the command. Both paths go through the same functions, so the CLI and the
// TUI take the same inputs and have the same effects. The root's persistent
// flags are the session's, not a command's: --config and --verbose apply to
// the whole TUI, and --json is the alternative to it.

// appRoutes is the home page and command palette: one route per command, in
// `--help` order.
func appRoutes() []tui.Route {
	// Execute adds the built-in help command when it runs; adding it here
	// too (it is idempotent) lists it regardless.
	rootCmd.InitDefaultHelpCmd()
	var routes []tui.Route
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range visibleCommands(c) {
			if r, ok := routeFor(sub, tui.Values{}); ok {
				routes = append(routes, r)
			}
			walk(sub)
		}
	}
	walk(rootCmd)
	return routes
}

// openTUI deep-links into the TUI at r. The page is loaded first so a load
// failure (not a repository, unknown scope, unreadable rules file) is an
// ordinary CLI error, exactly as when the command renders a table.
func openTUI(cmd *cobra.Command, r tui.Route) error {
	ctx := cmd.Context()
	page, err := r.Load(ctx, r.Values)
	if err != nil {
		return err
	}
	return tui.Run(ctx, tui.Config{Routes: appRoutes(), Start: &r, StartPage: page, Overview: repoOverview})
}

// valuesFrom is a CLI invocation as route values: its arguments and every
// command flag the user set. A repeatable flag's values are joined as one
// comma-separated value, which is how pflag reads it back.
func valuesFrom(cmd *cobra.Command, args []string) tui.Values {
	v := tui.Values{Args: slices.Clone(args), Flags: map[string]string{}}
	cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Changed {
			return
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			v.Flags[f.Name] = joinList(sv.GetSlice())
		} else {
			v.Flags[f.Name] = f.Value.String()
		}
	})
	return v
}

// commandRoute builds the route for cmd. Its parameters come from cmd's own
// definition (paramsFor), so the options form can never drift from the CLI.
func commandRoute(cmd *cobra.Command, v tui.Values, load func(context.Context, tui.Values) (tui.Page, error)) tui.Route {
	return tui.Route{
		Command:     strings.TrimPrefix(cmd.CommandPath(), rootCmd.Name()+" "),
		Description: cmd.Short,
		Long:        cmd.Long,
		Params:      paramsFor(cmd),
		Values:      v,
		Load:        load,
	}
}

// choiceFlags are the flags whose values the options form offers as a list
// instead of free text.
var choiceFlags = map[string]func(context.Context) ([]string, error){
	"strategy": staticChoices("union", "override", "intersect"),
	"file": func(ctx context.Context) ([]string, error) {
		s, err := openRulesSet(ctx)
		if err != nil {
			return nil, err
		}
		return s.rules.Paths(), nil
	},
	"scope": func(ctx context.Context) ([]string, error) {
		s, err := openRulesSet(ctx)
		if err != nil {
			return nil, err
		}
		return s.rules.Names(), nil
	},
}

func staticChoices(choices ...string) func(context.Context) ([]string, error) {
	return func(context.Context) ([]string, error) { return choices, nil }
}

// unsetAnnotation is the flag annotation tuiUnset sets.
const unsetAnnotation = "tui_unset"

// tuiUnset labels what leaving flag unset means in the TUI's options form,
// where the default "all" (or the flag's default value) would mislead, e.g.
// "unchanged" for rules edit. A labeled boolean flag is offered as true,
// false, or unset, since its absence differs from false; a labeled text
// flag as unset or set, since its absence differs from empty.
func tuiUnset(cmd *cobra.Command, flag, label string) {
	cobra.CheckErr(cmd.Flags().SetAnnotation(flag, unsetAnnotation, []string{label}))
}

// paramsFor lists cmd's positional argument (from its Use line, with its
// help from the "arg" annotation) and its visible flags, as options-form
// parameters. The root's persistent flags are the session's (see the top of
// this file), so only cmd's own flags are listed.
func paramsFor(cmd *cobra.Command) []tui.Param {
	var ps []tui.Param
	if arg, ok := argParam(cmd.Use); ok {
		arg.Help = cmd.Annotations["arg"]
		ps = append(ps, arg)
	}
	cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
		// --help, which cobra adds to a command it executes, is `help`.
		if f.Hidden || f.Deprecated != "" || f.Annotations[cobra.FlagSetByCobraAnnotation] != nil {
			return
		}
		p := tui.Param{Name: f.Name, Help: f.Usage}
		if unset := f.Annotations[unsetAnnotation]; len(unset) > 0 {
			p.Unset = unset[0]
		} else if f.DefValue != "" && choiceFlags[f.Name] != nil {
			p.Unset = f.DefValue + " (default)"
		}
		_, slice := f.Value.(pflag.SliceValue)
		switch {
		case f.Value.Type() == "bool" && p.Unset != "":
			p.Kind, p.Choices = tui.ParamChoice, staticChoices("true", "false")
		case f.Value.Type() == "bool":
			p.Kind = tui.ParamBool
		case choiceFlags[f.Name] != nil:
			p.Kind, p.Choices = tui.ParamChoice, choiceFlags[f.Name]
		case slice:
			p.Help += " (comma-separated, as on the command line)"
		}
		ps = append(ps, p)
	})
	return ps
}

// argParam reads the positional argument from a cobra Use line such as
// "files <pattern>" (required) or "get [pattern...]" (optional, variadic).
func argParam(use string) (tui.Param, bool) {
	fields := strings.Fields(use)
	if len(fields) < 2 {
		return tui.Param{}, false
	}
	a := fields[1]
	p := tui.Param{Arg: true, Required: strings.HasPrefix(a, "<")}
	a = strings.Trim(a, "<>[]")
	p.Variadic = strings.HasSuffix(a, "...")
	p.Name = strings.TrimSuffix(a, "...")
	return p, p.Name != ""
}

// matchRoute shows the match result per file, or per scope with --by-scope.
// Each file is also evaluated for who must approve it.
func matchRoute(v tui.Values) tui.Route {
	return commandRoute(matchCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		result, err := runMatch(ctx, match.Options{
			ChangedOnly: v.Bool("changed"),
			IncludeDiff: v.Bool("diff"),
			ScopeName:   v.Flag("scope"),
		})
		if err != nil {
			return nil, err
		}
		paths := make([]string, len(result.Files))
		for i, fm := range result.Files {
			paths[i] = fm.Path
		}
		own := ownershipOf(ctx, paths)
		if v.Bool("by-scope") {
			return matchByScopePage(result.GroupByScope(), own), nil
		}
		return matchPage(result, own), nil
	})
}

// ownership is who must approve each file under the current rules,
// with no approvals simulated: the approve phase's view of a file for the
// pages that do not run a review.
type ownership struct {
	files  map[string]approval.FileState
	events []approval.Event
	err    error
}

func ownershipOf(ctx context.Context, paths []string) ownership {
	engine, err := openEngine(quietLog)
	if err != nil {
		return ownership{err: err}
	}
	chain, err := engine.RuleChain(ctx, paths)
	if err != nil {
		return ownership{err: fmt.Errorf("evaluate approvers: %w", err)}
	}
	return ownershipFromState(engine.Requirements(ctx, chain))
}

func ownershipFromState(state *approval.ReviewState) ownership {
	return ownership{files: state.Files, events: state.Events}
}

// annotate adds a file's requirement to its row: a Requires field, its
// ownership badge, and the Chain and Trace tabs.
func (o ownership) annotate(it *tui.Item, path string) {
	fs, ok := o.files[path]
	if !ok {
		return
	}
	it.Badge = ownershipBadge(fs)
	it.Detail.Fields = append(it.Detail.Fields, tui.Field{Label: "Requires", Value: requiredOnlyLabel(fs), Badge: it.Badge})
	it.Detail.Tabs = append(chainTabs(fs, o.events), it.Detail.Tabs...)
}

// summary is the ownership counts for a page's status line.
func (o ownership) summary() string {
	if o.err != nil {
		return "approvers unavailable: " + o.err.Error()
	}
	fallback := 0
	for _, fs := range o.files {
		if fs.UsedFallback {
			fallback++
		}
	}
	return fmt.Sprintf("%d fall back to repository owners", fallback)
}

// fileRulesRoute is the drill-down from a file to `rules get` for it.
func fileRulesRoute(path string) tui.Route {
	r := rulesGetRoute(tui.Values{Args: []string{exactPath(path)}})
	r.Title = "rules for " + path
	return r
}

// matchPage lists files; the drill-down shows which scopes own the file
// (name, pattern, and the rules file that declared it), who must approve it
// and how its chain produced that, and, when the match ran with --diff, the
// file's diff. Enter opens the file's full rule chain.
func matchPage(result *match.Result, own ownership) tui.Page {
	items := make([]tui.Item, 0, len(result.Files))
	owned := 0
	for _, fm := range result.Files {
		if len(fm.Scopes) > 0 {
			owned++
		}
		var d tui.Detail
		d.Fields = []tui.Field{{Label: "File", Value: fm.Path}, {Label: "Scopes", Value: strconv.Itoa(len(fm.Scopes))}}
		for _, s := range fm.Scopes {
			d.Sections = append(d.Sections, tui.Section{Title: "Scope " + scopeLabel(s), Lines: scopeLines(s)})
		}
		if fm.Diff != "" {
			d.Tabs = append(d.Tabs, tui.Tab{Title: "Diff", Sections: []tui.Section{{Title: "Diff", Lines: strings.Split(strings.TrimRight(fm.Diff, "\n"), "\n"), Diff: true}}})
		}
		r := fileRulesRoute(fm.Path)
		it := tui.Item{Title: fm.Path, Description: scopeTitles(fm.Scopes), Detail: d, Route: &r}
		own.annotate(&it, fm.Path)
		items = append(items, it)
	}
	return tui.NewListPage("Files", items).
		WithSummary(fmt.Sprintf("%d files · %d owned · %d unowned · %s", len(items), owned, len(items)-owned, own.summary()))
}

func matchByScopePage(groups []match.ScopeGroup, own ownership) tui.Page {
	tg := make([]tui.TreeGroup, 0, len(groups))
	for _, g := range groups {
		files := slices.Sorted(slices.Values(g.Files))
		items := make([]tui.Item, 0, len(files))
		for _, f := range files {
			r := fileRulesRoute(f)
			it := tui.Item{
				Title:  f,
				Detail: tui.Detail{Fields: []tui.Field{{Label: "File", Value: f}, {Label: "Scope", Value: scopeLabel(g.Scope)}}},
				Route:  &r,
			}
			own.annotate(&it, f)
			items = append(items, it)
		}
		d := tui.Detail{Fields: append(scopeFields(g.Scope), tui.Field{Label: "Files", Value: strconv.Itoa(len(files))})}
		tg = append(tg, tui.TreeGroup{Title: scopeLabel(g.Scope), Detail: d, Items: items})
	}
	return tui.NewTreePage("Scopes", tg).WithSummary(countOf(len(groups), "scope") + " · " + own.summary())
}

func coverageRoute(v tui.Values) tui.Route {
	return commandRoute(coverageCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		opts := match.Options{ChangedOnly: v.Bool("changed"), ScopeName: v.Flag("scope")}
		report, match, err := runCoverage(ctx, opts)
		if err != nil {
			return nil, err
		}
		s, err := openRulesSet(ctx)
		if err != nil {
			return nil, err
		}
		return coveragePage(report, match, s), nil
	})
}

// detailFilesMax caps the file rows a detail lists inline; the drill-down
// has them all.
const detailFilesMax = 50

// coveragePage lists each scope's coverage with the files it matched; a
// scope with a name drills into `match --scope <name>`. The uncovered
// files are the last row, grouped by directory, each directory linking to
// `rules add` for its nearest rules file.
func coveragePage(report *match.CoverageReport, match *match.Result, s *rulesSet) tui.Page {
	items := make([]tui.Item, 0, len(report.PerScope)+1)
	for _, sc := range report.PerScope {
		d := tui.Detail{
			Fields: append(scopeFields(sc.Scope),
				tui.Field{Label: "Files", Value: strconv.Itoa(sc.Files)},
				tui.Field{Label: "Coverage", Value: fmt.Sprintf("%.2f%%", sc.Percent)},
			),
			Sections: []tui.Section{fileLinks("Files", match.FilesOf(sc.Scope))},
		}
		it := tui.Item{
			Title:       scopeLabel(sc.Scope) + " in " + sc.Scope.RulesFile,
			Description: fmt.Sprintf("%d file(s), %.1f%%", sc.Files, sc.Percent),
			Detail:      d,
		}
		if sc.Scope.Name != "" {
			r := matchRoute(tui.Values{Flags: map[string]string{"scope": sc.Scope.Name}})
			it.Route = &r
		}
		items = append(items, it)
	}
	uncovered := tui.Item{
		Title:       "Uncovered files",
		Description: fmt.Sprintf("%d file(s) match no rule", len(report.Uncovered)),
		Detail:      tui.Detail{Sections: []tui.Section{uncoveredSection(report.Uncovered, s)}},
	}
	if len(report.Uncovered) > 0 {
		uncovered.Badge = tui.BadgePending
	}
	items = append(items, uncovered)
	return tui.NewListPage("Coverage", items).
		WithSummary(fmt.Sprintf("Total coverage: %.2f%% of %d file(s)", report.Percent, report.Universe))
}

// fileLinks lists files as links to their rule chains, at most
// detailFilesMax of them.
func fileLinks(title string, files []string) tui.Section {
	sec := tui.Section{Title: fmt.Sprintf("%s (%d)", title, len(files))}
	for i, f := range files {
		if i == detailFilesMax {
			sec.Rows = append(sec.Rows, tui.Row{Text: fmt.Sprintf("+%d more", len(files)-i), Note: "enter on the row lists them all"})
			break
		}
		r := fileRulesRoute(f)
		sec.Rows = append(sec.Rows, tui.Row{Text: f, Route: &r})
	}
	return sec
}

// uncoveredSection groups uncovered files by directory. Each directory
// links to adding a rule for it to its nearest rules file, or to `init`
// when the repository has none.
func uncoveredSection(files []string, s *rulesSet) tui.Section {
	sec := tui.Section{Title: "By directory · enter adds a rule"}
	var dirs []string
	byDir := map[string][]string{}
	for _, f := range files {
		d := path.Dir(f)
		if _, ok := byDir[d]; !ok {
			dirs = append(dirs, d)
		}
		byDir[d] = append(byDir[d], f)
	}
	slices.Sort(dirs)
	for _, d := range dirs {
		nearest := s.nearestTo(d)
		var r tui.Route
		note := countOf(len(byDir[d]), "file")
		if nearest == "" {
			r = initRoute(tui.Values{})
			note += " · no rules file yet"
		} else {
			flags := map[string]string{"file": nearest}
			if d != "." {
				flags["pattern"] = "^" + regexp.QuoteMeta(d+"/")
			}
			r = rulesAddRoute(tui.Values{Flags: flags})
			note += " · nearest rules file: " + nearest
		}
		label := d + "/"
		if d == "." {
			label = "repository root"
		}
		sec.Rows = append(sec.Rows, tui.Row{Text: label, Note: note, Route: &r})
		for i, f := range byDir[d] {
			if i == detailFilesMax {
				sec.Rows = append(sec.Rows, tui.Row{Indent: 1, Text: fmt.Sprintf("+%d more", len(byDir[d])-i)})
				break
			}
			sec.Rows = append(sec.Rows, tui.Row{Indent: 1, Text: path.Base(f)})
		}
	}
	return sec
}

// rulesListRoute lists the rules of every rules file, or of the one --file
// names, with add, edit, and remove.
func rulesListRoute(v tui.Values) tui.Route {
	return commandRoute(rulesListCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		s, err := openRulesSet(ctx)
		if err != nil {
			return nil, err
		}
		only, err := s.resolve(v.Flag("file"))
		if err != nil {
			return nil, err
		}
		match, err := runMatch(ctx, match.Options{})
		return rulesPage(s, only, newRuleMatches(match, err)), nil
	})
}

// ruleMatches is one match over the repository, from which the rules page
// shows each rule's reach and its overlaps; err is why it is unavailable.
type ruleMatches struct {
	match *match.Result
	err   error
}

func newRuleMatches(match *match.Result, err error) ruleMatches {
	return ruleMatches{match: match, err: err}
}

// scopeOf is the scope of the rule keyed by pattern in rulesFile.
func scopeOf(rulesFile, pattern string) match.Scope {
	return match.Scope{RulesFile: rulesFile, Pattern: pattern}
}

// files are the files the rule keyed by pattern in rulesFile matched.
func (m ruleMatches) files(rulesFile, pattern string) []string {
	return m.match.FilesOf(scopeOf(rulesFile, pattern))
}

// overlaps are the other rules matching any of the rule's files, with how
// many files each shares, ordered by that count.
func (m ruleMatches) overlaps(rulesFile, pattern string) []tui.Row {
	overlaps := m.match.Overlaps(scopeOf(rulesFile, pattern))
	rows := make([]tui.Row, 0, len(overlaps))
	for _, o := range overlaps {
		sc := o.Scope
		r := rulesGetRoute(tui.Values{Args: []string{sc.Pattern}})
		rows = append(rows, tui.Row{Text: ruleLabel(sc.Name, sc.Pattern) + " in " + sc.RulesFile, Note: countOf(o.Shared, "shared file"), Route: &r})
	}
	return rows
}

// rulesPage lists the rules of every rules file, or only only's. The status
// line says when other rules files are hidden and how to see them.
func rulesPage(s *rulesSet, only string, matches ruleMatches) tui.Page {
	entries := s.entries(only)
	items := make([]tui.Item, len(entries))
	for i, e := range entries {
		items[i] = ruleItem(e, matches)
	}
	title, summary := "Rules", countOf(len(entries), "rule")+" in "+countOf(len(s.rules), "rules file")
	if only != "" {
		title += " · " + only
		summary = fmt.Sprintf("%s · showing %s, 1 of %s · o to change", countOf(len(entries), "rule"), only, countOf(len(s.rules), "rules file"))
	}
	if matches.err != nil {
		summary += " · file matches unavailable: " + matches.err.Error()
	}
	return tui.NewListPage(title, items).WithSummary(summary).WithActions(ruleActions(s)...)
}

// ruleItem is a rule row: the detail shows the full rule, what its
// strategy does, how many files it matches, and the other rules matching
// the same files, with the files themselves in a tab. Enter drills into
// `rules get <pattern>`, the files the pattern matches and their rule
// chains.
func ruleItem(e rulesfile.DeclaredRule, matches ruleMatches) tui.Item {
	route := rulesGetRoute(tui.Values{Args: []string{e.Rule.Pattern}})
	fields := append(ruleFieldsDetail(e.RulesFile, e.Rule),
		tui.Field{Label: "Effect", Value: strategyEffect(approval.EffectiveCombineStrategy(e.Rule.CombineStrategy), false)},
	)
	desc := orNone(e.Rule.Name) + " · " + e.RulesFile
	d := tui.Detail{Fields: fields}
	if matches.err == nil {
		files := matches.files(e.RulesFile, e.Rule.Pattern)
		d.Fields = append(d.Fields, tui.Field{Label: "Matches", Value: countOf(len(files), "file")})
		d.Sections = []tui.Section{{Title: "Overlaps · rules matching the same files", Rows: matches.overlaps(e.RulesFile, e.Rule.Pattern)}}
		d.Tabs = []tui.Tab{{Title: "Files", Sections: []tui.Section{fileLinks("Files", files)}}}
		desc += " · " + countOf(len(files), "file")
	}
	return tui.Item{Title: e.Rule.Pattern, Description: desc, Detail: d, Route: &route, Value: e}
}

func ruleFieldsDetail(path string, r rulesfile.Rule) []tui.Field {
	strategy := strategyName(r)
	if strategy == "" {
		strategy = "(default)"
	}
	return []tui.Field{
		{Label: "Pattern", Value: r.Pattern},
		{Label: "Name", Value: r.Name},
		{Label: "rules file", Value: path},
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
func ruleActions(s *rulesSet) []tui.Action {
	return []tui.Action{
		{
			Key: "a", Help: "add rule",
			Run: func(ctx context.Context, it tui.Item) (tui.Outcome, error) {
				choices := s.rules.Paths()
				if len(choices) == 0 {
					return tui.Outcome{}, errNoRulesFiles
				}
				seed := ruleFields{RulesFile: s.nearest()}
				if e, err := entryOf(it); err == nil {
					seed.RulesFile = e.RulesFile
				}
				page := newRuleFormPage("add rule", seed, choices, func(ctx context.Context, f ruleFields) (string, error) {
					return addRule(s, f.RulesFile, f)
				})
				return tui.Outcome{Page: page}, nil
			},
		},
		{
			Key: "e", Help: "edit rule", RequiresItem: true,
			Run: func(ctx context.Context, it tui.Item) (tui.Outcome, error) {
				e, err := entryOf(it)
				if err != nil {
					return tui.Outcome{}, err
				}
				page := newRuleFormPage("edit rule", fieldsFromRule(e.Rule), nil, func(ctx context.Context, f ruleFields) (string, error) {
					return editRule(s, e.RulesFile, e.Rule.Pattern, f)
				})
				return tui.Outcome{Page: page}, nil
			},
		},
		{
			Key: "x", Help: "remove rule", RequiresItem: true,
			Confirm: func(it tui.Item) string {
				if e, err := entryOf(it); err == nil {
					return fmt.Sprintf("Remove rule %q from %s?", e.Rule.Pattern, e.RulesFile)
				}
				return fmt.Sprintf("Remove rule %q?", it.Title)
			},
			Run: func(ctx context.Context, it tui.Item) (tui.Outcome, error) {
				e, err := entryOf(it)
				if err != nil {
					return tui.Outcome{}, err
				}
				info, err := removeRule(s, e.RulesFile, e.Rule.Pattern)
				return tui.Outcome{Info: info, Reload: true}, err
			},
		},
	}
}

// entryOf recovers the rule a rules-page row was built from (see ruleItem).
func entryOf(it tui.Item) (rulesfile.DeclaredRule, error) {
	e, ok := it.Value.(rulesfile.DeclaredRule)
	if !ok {
		return rulesfile.DeclaredRule{}, fmt.Errorf("row %q does not hold a rule", it.Title)
	}
	return e, nil
}

func addRule(s *rulesSet, rulesFile string, f ruleFields) (string, error) {
	rule, err := buildRuleFromFields(f)
	if err != nil {
		return "", err
	}
	if _, err := s.mutate(rulesFile, func(k *rulesfile.File) error { return k.Add(rule) }); err != nil {
		return "", err
	}
	return fmt.Sprintf("added rule %q to %s", rule.Pattern, rulesFile), nil
}

// editRule replaces the rule keyed by pattern; the pattern itself is the
// rule's identity and is not changed, matching `rules edit`.
func editRule(s *rulesSet, rulesFile, pattern string, f ruleFields) (string, error) {
	f.Pattern = pattern
	rule, err := buildRuleFromFields(f)
	if err != nil {
		return "", err
	}
	if _, err := s.mutate(rulesFile, func(k *rulesfile.File) error {
		return k.Edit(pattern, func(r *rulesfile.Rule) { *r = rule })
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited rule %q in %s", pattern, rulesFile), nil
}

// fieldsFromRule seeds the rule form (and `rules edit`) from an existing
// rule with the strategy the approval package applies for it, so re-saving a rule whose
// combine_strategy is omitted or corrupt never changes who has to approve.
func fieldsFromRule(r rulesfile.Rule) ruleFields {
	return ruleFields{
		Pattern:        r.Pattern,
		Name:           r.Name,
		Strategy:       approval.EffectiveCombineStrategy(r.CombineStrategy).String(),
		Ignore:         r.IgnoreParentDirs,
		CurrentDirOnly: r.CurrentDirectoryOnly,
		SelfApprove:    r.SelfApprove,
		ApproverText:   rulesfile.ApproversToText(r.Approvers),
	}
}

// rulesGetRoute resolves the rule chain for the files matching the pattern
// arguments (every file when there are none) and shows it as rule → files.
// The CLI argument is optional, but opening the route without one asks for
// the regex first, as `rules files` does, rather than loading every file.
func rulesGetRoute(v tui.Values) tui.Route {
	r := commandRoute(rulesGetCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		patterns := v.Args
		if len(patterns) == 0 {
			patterns = []string{`.*`}
		}
		engine, chain, err := ruleChainFor(ctx, patterns)
		if err != nil {
			return nil, err
		}
		return rulesGetPage(chain, ownershipFromState(engine.Requirements(ctx, chain))), nil
	})
	return requirePattern(r)
}

// requirePattern makes r's optional pattern argument required in the TUI,
// so opening the route asks for the regex first instead of loading every
// file; the CLI argument stays optional.
func requirePattern(r tui.Route) tui.Route {
	return withParam(r, func(p *tui.Param) bool { return p.Arg }, func(p *tui.Param) { p.Required = true })
}

// withParam applies change to r's parameters that match.
func withParam(r tui.Route, match func(*tui.Param) bool, change func(*tui.Param)) tui.Route {
	r.Params = slices.Clone(r.Params)
	for i := range r.Params {
		if match(&r.Params[i]) {
			change(&r.Params[i])
		}
	}
	return r
}

// ruleChainFor resolves the rule chain for every file matching any pattern
// at HEAD, with the engine that sifted it. A repository with no commit has
// no files and an empty chain.
func ruleChainFor(ctx context.Context, patterns []string) (*match.Engine, approval.RuleChain, error) {
	engine, err := openEngine(quietLog)
	if err != nil {
		return nil, approval.RuleChain{}, err
	}
	paths, err := engine.Files(ctx, match.Options{Patterns: patterns})
	if err != nil {
		return nil, approval.RuleChain{}, fmt.Errorf("find files: %w", err)
	}
	chain, err := engine.RuleChain(ctx, paths)
	if err != nil {
		return nil, approval.RuleChain{}, fmt.Errorf("fetch rules: %w", err)
	}
	return engine, chain, nil
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
		title := rf.Rule.Pattern + " in " + rf.RulesFile
		if rf.Rule.Name != "" {
			title += " (" + rf.Rule.Name + ")"
		}
		groups = append(groups, tui.TreeGroup{
			Title:  title,
			Detail: tui.Detail{Fields: append(ruleFieldsDetail(rf.RulesFile, rf.Rule), tui.Field{Label: "Files", Value: strconv.Itoa(len(rf.Files))})},
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

// rulesFilesRoute lists the rules files a pattern matches (`.*` for every one)
// and asks for the pattern first when opened from home. Enter opens the rules
// file's rules.
func rulesFilesRoute(v tui.Values) tui.Route {
	r := commandRoute(rulesFilesCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		var pattern string
		if len(v.Args) > 0 {
			pattern = v.Args[0]
		}
		files, err := findRulesFiles(pattern, v.Flag("revision"))
		if err != nil {
			return nil, err
		}
		s, err := openRulesSet(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]tui.Item, len(files))
		for i, f := range files {
			r := rulesListRoute(tui.Values{Flags: map[string]string{"file": f}})
			desc, d := rulesFileDetail(s, f, pattern)
			items[i] = tui.Item{Title: f, Description: desc, Detail: d, Route: &r}
		}
		title := "rules files"
		if pattern != "" {
			title += " matching " + pattern
		}
		return tui.NewListPage(title, items).WithSummary(countOf(len(files), "rules file")), nil
	})
	if len(v.Args) > 0 {
		r.Title = "files " + v.Args[0]
	}
	return requirePattern(r)
}

// rulesFileDetail describes a rules file as the working tree has it: its rules,
// each linking to the files it matches, and the rules file above it, whose
// rules it inherits unless a rule ignores parent directories.
func rulesFileDetail(s *rulesSet, file, pattern string) (string, tui.Detail) {
	d := tui.Detail{Fields: []tui.Field{{Label: "Path", Value: file}, {Label: "Matched by", Value: orNone(pattern)}}}
	rules, ok := s.rules[file]
	if !ok {
		d.Fields = append(d.Fields, tui.Field{Label: "Rules", Value: "not in the working tree"})
		return "rules file · not in the working tree", d
	}
	parent := "none (repository root)"
	if dir := path.Dir(file); dir != "." {
		parent = orNoneLabel(s.nearestTo(path.Dir(dir)))
	}
	d.Fields = append(d.Fields, tui.Field{Label: "Rules", Value: strconv.Itoa(len(rules))}, tui.Field{Label: "Parent", Value: parent})
	rows := make([]tui.Row, len(rules))
	for i, rule := range rules {
		r := rulesGetRoute(tui.Values{Args: []string{rule.Pattern}})
		rows[i] = tui.Row{Text: ruleLabel(rule.Name, rule.Pattern), Note: joinNotes(strategyName(rule), approversLabel(rule.Approvers)), Route: &r}
	}
	d.Sections = []tui.Section{{Title: "Rules · enter shows matching files", Rows: rows}}
	return "rules file · " + countOf(len(rules), "rule"), d
}

// validateRoute shows every rules file (or the one --file names) with its
// violations; enter opens the file's rules.
func validateRoute(v tui.Values) tui.Route {
	return commandRoute(validateCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		s, err := openRulesSet(ctx)
		if err != nil {
			return nil, err
		}
		only, err := s.resolve(v.Flag("file"))
		if err != nil {
			return nil, err
		}
		var items []tui.Item
		total := 0
		for _, path := range s.rules.Paths() {
			if only != "" && path != only {
				continue
			}
			errs := violationText(rulesfile.Set{path: s.rules[path]}.Validate())[path]
			total += len(errs)
			desc := "valid"
			if len(errs) > 0 {
				desc = countOf(len(errs), "violation")
			}
			r := rulesListRoute(tui.Values{Flags: map[string]string{"file": path}})
			badge := tui.BadgeOK
			if len(errs) > 0 {
				badge = tui.BadgeFail
			}
			items = append(items, tui.Item{
				Title:       path,
				Description: desc,
				Badge:       badge,
				Detail:      violationsDetail(path, s.rules[path], errs),
				Route:       &r,
			})
		}
		return tui.NewListPage("Validation", items).
			WithSummary(fmt.Sprintf("%s in %s", countOf(total, "violation"), countOf(len(items), "rules file"))), nil
	})
}

// violationsDetail places each violation beside the rule it is about, with
// the rule's fields and a link to edit it. Violations name their rule's
// pattern; any that name none are listed for the file as a whole.
func violationsDetail(file string, rules rulesfile.Rules, errs []string) tui.Detail {
	d := tui.Detail{Fields: []tui.Field{{Label: "rules file", Value: file}, {Label: "Violations", Value: strconv.Itoa(len(errs))}}}
	if len(errs) == 0 {
		d.Fields[1].Badge = tui.BadgeOK
		return d
	}
	d.Fields[1].Badge = tui.BadgeFail
	claimed := map[string]bool{}
	for _, rule := range rules {
		quoted := strconv.Quote(rule.Pattern)
		var rows []tui.Row
		for _, e := range errs {
			if strings.Contains(e, quoted) {
				claimed[e] = true
				rows = append(rows, tui.Row{Text: e, Badge: tui.BadgeFail})
			}
		}
		if len(rows) == 0 {
			continue
		}
		strategy := strategyName(rule)
		if strategy == "" {
			strategy = "(default)"
		}
		rows = append(rows,
			tui.Row{Indent: 1, Text: "name", Note: orNone(rule.Name)},
			tui.Row{Indent: 1, Text: "strategy", Note: strategy},
			tui.Row{Indent: 1, Text: "approvers", Note: orNoneLabel(approversLabel(rule.Approvers))},
		)
		edit := rulesEditRoute(tui.Values{Args: []string{rule.Pattern}, Flags: map[string]string{"file": file}})
		rows = append(rows, tui.Row{Text: "Edit this rule", Route: &edit})
		d.Sections = append(d.Sections, tui.Section{Title: "Rule " + rule.Pattern, Rows: rows})
	}
	var rest []tui.Row
	for _, e := range errs {
		if !claimed[e] {
			rest = append(rest, tui.Row{Text: e, Badge: tui.BadgeFail})
		}
	}
	if len(rest) > 0 {
		d.Sections = append(d.Sections, tui.Section{Title: "File", Rows: rest})
	}
	return d
}

// approversLabel renders approvers as "team (2), lead (1)": each target with
// the number of approvals it requires.
func approversLabel(targets rulesfile.ApproverTargets) string {
	parts := make([]string, len(targets))
	for i, t := range targets {
		parts[i] = fmt.Sprintf("%s (%d)", t.Target, t.RequiredCount)
	}
	return strings.Join(parts, ", ")
}

func scopeLabel(s match.Scope) string {
	if s.Name != "" {
		return s.Name
	}
	return s.Pattern
}

func scopeFields(s match.Scope) []tui.Field {
	return []tui.Field{
		{Label: "Scope", Value: s.Name},
		{Label: "Pattern", Value: s.Pattern},
		{Label: "Directory", Value: s.Directory},
		{Label: "rules file", Value: s.RulesFile},
	}
}

func scopeLines(s match.Scope) []string {
	return []string{"pattern   " + s.Pattern, "declared  " + s.RulesFile}
}

// exactPath is the rules get pattern matching exactly one path.
func exactPath(p string) string { return "^" + regexp.QuoteMeta(p) + "$" }

func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func orNone(s string) string {
	if s == "" {
		return "(unnamed)"
	}
	return s
}
