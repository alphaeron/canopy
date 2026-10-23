package gitrepo

import (
	"context"
	"errors"
	"os"
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

// TestWorktreeFiles pins the working-tree file set: committed files that
// still exist, plus new files that are not ignored; deletions drop out.
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
	got, err := repo.WorktreeFiles(t.Context())
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if want := []string{".gitignore", "RULESET.md", "rules", "src/main.go", "svc/rules"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorktreeFiles = %v, want %v", got, want)
	}
}

func TestWorktreeFilesNoCommit(t *testing.T) {
	dir, repo := newRepo(t, nil)
	writeFiles(t, dir, map[string]string{"rules": "a"})
	got, err := repo.WorktreeFiles(t.Context())
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if want := []string{"rules"}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorktreeFiles = %v, want %v", got, want)
	}
}

// TestFileAt pins the direct lookup: a committed file with its size and
// contents, and nil for a missing path or a directory.
func TestFileAt(t *testing.T) {
	_, repo := newRepo(t, map[string]string{"a/b.txt": "hello\n"})
	f, err := repo.FileAt("a/b.txt", "HEAD")
	if err != nil || f == nil {
		t.Fatalf("FileAt(a/b.txt) = %v, %v; want the file", f, err)
	}
	if got, err := f.Contents(); err != nil || got != "hello\n" || f.Size != 6 || f.Name != "a/b.txt" {
		t.Errorf("file = %q (size %d, name %q), %v; want hello, 6 bytes, a/b.txt", got, f.Size, f.Name, err)
	}
	for _, missing := range []string{"a/missing.txt", "missing/b.txt", "a"} {
		if f, err := repo.FileAt(missing, "HEAD"); err != nil || f != nil {
			t.Errorf("FileAt(%s) = %v, %v; want nil, nil", missing, f, err)
		}
	}
}

// TestHeadFilesIsASnapshot pins that a Repository lists HEAD once: a later
// commit is seen only by a newly opened Repository.
func TestHeadFilesIsASnapshot(t *testing.T) {
	dir, repo := newRepo(t, map[string]string{"a.txt": "a", "d/b.txt": "b"})
	got, err := repo.HeadFiles(t.Context())
	if want := []string{"a.txt", "d/b.txt"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("HeadFiles = %v, %v; want %v", got, err, want)
	}
	writeFiles(t, dir, map[string]string{"c.txt": "c"})
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if _, err := wt.Add("c.txt"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := wt.Commit("more", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@x"}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if again, _ := repo.HeadFiles(t.Context()); len(again) != 2 {
		t.Errorf("HeadFiles after a commit = %v, want the first listing", again)
	}
	reopened := &Repository{Repository: repo.Repository}
	if fresh, _ := reopened.HeadFiles(t.Context()); len(fresh) != 3 {
		t.Errorf("a new Repository's HeadFiles = %v, want the new commit's 3 files", fresh)
	}
}

// TestHeadFilesStopsWhenCancelled: a walk under a done context stops with
// its error, and the failed listing is not kept.
func TestHeadFilesStopsWhenCancelled(t *testing.T) {
	_, repo := newRepo(t, map[string]string{"a.txt": "a"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repo.HeadFiles(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("HeadFiles with a done context: err = %v, want context.Canceled", err)
	}
	if got, err := repo.HeadFiles(t.Context()); err != nil || !reflect.DeepEqual(got, []string{"a.txt"}) {
		t.Errorf("HeadFiles = %v, %v; want [a.txt]", got, err)
	}
}
