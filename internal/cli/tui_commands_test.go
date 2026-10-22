package cli

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// TestRoutesMirrorCommandTree is the parity contract: every command `kit
// --help` lists, at any depth, is a TUI route whose options are exactly the
// command's argument and visible flags. Shell completion is CLI only.
func TestRoutesMirrorCommandTree(t *testing.T) {
	// Execute adds completion when it runs; add it so the TUI must skip it.
	rootCmd.InitDefaultCompletionCmd()
	routes := map[string]tui.Route{}
	for _, r := range appRoutes() {
		if _, dup := routes[r.Command]; dup {
			t.Errorf("route %q listed twice", r.Command)
		}
		if r.Command == "completion" || strings.HasPrefix(r.Command, "completion ") {
			t.Errorf("route %q: shell completion is CLI only", r.Command)
		}
		routes[r.Command] = r
	}
	commands := 0
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range visibleCommands(c) {
			if isCompletion(sub) {
				continue
			}
			commands++
			walk(sub)
			// cobra adds --help to a command it executes; it must not
			// become an option.
			sub.InitDefaultHelpFlag()
			name := commandName(sub)
			r, ok := routes[name]
			if !ok {
				t.Errorf("kit %s has no TUI route", name)
				continue
			}
			if (r.Load == nil) == (r.Run == nil) {
				t.Errorf("kit %s: a route must either show a page or run", name)
			}
			want := map[string]bool{}
			if a, ok := argParam(sub.Use); ok {
				want[a.Name] = true
			}
			sub.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
				if !f.Hidden && f.Deprecated == "" && f.Name != "help" {
					want[f.Name] = true
				}
			})
			got := map[string]bool{}
			for _, p := range r.Params {
				got[p.Name] = true
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("kit %s options = %v, want its argument and flags %v", name, slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
		}
	}
	walk(rootCmd)
	if commands != len(routes) {
		t.Errorf("%d routes for %d commands", len(routes), commands)
	}
	// Pin the tree too, so a change to how commands are listed cannot
	// empty both sides of the comparison.
	for _, name := range []string{
		"coverage", "help", "init", "match", "rules", "rules add", "rules edit", "rules files",
		"review", "rules get", "rules list", "rules remove", "validate",
	} {
		if _, ok := routes[name]; !ok {
			t.Errorf("no route for kit %s", name)
		}
	}
}

// TestRuleParams pins how the rule commands' flags appear in the options
// form: rules edit offers its booleans as true, false, or unchanged and its
// pattern from the existing rules; add requires --pattern.
func TestRuleParams(t *testing.T) {
	byName := func(r tui.Route) map[string]tui.Param {
		m := map[string]tui.Param{}
		for _, p := range r.Params {
			m[p.Name] = p
		}
		return m
	}
	edit := byName(rulesEditRoute(tui.Values{}))
	if p := edit["self-approve"]; p.Kind != tui.ParamChoice || p.Unset != "unchanged" {
		t.Errorf("edit --self-approve = %+v, want a true/false/unchanged choice", p)
	}
	if p := edit["strategy"]; p.Kind != tui.ParamChoice || p.Unset != "unchanged" {
		t.Errorf("edit --strategy = %+v", p)
	}
	for _, name := range []string{"name", "approver"} {
		if p := edit[name]; p.Kind != tui.ParamText || p.Unset != "unchanged" {
			t.Errorf("edit --%s = %+v, want text that can be unchanged or set empty", name, p)
		}
	}
	if p := edit["pattern"]; !p.Arg || !p.Required || p.Kind != tui.ParamChoice {
		t.Errorf("edit <pattern> = %+v, want a required choice", p)
	}
	add := byName(rulesAddRoute(tui.Values{}))
	if p := add["pattern"]; !p.Required {
		t.Errorf("add --pattern must be required in the TUI: %+v", p)
	}
	if p := add["strategy"]; p.Kind != tui.ParamChoice || p.Unset != "union (default)" {
		t.Errorf("add --strategy = %+v", p)
	}
	if p := add["self-approve"]; p.Kind != tui.ParamBool {
		t.Errorf("add --self-approve = %+v, want a plain boolean", p)
	}
	if p := add["approver"]; !strings.Contains(p.Help, "comma-separated") {
		t.Errorf("add --approver help = %q", p.Help)
	}
	if p := add["file"]; p.Unset != "the repository-root KIT" {
		t.Errorf("add --file = %+v", p)
	}
}

