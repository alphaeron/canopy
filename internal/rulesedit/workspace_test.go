package rulesedit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// newTestWorkspace writes rules as rules files under a temp root and returns
// the workspace over them.
func newTestWorkspace(t *testing.T, rules rulesfile.Set) *Workspace {
	t.Helper()
	w := &Workspace{Root: t.TempDir(), Rules: rules}
	for p, rs := range rules {
		disk := w.DiskPath(p)
		if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := Save(disk, rs); err != nil {
			t.Fatalf("save %s: %v", p, err)
		}
	}
	return w
}

func loadTestRules(t *testing.T, w *Workspace, p string) rulesfile.Rules {
	t.Helper()
	k, err := Load(w.DiskPath(p))
	if err != nil {
		t.Fatalf("load %s: %v", p, err)
	}
	return k
}

var threeRulesFiles = rulesfile.Set{
	rulesfile.PathInDir("cmd/app"): {{Pattern: `.*\.go`, Name: "Core Backend Review"}},
	rulesfile.FileName:             {{Pattern: `.*\.go`, Name: "Security Review"}, {Pattern: `docs/.*`, Name: "Docs"}},
	rulesfile.PathInDir("a"):       {},
}

func TestWorkspaceEntriesAndPatterns(t *testing.T) {
	w := &Workspace{Rules: threeRulesFiles}
	var files []string
	for _, e := range w.Entries("") {
		files = append(files, e.RulesFile+" "+e.Pattern)
	}
	if want := []string{rulesfile.FileName + " .*\\.go", rulesfile.FileName + " docs/.*", rulesfile.PathInDir("cmd/app") + " .*\\.go"}; !reflect.DeepEqual(files, want) {
		t.Errorf("entries = %v, want %v", files, want)
	}
	if got := w.Entries(rulesfile.PathInDir("cmd/app")); len(got) != 1 || got[0].Name != "Core Backend Review" {
		t.Errorf("entries(only) = %+v", got)
	}
	if got, want := w.Patterns(), []string{`.*\.go`, `docs/.*`}; !reflect.DeepEqual(got, want) {
		t.Errorf("patterns = %v, want %v", got, want)
	}
}

// TestWorkspaceResolve pins that paths are from the repository root, a
// directory means its rules file, and anything else is an error naming the
// rules files that exist.
func TestWorkspaceResolve(t *testing.T) {
	w := &Workspace{Root: t.TempDir(), Rules: threeRulesFiles}
	tests := []struct {
		path, want, wantErr string
	}{
		{path: "", want: ""},
		{path: rulesfile.FileName, want: rulesfile.FileName},
		{path: ".", want: rulesfile.FileName},
		{path: "cmd/app", want: rulesfile.PathInDir("cmd/app")},
		{path: "./cmd/app/", want: rulesfile.PathInDir("cmd/app")},
		{path: rulesfile.PathInDir("cmd/app"), want: rulesfile.PathInDir("cmd/app")},
		{path: filepath.Join(w.Root, "cmd", "app"), want: rulesfile.PathInDir("cmd/app")},
		{path: "svc", wantErr: fmt.Sprintf("no rules file at %q (rules files: %s, %s, %s)", rulesfile.PathInDir("svc"), rulesfile.FileName, rulesfile.PathInDir("a"), rulesfile.PathInDir("cmd/app"))},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got, err := w.Resolve(tc.path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Resolve(%q) error = %v, want %q", tc.path, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("Resolve(%q) = %q, %v; want %q", tc.path, got, err, tc.want)
			}
		})
	}
	for _, p := range []string{"../elsewhere", filepath.Dir(w.Root)} {
		if _, err := w.Resolve(p); !errors.Is(err, ErrOutsideRepository) {
			t.Errorf("Resolve(%q) error = %v, want ErrOutsideRepository", p, err)
		}
	}
	if _, err := (&Workspace{Root: w.Root}).Resolve(rulesfile.FileName); !errors.Is(err, ErrNoRulesFiles) {
		t.Errorf("an empty workspace must report ErrNoRulesFiles, got %v", err)
	}
}

