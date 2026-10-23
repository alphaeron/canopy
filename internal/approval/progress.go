package approval

import "context"

// Progress is how far a running review has come, for showing it live.
type Progress struct {
	// Phase is the review's current phase: "change" (reading the change
	// and its review input), "fetch", "sift", "resolve" (resolving the
	// approver targets), or "approve".
	Phase string
	// Done and Total count the files evaluated in the approve phase; both
	// are 0 in every other phase.
	Done, Total int
}

// progressKey is the context key of a review's progress observer.
type progressKey struct{}

// WithProgress returns ctx with observe told of the progress of the reviews
// run under it, as net/http/httptrace attaches a trace to a request. observe
// is called on the review's goroutine, once per phase and once per file
// approved, so it must be cheap and must not block.
func WithProgress(ctx context.Context, observe func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, observe)
}

// ReportProgress tells ctx's progress observer, if any, of p.
func ReportProgress(ctx context.Context, p Progress) {
	if observe, ok := ctx.Value(progressKey{}).(func(Progress)); ok {
		observe(p)
	}
}
