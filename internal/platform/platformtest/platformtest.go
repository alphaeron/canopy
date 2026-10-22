// Package platformtest is the contract every review platform under
// internal/platform must meet. A platform's tests build its review.Source,
// approval.Tree, and approval.ApproverResolver over the fixtures this
// package defines, and the suites check them against the documented
// behavior of those interfaces, so a new platform is verified to plug into
// the engine rather than assumed to.
package platformtest

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// Fixture is a repository state: the files at the base revision, and the
// change under review on top of it. Paths are repository-relative and
// slash-separated.
type Fixture struct {
	// Base maps each file at the base revision to its contents.
	Base map[string]string
	// Change is the change to the base revision.
	Change Change
}

// Change is a set of edits to a base revision. A path appears in at most
// one of its fields.
type Change struct {
	// Write maps each added or modified file to its new contents. A path
	// that is not in the base revision is added.
	Write map[string]string
	// Delete lists the base files the change removes.
	Delete []string
	// Rename maps each moved base file's old path to its new one. The
	// contents do not change.
	Rename map[string]string
}

// SourceFactory builds the platform's review.Source over f: f.Base as the
// base revision and f.Change as the change under review. It fails t when it
// cannot.
type SourceFactory func(t *testing.T, f Fixture) review.Source

// TreeFactory builds the platform's approval.Tree holding files. It fails t
// when it cannot.
type TreeFactory func(t *testing.T, files map[string]string) approval.Tree

// Rules files in the fixtures. The change never touches them: platforms
// deliberately differ in which revision they read rules from (a local
// review previews the working tree's, a hosted review enforces the base's),
// so the contract only covers rules both sides agree on.
const (
	rootRules = "- \".*\":\n  name: \"Root\"\n"
	svcRules  = "- \".*\\\\.go\":\n  name: \"Svc\"\n"
)

// fixture is the repository state the source contract runs over: one
// modified, one added, one deleted, and one renamed file, next to unchanged
// ones and two rules files.
func fixture() Fixture {
	return Fixture{
		Base: map[string]string{
			rulesfile.FileName:         rootRules,
			rulesfile.PathInDir("svc"): svcRules,
			"main.go":                  "package main\n",
			"svc/api.go":               "package svc\n\nfunc A() {}\n",
			"svc/old.go":               "package svc\n",
			"docs/guide.md":            "# Guide\n",
			"docs/old.md":              "# Old\n",
		},
		Change: Change{
			Write: map[string]string{
				"svc/api.go": "package svc\n\nfunc B() {}\n",
				"svc/new.go": "package svc\n\nvar New = 1\n",
			},
			Delete: []string{"docs/old.md"},
			Rename: map[string]string{"svc/old.go": "legacy/old.go"},
		},
	}
}

