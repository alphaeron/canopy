package approval

import (
	"context"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// Fetcher retrieves the review rules that apply to a repository. Like every
// other layer of the pipeline it exposes a single Execute command, so the
// fetch phase is traced and consumed the same way as sift and approve.
type Fetcher interface {
	// Execute runs the fetch phase: it locates every rules file in the
	// platform's tree and parses its rules, returning the rules file set
	// (each repo-relative rules file path to its parsed rules) and any error
	// encountered while locating, reading, or parsing rules files.
	// Implementations must not swallow errors. A set that is nil means no
	// rules could be read; a set with an error holds the rules files that
	// loaded, and the review runs over them (see Evaluator.Execute).
	Execute(ctx context.Context) (rulesfile.Set, error)
}
