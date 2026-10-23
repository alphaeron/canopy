package tui

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/alphaeron/canopy/internal/brand"
)

// Route is a navigable destination in the TUI. Each route is one CLI
// command with its argument and flag values: the home page and the command
// palette list every command with default values, running that command on a
// terminal opens the TUI on the same route with the given values, and the
// options form (o) edits the values in place. The CLI and the TUI therefore
// take the same inputs and load the same data.
type Route struct {
	// Command is the command path after the command name, e.g. "rules list". It
	// is the home and palette label.
	Command string
	// Title, when set, replaces the breadcrumb label, for drill-downs whose
	// command line is unwieldy (e.g. "rules for cmd/app/main.go").
	Title string
	// Description is a one-line summary.
	Description string
	// Long is the command's full help text, shown on the home page.
	Long string
	// Utility marks a command that is not about the repository (help):
	// the home page lists it last, its group collapsed.
	Utility bool
	// Summary, when set, is the live one-line description the home page
	// previews for the route, for a route whose page is not itself a good
	// preview (a command group).
	Summary func(ctx context.Context) (string, error)
	// Params are the command's arguments and flags, in display order.
	Params []Param
	// Values are the arguments and flags this route opens with.
	Values Values
	// Load builds the route's page for v. It runs off the UI loop, so it
	// may do I/O; it runs again when the page is reloaded or its options
	// change.
	Load func(ctx context.Context, v Values) (Page, error)
	// Run, set instead of Load, makes the route a command that changes
	// something (init, rules add): it has no page, so opening it always
	// shows the options form, and submitting the form runs it off the UI
	// loop. Its Outcome is applied as an action's is; on an error the form
	// reopens with the submitted values so they can be corrected.
	Run func(ctx context.Context, v Values) (Outcome, error)
	// Confirm, when set and returning non-empty text for v, asks that
	// question before Run runs, as the CLI prompts unless --force is given.
	Confirm func(v Values) string
}

// ParamKind is how the options form edits a parameter.
type ParamKind int

const (
	// ParamText is free text: a string flag or a positional argument.
	ParamText ParamKind = iota
	// ParamBool is a boolean flag.
	ParamBool
	// ParamChoice is one of a set of values, or none (shown as "all").
	ParamChoice
)

// Param is one argument or flag of a route's command.
type Param struct {
	// Name is the flag name without dashes, or the argument's name.
	Name string
	// Help is the flag's or argument's usage text.
	Help string
	Kind ParamKind
	// Arg marks a positional argument. A route has at most one Arg param,
	// which holds Values.Args.
	Arg bool
	// Variadic lets an Arg param take several values (one per line in the
	// options form).
	Variadic bool
	// Required means the route cannot load without a value; opening it
	// with none shows the options form first.
	Required bool
	// Choices lists the values a ParamChoice may take. It may do I/O and
	// runs off the UI loop when the options form opens. A variadic choice
	// argument's value is split into words (a command path such as
	// "rules add").
	Choices func(ctx context.Context) ([]string, error)
	// Unset labels the empty choice of an optional ParamChoice: what the
	// command does when the flag is not given. It defaults to "all". On a
	// text flag it makes the flag clearable: not giving it (Unset, e.g.
	// "unchanged") differs from giving it empty, as `--name ''` does.
	Unset string
}

// clearable reports whether p is a text flag whose empty value is a value,
// distinct from leaving the flag unset.
func (p Param) clearable() bool { return !p.Arg && p.Kind == ParamText && p.Unset != "" }

// CommandLine is the CLI invocation equivalent to the route, quoted for a
// POSIX shell so it can be copied and run, e.g.
// "match --changed --scope=core". Flags appear in Params order; false
// booleans and empty values are omitted as they are the defaults. A "false"
// choice and a clearable flag's empty value are kept: their unset value
// means something else, such as "unchanged" for rules edit.
func (r Route) CommandLine() string {
	parts := []string{brand.Command, r.Command}
	for _, a := range r.Values.Args {
		parts = append(parts, shellQuote(a))
	}
	seen := map[string]bool{}
	choice, clearable := map[string]bool{}, map[string]bool{}
	for _, p := range r.Params {
		choice[p.Name] = !p.Arg && p.Kind == ParamChoice
		clearable[p.Name] = p.clearable()
	}
	appendFlag := func(name string) {
		seen[name] = true
		val, ok := r.Values.Flags[name]
		switch {
		case !ok || val == "" && !clearable[name] || val == "false" && !choice[name]:
		case val == "true":
			parts = append(parts, "--"+name)
		default:
			parts = append(parts, "--"+name+"="+shellQuote(val))
		}
	}
	for _, p := range r.Params {
		if !p.Arg {
			appendFlag(p.Name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(r.Values.Flags)) {
		if !seen[name] {
			appendFlag(name)
		}
	}
	return strings.Join(parts, " ")
}

// label is the breadcrumb text: the Title, or the command line without
// the command-name prefix.
func (r Route) label() string {
	if r.Title != "" {
		return r.Title
	}
	return strings.TrimPrefix(r.CommandLine(), brand.Command+" ")
}

// missingRequired reports whether a required parameter has no value, in
// which case the route must collect it in the options form before loading.
func (r Route) missingRequired() bool {
	for _, p := range r.Params {
		if !p.Required {
			continue
		}
		if p.Arg && len(r.Values.Args) == 0 || !p.Arg && r.Values.Flag(p.Name) == "" {
			return true
		}
	}
	return false
}

// shellQuote quotes s for a POSIX shell unless it is made only of
// characters that need no quoting.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsFunc(s, needsQuote) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// needsQuote reports whether r is special to a POSIX shell (or whitespace).
func needsQuote(r rune) bool {
	safe := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-./=:@,+", r)
	return !safe
}