// RunSourceContract checks the review.Source that newSource builds against
// the Source contract: the base revision's files, every changed path
// (both sides of a rename), per-file diffs, and a complete evaluator that
// reads the rules files.
func RunSourceContract(t *testing.T, newSource SourceFactory) {
	t.Helper()
	f := fixture()

	t.Run("AllFiles is the base revision, sorted", func(t *testing.T) {
		got, err := newSource(t, f).AllFiles(t.Context())
		if err != nil {
			t.Fatalf("AllFiles: %v", err)
		}
		if want := slices.Sorted(maps.Keys(f.Base)); !slices.Equal(got, want) {
			t.Errorf("AllFiles = %v, want %v", got, want)
		}
	})

	t.Run("ChangedFiles lists every changed path, sorted", func(t *testing.T) {
		got, err := newSource(t, f).ChangedFiles(t.Context())
		if err != nil {
			t.Fatalf("ChangedFiles: %v", err)
		}
		want := []string{"docs/old.md", "legacy/old.go", "svc/api.go", "svc/new.go", "svc/old.go"}
		if !slices.Equal(got, want) {
			t.Errorf("ChangedFiles = %v, want %v (a rename lists both paths)", got, want)
		}
	})

	t.Run("ChangedFiles is empty without a change", func(t *testing.T) {
		got, err := newSource(t, Fixture{Base: f.Base}).ChangedFiles(t.Context())
		if err != nil {
			t.Fatalf("ChangedFiles: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ChangedFiles = %v, want none", got)
		}
	})

	t.Run("DiffContent shows each change", func(t *testing.T) {
		src := newSource(t, f)
		tests := []struct {
			path string
			want []string // substrings of the diff; none means the diff is ""
		}{
			{path: "svc/api.go", want: []string{"-func A() {}", "+func B() {}"}},
			{path: "svc/new.go", want: []string{"+var New = 1"}},
			{path: "docs/old.md", want: []string{"-# Old"}},
			{path: "main.go"},
			{path: "absent.go"},
		}
		for _, tt := range tests {
			diff, err := src.DiffContent(t.Context(), tt.path)
			if err != nil {
				t.Errorf("DiffContent(%s): %v", tt.path, err)
				continue
			}
			if len(tt.want) == 0 && diff != "" {
				t.Errorf("DiffContent(%s) = %q, want empty for an unchanged path", tt.path, diff)
			}
			for _, w := range tt.want {
				if !strings.Contains(diff, w) {
					t.Errorf("DiffContent(%s) = %q, want it to contain %q", tt.path, diff, w)
				}
			}
		}
	})

	t.Run("Evaluator fetches the rules files", func(t *testing.T) {
		rev := newSource(t, f).Evaluator()
		if rev == nil || rev.Fetch == nil || rev.Resolver == nil {
			t.Fatalf("Evaluator = %+v, want a fetcher and a resolver", rev)
		}
		rules, err := rev.Fetch.Execute(t.Context())
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		want := []string{rulesfile.FileName, rulesfile.PathInDir("svc")}
		if got := rules.Paths(); !slices.Equal(got, want) {
			t.Fatalf("rules files = %v, want %v", got, want)
		}
		if got := rules[rulesfile.PathInDir("svc")]; len(got) != 1 || got[0].Name != "Svc" {
			t.Errorf("svc rules = %+v, want the parsed Svc rule", got)
		}
	})
}

// RunTreeContract checks the approval.Tree that newTree builds against the
// Tree contract: every file listed in sorted order, contents read back
// unchanged, and fs.ErrNotExist for a path outside the tree.
func RunTreeContract(t *testing.T, newTree TreeFactory) {
	t.Helper()
	files := map[string]string{
		rulesfile.FileName:                rootRules,
		"a/b.go":                          "package a\n",
		"a/" + rulesfile.FileName + ".md": "# not rules\n",
	}

	t.Run("Files lists every file, sorted", func(t *testing.T) {
		got, err := newTree(t, files).Files(t.Context())
		if err != nil {
			t.Fatalf("Files: %v", err)
		}
		if want := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
			t.Errorf("Files = %v, want %v", got, want)
		}
	})

	t.Run("ReadFile returns the contents", func(t *testing.T) {
		tree := newTree(t, files)
		for path, want := range files {
			got, err := tree.ReadFile(t.Context(), path)
			if err != nil {
				t.Errorf("ReadFile(%s): %v", path, err)
				continue
			}
			if string(got) != want {
				t.Errorf("ReadFile(%s) = %q, want %q", path, got, want)
			}
		}
	})

	t.Run("ReadFile of a missing path is fs.ErrNotExist", func(t *testing.T) {
		_, err := newTree(t, files).ReadFile(t.Context(), "missing.go")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(missing.go) err = %v, want fs.ErrNotExist", err)
		}
	})
}

// ResolverHarness is a platform's approval.ApproverResolver with the
// platform-specific targets the resolver contract needs, since target
// syntax differs by platform.
type ResolverHarness struct {
	// Resolver is the platform's resolver.
	Resolver approval.ApproverResolver
	// Group is a group target whose members are those passed to the
	// factory; "" when the platform has no groups.
	Group string
	// UnknownGroup is a well-formed group target the platform does not
	// have; "" when the platform has no groups.
	UnknownGroup string
	// Invalid is a target the platform rejects as malformed; "" when every
	// target is well-formed.
	Invalid string
	// Calls reports how many platform API calls the resolver has made; nil
	// when it makes none.
	Calls func() int
}

