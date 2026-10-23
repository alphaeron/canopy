package rulesedit

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

var fixtureRules = rulesfile.Rules{
	{
		Pattern:         ".*\\.go$",
		Name:            "backend review",
		CombineStrategy: rulesfile.RuleCombineStrategyUnion,
		SelfApprove:     true,
		Approvers: []rulesfile.ApproverTarget{
			{Target: "org/backend-team", RequiredCount: 2},
		},
	},
	{
		Pattern:         ".*\\.tf$",
		Name:            "infra review",
		CombineStrategy: rulesfile.RuleCombineStrategyIntersect,
		Approvers: []rulesfile.ApproverTarget{
			{Target: "org/cloud-team", RequiredCount: 1},
		},
	},
}

// writeFixture saves rules to a rules file in dir and reloads it through
// Load.
func writeFixture(t *testing.T, dir string, rules rulesfile.Rules) rulesfile.Rules {
	t.Helper()
	path := filepath.Join(dir, rulesfile.FileName)
	if err := Save(path, rules); err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return loaded
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	got := writeFixture(t, t.TempDir(), fixtureRules)
	if len(got) != len(fixtureRules) {
		t.Fatalf("rule count = %d, want %d", len(got), len(fixtureRules))
	}
	for i, want := range fixtureRules {
		if got[i].Pattern != want.Pattern || got[i].Name != want.Name {
			t.Errorf("rule %d = (%q, %q), want (%q, %q)",
				i, got[i].Pattern, got[i].Name, want.Pattern, want.Name)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected wrap of os.ErrNotExist, got %v", err)
	}
}

func TestCreate(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("svc", 0o755); err != nil {
		t.Fatal(err)
	}
	if got := PathFor(""); got != rulesfile.FileName {
		t.Errorf("PathFor(\"\") = %q", got)
	}
	path := PathFor("svc")
	if path != filepath.Join("svc", rulesfile.FileName) {
		t.Fatalf("PathFor(dir) = %q", path)
	}
	if got := PathFor("x.yaml"); got != "x.yaml" {
		t.Errorf("PathFor(file) = %q", got)
	}

	k, err := Create(path, false, rulesfile.Rule{Pattern: `.*`, Name: "svc", Approvers: rulesfile.ApproverTargets{{Target: "lead", RequiredCount: 1}}})
	if err != nil || len(k) != 1 || k[0].Approvers[0].Target != "lead" {
		t.Fatalf("Create = %+v, %v; want the file with its rule", k, err)
	}
	if got, err := Load(path); err != nil || !reflect.DeepEqual(got, k) {
		t.Fatalf("Create must write the file: %+v, %v", got, err)
	}
	if _, err := Create(path, false); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Create over an existing file error = %v, want fs.ErrExist", err)
	}
	if k, err := Create(path, true); err != nil || len(k) != 0 {
		t.Errorf("Create with overwrite and no rules = %+v, %v; want an empty file", k, err)
	}
}

// TestOmittedStrategyRoundTrips guards against saving a rule loaded without
// combine_strategy as "combine_strategy: unknown", which then fails to
// parse and makes the whole rules file unreadable (e.g. after `rules add`
// to a file holding such a rule).
func TestOmittedStrategyRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), rulesfile.FileName)
	if err := os.WriteFile(path, []byte("- '.*':\n    name: legacy\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rules, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if rules, err = add(rules, rulesfile.Rule{Pattern: "new", CombineStrategy: rulesfile.RuleCombineStrategyUnion}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := Save(path, rules); err != nil {
		t.Fatalf("save: %v", err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatalf("a saved file must load again: %v", err)
	}
	if len(again) != 2 || again[0].CombineStrategy != 0 || again[1].CombineStrategy != rulesfile.RuleCombineStrategyUnion {
		t.Errorf("rules after round trip = %+v", again)
	}
}
