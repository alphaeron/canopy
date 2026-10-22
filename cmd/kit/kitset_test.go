package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
)

// newTestKitSet writes rules as KIT files under a temp root and returns the
// set over them.
func newTestKitSet(t *testing.T, rules map[string]kitfile.Rules) *kitSet {
	t.Helper()
	s := &kitSet{root: t.TempDir(), rules: rules}
	for p, rs := range rules {
		disk := s.diskPath(p)
		if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := (&kitfile.KitFile{Path: disk, Rules: rs}).Save(disk); err != nil {
			t.Fatalf("save %s: %v", p, err)
		}
	}
	return s
}

func loadRules(t *testing.T, s *kitSet, p string) kitfile.Rules {
	t.Helper()
	k, err := kitfile.LoadKitFile(s.diskPath(p))
	if err != nil {
		t.Fatalf("load %s: %v", p, err)
	}
	return k.Rules
}

var twoKitFiles = map[string]kitfile.Rules{
	"cmd/kit/KIT": {{Pattern: `.*\.go`, Name: "Core Backend Review"}},
	"KIT":         {{Pattern: `.*\.go`, Name: "Security Review"}, {Pattern: `docs/.*`, Name: "Docs"}},
	"a/KIT":       {},
}

func TestKitSetPathsAndEntries(t *testing.T) {
	s := &kitSet{rules: twoKitFiles}
	if got, want := s.paths(), []string{"KIT", "a/KIT", "cmd/kit/KIT"}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v (root first, then by path)", got, want)
	}
	var files []string
	for _, e := range s.entries("") {
		files = append(files, e.kitFile+" "+e.rule.Pattern)
	}
	if want := []string{"KIT .*\\.go", "KIT docs/.*", "cmd/kit/KIT .*\\.go"}; !reflect.DeepEqual(files, want) {
		t.Errorf("entries = %v, want %v", files, want)
	}
	if got := s.entries("cmd/kit/KIT"); len(got) != 1 || got[0].rule.Name != "Core Backend Review" {
		t.Errorf("entries(only) = %+v", got)
	}
	if got, want := s.scopeNames(), []string{"Core Backend Review", "Docs", "Security Review"}; !reflect.DeepEqual(got, want) {
		t.Errorf("scopeNames = %v, want %v", got, want)
	}
}

// TestKitSetResolve pins --file: paths are from the repository root, a
// directory means its KIT file, and anything else is an error naming the
// KIT files that exist.
func TestKitSetResolve(t *testing.T) {
	s := &kitSet{root: t.TempDir(), rules: twoKitFiles}
	tests := []struct {
		flag, want, wantErr string
	}{
		{flag: "", want: ""},
		{flag: "KIT", want: "KIT"},
		{flag: ".", want: "KIT"},
		{flag: "cmd/kit", want: "cmd/kit/KIT"},
		{flag: "./cmd/kit/", want: "cmd/kit/KIT"},
		{flag: "cmd/kit/KIT", want: "cmd/kit/KIT"},
		{flag: filepath.Join(s.root, "cmd", "kit"), want: "cmd/kit/KIT"},
		{flag: "../elsewhere", wantErr: "outside the repository"},
		{flag: "svc", wantErr: `no KIT file at "svc/KIT" (KIT files: KIT, a/KIT, cmd/kit/KIT)`},
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
	if _, err := (&kitSet{root: s.root}).resolve("KIT"); !errors.Is(err, errNoKitFiles) {
		t.Errorf("an empty set must report errNoKitFiles, got %v", err)
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
	if err := os.MkdirAll(filepath.Join(real, "cmd", "kit"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	tests := []struct {
		name, root, p, want string
		wantOK              bool
	}{
		{name: "same spelling", root: link, p: filepath.Join(link, "cmd", "kit"), want: "cmd/kit", wantOK: true},
		{name: "symlinked root, real path", root: link, p: filepath.Join(real, "cmd", "kit"), want: "cmd/kit", wantOK: true},
		{name: "real root, symlinked path", root: real, p: filepath.Join(link, "cmd", "kit"), want: "cmd/kit", wantOK: true},
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

// TestKitSetRuleTarget pins how edit and remove find a rule's KIT file
// without --file, including that a script never silently picks one of
// several.
func TestKitSetRuleTarget(t *testing.T) {
	s := &kitSet{root: t.TempDir(), rules: twoKitFiles}
	tests := []struct {
		name, file, pattern, want, wantErr string
		wantIs                             error
	}{
		{name: "unique pattern", pattern: `docs/.*`, want: "KIT"},
		{name: "--file wins", file: "cmd/kit", pattern: `.*\.go`, want: "cmd/kit/KIT"},
		{name: "ambiguous", pattern: `.*\.go`, wantErr: `pattern ".*\\.go" is in 2 KIT files (KIT, cmd/kit/KIT); pass --file`},
		{name: "missing", pattern: `nope`, wantIs: kitfile.ErrPatternNotFound},
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

// TestKitSetNearest checks the add form's default KIT file: the one in the
// current directory or its nearest ancestor.
func TestKitSetNearest(t *testing.T) {
	s := &kitSet{root: t.TempDir(), rules: twoKitFiles}
	for _, dir := range []string{"cmd/kit/sub", "docs"} {
		if err := os.MkdirAll(filepath.Join(s.root, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	tests := map[string]string{
		"":            "KIT",
		"cmd/kit":     "cmd/kit/KIT",
		"cmd/kit/sub": "cmd/kit/KIT",
		"docs":        "KIT",
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

func TestKitSetMutate(t *testing.T) {
	s := newTestKitSet(t, map[string]kitfile.Rules{"KIT": {{Pattern: "a"}}})
	if _, err := s.mutate("KIT", func(k *kitfile.KitFile) error { return k.Add(kitfile.Rule{Pattern: "b"}) }); err != nil {
		t.Fatalf("mutate: %v", err)
	}
	if got := loadRules(t, s, "KIT"); len(got) != 2 {
		t.Errorf("mutate must save the change: %+v", got)
	}
	boom := errors.New("boom")
	if _, err := s.mutate("KIT", func(*kitfile.KitFile) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("mutate must return the change's error, got %v", err)
	}
	if got := loadRules(t, s, "KIT"); len(got) != 2 {
		t.Errorf("a failed change must not be saved: %+v", got)
	}
}
