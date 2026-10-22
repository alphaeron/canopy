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
	"github.com/alphaeron/kit/internal/rulesfile"
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

// TestLocalFetchReadsWorkingTree pins that a local review sees the working
// tree's rules files: it used to list rules files at HEAD only, so a new
// uncommitted rules file was invisible to every command, and look-alike names
// must not be read as rules.
func TestLocalFetchReadsWorkingTree(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	writeTestFile(t, dir, rulesfile.FileName, "- \".*\":\n  name: \"Committed\"\n")
	writeTestFile(t, dir, rulesfile.FileName+"CHEN.md", "not a rules file")
	for _, p := range []string{rulesfile.FileName, rulesfile.FileName + "CHEN.md"} {
		if _, err := wt.Add(p); err != nil {
			t.Fatalf("Add %s: %v", p, err)
		}
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@x"}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// An uncommitted edit to a committed rules file, and a new untracked one.
	writeTestFile(t, dir, rulesfile.FileName, "- \".*\":\n  name: \"Edited\"\n")
	writeTestFile(t, dir, rulesfile.PathInDir("svc"), "- \".*\\\\.go\":\n  name: \"New\"\n")

	rules, err := NewEvaluator(&gitrepo.Repository{Repository: *repo}, testLogger()).Fetch.Execute(t.Context())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := rules.Paths(), []string{rulesfile.FileName, rulesfile.PathInDir("svc")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rules files = %v, want %v", got, want)
	}
	if got := rules[rulesfile.FileName]; len(got) != 1 || got[0].Name != "Edited" {
		t.Errorf("root rules file must be read from disk, got %+v", got)
	}
	if got := rules[rulesfile.PathInDir("svc")]; len(got) != 1 || got[0].Name != "New" {
		t.Errorf("untracked rules file rules = %+v", got)
	}
}

// testLogger returns a logger that discards all output so tests stay quiet.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
