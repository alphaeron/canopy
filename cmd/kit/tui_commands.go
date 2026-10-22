package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// routeFor is c's TUI route opened with v. Every command `kit --help` lists
// has one (TestRoutesMirrorCommandTree); ok is false for the root, whose
// TUI form is the home page itself.
func routeFor(c *cobra.Command, v tui.Values) (tui.Route, bool) {
	switch {
	case c == matchCmd:
		return matchRoute(v), true
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
		return helpRoute(c, v), true
	case c.Parent() != nil && c.Parent().Parent() == rootCmd && c.Parent().Name() == "completion":
		return completionRoute(c, v), true
	case c != rootCmd && !c.Runnable() && c.HasAvailableSubCommands():
		// A command group such as `kit rules` prints its help.
		return commandRoute(c, v, func(context.Context, tui.Values) (tui.Page, error) { return helpPage(c), nil }), true
	}
	return tui.Route{}, false
}

// visibleCommands are c's subcommands that `kit --help` lists.
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
func runRoute(cmd *cobra.Command, v tui.Values, run func(tui.Values) (string, error)) tui.Route {
	r := commandRoute(cmd, v, nil)
	r.Run = func(_ context.Context, v tui.Values) (tui.Outcome, error) {
		info, err := run(v)
		return tui.Outcome{Info: info, Reload: true}, err
	}
	return r
}

// initRoute is `kit init [path]`.
func initRoute(v tui.Values) tui.Route {
	return runRoute(initCmd, v, initFromValues)
}

func initFromValues(v tui.Values) (string, error) {
	var arg string
	if len(v.Args) > 0 {
		arg = v.Args[0]
	}
	fields, err := ruleFieldsFrom(tui.RuleFields{Strategy: "union"}, v)
	if err != nil {
		return "", err
	}
	path := initPath(arg)
	k, err := writeKitFile(path, v.Bool("force"), fields)
	if err != nil {
		return "", err
	}
	return initMessage(path, k), nil
}

// rulesAddRoute is `kit rules add`. The options form is the interactive
// form, so --pattern is required in it; --file defaults to the
// repository-root KIT, as in a scripted run.
func rulesAddRoute(v tui.Values) tui.Route {
	r := runRoute(rulesAddCmd, v, func(v tui.Values) (string, error) {
		s, err := openKitSet()
		if err != nil {
			return "", err
		}
		return addFromValues(s, v)
	})
	return withParam(r, func(p *tui.Param) bool { return p.Name == "pattern" }, func(p *tui.Param) { p.Required = true })
}

func addFromValues(s *kitSet, v tui.Values) (string, error) {
	file := v.Flag("file")
	if file == "" {
		file = kitfile.FileName
	}
	target, err := s.resolve(file)
	if err != nil {
		return "", err
	}
	fields, err := ruleFieldsFrom(tui.RuleFields{Strategy: "union"}, v)
	if err != nil {
		return "", err
	}
	return addRule(s, target, fields)
}

// rulesEditRoute is `kit rules edit <pattern>`: flags left unset keep the
// rule's values. A pattern declared by several KIT files needs --file, as
// in a scripted run.
func rulesEditRoute(v tui.Values) tui.Route {
	return withPatternChoices(runRoute(rulesEditCmd, v, func(v tui.Values) (string, error) {
		s, err := openKitSet()
		if err != nil {
			return "", err
		}
		return editFromValues(s, v)
	}))
}

func editFromValues(s *kitSet, v tui.Values) (string, error) {
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

// rulesRemoveRoute is `kit rules remove <pattern>`, which asks before
// removing unless --force is set.
func rulesRemoveRoute(v tui.Values) tui.Route {
	r := withPatternChoices(runRoute(rulesRemoveCmd, v, func(v tui.Values) (string, error) {
		s, err := openKitSet()
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

func removeFromValues(s *kitSet, v tui.Values) (string, error) {
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
		p.Choices = func(context.Context) ([]string, error) {
			s, err := openKitSet()
			if err != nil {
				return nil, err
			}
			return s.patterns(), nil
		}
	})
}

// helpRoute is `kit help [command]`; its argument is a command path of any
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
		p.Kind, p.Variadic, p.Unset = tui.ParamChoice, true, "kit"
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

// helpPage is `kit help` for c: a row with c's help text, then a row per
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

// commandName is c's command path without the leading "kit", or "kit" for
// the root.
func commandName(c *cobra.Command) string {
	if c == rootCmd {
		return rootCmd.Name()
	}
	return strings.TrimPrefix(c.CommandPath(), rootCmd.Name()+" ")
}

// completionRoute is `kit completion <shell>`: the script the command
// prints, and how to install it.
func completionRoute(c *cobra.Command, v tui.Values) tui.Route {
	shell := c.Name()
	return commandRoute(c, v, func(_ context.Context, v tui.Values) (tui.Page, error) {
		var buf bytes.Buffer
		if err := genCompletion(&buf, shell, !v.Bool("no-descriptions")); err != nil {
			return nil, err
		}
		script := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		items := []tui.Item{
			{
				Title:       shell + " completion script",
				Description: countOf(len(script), "line"),
				Detail:      tui.Detail{Sections: []tui.Section{{Title: "Script", Lines: script}}},
			},
			{
				Title:       "Installing it",
				Description: c.Short,
				Detail:      tui.Detail{Sections: []tui.Section{{Title: "Install", Lines: strings.Split(strings.TrimSpace(c.Long), "\n")}}},
			},
		}
		return tui.NewListPage(shell+" completion", items).WithSummary("kit " + commandName(c) + " prints this script"), nil
	})
}

// genCompletion writes the completion script for shell exactly as cobra's
// `completion <shell>` command does.
func genCompletion(w io.Writer, shell string, descriptions bool) error {
	switch shell {
	case "bash":
		return rootCmd.GenBashCompletionV2(w, descriptions)
	case "zsh":
		if descriptions {
			return rootCmd.GenZshCompletion(w)
		}
		return rootCmd.GenZshCompletionNoDesc(w)
	case "fish":
		return rootCmd.GenFishCompletion(w, descriptions)
	case "powershell":
		if descriptions {
			return rootCmd.GenPowerShellCompletionWithDesc(w)
		}
		return rootCmd.GenPowerShellCompletion(w)
	}
	return fmt.Errorf("no completion script for shell %q", shell)
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
