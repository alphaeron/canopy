package kenin

import (
	"fmt"
	"io"
	"log/slog"
	"sort"

	"github.com/go-git/go-git/v6"

	"github.com/alphaeron/kit/internal/gitutil"
	"github.com/alphaeron/kit/internal/shonin"
)

// LocalSource implements Source against a local go-git checkout. The file
// universe is the working tree (AllFiles) and its diff against HEAD
// (ChangedFiles); rules and approvers come from the local shonin platform
// (shonin.NewLocal), so local CLI analysis and local reviews read the same
// KIT files and resolve approvers the same way.
type LocalSource struct {
	// Repo is the go-git repository for file listings and diffs.
	Repo *gitutil.KitGitRepository
	// Log receives diagnostics; nil means slog.Default().
	Log *slog.Logger

	reviewer *shonin.Shonin
}

// NewLocalSource builds a LocalSource bound to a repository, on the local
// shonin platform.
func NewLocalSource(repo *gitutil.KitGitRepository, log *slog.Logger) *LocalSource {
	return &LocalSource{Repo: repo, Log: log, reviewer: shonin.NewLocal(repo, log)}
}

// Reviewer is the local shonin platform: the working tree's KIT files and
// pass-through approver resolution.
func (l *LocalSource) Reviewer() *shonin.Shonin { return l.reviewer }

// RepoRoot returns the on-disk repository root. The error path (a repo that
// does not expose a local filesystem root) is reported through the error,
// never as a silent empty string the caller mistakes for a valid root.
func (l *LocalSource) RepoRoot() string {
	root, err := l.Repo.Root()
	if err != nil {
		l.log().Error("kenin: unable to resolve repository root",
			"component", "kenin", "source", "local", "error", err)
	}
	return root
}

// AllFiles lists every tracked file at HEAD, i.e. the complete file universe
// for matching and coverage. The ".*" regex matches every path: the list is
// sorted for deterministic traversal downstream.
func (l *LocalSource) AllFiles() ([]string, error) {
	// A repository with no commit yet has no tracked files: report an empty
	// universe rather than a missing-HEAD error, so `kit match` is usable
	// right after `git init`.
	if !l.Repo.HasCommits() {
		return []string{}, nil
	}
	ref, err := l.Repo.Head()
	if err != nil {
		return nil, err
	}
	files, err := l.Repo.FindFiles(`.*`, ref, false)
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// ChangedFiles returns the files in the working tree that are not identical
// to HEAD: added, modified, deleted, and untracked files, per go-git's
// status codes. This is the universe for `match --changed`.
func (l *LocalSource) ChangedFiles() ([]string, error) {
	// The worktree's diff is against HEAD specifically, and `wt.Status()`
	// errors rather than returning an empty result when HEAD does not exist
	// yet. Treat that as "nothing has changed": there is no tracked revision
	// to be different from, so the changed-file set is empty by definition.
	if !l.Repo.HasCommits() {
		return []string{}, nil
	}
	wt, err := l.Repo.Worktree()
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
func (l *LocalSource) DiffContent(path string) (string, error) {
	old, _, err := l.Repo.FileContentAt(path, "HEAD")
	if err != nil {
		return "", err
	}
	newContent, err := l.worktreeContent(path)
	if err != nil {
		return "", err
	}
	return gitutil.UnifiedDiff(path, old, newContent), nil
}

// worktreeContent reads the working-tree version of a file, or "" when the
// file is absent from the working tree (deleted or only ever staged).
func (l *LocalSource) worktreeContent(path string) (string, error) {
	wt, err := l.Repo.Worktree()
	if err != nil {
		return "", err
	}
	f, err := wt.Filesystem().Open(path)
	if err != nil {
		if osIsNotExist(err) {
			return "", nil
		}
		return "", gitutil.WrapReadError(path, err)
	}
	dat, err := io.ReadAll(f)
	closeErr := f.Close()
	if err != nil {
		return "", gitutil.WrapReadError(path, err)
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
	return gitutil.IsNotExistErr(err)
}

func (l *LocalSource) log() *slog.Logger {
	return logOrDefault(l.Log)
}

var _ Source = (*LocalSource)(nil)
