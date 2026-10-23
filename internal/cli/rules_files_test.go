package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// TestFindRulesFiles pins that `rules files` lists only rules files, root rules
// file first; that without a revision it sees uncommitted rules files; and that
// a revision limits it to the files committed there.
func TestFindRulesFiles(t *testing.T) {
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
	write(rulesfile.PathInDir("internal/x"), rulesfile.FileName, rulesfile.PathInDir("cmd/app"), "cmd/app/main.go", "cmd/app/"+rulesfile.FileName+"CHEN.md")
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
	write(rulesfile.PathInDir("new"))
	t.Chdir(root)

	tests := []struct {
		pattern, revision string
		want              []string
	}{
		{"", "", []string{rulesfile.FileName, rulesfile.PathInDir("cmd/app"), rulesfile.PathInDir("internal/x"), rulesfile.PathInDir("new")}},
		{"cmd", "", []string{rulesfile.PathInDir("cmd/app")}},
		{"main", "", []string{}},
		{"new", "", []string{rulesfile.PathInDir("new")}},
		{"", "HEAD", []string{rulesfile.FileName, rulesfile.PathInDir("cmd/app"), rulesfile.PathInDir("internal/x")}},
		{"cmd", "HEAD", []string{rulesfile.PathInDir("cmd/app")}},
	}
	for _, tc := range tests {
		got, err := findRulesFiles(t.Context(), tc.pattern, tc.revision)
		if err != nil {
			t.Fatalf("findRulesFiles(%q, %q): %v", tc.pattern, tc.revision, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("findRulesFiles(%q, %q) = %q, want %q", tc.pattern, tc.revision, got, tc.want)
		}
	}
	if _, err := findRulesFiles(t.Context(), "(", ""); err == nil {
		t.Error("an invalid pattern must be an error")
	}
	if _, err := findRulesFiles(t.Context(), "", "no-such-rev"); err == nil {
		t.Error("an unknown revision must be an error")
	}
}
