// Package platformtest is the contract every review platform under
// internal/platform must meet. A platform's tests build its match.Source and
// approval.Tree over the fixtures this package defines, and the suites check
// them against the documented behavior of those interfaces, so a new
// platform is verified to plug into the engine rather than assumed to.
package platformtest

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/match"
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

// SourceFactory builds the platform's match.Source over f: f.Base as the
// base revision and f.Change as the change under review. It fails t when it
// cannot.
type SourceFactory func(t *testing.T, f Fixture) match.Source

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

// RunSourceContract checks the match.Source that newSource builds against
// the Source contract: the base revision's files, every changed path
// (both sides of a rename), per-file diffs, and a complete reviewer that
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

	t.Run("Reviewer fetches the rules files", func(t *testing.T) {
		rev := newSource(t, f).Reviewer()
		if rev == nil || rev.Fetch == nil || rev.Resolver == nil {
			t.Fatalf("Reviewer = %+v, want a fetcher and a resolver", rev)
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
