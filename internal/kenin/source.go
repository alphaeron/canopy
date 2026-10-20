package kenin

import (
	"log/slog"

	"github.com/alphaeron/kit/internal/kitfile"
)

// Source is the only seam between the Engine and where the files and rules
// live. LocalSource implements it against a go-git worktree; RemoteSource
// implements it against a pull request (its base tree for AllFiles, its
// changed-files payload for ChangedFiles). The Engine never branches on
// which implementation it is running against.
type Source interface {
	// RepoRoot returns the repository root string. Empty for Remote.
	RepoRoot() string

	// KitFiles loads every KIT file in the source (the local worktree, or
	// the pull request's base tree through shonin.RemoteFetcher) and returns
	// a map from KIT file path (as go-git reports it, relative to the repo
	// root) to its parsed rule list — the exact shape shonin.GetFileRuleChain
	// consumes.
	KitFiles() (map[string]kitfile.Rules, error)

	// AllFiles returns the full file set over which to match and evaluate
	// coverage. Local: all tracked files. Remote: the PR base tree.
	AllFiles() ([]string, error)

	// ChangedFiles returns the subset of AllFiles that are new, modified,
	// or removed relative to the base revision. Local: worktree.diff against
	// HEAD; Remote: the PR payload plus the base tree.
	ChangedFiles() ([]string, error)

	// DiffContent returns the unified diff for a single file (the text of
	// its changes), or "" when the file has no diff in the source's
	// universe. Local: computed from the worktree diff; Remote: the patch
	// field carried by the payload for that file.
	DiffContent(path string) (string, error)
}

// logOrDefault returns the configured logger or the default slog logger. All
// kenin and the internal packages it delegates to (shonin, gitutil) use
// slog.Default() so this indirection is a thin convenience.
func logOrDefault(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
