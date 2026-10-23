package approval

import (
	"context"
	"sync"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// OnceFetcher runs Fetcher once and returns that result, error included, on
// every call; concurrent calls share the one fetch. It suits an evaluator
// over one snapshot of a repository (a pull request's base revision, a
// local Repository), whose rules cannot change under it, so the phases of a
// review, or the views of one page, read the rules once between them. The
// first call's ctx is the one the fetch runs under.
type OnceFetcher struct {
	Fetcher

	once  sync.Once
	rules rulesfile.Set
	err   error
}

// Execute runs the fetch on first use and returns its result.
func (f *OnceFetcher) Execute(ctx context.Context) (rulesfile.Set, error) {
	f.once.Do(func() { f.rules, f.err = f.Fetcher.Execute(ctx) })
	return f.rules, f.err
}
