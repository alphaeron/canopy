package kenin

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/alphaeron/kit/internal/gitutil"
)

// newScratchRepo creates a go-git repository in a temp dir, seeded with the
// supplied files, and returns the working path and its repository handle.
// Every file is staged, committed, then the working tree is restored to the
// committed state, so status starts clean.
func newScratchRepo(t *testing.T, files map[string]string) (string, *gitutil.KitGitRepository) {
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
	return dir, &gitutil.KitGitRepository{Repository: *repo}
}

// TestLocalNoCommitYieldsEmptyUniverses pins the no-commit UX: a plain-init
// repository with no commit reports an empty AllFiles and ChangedFiles, with
// no error, so `kit match` and `kit coverage` run cleanly right after `git
// init` rather than failing on the unborn HEAD.
func TestLocalNoCommitYieldsEmptyUniverses(t *testing.T) {
	repo, err := git.PlainInit(t.TempDir(), false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	src := NewLocalSource(&gitutil.KitGitRepository{Repository: *repo}, nil, nil)

	got, err := src.AllFiles()
	if err != nil {
		t.Fatalf("AllFiles (no commit): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("AllFiles (no commit) = %v, want empty", got)
	}

	changed, err := src.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles (no commit): %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("ChangedFiles (no commit) = %v, want empty", changed)
	}
}

func TestLocalAllFilesMatchesCommittedSet(t *testing.T) {
	_, repo := newScratchRepo(t, map[string]string{
		"a.go":     "one",
		"src/b.go": "two",
		"KIT":      "- \"a$\":\n  name: \"n\"\n",
	})
	src := NewLocalSource(repo, nil, nil)
	files, err := src.AllFiles()
	if err != nil {
		t.Fatalf("AllFiles: %v", err)
	}
	sort.Strings(files)
	want := []string{"KIT", "a.go", "src/b.go"}
	if strings.Join(files, ",") != strings.Join(want, ",") {
		t.Errorf("AllFiles = %v, want %v", files, want)
	}
}

func TestLocalChangedFilesUntrackedAndModified(t *testing.T) {
	dir, repo := newScratchRepo(t, map[string]string{
		"one.go": "hello",
	})
	src := NewLocalSource(repo, nil, nil)

	// Before any changes there are no changed files.
	changed, err := src.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles clean: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("clean worktree reported changed files: %v", changed)
	}

	// Modify one tracked file and add one untracked file.
	if err := os.WriteFile(filepath.Join(dir, "one.go"), []byte("goodbye"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.go"), []byte("fresh"), 0o644); err != nil {
		t.Fatalf("new file: %v", err)
	}
	changed, err = src.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles after edits: %v", err)
	}
	sort.Strings(changed)
	if len(changed) != 2 || changed[0] != "one.go" || changed[1] != "two.go" {
		t.Errorf("ChangedFiles = %v, want [one.go two.go]", changed)
	}
}

func TestLocalDiffContentModifiedAndAdded(t *testing.T) {
	dir, repo := newScratchRepo(t, map[string]string{
		"one.go": "a\nb\nc\n",
	})
	src := NewLocalSource(repo, nil, nil)

	// Before edits, no diff content.
	if diff, err := src.DiffContent("one.go"); err != nil || diff != "" {
		t.Errorf("no-change diff = %q (err %v), want empty", diff, err)
	}

	// Modify one line and add a new file.
	if err := os.WriteFile(filepath.Join(dir, "one.go"), []byte("a\nB\nc\n"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.go"), []byte("line1\n"), 0o644); err != nil {
		t.Fatalf("new file: %v", err)
	}

	oneDiff, err := src.DiffContent("one.go")
	if err != nil {
		t.Fatalf("diff one.go: %v", err)
	}
	if !strings.Contains(oneDiff, "-b\n") || !strings.Contains(oneDiff, "+B\n") {
		t.Errorf("one.go diff = %q, want -b and +B", oneDiff)
	}
	twoDiff, err := src.DiffContent("two.go")
	if err != nil {
		t.Fatalf("diff two.go: %v", err)
	}
	if !strings.Contains(twoDiff, "+line1\n") {
		t.Errorf("two.go diff = %q, want +line1", twoDiff)
	}
}
