package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCommandLine(t *testing.T) {
	params := []Param{
		{Name: "pattern", Arg: true, Variadic: true},
		{Name: "changed", Kind: ParamBool},
		{Name: "scope", Kind: ParamChoice},
	}
	tests := []struct {
		name string
		v    Values
		want string
	}{
		{name: "defaults", want: "kit match"},
		{name: "flags in param order", v: Values{Flags: map[string]string{"scope": "core", "changed": "true"}}, want: "kit match --changed --scope=core"},
		{name: "false and empty omitted", v: Values{Flags: map[string]string{"changed": "false", "scope": ""}}, want: "kit match"},
		{name: "shell-quoted values", v: Values{Args: []string{`.*\.go`}, Flags: map[string]string{"scope": "Core Backend Review"}}, want: `kit match '.*\.go' --scope='Core Backend Review'`},
		{name: "single quote escaped", v: Values{Args: []string{"it's"}}, want: `kit match 'it'\''s'`},
		{name: "undeclared flags last, sorted", v: Values{Flags: map[string]string{"zeta": "1", "alpha": "2", "changed": "true"}}, want: "kit match --changed --alpha=2 --zeta=1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Route{Command: "match", Params: params, Values: tc.v}
			if got := r.CommandLine(); got != tc.want {
				t.Errorf("CommandLine = %q, want %q", got, tc.want)
			}
		})
	}
}

// valuesRoute is a route whose page shows the values it was loaded with and
// counts its loads.
func valuesRoute(params []Param, loads *[]Values) Route {
	return Route{
		Command: "match",
		Params:  params,
		Load: func(_ context.Context, v Values) (Page, error) {
			*loads = append(*loads, v)
			return NewListPage("Files", []Item{{Title: "loaded " + strings.Join(v.Args, ",")}}), nil
		},
	}
}

// TestOptionsReloadInPlace is the TUI form of retyping a command with new
// flags: o opens the options, and applying them reloads the same page with
// the new values, shown in the breadcrumb as the CLI invocation.
func TestOptionsReloadInPlace(t *testing.T) {
	var loads []Values
	r := valuesRoute([]Param{{Name: "changed", Kind: ParamBool, Help: "only changed files"}}, &loads)
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r})
	h.press("o")
	if _, ok := h.a.top().page.(*optionsPage); !ok {
		t.Fatalf("o must open the options form:\n%s", h.screen())
	}
	if !strings.Contains(h.screen(), "Runs  kit match") {
		t.Errorf("the form must preview the command line:\n%s", h.screen())
	}
	h.press("left", "enter") // Confirm fields default to "No"; left selects "Yes".
	if len(h.a.stack) != 2 || len(loads) != 2 || !loads[1].Bool("changed") {
		t.Fatalf("applying must reload the page in place with the new values (stack %d, loads %+v)", len(h.a.stack), loads)
	}
	if got := h.crumbs(); !strings.Contains(got, "kit › match --changed") {
		t.Errorf("breadcrumb must show the new command line, got %q", got)
	}
	h.press("o", "esc")
	if len(h.a.stack) != 2 || len(loads) != 2 {
		t.Errorf("esc must close the options without reloading")
	}
}

// TestRequiredArgOpensOptionsFirst covers commands such as `rules files
// <pattern>`: opened without the argument, they ask for it before loading.
func TestRequiredArgOpensOptionsFirst(t *testing.T) {
	var loads []Values
	r := valuesRoute([]Param{{Name: "pattern", Arg: true, Required: true}}, &loads)
	h := newHarness(t, Config{Routes: []Route{r}})
	h.press("enter")
	if _, ok := h.a.top().page.(*optionsPage); !ok || len(loads) != 0 {
		t.Fatalf("a missing required argument must open the options form first (loads %d)", len(loads))
	}
	h.press("enter")
	if _, ok := h.a.top().page.(*optionsPage); !ok || len(loads) != 0 {
		t.Fatalf("the form must not submit without the argument:\n%s", h.screen())
	}
	h.send(tea.PasteMsg{Content: "a.go"})
	h.press("enter")
	if len(h.a.stack) != 2 || len(loads) != 1 || !reflect.DeepEqual(loads[0].Args, []string{"a.go"}) {
		t.Fatalf("submitting must open the page with the argument (stack %d, loads %+v)", len(h.a.stack), loads)
	}
}

func TestOptionsChoices(t *testing.T) {
	var loads []Values
	r := valuesRoute([]Param{{
		Name: "file", Kind: ParamChoice,
		Choices: func(context.Context) ([]string, error) { return []string{"KIT", "svc/KIT"}, nil },
	}}, &loads)
	r.Values = Values{Flags: map[string]string{"file": "gone/KIT"}}
	h := newHarness(t, Config{Routes: []Route{r}, Start: &r})
	h.press("o")
	for _, want := range []string{"all", "svc/KIT", "gone/KIT"} {
		if !strings.Contains(h.screen(), want) {
			t.Errorf("choice %q missing from the options form:\n%s", want, h.screen())
		}
	}
}

