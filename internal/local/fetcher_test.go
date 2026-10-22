package local

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/alphaeron/kit/internal/gitrepo"
)

func writeTestFile(t *testing.T, dir, p, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", p, err)
	}
}

// TestLocalFetcherReadsWorkingTree pins that a local review sees the
// working tree's KIT files: it used to list KIT files at HEAD only, so a new
// uncommitted KIT file was invisible to every command, and look-alike names
// must not be read as rules.
func TestLocalFetcherReadsWorkingTree(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	writeTestFile(t, dir, "KIT", "- \".*\":\n  name: \"Committed\"\n")
	writeTestFile(t, dir, "KITCHEN.md", "not a KIT file")
	for _, p := range []string{"KIT", "KITCHEN.md"} {
		if _, err := wt.Add(p); err != nil {
			t.Fatalf("Add %s: %v", p, err)
		}
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@x"}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// An uncommitted edit to a committed KIT file, and a new untracked one.
	writeTestFile(t, dir, "KIT", "- \".*\":\n  name: \"Edited\"\n")
	writeTestFile(t, dir, "svc/KIT", "- \".*\\\\.go\":\n  name: \"New\"\n")

	fetcher := &Fetcher{Repo: &gitrepo.Repository{Repository: *repo}, Log: testLogger()}
	_, rules, err := fetcher.Execute(t.Context())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := rules.Paths(), []string{"KIT", "svc/KIT"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("KIT files = %v, want %v", got, want)
	}
	if got := rules["KIT"]; len(got) != 1 || got[0].Name != "Edited" {
		t.Errorf("root KIT must be read from disk, got %+v", got)
	}
	if got := rules["svc/KIT"]; len(got) != 1 || got[0].Name != "New" {
		t.Errorf("untracked KIT file rules = %+v", got)
	}
}

// testLogger returns a logger that discards all output so tests stay quiet.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