// TestValuesFromRepeatedFlag: a repeatable flag round-trips through route
// values and the command line the TUI shows.
func TestValuesFromRepeatedFlag(t *testing.T) {
	cmd := &cobra.Command{Use: "add"}
	cmd.Flags().StringSlice("approver", nil, "")
	cmd.Flags().Bool("self-approve", false, "")
	(&cobra.Command{Use: "kit"}).AddCommand(cmd)
	if err := cmd.ParseFlags([]string{"--approver", "org/sec,1", "--approver", "lead,2", "--self-approve=false"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := valuesFrom(cmd, nil)
	want := map[string]string{"approver": "org/sec,1,lead,2", "self-approve": "false"}
	if !reflect.DeepEqual(v.Flags, want) {
		t.Errorf("values = %v, want %v", v.Flags, want)
	}
	got, err := splitList(v.Flag("approver"))
	if err != nil || !reflect.DeepEqual(got, []string{"org/sec", "1", "lead", "2"}) {
		t.Errorf("splitList = %q, %v", got, err)
	}
	if got, err := splitList(joinList([]string{"a,b", `c"d`})); err != nil || !reflect.DeepEqual(got, []string{"a,b", `c"d`}) {
		t.Errorf("quoted round trip = %q, %v", got, err)
	}
}

func TestRuleFieldsFrom(t *testing.T) {
	seed := ruleFields{Pattern: "p", Name: "core", Strategy: "override", SelfApprove: true, ApproverText: "org/sec\t1"}
	tests := []struct {
		name  string
		flags map[string]string
		want  ruleFields
	}{
		{"unset flags keep the seed", nil, seed},
		{"set flags win", map[string]string{"name": "x", "strategy": "union", "self-approve": "false", "ignore-parent": "true"},
			ruleFields{Pattern: "p", Name: "x", Strategy: "union", Ignore: true, ApproverText: "org/sec\t1"}},
		{"approvers are replaced", map[string]string{"approver": "lead,2"},
			func() ruleFields { f := seed; f.ApproverText = "lead\t2"; return f }()},
	}
	for _, tc := range tests {
		got, err := ruleFieldsFrom(seed, tui.Values{Flags: tc.flags})
		if err != nil || got != tc.want {
			t.Errorf("%s: got %+v, %v; want %+v", tc.name, got, err, tc.want)
		}
	}
	if _, err := ruleFieldsFrom(seed, tui.Values{Flags: map[string]string{"approver": "lead"}}); err == nil {
		t.Error("an odd approver list must be rejected")
	}
}

// TestRuleCommandsFromValues runs rules add, edit, and remove as the TUI
// does, with the CLI's semantics.
func TestRuleCommandsFromValues(t *testing.T) {
	s := newTestKitSet(t, map[string]kitfile.Rules{"KIT": {}, "svc/KIT": {}})
	flags := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	if _, err := addFromValues(s, tui.Values{Flags: flags("pattern", `.*\.go`, "name", "root", "approver", "org/sec,1")}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got := loadRules(t, s, "KIT"); len(got) != 1 || got[0].Name != "root" || len(got[0].Approvers) != 1 {
		t.Fatalf("add without --file must write the repository-root KIT: %+v", got)
	}
	if _, err := addFromValues(s, tui.Values{Flags: flags("pattern", `.*\.go`, "name", "svc", "self-approve", "true", "file", "svc")}); err != nil {
		t.Fatalf("add --file: %v", err)
	}
	s.rules["KIT"], s.rules["svc/KIT"] = loadRules(t, s, "KIT"), loadRules(t, s, "svc/KIT")

	if _, err := editFromValues(s, tui.Values{Args: []string{`.*\.go`}, Flags: flags("self-approve", "false")}); err == nil || !strings.Contains(err.Error(), "pass --file") {
		t.Errorf("an ambiguous edit must ask for --file, got %v", err)
	}
	if _, err := editFromValues(s, tui.Values{Args: []string{`.*\.go`}, Flags: flags("file", "svc/KIT", "self-approve", "false")}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if got := loadRules(t, s, "svc/KIT"); got[0].SelfApprove || got[0].Name != "svc" {
		t.Errorf("edit must set only the given flags: %+v", got[0])
	}
	if _, err := editFromValues(s, tui.Values{Args: []string{`.*\.go`}, Flags: flags("file", "svc/KIT", "name", "", "approver", "")}); err != nil {
		t.Fatalf("edit --name '' --approver '': %v", err)
	}
	if got := loadRules(t, s, "svc/KIT"); got[0].Name != "" || len(got[0].Approvers) != 0 || got[0].CombineStrategy.String() != "union" {
		t.Errorf("empty --name and --approver must clear only those fields: %+v", got[0])
	}
	if _, err := editFromValues(s, tui.Values{}); err == nil {
		t.Error("edit without a pattern must fail")
	}

	if _, err := removeFromValues(s, tui.Values{Args: []string{"nope"}}); !errors.Is(err, kitfile.ErrPatternNotFound) {
		t.Errorf("removing an unknown pattern = %v", err)
	}
	if _, err := removeFromValues(s, tui.Values{Args: []string{`.*\.go`}, Flags: flags("file", "KIT")}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := loadRules(t, s, "KIT"); len(got) != 0 {
		t.Errorf("remove left %+v", got)
	}

	r := rulesRemoveRoute(tui.Values{})
	if q := r.Confirm(tui.Values{Args: []string{"a"}}); q == "" {
		t.Error("remove must ask without --force")
	}
	if q := r.Confirm(tui.Values{Args: []string{"a"}, Flags: flags("force", "true")}); q != "" {
		t.Errorf("remove --force must not ask, got %q", q)
	}
}

func TestInitFromValues(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("svc", 0o755); err != nil {
		t.Fatal(err)
	}
	v := tui.Values{Args: []string{"svc"}, Flags: map[string]string{"pattern": `.*`, "name": "svc", "approver": "lead,1"}}
	if _, err := initFromValues(v); err != nil {
		t.Fatalf("init: %v", err)
	}
	k, err := kitfile.Load(filepath.Join("svc", "KIT"))
	if err != nil || len(k.Rules) != 1 || k.Rules[0].Name != "svc" || k.Rules[0].Approvers[0].Target != "lead" {
		t.Fatalf("init into a directory must write its KIT with the rule: %+v, %v", k, err)
	}
	if _, err := initFromValues(v); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("init over an existing KIT file must need --force, got %v", err)
	}
	v.Flags = map[string]string{"force": "true"}
	if msg, err := initFromValues(v); err != nil || !strings.Contains(msg, "empty KIT file") {
		t.Errorf("init --force = %q, %v", msg, err)
	}
}

func TestHelpRoutes(t *testing.T) {
	routes := map[string]tui.Route{}
	for _, r := range appRoutes() {
		routes[r.Command] = r
	}
	ctx := context.Background()
	help := routes["help"]
	for _, args := range [][]string{nil, {"rules"}, {"rules", "add"}} {
		if _, err := help.Load(ctx, tui.Values{Args: args}); err != nil {
			t.Errorf("help %q: %v", args, err)
		}
	}
	if _, err := help.Load(ctx, tui.Values{Args: []string{"bogus"}}); err == nil {
		t.Error("help for an unknown command must fail")
	}
	if _, err := routes["rules"].Load(ctx, tui.Values{}); err != nil {
		t.Errorf("rules: %v", err)
	}
}
