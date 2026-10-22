package cli

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/brand"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// routeFor is c's TUI route opened with v. Every command `--help` lists
// has one (TestRoutesMirrorCommandTree) except shell completion, which is
// CLI only; ok is also false for the root, whose TUI form is the home page
// itself.
func routeFor(c *cobra.Command, v tui.Values) (tui.Route, bool) {
	switch {
	case isCompletion(c):
		return tui.Route{}, false
	case c == matchCmd:
		return matchRoute(v), true
	case c == reviewCmd:
		return reviewRoute(v), true
	case c == coverageCmd:
		return coverageRoute(v), true
	case c == validateCmd:
		return validateRoute(v), true
	case c == initCmd:
		return initRoute(v), true
	case c == rulesListCmd:
		return rulesListRoute(v), true
	case c == rulesGetCmd:
		return rulesGetRoute(v), true
	case c == rulesFilesCmd:
		return rulesFilesRoute(v), true
	case c == rulesAddCmd:
		return rulesAddRoute(v), true
	case c == rulesEditCmd:
		return rulesEditRoute(v), true
	case c == rulesRemoveCmd:
		return rulesRemoveRoute(v), true
	case c.Parent() == rootCmd && c.Name() == "help":
		r := helpRoute(c, v)
		r.Utility = true
		return r, true
	case c != rootCmd && !c.Runnable() && c.HasAvailableSubCommands():
		// A command group such as `rules` prints its help.
		r := commandRoute(c, v, func(context.Context, tui.Values) (tui.Page, error) { return helpPage(c), nil })
		if c == rulesCmd {
			r.Summary = rulesSummary
		}
		return r, true
	}
	return tui.Route{}, false
}

// isCompletion reports whether c is cobra's `completion` command or one
// of its shells. Completion scripts are for the shell, so the TUI omits them.
func isCompletion(c *cobra.Command) bool {
	for ; c != nil; c = c.Parent() {
		if c.Parent() == rootCmd {
			return c.Name() == "completion"
		}
	}
	return false
}

// rulesSummary is the home page's live line for `rules`.
func rulesSummary(ctx context.Context) (string, error) {
	s, err := openRulesSet(ctx)
	if err != nil {
		return "", err
	}
	return countOf(len(s.entries("")), "rule") + " in " + countOf(len(s.rules), "rules file"), nil
}

// visibleCommands are c's subcommands that `--help` lists.
func visibleCommands(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() || sub.Name() == "help" {
			out = append(out, sub)
		}
	}
	return out
}

// runRoute is the route of a command that changes something: submitting its
// options form runs run, whose message is shown in the status line, and
// reloads the page beneath so it reflects the change.
func runRoute(cmd *cobra.Command, v tui.Values, run func(context.Context, tui.Values) (string, error)) tui.Route {
	r := commandRoute(cmd, v, nil)
	r.Run = func(ctx context.Context, v tui.Values) (tui.Outcome, error) {
		info, err := run(ctx, v)
		return tui.Outcome{Info: info, Reload: true}, err
	}
	return r
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
	fields, err := ruleFieldsFrom(ruleFields{Strategy: "union"}, v)
	if err != nil {
		return "", err
	}
	path := initPath(arg)
	k, err := writeRulesFile(path, v.Bool("force"), fields)
	if err != nil {
		return "", err
	}
	return initMessage(path, k), nil
}

// rulesAddRoute is `rules add`. The options form is the interactive
// form, so --pattern is required in it; --file defaults to the
// repository-root rules file, as in a scripted run.
func rulesAddRoute(v tui.Values) tui.Route {
	r := runRoute(rulesAddCmd, v, func(ctx context.Context, v tui.Values) (string, error) {
		s, err := openRulesSet(ctx)
		if err != nil {
			return "", err
		}
		return addFromValues(s, v)
	})
	return withParam(r, func(p *tui.Param) bool { return p.Name == "pattern" }, func(p *tui.Param) { p.Required = true })
}

func addFromValues(s *rulesSet, v tui.Values) (string, error) {
	file := v.Flag("file")
	if file == "" {
		file = rulesfile.FileName
	}
	target, err := s.resolve(file)
	if err != nil {
		return "", err
	}
	fields, err := ruleFieldsFrom(ruleFields{Strategy: "union"}, v)
	if err != nil {
		return "", err
	}
	return addRule(s, target, fields)
}

// rulesEditRoute is `rules edit <pattern>`: flags left unset keep the
// rule's values. A pattern declared by several rules files needs --file, as
// in a scripted run.
func rulesEditRoute(v tui.Values) tui.Route {
	return withPatternChoices(runRoute(rulesEditCmd, v, func(ctx context.Context, v tui.Values) (string, error) {
		s, err := openRulesSet(ctx)
		if err != nil {
			return "", err
		}
		return editFromValues(s, v)
	}))
}

