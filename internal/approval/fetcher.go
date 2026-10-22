package approval

import (
	"context"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// Fetcher retrieves the review rules that apply to a repository. Like every
// other layer of the pipeline it exposes a single Execute command, so the
// fetch phase is traced and consumed the same way as sift and approve.
type Fetcher interface {
	// Execute runs the fetch phase: it locates every rules file in the source
	// (the local working tree, or a remote revision's tree) and parses its
	// rules, returning the repository root, the rules file set (each
	// repo-relative rules file path to its parsed rules), and any error
	// encountered while locating, reading, or parsing rules files.
	// Implementations must not swallow errors.
	Execute(ctx context.Context) (string, rulesfile.Set, error)
}
