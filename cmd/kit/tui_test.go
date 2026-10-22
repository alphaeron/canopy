package main

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
	"github.com/alphaeron/kit/internal/tui"
)

// TestRouteFromCommand pins that a command's TUI route is derived from its
// cobra definition (so the options form matches the CLI) and that a CLI
// invocation becomes the same route values.
func TestRouteFromCommand(t *testing.T) {
	root := &cobra.Command{Use: "kit"}
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
	if got, want := r.CommandLine(), `kit files '.*\.go' --changed --scope=core`; got != want {
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
		in   kitfile.ApproverTargets
		want string
	}{
		{nil, ""},
		{kitfile.ApproverTargets{{Target: "org/team", RequiredCount: 2}}, "org/team (2)"},
		{kitfile.ApproverTargets{{Target: "a", RequiredCount: 1}, {Target: "b", RequiredCount: 3}}, "a (1), b (3)"},
	}
	for _, tc := range tests {
		if got := approversLabel(tc.in); got != tc.want {
			t.Errorf("approversLabel(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExactPath pins that drilling from a file into `rules get` targets
// that file only, even when the path contains regex metacharacters.
func TestExactPath(t *testing.T) {
	re := regexp.MustCompile(exactPath("pkg/a.go"))
	for path, want := range map[string]bool{
		"pkg/a.go":     true,
		"pkg/aXgo":     false,
		"pkg/a.go.bak": false,
		"x/pkg/a.go":   false,
	} {
		if got := re.MatchString(path); got != want {
			t.Errorf("match %q = %v, want %v", path, got, want)
		}
	}
}

// TestProvenance checks the per-file drill-down: one section per layer of
// the file's chain, naming the declaring KIT file and each rule in it.
func TestProvenance(t *testing.T) {
	layers := []shonin.RuleLayer{
		{DirectoryPath: "svc", Rules: []shonin.Rule{{Rule: kitfile.Rule{
			Pattern: `.*\.go`, Name: "backend",
			Approvers: kitfile.ApproverTargets{{Target: "org/backend", RequiredCount: 2}},
		}}}},
		{Rules: []shonin.Rule{{Rule: kitfile.Rule{Pattern: `.*`}}}},
	}
	d := provenance("svc/a.go", layers)
	if len(d.Sections) != 2 {
		t.Fatalf("want one section per layer, got %+v", d.Sections)
	}
	if d.Sections[0].Title != "svc/KIT" || d.Sections[1].Title != "KIT" {
		t.Errorf("sections must name the declaring KIT files: %q, %q", d.Sections[0].Title, d.Sections[1].Title)
	}
	line := d.Sections[0].Lines[0]
	for _, want := range []string{"backend", `.*\.go`, "org/backend (2)"} {
		if !strings.Contains(line, want) {
			t.Errorf("rule line %q missing %q", line, want)
		}
	}
}

// TestFieldsFromRuleRoundTrip guards the edit seed (form and `rules edit`):
// a rule turned into fields and back is unchanged, including the pattern
// the form requires. An omitted strategy, which shonin evaluates as
// override, comes back as an explicit override rather than an error.
func TestFieldsFromRuleRoundTrip(t *testing.T) {
	base := kitfile.Rule{
		Pattern: `.*\.go`, Name: "core", SelfApprove: true, CurrentDirectoryOnly: true,
		Approvers: kitfile.ApproverTargets{{Target: "org/core", RequiredCount: 2}},
	}
	tests := []struct {
		name     string
		strategy kitfile.RuleCombineStrategy
		want     kitfile.RuleCombineStrategy
	}{
		{name: "union", strategy: kitfile.RuleCombineStrategyUnion, want: kitfile.RuleCombineStrategyUnion},
		{name: "intersect", strategy: kitfile.RuleCombineStrategyIntersect, want: kitfile.RuleCombineStrategyIntersect},
		{name: "omitted", want: kitfile.RuleCombineStrategyOverride},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := base
			rule.CombineStrategy = tc.strategy
			got, err := buildRuleFromFields(fieldsFromRule(rule))
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			want := base
			want.CombineStrategy = tc.want
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip:\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

// TestRuleActions exercises the TUI's add, edit, and remove against real
// KIT files, through the same kitSet.mutate the CLI commands use. Each row
// carries its own KIT file, so two files declaring the same pattern stay
// distinct.
func TestRuleActions(t *testing.T) {
	s := newTestKitSet(t, map[string]kitfile.Rules{
		"KIT":     {{Pattern: `.*\.go`, Name: "security"}},
		"svc/KIT": {},
	})

	if _, err := addRule(s, "svc/KIT", tui.RuleFields{Pattern: `.*\.go`, Name: "core", Strategy: "union"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := editRule(s, "svc/KIT", `.*\.go`, tui.RuleFields{Pattern: "ignored", Name: "renamed", Strategy: "union"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := loadRules(t, s, "svc/KIT"); len(got) != 1 || got[0].Pattern != `.*\.go` || got[0].Name != "renamed" {
		t.Fatalf("edit must rename the rule and keep its pattern: %+v", got)
	}
	if got := loadRules(t, s, "KIT"); len(got) != 1 || got[0].Name != "security" {
		t.Fatalf("the other KIT file's rule with the same pattern must be untouched: %+v", got)
	}

	remove := ruleActions(s)[2]
	if _, err := remove.Run(context.Background(), tui.Item{Title: "bogus", Value: "not a rule"}); err == nil {
		t.Error("remove must reject a row that does not hold a rule")
	}
	out, err := remove.Run(context.Background(), tui.Item{Value: ruleEntry{kitFile: "svc/KIT", rule: kitfile.Rule{Pattern: `.*\.go`}}})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !out.Reload {
		t.Error("remove must reload the page")
	}
	if got := loadRules(t, s, "svc/KIT"); len(got) != 0 {
		t.Errorf("rule must be gone, got %+v", got)
	}
	if got := loadRules(t, s, "KIT"); len(got) != 1 {
		t.Errorf("remove must only touch the row's KIT file: %+v", got)
	}
}

func TestRulesListOutput(t *testing.T) {
	entries := []ruleEntry{
		{kitFile: "KIT", rule: kitfile.Rule{Pattern: `.*\.go`, Name: "security", CombineStrategy: kitfile.RuleCombineStrategyUnion,
			Approvers: kitfile.ApproverTargets{{Target: "org/sec", RequiredCount: 1}}}},
		{kitFile: "svc/KIT", rule: kitfile.Rule{Pattern: `.*`}},
	}
	js := rulesJSON(entries)
	if js[0].KitFile != "KIT" || js[0].CombineStrategy != "union" || js[0].Approvers[0] != (approverJSON{"org/sec", 1}) {
		t.Errorf("first JSON rule = %+v", js[0])
	}
	if js[1].CombineStrategy != "" || js[1].Approvers == nil {
		t.Errorf("an omitted strategy is omitted and approvers is never null: %+v", js[1])
	}
	if got := rulesJSON(nil); got == nil || len(got) != 0 {
		t.Errorf("no rules must encode as [], got %#v", got)
	}
	rows := ruleRows(entries)
	if want := []string{"svc/KIT", ".*", "", "(default)", ""}; !reflect.DeepEqual(rows[1], want) {
		t.Errorf("row = %q, want %q", rows[1], want)
	}

	var buf strings.Builder
	for _, tc := range []struct {
		only  string
		total int
	}{{"", 2}, {"KIT", 1}, {"svc/KIT", 2}} {
		if err := narrowedNotice(&buf, tc.only, tc.total); err != nil {
			t.Fatalf("narrowedNotice: %v", err)
		}
	}
	if got := buf.String(); got != "showing svc/KIT, 1 of 2 KIT files; omit --file to list all\n" {
		t.Errorf("notice = %q", got)
	}
}