func editFromValues(s *rulesSet, v tui.Values) (string, error) {
	pattern, err := patternArg(v)
	if err != nil {
		return "", err
	}
	target, err := s.ruleTarget(v.Flag("file"), pattern, false)
	if err != nil {
		return "", err
	}
	rule, err := s.rule(target, pattern)
	if err != nil {
		return "", err
	}
	fields, err := ruleFieldsFrom(fieldsFromRule(rule), v)
	if err != nil {
		return "", err
	}
	return editRule(s, target, pattern, fields)
}

// rulesRemoveRoute is `rules remove <pattern>`, which asks before
// removing unless --force is set.
func rulesRemoveRoute(v tui.Values) tui.Route {
	r := withPatternChoices(runRoute(rulesRemoveCmd, v, func(ctx context.Context, v tui.Values) (string, error) {
		s, err := openRulesSet(ctx)
		if err != nil {
			return "", err
		}
		return removeFromValues(s, v)
	}))
	r.Confirm = func(v tui.Values) string {
		if v.Bool("force") {
			return ""
		}
		return fmt.Sprintf("Remove rule %q?", strings.Join(v.Args, " "))
	}
	return r
}

func removeFromValues(s *rulesSet, v tui.Values) (string, error) {
	pattern, err := patternArg(v)
	if err != nil {
		return "", err
	}
	target, err := s.ruleTarget(v.Flag("file"), pattern, false)
	if err != nil {
		return "", err
	}
	return removeRule(s, target, pattern)
}

// patternArg is the <pattern> argument of rules edit and rules remove.
func patternArg(v tui.Values) (string, error) {
	if len(v.Args) != 1 {
		return "", errors.New("a rule pattern is required")
	}
	return v.Args[0], nil
}

// withPatternChoices offers the existing rule patterns for r's argument.
func withPatternChoices(r tui.Route) tui.Route {
	return withParam(r, func(p *tui.Param) bool { return p.Arg }, func(p *tui.Param) {
		p.Kind = tui.ParamChoice
		p.Choices = func(ctx context.Context) ([]string, error) {
			s, err := openRulesSet(ctx)
			if err != nil {
				return nil, err
			}
			return s.patterns(), nil
		}
	})
}

// helpRoute is `help [command]`; its argument is a command path of any
// length, offered as a choice of every command.
func helpRoute(help *cobra.Command, v tui.Values) tui.Route {
	r := commandRoute(help, v, func(_ context.Context, v tui.Values) (tui.Page, error) {
		c, _, err := rootCmd.Find(v.Args)
		if c == nil || err != nil {
			return nil, fmt.Errorf("unknown help topic %q", strings.Join(v.Args, " "))
		}
		return helpPage(c), nil
	})
	return withParam(r, func(p *tui.Param) bool { return p.Arg }, func(p *tui.Param) {
		p.Help = "the command to describe"
		p.Kind, p.Variadic, p.Unset = tui.ParamChoice, true, brand.Command
		p.Choices = func(context.Context) ([]string, error) {
			var paths []string
			var walk func(*cobra.Command)
			walk = func(c *cobra.Command) {
				for _, sub := range visibleCommands(c) {
					paths = append(paths, commandName(sub))
					walk(sub)
				}
			}
			walk(rootCmd)
			return paths, nil
		}
	})
}

// helpPage is `help` for c: a row with c's help text, then a row per
// subcommand that opens it.
func helpPage(c *cobra.Command) tui.Page {
	self := helpItem(c)
	if !c.Runnable() || c == rootCmd {
		// The row would reopen this page (a group) or home (the root).
		self.Route = nil
	}
	items := []tui.Item{self}
	for _, sub := range visibleCommands(c) {
		items = append(items, helpItem(sub))
	}
	return tui.NewListPage("help · "+commandName(c), items).WithSummary(c.Short)
}

// helpItem is c's row on a help page: its help text, and enter opens it.
func helpItem(c *cobra.Command) tui.Item {
	text := c.Long
	if text == "" {
		text = c.Short
	}
	text = strings.TrimSpace(text) + "\n\n" + strings.TrimRight(c.UsageString(), "\n")
	it := tui.Item{
		Title:       commandName(c),
		Description: c.Short,
		Detail:      tui.Detail{Sections: []tui.Section{{Title: "Help", Lines: strings.Split(text, "\n")}}},
	}
	if r, ok := routeFor(c, tui.Values{}); ok {
		it.Route = &r
	}
	return it
}

// commandName is c's command path without the leading command name, or the
// command name for the root.
func commandName(c *cobra.Command) string {
	if c == rootCmd {
		return rootCmd.Name()
	}
	return strings.TrimPrefix(c.CommandPath(), rootCmd.Name()+" ")
}

// joinList encodes a repeatable flag's values as one comma-separated value,
// quoting as pflag does, so splitList (and pflag) read back the same list.
func joinList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write(values) // A strings.Builder cannot fail.
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n")
}

// splitList reads a comma-separated flag value as pflag reads a repeatable
// flag's value.
func splitList(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	return csv.NewReader(strings.NewReader(s)).Read()
}
