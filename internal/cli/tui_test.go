package cli

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/brand"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// TestRouteFromCommand pins that a command's TUI route is derived from its
// cobra definition (so the options form matches the CLI) and that a CLI
// invocation becomes the same route values.
func TestRouteFromCommand(t *testing.T) {
	root := &cobra.Command{Use: brand.Command}
	cmd := &cobra.Command{Use: "files <pattern>", Short: "List files", Annotations: map[string]string{"arg": "a regex"}}
	cmd.Flags().Bool("changed", false, "only changed")
	cmd.Flags().String("scope", "", "one scope")
	cmd.Flags().String("revision", "", "tree revision")
	cmd.Flags().Bool("all", false, "old")
	cobra.CheckErr(cmd.Flags().MarkDeprecated("all", "gone"))
	root.AddCommand(cmd)
	if err := cmd.ParseFlags([]string{"--changed", "--scope", "core"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	params := paramsFor(cmd)
	got := map[string]tui.Param{}
	for _, p := range params {
		got[p.Name] = p
	}
	if len(params) != 4 {
		t.Fatalf("want pattern, changed, revision, scope (deprecated flags hidden), got %+v", params)
	}
	if p := got["pattern"]; !p.Arg || !p.Required || p.Variadic || p.Help != "a regex" {
		t.Errorf("pattern param = %+v", p)
	}
	if got["changed"].Kind != tui.ParamBool || got["scope"].Kind != tui.ParamChoice || got["revision"].Kind != tui.ParamText {
		t.Errorf("param kinds = %+v", params)
	}

	v := valuesFrom(cmd, []string{`.*\.go`})
	want := tui.Values{Args: []string{`.*\.go`}, Flags: map[string]string{"changed": "true", "scope": "core"}}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("valuesFrom = %+v, want %+v", v, want)
	}
	r := tui.Route{Command: "files", Params: params, Values: v}
	if got, want := r.CommandLine(), brand.Command+` files '.*\.go' --changed --scope=core`; got != want {
		t.Errorf("CommandLine = %q, want %q", got, want)
	}
}

func TestArgParam(t *testing.T) {
	tests := []struct {
		use  string
		want tui.Param
		ok   bool
	}{
		{use: "list"},
		{use: "files <pattern>", want: tui.Param{Name: "pattern", Arg: true, Required: true}, ok: true},
		{use: "get [pattern...]", want: tui.Param{Name: "pattern", Arg: true, Variadic: true}, ok: true},
	}
	for _, tc := range tests {
		got, ok := argParam(tc.use)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("argParam(%q) = %+v, %v; want %+v, %v", tc.use, got, ok, tc.want, tc.ok)
		}
	}
}

