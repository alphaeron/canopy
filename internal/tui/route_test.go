package tui

import (
	"context"
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

func TestValuesWithFlag(t *testing.T) {
	v := Values{Args: []string{"a"}, Flags: map[string]string{"file": "KIT"}}
	w := v.WithFlag("file", "svc/KIT").WithFlag("changed", "true")
	if v.Flag("file") != "KIT" || v.Bool("changed") {
		t.Errorf("WithFlag must not modify the receiver: %+v", v)
	}
	if w.Flag("file") != "svc/KIT" || !w.Bool("changed") || !reflect.DeepEqual(w.Args, []string{"a"}) {
		t.Errorf("WithFlag result = %+v", w)
	}
	if u := w.WithFlag("file", ""); u.Flag("file") != "" {
		t.Errorf("empty value must unset the flag: %+v", u)
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