func TestRuleFormOffersKitFiles(t *testing.T) {
	p := NewRuleFormPage("add rule", RuleFields{KitFile: "svc/KIT"}, []string{"KIT", "svc/KIT"}, nil).(*formPage)
	// huh renders nothing until sized and initialized, as mount does.
	p.setSize(100, 40)
	_ = p.init()
	view := ansi.Strip(p.form.View())
	if !strings.Contains(view, "KIT file") || !strings.Contains(view, "> svc/KIT") {
		t.Errorf("the rule form must start with the KIT file picker:\n%s", view)
	}
	bare := NewRuleFormPage("init", RuleFields{}, nil, nil).(*formPage)
	bare.setSize(100, 40)
	_ = bare.init()
	if strings.Contains(bare.form.View(), "KIT file") {
		t.Error("without KIT files the picker must be absent")
	}
}

// runRoute is a Run route that records the values it ran with and fails
// while fail is set.
func runRoute(params []Param, runs *[]Values, fail *bool) Route {
	return Route{
		Command: "rules add",
		Params:  params,
		Run: func(_ context.Context, v Values) (Outcome, error) {
			*runs = append(*runs, v)
			if *fail {
				return Outcome{}, errors.New("bad pattern")
			}
			return Outcome{Info: "added rule"}, nil
		},
	}
}

// TestRunRouteRunsFromOptions is the TUI form of a command that changes
// something: opening it shows its options (the command line), submitting
// runs it, and a failure reopens the form with the values to correct.
func TestRunRouteRunsFromOptions(t *testing.T) {
	var runs []Values
	fail := true
	r := runRoute([]Param{{Name: "pattern", Required: true}}, &runs, &fail)
	h := newHarness(t, Config{Routes: []Route{r}})
	h.press("enter")
	if _, ok := h.a.top().page.(*optionsPage); !ok || len(runs) != 0 {
		t.Fatalf("opening a Run route must show its options first (runs %d)", len(runs))
	}
	h.send(tea.PasteMsg{Content: "a.go"})
	h.press("enter")
	if len(runs) != 1 || runs[0].Flag("pattern") != "a.go" {
		t.Fatalf("submitting must run the command with the form's values, got %+v", runs)
	}
	p, ok := h.a.top().page.(*optionsPage)
	if !ok || p.route.Values.Flag("pattern") != "a.go" || !strings.Contains(h.screen(), "✗ bad pattern") {
		t.Fatalf("a failure must reopen the form with the values and report the error:\n%s", h.screen())
	}
	fail = false
	h.press("enter")
	if len(runs) != 2 || len(h.a.stack) != 1 || !strings.Contains(h.screen(), "✓ added rule") {
		t.Fatalf("a success must close the form and report (runs %d, stack %d):\n%s", len(runs), len(h.a.stack), h.screen())
	}
}

// TestRunRouteConfirms covers rules remove: without --force it asks first.
func TestRunRouteConfirms(t *testing.T) {
	var runs []Values
	fail := false
	r := runRoute([]Param{{Name: "force", Kind: ParamBool}}, &runs, &fail)
	r.Confirm = func(v Values) string {
		if v.Bool("force") {
			return ""
		}
		return "Remove rule?"
	}
	h := newHarness(t, Config{Routes: []Route{r}})
	h.press("enter", "enter")
	if !strings.Contains(h.screen(), "Remove rule? [y/N]") || len(runs) != 0 {
		t.Fatalf("must ask before running:\n%s", h.screen())
	}
	h.press("n")
	if len(runs) != 0 {
		t.Fatal("declining must not run")
	}
	h.press("enter", "enter")
	h.press("y")
	if len(runs) != 1 {
		t.Fatal("accepting must run")
	}
	h.press("enter", "left", "enter")
	if len(runs) != 2 || h.a.prompt != nil {
		t.Fatalf("--force must run without asking (runs %d)", len(runs))
	}
}

// TestChoiceUnsetAndRequired pins the empty choice's label and that a
// required choice has none.
func TestChoiceUnsetAndRequired(t *testing.T) {
	labels := func(p Param) []string {
		var out []string
		for _, o := range choiceOptions(p, []string{"true", "false"}, "") {
			out = append(out, o.Key)
		}
		return out
	}
	if got := labels(Param{}); !reflect.DeepEqual(got, []string{"all", "true", "false"}) {
		t.Errorf("default unset label = %q", got)
	}
	if got := labels(Param{Unset: "unchanged"}); got[0] != "unchanged" {
		t.Errorf("unset label = %q", got)
	}
	if got := labels(Param{Required: true}); !reflect.DeepEqual(got, []string{"true", "false"}) {
		t.Errorf("a required choice must have no empty option, got %q", got)
	}
}

