package review

import (
	"context"
	"errors"

	"github.com/alphaeron/kit/internal/approval"
)

// Source is the only seam between the Engine and where the files and rules
// live. Each review platform under internal/platform implements it and
// documents how it answers each method; the Engine never branches on which
// implementation it is running against. platformtest.RunSourceContract
// checks an implementation against this contract.
//
// A source compares two states of a repository: the base revision (what
// the change applies to) and the change itself (a working tree, a pull
// request, a merge request). Every path is repository-relative and
// slash-separated, and every list is sorted.
type Source interface {
	// Evaluator is the source's review platform: how its rules are
	// fetched and how its approver targets resolve. Each source builds its
	// own platform's evaluator, so the engine never chooses a fetcher or
	// resolver itself.
	Evaluator() *approval.Evaluator

	// AllFiles returns every file at the base revision: the universe over
	// which to match and evaluate coverage. A listing the platform
	// truncated is ErrTreeTruncated, never a partial result.
	AllFiles(ctx context.Context) ([]string, error)

	// ChangedFiles returns every path the change adds, modifies, or
	// removes relative to the base revision. A renamed file is listed at
	// both its old and its new path, so the rules over the place it left
	// still apply. Paths the change adds are not in AllFiles, so this is
	// not a subset of it. Empty when nothing changed. A listing the
	// platform cut short is ErrFileListIncomplete.
	ChangedFiles(ctx context.Context) ([]string, error)

	// DiffContent returns the unified diff of the change to path, or ""
	// when the change does not touch path. It may also be "" for a changed
	// path the platform has no text diff for: a binary file, a diff too
	// large for the platform to report, or either side of a rename. The
	// header lines around the hunks are platform-specific.
	DiffContent(ctx context.Context, path string) (string, error)
}

// Rooted is implemented by a Source over an on-disk checkout. The Engine
// reports the root in its results when the source has one; a source on a
// hosted platform has none and does not implement Rooted.
type Rooted interface {
	// RepoRoot returns the on-disk repository root.
	RepoRoot() string
}

// ErrTreeTruncated reports that the platform listed a revision's files only
// partially, as a platform may for a very large repository. The listing is
// never used: a missing rules file would silently drop required
// approvers, so a review over a truncated tree fails.
var ErrTreeTruncated = errors.New("repository tree listing is truncated")

// ErrFileListIncomplete reports that the platform listed a change's files
// only partially, as a platform may past its own limit. The partial listing
// is never presented as complete.
var ErrFileListIncomplete = errors.New("changed file listing is incomplete")
