package shonin

import (
	"github.com/alphaeron/kit/internal/kitfile"
)

// Fetcher retrieves the review rules that apply to a repository. Like every
// other layer of the pipeline it exposes a single Execute command, so the
// fetch phase is traced and consumed the same way as sift and approve.
type Fetcher interface {
	// Execute runs the fetch phase: it locates every KIT file at HEAD and
	// parses its rules, returning the repository root, a map of KIT file
	// path to its parsed rules, and any error encountered while locating,
	// reading, or parsing KIT files. Implementations must not swallow
	// errors.
	Execute() (string, map[string]kitfile.Rules, error)
}
