package cli

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
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
	return tui.Run(ctx, tui.Options{Routes: appRoutes(), Start: &r, StartPage: page, Overview: repoOverview})
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
	"strategy": staticChoices(rulesfile.RuleCombineStrategyNames()...),
	"file": func(ctx context.Context) ([]string, error) {
		s, err := openWorkspace(ctx, quietLog)
		if err != nil {
			return nil, err
		}
		return s.Paths(), nil
	},
	"scope": func(ctx context.Context) ([]string, error) {
		s, err := openWorkspace(ctx, quietLog)
		if err != nil {
			return nil, err
		}
		return s.Rules.Names(), nil
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

// ownership is who must approve each file under the current rules,
// with no approvals simulated: the approve phase's view of a file for the
// pages that do not run a review.
type ownership struct {
	state *approval.ReviewState // nil when err is set
	err   error
}

// ownershipOf is who must approve each of paths, read through engine, so
// a page shares one read of the repository.
func ownershipOf(ctx context.Context, engine *review.Engine, paths []string) ownership {
	chain, err := engine.RuleChain(ctx, paths)
	if err != nil {
		return ownership{err: fmt.Errorf("evaluate approvers: %w", err)}
	}
	return ownership{state: engine.Requirements(ctx, chain)}
}

// annotate adds a file's requirement to its row: a Requires field, its
// ownership badge, and the Chain and Trace tabs.
func (o ownership) annotate(it *tui.Item, path string) {
	if o.state == nil {
		return
	}
	fs, ok := o.state.Files[path]
	if !ok {
		return
	}
	it.Badge = ownershipBadge(fs)
	it.Detail.Fields = append(it.Detail.Fields, tui.Field{Label: "Requires", Value: requiredOnlyLabel(fs), Badge: it.Badge})
	it.Detail.Tabs = append(chainTabs(fs, o.state), it.Detail.Tabs...)
}

// summary is the ownership counts for a page's status line.
func (o ownership) summary() string {
	if o.err != nil {
		return "approvers unavailable: " + o.err.Error()
	}
	_, fallback := o.state.RequiredTargets()
	return fmt.Sprintf("%d fall back to repository owners", fallback)
}

// fileRulesRoute is the drill-down from a file to `rules get` for it.
func fileRulesRoute(path string) tui.Route {
	r := rulesGetRoute(tui.Values{Args: []string{review.ExactPattern(path)}})
	r.Title = "rules for " + path
	return r
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

// approversLabel renders approvers as "team (2), lead (1)": each target with
// the number of approvals it requires.
func approversLabel(targets rulesfile.ApproverTargets) string {
	parts := make([]string, len(targets))
	for i, t := range targets {
		parts[i] = fmt.Sprintf("%s (%d)", t.Target, t.RequiredCount)
	}
	return strings.Join(parts, ", ")
}

// ruleTitle is a rule's name, or its pattern when it has none.
func ruleTitle(r rulesfile.Rule) string {
	if r.Name != "" {
		return r.Name
	}
	return r.Pattern
}

// ruleLines are the pattern and declaring rules file of r.
func ruleLines(r rulesfile.Rule) []string {
	return []string{"pattern   " + r.Pattern, "declared  " + r.Location()}
}

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

// quietLog discards the review algorithm's structured output in the TUI,
// where a line on stderr would tear the screen. Everything it logs is also
// in the ReviewState the pages render.
var quietLog = slog.New(slog.DiscardHandler)
