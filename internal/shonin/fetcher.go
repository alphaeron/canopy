package shonin

import (
	"github.com/alphaeron/kit/internal/kitfile"
)

// Fetcher retrieves the review rules that apply to a repository.
type Fetcher interface {
	// GetRules returns the repository root, a map of KIT file path to its
	// parsed rules, and any error encountered while locating, reading, or
	// parsing KIT files. Implementations must not swallow errors.
	GetRules() (string, map[string]kitfile.Rules, error)
}
