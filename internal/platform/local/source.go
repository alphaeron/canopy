package local

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"

	"github.com/go-git/go-git/v6"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/gitrepo"
	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/review"
)

// Source implements review.Source against a local go-git checkout. The file
// universe is the working tree (AllFiles) and its diff against HEAD
// (ChangedFiles); rules and approvers come from the local review platform
// (NewEvaluator), so local CLI analysis and local reviews read the same
// rules files and resolve approvers the same way.
type Source struct {
	repo      *gitrepo.Repository // file listings and diffs; the evaluator shares it
	log       *slog.Logger        // diagnostics; nil means slog.Default()
	evaluator *approval.Evaluator
}

// NewSource builds a Source bound to a repository, on the local
// review platform.
func NewSource(repo *gitrepo.Repository, log *slog.Logger) *Source {
	return &Source{repo: repo, log: log, evaluator: NewEvaluator(repo, log)}
}

// Evaluator is the local review platform: the working tree's rules files and
// pass-through approver resolution.
func (l *Source) Evaluator() *approval.Evaluator { return l.evaluator }

// RepoRoot returns the on-disk repository root (review.Rooted). A repository
// that does not expose a filesystem root is logged as an error and yields "".
func (l *Source) RepoRoot() string {
	root, err := l.repo.Root()
	if err != nil {
		l.logger().Error("match: unable to resolve repository root",
			"component", "match", "source", "local", "error", err)
	}
	return root
}

// AllFiles lists every tracked file at HEAD, i.e. the complete file universe
// for matching and coverage. The ".*" regex matches every path: the list is
// sorted for deterministic traversal downstream.
func (l *Source) AllFiles(ctx context.Context) ([]string, error) {
	// A repository with no commit yet has no tracked files: report an empty
	// universe rather than a missing-HEAD error, so `match` is usable
	// right after `git init`.
	if !l.repo.HasCommits() {
		return []string{}, nil
	}
	ref, err := l.repo.Head()
	if err != nil {
		return nil, err
	}
	files, err := l.repo.FindFiles(`.*`, ref, false)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// ChangedFiles returns the files in the working tree that are not identical
// to HEAD: added, modified, deleted, and untracked files, per go-git's
// status codes. This is the universe for `match --changed`.
func (l *Source) ChangedFiles(ctx context.Context) ([]string, error) {
	// The worktree's diff is against HEAD specifically, and `wt.Status()`
	// errors rather than returning an empty result when HEAD does not exist
	// yet. Treat that as "nothing has changed": there is no tracked revision
	// to be different from, so the changed-file set is empty by definition.
	if !l.repo.HasCommits() {
		return []string{}, nil
	}
	wt, err := l.repo.Worktree()
	if err != nil {
		return nil, err
	}
	status, err := wt.Status()
	if err != nil {
		return nil, err
	}
	var changed []string
	for path, st := range status {
		if st.Staging == git.Unmodified && st.Worktree == git.Unmodified {
			continue
		}
		changed = append(changed, path)
	}
	sort.Strings(changed)
	return changed, nil
}

// DiffContent produces the unified diff for a single tracked file between
// HEAD and the working tree: the HEAD blob is the old side, the file on disk
// the new side. Untracked files have empty old content; deleted files empty
// new content. An input file not present at all in the source universe
// yields ("" , nil) — the diff is absent, not an error.
func (l *Source) DiffContent(ctx context.Context, path string) (string, error) {
	old, _, err := l.repo.FileContentAt(path, "HEAD")
	if err != nil {
		return "", err
	}
	newContent, err := l.worktreeContent(path)
	if err != nil {
		return "", err
	}
	return gitrepo.UnifiedDiff(path, old, newContent), nil
}

// worktreeContent reads the working-tree version of a file, or "" when the
// file is absent from the working tree (deleted or only ever staged).
func (l *Source) worktreeContent(path string) (string, error) {
	wt, err := l.repo.Worktree()
	if err != nil {
		return "", err
	}
	f, err := wt.Filesystem().Open(path)
	if err != nil {
		if osIsNotExist(err) {
			return "", nil
		}
		return "", gitrepo.WrapReadError(path, err)
	}
	dat, err := io.ReadAll(f)
	closeErr := f.Close()
	if err != nil {
		return "", gitrepo.WrapReadError(path, err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close %s: %w", path, closeErr)
	}
	return string(dat), nil
}

// osIsNotExist reports whether err is the filesystem's not-exist error. The
// check is against the OS package so the billy filesystem's wrapper errors
// map cleanly.
func osIsNotExist(err error) bool {
	return gitrepo.IsNotExistErr(err)
}

func (l *Source) logger() *slog.Logger {
	return logx.OrDefault(l.log)
}

var (
	_ review.Source = (*Source)(nil)
	_ review.Rooted = (*Source)(nil)
)
