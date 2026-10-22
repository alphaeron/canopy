package cli

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

// newTestRulesSet writes rules as rules files under a temp root and returns the
// set over them.
func newTestRulesSet(t *testing.T, rules map[string]rulesfile.Rules) *rulesSet {
	t.Helper()
	s := &rulesSet{root: t.TempDir(), rules: rules}
	for p, rs := range rules {
		disk := s.diskPath(p)
		if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := (&rulesfile.File{Path: disk, Rules: rs}).Save(disk); err != nil {
			t.Fatalf("save %s: %v", p, err)
		}
	}
	return s
}

func loadRules(t *testing.T, s *rulesSet, p string) rulesfile.Rules {
	t.Helper()
	k, err := rulesfile.Load(s.diskPath(p))
	if err != nil {
		t.Fatalf("load %s: %v", p, err)
	}
	return k.Rules
}

var twoRulesFiles = map[string]rulesfile.Rules{
	rulesfile.PathInDir("cmd/app"): {{Pattern: `.*\.go`, Name: "Core Backend Review"}},
	rulesfile.FileName:             {{Pattern: `.*\.go`, Name: "Security Review"}, {Pattern: `docs/.*`, Name: "Docs"}},
	rulesfile.PathInDir("a"):       {},
}

func TestRulesSetPathsAndEntries(t *testing.T) {
	s := &rulesSet{rules: twoRulesFiles}
	if got, want := s.rules.Paths(), []string{rulesfile.FileName, rulesfile.PathInDir("a"), rulesfile.PathInDir("cmd/app")}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v (root first, then by path)", got, want)
	}
	var files []string
	for _, e := range s.entries("") {
		files = append(files, e.RulesFile+" "+e.Rule.Pattern)
	}
	if want := []string{rulesfile.FileName + " .*\\.go", rulesfile.FileName + " docs/.*", rulesfile.PathInDir("cmd/app") + " .*\\.go"}; !reflect.DeepEqual(files, want) {
		t.Errorf("entries = %v, want %v", files, want)
	}
	if got := s.entries(rulesfile.PathInDir("cmd/app")); len(got) != 1 || got[0].Rule.Name != "Core Backend Review" {
		t.Errorf("entries(only) = %+v", got)
	}
	if got, want := s.rules.Names(), []string{"Core Backend Review", "Docs", "Security Review"}; !reflect.DeepEqual(got, want) {
		t.Errorf("scopeNames = %v, want %v", got, want)
	}
}

// TestRulesSetResolve pins --file: paths are from the repository root, a
// directory means its rules file, and anything else is an error naming the
// rules files that exist.
func TestRulesSetResolve(t *testing.T) {
	s := &rulesSet{root: t.TempDir(), rules: twoRulesFiles}
	tests := []struct {
		flag, want, wantErr string
	}{
		{flag: "", want: ""},
		{flag: rulesfile.FileName, want: rulesfile.FileName},
		{flag: ".", want: rulesfile.FileName},
		{flag: "cmd/app", want: rulesfile.PathInDir("cmd/app")},
		{flag: "./cmd/app/", want: rulesfile.PathInDir("cmd/app")},
		{flag: rulesfile.PathInDir("cmd/app"), want: rulesfile.PathInDir("cmd/app")},
		{flag: filepath.Join(s.root, "cmd", "app"), want: rulesfile.PathInDir("cmd/app")},
		{flag: "../elsewhere", wantErr: "outside the repository"},
		{flag: "svc", wantErr: fmt.Sprintf("no rules file at %q (rules files: %s, %s, %s)", rulesfile.PathInDir("svc"), rulesfile.FileName, rulesfile.PathInDir("a"), rulesfile.PathInDir("cmd/app"))},
	}
	for _, tc := range tests {
		t.Run(tc.flag, func(t *testing.T) {
			got, err := s.resolve(tc.flag)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("resolve(%q) error = %v, want %q", tc.flag, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("resolve(%q) = %q, %v; want %q", tc.flag, got, err, tc.want)
			}
		})
	}
	if _, err := (&rulesSet{root: s.root}).resolve(rulesfile.FileName); !errors.Is(err, errNoRulesFiles) {
		t.Errorf("an empty set must report errNoRulesFiles, got %v", err)
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

// TestRulesSetRuleTarget pins how edit and remove find a rule's rules file
// without --file, including that a script never silently picks one of
// several.
func TestRulesSetRuleTarget(t *testing.T) {
	s := &rulesSet{root: t.TempDir(), rules: twoRulesFiles}
	tests := []struct {
		name, file, pattern, want, wantErr string
		wantIs                             error
	}{
		{name: "unique pattern", pattern: `docs/.*`, want: rulesfile.FileName},
		{name: "--file wins", file: "cmd/app", pattern: `.*\.go`, want: rulesfile.PathInDir("cmd/app")},
		{name: "ambiguous", pattern: `.*\.go`, wantErr: fmt.Sprintf(`pattern ".*\\.go" is in 2 rules files (%s, %s); pass --file`, rulesfile.FileName, rulesfile.PathInDir("cmd/app"))},
		{name: "missing", pattern: `nope`, wantIs: rulesfile.ErrPatternNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ruleTarget(tc.file, tc.pattern, false)
			switch {
			case tc.wantIs != nil:
				if !errors.Is(err, tc.wantIs) {
					t.Errorf("error = %v, want %v", err, tc.wantIs)
				}
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}
			case err != nil || got != tc.want:
				t.Errorf("ruleTarget = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestRulesSetNearest checks the add form's default rules file: the one in the
// current directory or its nearest ancestor.
func TestRulesSetNearest(t *testing.T) {
	s := &rulesSet{root: t.TempDir(), rules: twoRulesFiles}
	for _, dir := range []string{"cmd/app/sub", "docs"} {
		if err := os.MkdirAll(filepath.Join(s.root, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	tests := map[string]string{
		"":            rulesfile.FileName,
		"cmd/app":     rulesfile.PathInDir("cmd/app"),
		"cmd/app/sub": rulesfile.PathInDir("cmd/app"),
		"docs":        rulesfile.FileName,
	}
	for dir, want := range tests {
		t.Run(dir, func(t *testing.T) {
			t.Chdir(filepath.Join(s.root, dir))
			if got := s.nearest(); got != want {
				t.Errorf("nearest from %q = %q, want %q", dir, got, want)
			}
		})
	}
}

func TestRulesSetMutate(t *testing.T) {
	s := newTestRulesSet(t, map[string]rulesfile.Rules{rulesfile.FileName: {{Pattern: "a"}}})
	if _, err := s.mutate(rulesfile.FileName, func(k *rulesfile.File) error { return k.Add(rulesfile.Rule{Pattern: "b"}) }); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if got := loadRules(t, s, rulesfile.FileName); len(got) != 2 {
		t.Errorf("mutate must save the change: %+v", got)
	}
	boom := errors.New("boom")
	if _, err := s.mutate(rulesfile.FileName, func(*rulesfile.File) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("mutate must return the change's error, got %v", err)
	}
	if got := loadRules(t, s, rulesfile.FileName); len(got) != 2 {
		t.Errorf("a failed change must not be saved: %+v", got)
	}
}
