package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"sort"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/gitrepo"
	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/review"
)

// Source implements review.Source against a local go-git checkout. The file
// universe is the working tree (AllFiles) and its diff against HEAD
// (ChangedFiles); rules and approvers come from the local review platform
// (NewEvaluator), so local CLI analysis and local reviews read the same
// rules files and resolve approvers the same way. Rules are read from the
// working tree, uncommitted changes included, so the CLI previews the rules
// a branch proposes.
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
		l.logger().Error("unable to resolve repository root",
			"component", "local", "error", err)
	}
	return root
}

// AllFiles lists the working tree's files, the file universe for matching,
// coverage, and review: the files tracked at HEAD that still exist plus new
// ones that are not ignored (gitrepo.Repository.WorktreeFiles). It is the
// listing the rules are read from, shared with the evaluator's fetch, so
// uncommitted additions count and uncommitted deletions do not.
func (l *Source) AllFiles(ctx context.Context) ([]string, error) {
	return l.repo.WorktreeFiles(ctx)
}

// ReadHead reads a file as the working tree has it, the head a local change
// proposes (see review.Source).
func (l *Source) ReadHead(ctx context.Context, path string) ([]byte, error) {
	return (&worktree{repo: l.repo}).ReadFile(ctx, path)
}

// ChangedFiles returns the files in the working tree that are not identical
// to HEAD: added, modified, deleted, and untracked files, per go-git's
// status codes. This is the universe for `match --changed`.
func (l *Source) ChangedFiles(ctx context.Context) ([]string, error) {
	// The worktree's diff is against HEAD specifically, and the status
	// errors rather than returning an empty result when HEAD does not exist
	// yet. Treat that as "nothing has changed": there is no tracked revision
	// to be different from, so the changed-file set is empty by definition.
	if !l.repo.HasCommits() {
		return []string{}, nil
	}
	status, err := l.repo.Status()
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
// yields ("" , nil) — the diff is absent, not an error. As git and GitHub
// do, a changed binary file is gitrepo.BinaryDiff, and a changed file with
// a side over gitrepo.MaxDiffBytes is gitrepo.LargeFileDiff: neither is
// line-diffed, and a large file is compared without being held in memory.
func (l *Source) DiffContent(ctx context.Context, path string) (string, error) {
	old, err := l.repo.FileAt(path, "HEAD")
	if err != nil {
		return "", err
	}
	wt, err := l.repo.Worktree()
	if err != nil {
		return "", err
	}
	fsys := wt.Filesystem()
	var oldSize, newSize int64
	if old != nil {
		oldSize = old.Size
	}
	info, err := fsys.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		info = nil
	case err != nil:
		return "", fmt.Errorf("read %s: %w", path, err)
	default:
		newSize = info.Size()
	}

	if max(oldSize, newSize) > gitrepo.MaxDiffBytes {
		open := func() (io.ReadCloser, error) { return fsys.Open(path) }
		same, err := sameContent(old, open, path, info != nil, oldSize, newSize)
		if err != nil || same {
			return "", err
		}
		return gitrepo.LargeFileDiff(path, max(oldSize, newSize)), nil
	}

	var oldContent string
	if old != nil {
		if oldContent, err = old.Contents(); err != nil {
			return "", fmt.Errorf("read blob for %s: %w", path, err)
		}
	}
	newContent, err := l.worktreeContent(path)
	if err != nil {
		return "", err
	}
	if (gitrepo.IsBinary(oldContent) || gitrepo.IsBinary(newContent)) && oldContent != newContent {
		return gitrepo.BinaryDiff(path), nil
	}
	return gitrepo.UnifiedDiff(path, oldContent, newContent), nil
}

// sameContent reports whether the HEAD file old (nil when absent) and the
// working tree file at path (absent unless onDisk; open opens it) hold the
// same bytes, streaming both rather than reading either whole.
func sameContent(old *object.File, open func() (io.ReadCloser, error), path string, onDisk bool, oldSize, newSize int64) (same bool, err error) {
	if old == nil || !onDisk || oldSize != newSize {
		return old == nil && !onDisk, nil
	}
	a, err := old.Reader()
	if err != nil {
		return false, fmt.Errorf("read blob for %s: %w", path, err)
	}
	defer closeInto(&err, a, "blob for "+path)
	b, err := open()
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	defer closeInto(&err, b, path)
	bufA, bufB := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		n, errA := io.ReadFull(a, bufA)
		m, errB := io.ReadFull(b, bufB)
		if n != m || !bytes.Equal(bufA[:n], bufB[:m]) {
			return false, nil
		}
		endA := errors.Is(errA, io.EOF) || errors.Is(errA, io.ErrUnexpectedEOF)
		endB := errors.Is(errB, io.EOF) || errors.Is(errB, io.ErrUnexpectedEOF)
		switch {
		case errA != nil && !endA:
			return false, fmt.Errorf("read blob for %s: %w", path, errA)
		case errB != nil && !endB:
			return false, fmt.Errorf("read %s: %w", path, errB)
		case endA || endB:
			return endA && endB, nil
		}
	}
}

// closeInto closes c, setting *err to the failure when no earlier error
// was set: a read that succeeded is not reported as one that closed
// cleanly when it did not.
func closeInto(err *error, c io.Closer, what string) {
	if cerr := c.Close(); cerr != nil && *err == nil {
		*err = fmt.Errorf("close %s: %w", what, cerr)
	}
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
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	dat, err := io.ReadAll(f)
	closeErr := f.Close()
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close %s: %w", path, closeErr)
	}
	return string(dat), nil
}

func (l *Source) logger() *slog.Logger {
	return logx.OrDefault(l.log)
}

var (
	_ review.Source = (*Source)(nil)
	_ review.Rooted = (*Source)(nil)
)