func TestApproversLabel(t *testing.T) {
	tests := []struct {
		in   rulesfile.ApproverTargets
		want string
	}{
		{nil, ""},
		{rulesfile.ApproverTargets{{Target: "org/team", RequiredCount: 2}}, "org/team (2)"},
		{rulesfile.ApproverTargets{{Target: "a", RequiredCount: 1}, {Target: "b", RequiredCount: 3}}, "a (1), b (3)"},
	}
	for _, tc := range tests {
		if got := approversLabel(tc.in); got != tc.want {
			t.Errorf("approversLabel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestProvenance checks the per-file drill-down: one section per layer of
// the file's chain, naming the declaring rules file and each rule in it.
func TestProvenance(t *testing.T) {
	layers := []approval.RuleLayer{
		{DirectoryPath: "svc", Rules: []approval.ExecutableRule{{Rule: rulesfile.Rule{
			Pattern: `.*\.go`, Name: "backend",
			Approvers: rulesfile.ApproverTargets{{Target: "org/backend", RequiredCount: 2}},
		}}}},
		{Rules: []approval.ExecutableRule{{Rule: rulesfile.Rule{Pattern: `.*`}}}},
	}
	d := provenance("svc/a.go", layers)
	if len(d.Sections) != 2 {
		t.Fatalf("want one section per layer, got %+v", d.Sections)
	}
	if d.Sections[0].Title != rulesfile.PathInDir("svc") || d.Sections[1].Title != rulesfile.FileName {
		t.Errorf("sections must name the declaring rules files: %q, %q", d.Sections[0].Title, d.Sections[1].Title)
	}
	line := d.Sections[0].Lines[0]
	for _, want := range []string{"backend", `.*\.go`, "org/backend (2)"} {
		if !strings.Contains(line, want) {
			t.Errorf("rule line %q missing %q", line, want)
		}
	}
}

// TestRuleActions exercises the TUI's add, edit, and remove against real
// rules files, through the same rulesedit.Workspace the CLI commands use. Each row
// carries its own rules file, so two files declaring the same pattern stay
// distinct.
func TestRuleActions(t *testing.T) {
	s := newTestWorkspace(t, map[string]rulesfile.Rules{
		rulesfile.FileName:         {{Pattern: `.*\.go`, Name: "security"}},
		rulesfile.PathInDir("svc"): {},
	})

	if _, err := addRule(s, rulesfile.PathInDir("svc"), rulesfile.Rule{Pattern: `.*\.go`, Name: "core", CombineStrategy: rulesfile.RuleCombineStrategyUnion}); err != nil {
		t.Fatalf("add: %v", err)
	}
	reloadRules(t, s)
	target := s.Entries(rulesfile.PathInDir("svc"))[0]
	if _, err := editRule(s, target, rulesfile.Rule{Pattern: "ignored", Name: "renamed", CombineStrategy: rulesfile.RuleCombineStrategyUnion}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := loadRules(t, s, rulesfile.PathInDir("svc")); len(got) != 1 || got[0].Pattern != `.*\.go` || got[0].Name != "renamed" {
		t.Fatalf("edit must rename the rule and keep its pattern: %+v", got)
	}
	if got := loadRules(t, s, rulesfile.FileName); len(got) != 1 || got[0].Name != "security" {
		t.Fatalf("the other rules file's rule with the same pattern must be untouched: %+v", got)
	}

	remove := ruleActions(s)[2]
	if _, err := remove.Run(context.Background(), tui.Item{Title: "bogus", Value: "not a rule"}); err == nil {
		t.Error("remove must reject a row that does not hold a rule")
	}
	reloadRules(t, s)
	out, err := remove.Run(context.Background(), tui.Item{Value: s.Entries(rulesfile.PathInDir("svc"))[0]})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !out.Reload {
		t.Error("remove must reload the page")
	}
	if got := loadRules(t, s, rulesfile.PathInDir("svc")); len(got) != 0 {
		t.Errorf("rule must be gone, got %+v", got)
	}
	if got := loadRules(t, s, rulesfile.FileName); len(got) != 1 {
		t.Errorf("remove must only touch the row's rules file: %+v", got)
	}
}

func TestRulesListOutput(t *testing.T) {
	entries := rulesfile.Rules{
		{RulesFile: rulesfile.FileName, Pattern: `.*\.go`, Name: "security", CombineStrategy: rulesfile.RuleCombineStrategyUnion,
			Approvers: rulesfile.ApproverTargets{{Target: "org/sec", RequiredCount: 1}}},
		{RulesFile: rulesfile.PathInDir("svc"), Pattern: `.*`},
	}
	js := rulesJSON(entries)
	if js[0].CombineStrategy != rulesfile.RuleCombineStrategyUnion || js[0].Approvers[0] != (rulesfile.ApproverTarget{Target: "org/sec", RequiredCount: 1}) {
		t.Errorf("first JSON rule = %+v", js[0])
	}
	if out, err := json.Marshal(js); err != nil || !strings.Contains(string(out), `"rules_file":"`+rulesfile.FileName+`","position":0,"pattern":".*\\.go"`) {
		t.Errorf("rules JSON = %s, %v; want each rule's location and fields", out, err)
	}
	if js[1].CombineStrategy != 0 || js[1].Approvers == nil {
		t.Errorf("an omitted strategy is omitted and approvers is never null: %+v", js[1])
	}
	if got := rulesJSON(nil); got == nil || len(got) != 0 {
		t.Errorf("no rules must encode as [], got %#v", got)
	}
	rows := ruleRows(entries)
	if want := []string{rulesfile.PathInDir("svc"), "0", ".*", "", "(default)", ""}; !reflect.DeepEqual(rows[1], want) {
		t.Errorf("row = %q, want %q", rows[1], want)
	}

	var buf strings.Builder
	for _, tc := range []struct {
		only  string
		total int
	}{{"", 2}, {rulesfile.FileName, 1}, {rulesfile.PathInDir("svc"), 2}} {
		if err := narrowedNotice(&buf, tc.only, tc.total); err != nil {
			t.Fatalf("narrowedNotice: %v", err)
		}
	}
	if got := buf.String(); got != "showing "+rulesfile.PathInDir("svc")+", 1 of 2 rules files; omit --file to list all\n" {
		t.Errorf("notice = %q", got)
	}
}

// TestRulesGetRouteAsksForPattern pins that `rules get` and `rules files`
// opened from the TUI ask for their regex first, while the CLI argument
// stays optional.
func TestRulesGetRouteAsksForPattern(t *testing.T) {
	for _, r := range []tui.Route{rulesGetRoute(tui.Values{}), rulesFilesRoute(tui.Values{})} {
		var arg tui.Param
		for _, p := range r.Params {
			if p.Arg {
				arg = p
			}
		}
		if !arg.Required {
			t.Errorf("%s: pattern param must be required in the TUI: %+v", r.Command, arg)
		}
	}
	for _, cmd := range []*cobra.Command{rulesGetCmd, rulesFilesCmd} {
		if err := cmd.ValidateArgs(nil); err != nil {
			t.Errorf("%s: the CLI pattern argument must stay optional: %v", cmd.Name(), err)
		}
	}
}
