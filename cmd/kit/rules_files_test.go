package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// TestFindKitFiles pins that `rules files` lists only KIT files, root KIT
// first; that without a revision it sees uncommitted KIT files; and that a
// revision limits it to the files committed there.
func TestFindKitFiles(t *testing.T) {
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	write := func(paths ...string) {
		t.Helper()
		for _, p := range paths {
			disk := filepath.Join(root, p)
			if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(disk, nil, 0o644); err != nil {
				t.Fatalf("write %s: %v", p, err)
			}
		}
	}
	write("internal/x/KIT", "KIT", "cmd/kit/KIT", "cmd/kit/main.go", "cmd/kit/KITCHEN.md")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("add: %v", err)
	}
	sig := &object.Signature{Name: "test", Email: "test@example.com", When: time.Unix(0, 0)}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	write("new/KIT")
	t.Chdir(root)

	tests := []struct {
		pattern, revision string
		want              []string
	}{
		{"", "", []string{"KIT", "cmd/kit/KIT", "internal/x/KIT", "new/KIT"}},
		{"cmd", "", []string{"cmd/kit/KIT"}},
		{"main", "", []string{}},
		{"new", "", []string{"new/KIT"}},
		{"", "HEAD", []string{"KIT", "cmd/kit/KIT", "internal/x/KIT"}},
		{"cmd", "HEAD", []string{"cmd/kit/KIT"}},
	}
	for _, tc := range tests {
		got, err := findKitFiles(tc.pattern, tc.revision)
		if err != nil {
			t.Fatalf("findKitFiles(%q, %q): %v", tc.pattern, tc.revision, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("findKitFiles(%q, %q) = %q, want %q", tc.pattern, tc.revision, got, tc.want)
		}
	}
	if _, err := findKitFiles("(", ""); err == nil {
		t.Error("an invalid pattern must be an error")
	}
	if _, err := findKitFiles("", "no-such-rev"); err == nil {
		t.Error("an unknown revision must be an error")
	}
}
