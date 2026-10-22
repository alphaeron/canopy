package gitrepo

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// writeFiles writes files under dir, creating parent directories.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", p, err)
		}
	}
}

// newRepo initializes a repository in a temp dir and, when committed is
// non-empty, commits those files.
func newRepo(t *testing.T, committed map[string]string) (string, *Repository) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	if len(committed) > 0 {
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatalf("Worktree: %v", err)
		}
		writeFiles(t, dir, committed)
		for p := range committed {
			if _, err := wt.Add(p); err != nil {
				t.Fatalf("Add %s: %v", p, err)
			}
		}
		if _, err := wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@x"}}); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	return dir, &Repository{Repository: *repo}
}

func isRulesFile(p string) bool { return path.Base(p) == "rules" }

// TestWorktreeFiles pins the working-tree file set: committed files that
// still exist, plus new files that are not ignored; deletions drop out and
// the predicate matches whole names.
func TestWorktreeFiles(t *testing.T) {
	dir, repo := newRepo(t, map[string]string{
		"rules":       "a",
		"gone/rules":  "b",
		".gitignore":  "build/\n",
		"RULESET.md":  "not a rules file",
		"src/main.go": "package main",
	})
	writeFiles(t, dir, map[string]string{
		"svc/rules":   "new, untracked",
		"build/rules": "ignored",
	})
	if err := os.Remove(filepath.Join(dir, "gone", "rules")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err := repo.WorktreeFiles(isRulesFile)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if want := []string{"rules", "svc/rules"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorktreeFiles = %v, want %v", got, want)
	}
}

func TestWorktreeFilesNoCommit(t *testing.T) {
	dir, repo := newRepo(t, nil)
	writeFiles(t, dir, map[string]string{"rules": "a"})
	got, err := repo.WorktreeFiles(isRulesFile)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if want := []string{"rules"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorktreeFiles = %v, want %v", got, want)
	}
}