// TestCommandLineFalseChoice: a tri-state boolean (rules edit
// --self-approve) set to false must appear, unlike a plain false boolean.
func TestCommandLineFalseChoice(t *testing.T) {
	r := Route{
		Command: "rules edit",
		Params: []Param{
			{Name: "pattern", Arg: true, Required: true},
			{Name: "self-approve", Kind: ParamChoice},
			{Name: "force", Kind: ParamBool},
		},
		Values: Values{Args: []string{"a"}, Flags: map[string]string{"self-approve": "false", "force": "false"}},
	}
	if got, want := r.CommandLine(), "kit rules edit a --self-approve=false"; got != want {
		t.Errorf("CommandLine = %q, want %q", got, want)
	}
}

// TestVariadicChoiceArg: `help rules add` takes a command path as several
// words, chosen as one value.
func TestVariadicChoiceArg(t *testing.T) {
	r := Route{Command: "help", Params: []Param{{Name: "command", Arg: true, Variadic: true, Kind: ParamChoice}},
		Values: Values{Args: []string{"rules", "add"}}}
	p := newOptionsPage(r, map[string][]string{"command": {"rules", "rules add"}}, false)
	if got := p.values().Args; !reflect.DeepEqual(got, []string{"rules", "add"}) {
		t.Errorf("args = %q", got)
	}
}

// TestRequiredChoice covers rules edit's pattern picker: the first choice
// is selected, and with no choices (no rules) the form does not open.
func TestRequiredChoice(t *testing.T) {
	var choices []string
	var runs []Values
	fail := false
	r := runRoute([]Param{{Name: "pattern", Arg: true, Required: true, Kind: ParamChoice,
		Choices: func(context.Context) ([]string, error) { return choices, nil }}}, &runs, &fail)
	h := newHarness(t, Config{Routes: []Route{r}})
	h.press("enter")
	if len(h.a.stack) != 1 || !strings.Contains(h.screen(), "✗ rules add: no <pattern> to choose from") {
		t.Fatalf("no choices must report instead of opening an unsubmittable form:\n%s", h.screen())
	}
	choices = []string{`.*\.go`, "docs/"}
	h.press("enter", "enter")
	if len(runs) != 1 || !reflect.DeepEqual(runs[0].Args, []string{`.*\.go`}) {
		t.Errorf("ran with %+v, want the first choice", runs)
	}
}

// TestClearableFlag covers rules edit --name: unset (unchanged) differs
// from set empty, and the input is a hidden group that is skipped unless
// the flag is set.
func TestClearableFlag(t *testing.T) {
	params := []Param{
		{Name: "pattern", Arg: true, Required: true},
		{Name: "name", Unset: "unchanged"},
		{Name: "force", Kind: ParamBool},
	}
	const input = "empty sets it empty"
	tests := []struct {
		name   string
		keys   []string // after the pattern, on the --name choice
		paste  string
		want   map[string]string
		inputs bool
	}{
		{"unchanged skips the input", []string{"enter"}, "", map[string]string{}, false},
		{"set empty", []string{"down", "enter"}, "", map[string]string{"name": ""}, true},
		{"set a value", []string{"down", "enter"}, "core", map[string]string{"name": "core"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var runs []Values
			fail := false
			r := runRoute(params, &runs, &fail)
			h := newHarness(t, Config{Routes: []Route{r}})
			h.press("enter")
			h.send(tea.PasteMsg{Content: "p"})
			h.press("enter")
			if strings.Contains(h.screen(), input) {
				t.Fatalf("the input must be hidden until the flag is set:\n%s", h.screen())
			}
			h.press(tc.keys...)
			if got := strings.Contains(h.screen(), input); got != tc.inputs {
				t.Fatalf("input shown = %v, want %v:\n%s", got, tc.inputs, h.screen())
			}
			if tc.inputs {
				if tc.paste != "" {
					h.send(tea.PasteMsg{Content: tc.paste})
				}
				h.press("enter")
			}
			h.press("enter") // --force, the last field, submits.
			if len(runs) != 1 || !reflect.DeepEqual(runs[0].Flags, tc.want) {
				t.Fatalf("ran with %+v, want flags %v", runs, tc.want)
			}
		})
	}
}

func TestClearableSeedAndCommandLine(t *testing.T) {
	r := Route{
		Command: "rules edit",
		Params:  []Param{{Name: "pattern", Arg: true}, {Name: "name", Unset: "unchanged"}, {Name: "scope"}},
		Values:  Values{Args: []string{"p"}, Flags: map[string]string{"name": "", "scope": ""}},
	}
	if got, want := r.CommandLine(), "kit rules edit p --name=''"; got != want {
		t.Errorf("CommandLine = %q, want %q", got, want)
	}
	p := newOptionsPage(r, nil, false)
	if got := p.values().Flags; !reflect.DeepEqual(got, map[string]string{"name": ""}) {
		t.Errorf("a set empty flag must reopen as set, got %v", got)
	}
	r.Values.Flags = map[string]string{"name": "core"}
	if got := newOptionsPage(r, nil, false).values().Flags["name"]; got != "core" {
		t.Errorf("name = %q", got)
	}
}