// TestRelInside pins that a path reached through a symlink still counts as
// inside the root, including a path that does not exist yet (EvalSymlinks
// fails on it, so it is first compared as given).
func TestRelInside(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(real, "cmd", "app"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tests := []struct {
		name, root, p, want string
		wantOK              bool
	}{
		{name: "same spelling", root: link, p: filepath.Join(link, "cmd", "app"), want: "cmd/app", wantOK: true},
		{name: "symlinked root, real path", root: link, p: filepath.Join(real, "cmd", "app"), want: "cmd/app", wantOK: true},
		{name: "real root, symlinked path", root: real, p: filepath.Join(link, "cmd", "app"), want: "cmd/app", wantOK: true},
		{name: "missing path", root: link, p: filepath.Join(link, "svc", "new"), want: "svc/new", wantOK: true},
		{name: "root itself", root: link, p: real, want: ".", wantOK: true},
		{name: "outside", root: real, p: filepath.Dir(real), wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := relInside(tc.root, tc.p)
			if ok != tc.wantOK || (ok && filepath.ToSlash(got) != tc.want) {
				t.Errorf("relInside(%q, %q) = %q, %v; want %q, %v", tc.root, tc.p, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestWorkspaceLocate pins how edit and remove find a rule by pattern: one
// match is the rule, with its location; several are an AmbiguousRuleError
// listing every location, which --file and --position narrow.
func TestWorkspaceLocate(t *testing.T) {
	rules := rulesfile.Set{
		rulesfile.FileName:             {{Pattern: `.*\.go`, Name: "base"}, {Pattern: `docs/.*`}, {Pattern: `.*\.go`, Name: "override"}},
		rulesfile.PathInDir("cmd/app"): {{Pattern: `.*\.go`, Name: "core"}},
	}
	w := &Workspace{Rules: rules}
	if got, err := w.Locate(`docs/.*`, "", -1); err != nil || got.Location() != rulesfile.FileName+"[1]" {
		t.Errorf("Locate(unique) = %v, %v; want %s[1]", got, err, rulesfile.FileName)
	}
	if _, err := w.Locate("nope", "", -1); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("Locate(missing) error = %v, want ErrRuleNotFound", err)
	}
	_, err := w.Locate(`.*\.go`, "", -1)
	ambiguous, ok := errors.AsType[*AmbiguousRuleError](err)
	if !ok || len(ambiguous.Rules) != 3 {
		t.Fatalf("Locate(ambiguous) error = %v, want an AmbiguousRuleError over all three rules", err)
	}
	if want := fmt.Sprintf(`pattern ".*\\.go" matches 3 rules: %s[0] (base), %s[2] (override), %s[0] (core)`, rulesfile.FileName, rulesfile.FileName, rulesfile.PathInDir("cmd/app")); err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if _, err := w.Locate(`.*\.go`, rulesfile.FileName, -1); !errors.As(err, &ambiguous) || len(ambiguous.Rules) != 2 {
		t.Errorf("Locate in one rules file = %v, want its two rules", err)
	}
	if got, err := w.Locate(`.*\.go`, rulesfile.FileName, 2); err != nil || got.Name != "override" {
		t.Errorf("Locate at a position = %v, %v; want the override", got, err)
	}
	if _, err := w.Locate(`.*\.go`, rulesfile.FileName, 1); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("Locate at a position with another pattern error = %v, want ErrRuleNotFound", err)
	}
}

// TestWorkspaceNearest checks the rules file in a directory or its nearest
// ancestor, falling back to the first rules file.
func TestWorkspaceNearest(t *testing.T) {
	w := &Workspace{Root: t.TempDir(), Rules: threeRulesFiles}
	tests := map[string]string{
		"":            rulesfile.FileName,
		"cmd/app":     rulesfile.PathInDir("cmd/app"),
		"cmd/app/sub": rulesfile.PathInDir("cmd/app"),
		"docs":        rulesfile.FileName,
	}
	for dir, want := range tests {
		if got := w.Nearest(filepath.Join(w.Root, dir)); got != want {
			t.Errorf("Nearest(%q) = %q, want %q", dir, got, want)
		}
	}
	if got := w.Nearest(filepath.Dir(w.Root)); got != rulesfile.FileName {
		t.Errorf("Nearest(outside) = %q, want the first rules file", got)
	}
	if got := (&Workspace{Rules: rulesfile.Set{rulesfile.PathInDir("x"): {}}}).NearestTo("y"); got != "" {
		t.Errorf("NearestTo with no governing rules file = %q, want empty", got)
	}
}

func TestWorkspaceMutate(t *testing.T) {
	w := newTestWorkspace(t, rulesfile.Set{rulesfile.FileName: {{Pattern: "a"}}})
	if _, err := w.mutate(rulesfile.FileName, func(rules rulesfile.Rules) (rulesfile.Rules, error) {
		return add(rules, rulesfile.Rule{Pattern: "b"})
	}); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if got := loadTestRules(t, w, rulesfile.FileName); len(got) != 2 {
		t.Errorf("mutate must save the change: %+v", got)
	}
	boom := errors.New("boom")
	if _, err := w.mutate(rulesfile.FileName, func(rulesfile.Rules) (rulesfile.Rules, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("mutate must return the change's error, got %v", err)
	}
	if got := loadTestRules(t, w, rulesfile.FileName); len(got) != 2 {
		t.Errorf("a failed change must not be saved: %+v", got)
	}
}

// TestWorkspaceAddEditRemove pins that each change touches only its own
// rules file and its own rule: a repeated pattern is a separate rule, Edit
// keeps the rule's pattern, and a stale location is refused.
func TestWorkspaceAddEditRemove(t *testing.T) {
	w := newTestWorkspace(t, rulesfile.Set{rulesfile.FileName: {{Pattern: `.*\.go`, Name: "security"}}, rulesfile.PathInDir("svc"): {}})
	svc := rulesfile.PathInDir("svc")
	if _, err := w.Add(svc, rulesfile.Rule{Pattern: `.*\.go`, Name: "core"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	added, err := w.Add(svc, rulesfile.Rule{Pattern: `.*\.go`, Name: "override", CombineStrategy: rulesfile.RuleCombineStrategyOverride})
	if err != nil || len(added) != 2 || added[1].Location() != svc+"[1]" {
		t.Fatalf("Add of a repeated pattern = %+v, %v; want it appended at position 1", added, err)
	}
	rule, err := w.Edit(added[1], rulesfile.Rule{Pattern: "ignored", Name: "renamed"})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	got := loadTestRules(t, w, svc)
	if len(got) != 2 || got[0].Name != "core" || got[1].Pattern != `.*\.go` || got[1].Name != "renamed" || rule.Location() != svc+"[1]" {
		t.Fatalf("Edit must rename only the second rule and keep its pattern: %+v, %+v", got, rule)
	}
	if err := w.Remove(added[0]); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := loadTestRules(t, w, svc); len(got) != 1 || got[0].Name != "renamed" {
		t.Errorf("only the first rule must be gone, got %+v", got)
	}
	if got := loadTestRules(t, w, rulesfile.FileName); len(got) != 1 || got[0].Name != "security" {
		t.Errorf("the other rules file's rule with the same pattern must be untouched: %+v", got)
	}
	if err := w.Remove(added[1]); !errors.Is(err, ErrRulesFileChanged) {
		t.Errorf("Remove at a stale position error = %v, want ErrRulesFileChanged", err)
	}
}

func TestWorkspaceParentAndValidate(t *testing.T) {
	w := &Workspace{Rules: rulesfile.Set{
		rulesfile.FileName:              {{Pattern: "("}},
		rulesfile.PathInDir("svc"):      {{Pattern: "a"}},
		rulesfile.PathInDir("svc/x/y"):  {{Pattern: "a"}, {Pattern: "["}},
		rulesfile.PathInDir("lonely/z"): {},
	}}
	for file, want := range map[string]string{
		rulesfile.FileName:              "",
		rulesfile.PathInDir("svc"):      rulesfile.FileName,
		rulesfile.PathInDir("svc/x/y"):  rulesfile.PathInDir("svc"),
		rulesfile.PathInDir("lonely/z"): rulesfile.FileName,
	} {
		if got := w.Parent(file); got != want {
			t.Errorf("Parent(%q) = %q, want %q", file, got, want)
		}
	}
	all := w.Validate("")
	if len(all) != 2 || len(all[rulesfile.FileName]) != 1 || len(all[rulesfile.PathInDir("svc/x/y")]) != 1 {
		t.Errorf("Validate(\"\") = %v, want one violation in each of two files", all)
	}
	if got := w.Validate(rulesfile.PathInDir("svc/x/y")); len(got) != 1 || len(got[rulesfile.PathInDir("svc/x/y")]) != 1 {
		t.Errorf("Validate(only) = %v, want only that file's violation", got)
	}
}

// TestWorkspaceBroken pins that a rules file that could not be loaded is
// still a rules file of the workspace: listed, resolvable, and reported by
// Validate as its one violation, while lookups answer from the loaded rules.
func TestWorkspaceBroken(t *testing.T) {
	brokenPath := rulesfile.PathInDir("svc")
	broken := errors.New("parse rules file " + brokenPath + ": yaml: bad")
	w := &Workspace{Root: t.TempDir(), Rules: threeRulesFiles, Broken: map[string]error{brokenPath: broken}}

	if got, want := w.Paths(), []string{rulesfile.FileName, rulesfile.PathInDir("a"), rulesfile.PathInDir("cmd/app"), rulesfile.PathInDir("svc")}; !reflect.DeepEqual(got, want) {
		t.Errorf("Paths = %v, want %v", got, want)
	}
	tests := []struct {
		pattern string
		want    []string
	}{
		{"", []string{rulesfile.FileName, rulesfile.PathInDir("a"), rulesfile.PathInDir("cmd/app"), rulesfile.PathInDir("svc")}},
		{"svc|cmd", []string{rulesfile.PathInDir("cmd/app"), rulesfile.PathInDir("svc")}},
		{"nothing", []string{}},
	}
	for _, tc := range tests {
		if got, err := w.PathsMatching(tc.pattern); err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("PathsMatching(%q) = %q, %v; want %q", tc.pattern, got, err, tc.want)
		}
	}
	if _, err := w.PathsMatching("("); err == nil {
		t.Error("an invalid pattern must be an error")
	}

	if got, err := w.Resolve("svc"); err != nil || got != brokenPath {
		t.Errorf("Resolve(broken) = %q, %v; want %q", got, err, brokenPath)
	}
	all := w.Validate("")
	if errs := all[brokenPath]; len(errs) != 1 || errs[0] != broken {
		t.Errorf("Validate(\"\")[broken] = %v, want its FileError", errs)
	}
	if got := w.Validate(brokenPath); len(got) != 1 || len(got[brokenPath]) != 1 {
		t.Errorf("Validate(broken) = %v, want only its FileError", got)
	}
	if got := w.Validate(rulesfile.FileName); len(got[brokenPath]) != 0 {
		t.Errorf("Validate(other) must leave the broken file out: %v", got)
	}
}