// ResolverFactory builds the platform's resolver harness over a group
// holding members. It fails t when it cannot.
type ResolverFactory func(t *testing.T, members []string) ResolverHarness

// RunResolverContract checks a platform's approval.ApproverResolver against
// the resolver contract: a user stands for itself, a group resolves to its
// members, an unknown group is approval.ErrUnknownTarget, a malformed target
// is rejected without an API call, and Canonical is idempotent.
func RunResolverContract(t *testing.T, newHarness ResolverFactory) {
	t.Helper()
	members := []string{"Ann", "bo"}

	t.Run("a user resolves to itself", func(t *testing.T) {
		h := newHarness(t, members)
		got, err := h.Resolver.Resolve(t.Context(), "Dana")
		if err != nil {
			t.Fatalf("Resolve(Dana): %v", err)
		}
		if len(got) != 1 || h.Resolver.Canonical(got[0]) != h.Resolver.Canonical("Dana") {
			t.Errorf("Resolve(Dana) = %v, want Dana alone", got)
		}
	})

	t.Run("Canonical is idempotent", func(t *testing.T) {
		r := newHarness(t, members).Resolver
		for _, name := range []string{"Dana", "dana", "DANA", "ann-b"} {
			if once := r.Canonical(name); r.Canonical(once) != once {
				t.Errorf("Canonical(Canonical(%q)) = %q, want %q", name, r.Canonical(once), once)
			}
		}
	})

	t.Run("a group resolves to its members", func(t *testing.T) {
		h := newHarness(t, members)
		if h.Group == "" {
			t.Skip("the platform has no groups")
		}
		got, err := h.Resolver.Resolve(t.Context(), h.Group)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", h.Group, err)
		}
		if !slices.Equal(canonicalSet(h.Resolver, got), canonicalSet(h.Resolver, members)) {
			t.Errorf("Resolve(%s) = %v, want %v", h.Group, got, members)
		}
	})

	t.Run("an unknown group is ErrUnknownTarget", func(t *testing.T) {
		h := newHarness(t, members)
		if h.UnknownGroup == "" {
			t.Skip("the platform has no groups")
		}
		if _, err := h.Resolver.Resolve(t.Context(), h.UnknownGroup); !errors.Is(err, approval.ErrUnknownTarget) {
			t.Errorf("Resolve(%s) err = %v, want approval.ErrUnknownTarget", h.UnknownGroup, err)
		}
	})

	t.Run("a malformed target is rejected offline", func(t *testing.T) {
		h := newHarness(t, members)
		if h.Invalid == "" {
			t.Skip("the platform accepts every target")
		}
		v, ok := h.Resolver.(approval.TargetValidator)
		if !ok {
			t.Fatal("a platform that rejects targets must implement approval.TargetValidator")
		}
		for _, valid := range []string{"Dana", h.Group} {
			if err := v.ValidateTarget(valid); valid != "" && err != nil {
				t.Errorf("ValidateTarget(%q) = %v, want it valid", valid, err)
			}
		}
		if err := v.ValidateTarget(h.Invalid); !errors.Is(err, approval.ErrInvalidTarget) {
			t.Errorf("ValidateTarget(%q) = %v, want approval.ErrInvalidTarget", h.Invalid, err)
		}
		before := calls(h)
		_, err := h.Resolver.Resolve(t.Context(), h.Invalid)
		if !errors.Is(err, approval.ErrUnknownTarget) || !errors.Is(err, approval.ErrInvalidTarget) {
			t.Errorf("Resolve(%q) err = %v, want approval.ErrUnknownTarget and approval.ErrInvalidTarget", h.Invalid, err)
		}
		if calls(h) != before {
			t.Errorf("Resolve(%q) called the platform API", h.Invalid)
		}
	})
}

// canonicalSet is names in canonical form, sorted and deduplicated.
func canonicalSet(r approval.ApproverResolver, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, r.Canonical(n))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// calls is the harness's API call count, or 0 when it makes none.
func calls(h ResolverHarness) int {
	if h.Calls == nil {
		return 0
	}
	return h.Calls()
}
