package tui

import (
	"context"
	"maps"
	"slices"
	"strings"
)

// Route is a navigable destination in the TUI. Each route is one CLI
// command with its argument and flag values: the home page and the command
// palette list every command with default values, running that command on a
// terminal opens the TUI on the same route with the given values, and the
// options form (o) edits the values in place. The CLI and the TUI therefore
// take the same inputs and load the same data.
type Route struct {
	// Command is the command path after "kit", e.g. "rules list". It is the
	// home and palette label.
	Command string
	// Title, when set, replaces the breadcrumb label, for drill-downs whose
	// command line is unwieldy (e.g. "rules for cmd/kit/main.go").
	Title string
	// Description is a one-line summary.
	Description string
	// Params are the command's arguments and flags, in display order.
	Params []Param
	// Values are the arguments and flags this route opens with.
	Values Values
	// Load builds the route's page for v. It runs off the UI loop, so it
	// may do I/O; it runs again when the page is reloaded or its options
	// change.
	Load func(ctx context.Context, v Values) (Page, error)
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
	// runs off the UI loop when the options form opens.
	Choices func(ctx context.Context) ([]string, error)
}

// Values are a route's positional arguments and flag values, exactly as the
// CLI receives them. Boolean flags are "true" or "false"; unset flags are
// absent.
type Values struct {
	Args  []string
	Flags map[string]string
}

// Flag returns a flag's value, or "" when unset.
func (v Values) Flag(name string) string { return v.Flags[name] }

// Bool reports whether a boolean flag is set to true.
func (v Values) Bool(name string) bool { return v.Flags[name] == "true" }

// WithFlag returns a copy of v with flag name set to value ("" unsets it).
func (v Values) WithFlag(name, value string) Values {
	flags := maps.Clone(v.Flags)
	if flags == nil {
		flags = map[string]string{}
	}
	if value == "" {
		delete(flags, name)
	} else {
		flags[name] = value
	}
	return Values{Args: slices.Clone(v.Args), Flags: flags}
}

// CommandLine is the CLI invocation equivalent to the route, quoted for a
// POSIX shell so it can be copied and run, e.g.
// "kit match --changed --scope=core". Flags appear in Params order; false
// booleans and empty values are omitted as they are the defaults.
func (r Route) CommandLine() string {
	parts := []string{"kit", r.Command}
	for _, a := range r.Values.Args {
		parts = append(parts, shellQuote(a))
	}
	seen := map[string]bool{}
	appendFlag := func(name string) {
		seen[name] = true
		val, ok := r.Values.Flags[name]
		switch {
		case !ok || val == "" || val == "false":
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
// the "kit " prefix.
func (r Route) label() string {
	if r.Title != "" {
		return r.Title
	}
	return strings.TrimPrefix(r.CommandLine(), "kit ")
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
