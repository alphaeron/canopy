package gitutil

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
func newRepo(t *testing.T, committed map[string]string) (string, *KitGitRepository) {
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
	return dir, &KitGitRepository{Repository: *repo}
}

func isKIT(p string) bool { return path.Base(p) == "KIT" }

// TestWorktreeFiles pins the working-tree file set: committed files that
// still exist, plus new files that are not ignored; deletions drop out and
// the predicate matches whole names.
func TestWorktreeFiles(t *testing.T) {
	dir, repo := newRepo(t, map[string]string{
		"KIT":         "a",
		"gone/KIT":    "b",
		".gitignore":  "build/\n",
		"KITCHEN.md":  "not a KIT file",
		"src/main.go": "package main",
	})
	writeFiles(t, dir, map[string]string{
		"svc/KIT":   "new, untracked",
		"build/KIT": "ignored",
	})
	if err := os.Remove(filepath.Join(dir, "gone", "KIT")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err := repo.WorktreeFiles(isKIT)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if want := []string{"KIT", "svc/KIT"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorktreeFiles = %v, want %v", got, want)
	}
}

func TestWorktreeFilesNoCommit(t *testing.T) {
	dir, repo := newRepo(t, nil)
	writeFiles(t, dir, map[string]string{"KIT": "a"})
	got, err := repo.WorktreeFiles(isKIT)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if want := []string{"KIT"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorktreeFiles = %v, want %v", got, want)
	}
}
