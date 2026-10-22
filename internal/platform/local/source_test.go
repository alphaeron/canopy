package local

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/gitrepo"
)

// newScratchRepo creates a go-git repository in a temp dir, seeded with the
// supplied files, and returns the working path and its repository handle.
// Every file is staged, committed, then the working tree is restored to the
// committed state, so status starts clean.
func newScratchRepo(t *testing.T, files map[string]string) (string, *gitrepo.Repository) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for path, content := range files {
		fullPath := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
		if _, err := wt.Add(path); err != nil {
			t.Fatalf("Add %s: %v", path, err)
		}
	}
	if _, err := wt.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@x"}}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return dir, &gitrepo.Repository{Repository: *repo}
}

// TestLocalNoCommitYieldsEmptyUniverses pins the no-commit UX: a plain-init
// repository with no commit reports an empty AllFiles and ChangedFiles, with
// no error, so `match` and `coverage` run cleanly right after `git
// init` rather than failing on the unborn HEAD.
func TestLocalNoCommitYieldsEmptyUniverses(t *testing.T) {
	repo, err := git.PlainInit(t.TempDir(), false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	src := NewSource(&gitrepo.Repository{Repository: *repo}, nil)

	got, err := src.AllFiles(t.Context())
	if err != nil {
		t.Fatalf("AllFiles (no commit): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("AllFiles (no commit) = %v, want empty", got)
	}

	changed, err := src.ChangedFiles(t.Context())
	if err != nil {
		t.Fatalf("ChangedFiles (no commit): %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("ChangedFiles (no commit) = %v, want empty", changed)
	}
}

// TestLocalSourceIsOnTheLocalPlatform: a local source reads the working
// tree's rules and resolves approvers as themselves.
func TestLocalSourceIsOnTheLocalPlatform(t *testing.T) {
	rev := NewSource(nil, nil).Evaluator()
	if fetch, ok := rev.Fetch.(*approval.TreeFetcher); !ok {
		t.Errorf("fetcher = %T, want *approval.TreeFetcher", rev.Fetch)
	} else if _, ok := fetch.Tree.(*worktree); !ok {
		t.Errorf("fetch tree = %T, want the working tree", fetch.Tree)
	}
	if _, ok := rev.Resolver.(*approval.DefaultApproverResolver); !ok {
		t.Errorf("resolver = %T, want *approval.DefaultApproverResolver", rev.Resolver)
	}
}

// TestLocalChangedFilesListsBothPathsOfARename pins that a staged rename
// reports the path it left as well as the path it arrived at, so the rules
// over the old directory still apply. The source contract covers an
// unstaged rename.
func TestLocalChangedFilesListsBothPathsOfARename(t *testing.T) {
	tests := []struct {
		name string
		move func(t *testing.T, dir string, repo *gitrepo.Repository)
	}{
		{name: "staged", move: func(t *testing.T, _ string, repo *gitrepo.Repository) {
			wt, err := repo.Worktree()
			if err != nil {
				t.Fatalf("Worktree: %v", err)
			}
			if _, err := wt.Move("old/a.go", "new/a.go"); err != nil {
				t.Fatalf("Move: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, repo := newScratchRepo(t, map[string]string{"old/a.go": "package a"})
			tt.move(t, dir, repo)

			changed, err := NewSource(repo, nil).ChangedFiles(t.Context())
			if err != nil {
				t.Fatalf("ChangedFiles: %v", err)
			}
			if want := []string{"new/a.go", "old/a.go"}; !slices.Equal(changed, want) {
				t.Errorf("ChangedFiles = %v, want %v", changed, want)
			}
		})
	}
}
