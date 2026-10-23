package approval

import "slices"

// ReviewStateBuilder builds the complete review state from the algorithm
// execution. It accumulates the chronological event trace and every error
// encountered during fetch, sift, and approve so that the resulting
// ReviewState exposes the full failure picture.
type ReviewStateBuilder struct {
	// events is the trace in fixed-size blocks: a large review records
	// millions of events, and growing one slice would copy them all again
	// at each growth. Build joins the blocks once.
	events   [][]Event
	errors   []string
	causes   []error // the errors behind errors, for errors.Is on Err
	warnings []string
	files    map[string]FileState
	overall  Status
}

// NewReviewStateBuilder creates a new review state builder.
func NewReviewStateBuilder() *ReviewStateBuilder {
	return &ReviewStateBuilder{}
}

// AddEvent adds an event to the review state. It is a no-op on a nil
// builder, which lets pure helpers such as the sifter operate without a
// trace attached.
func (b *ReviewStateBuilder) AddEvent(phase, path, layer, rule string, status Status, note string) {
	if b == nil {
		return
	}
	if n := len(b.events); n == 0 || len(b.events[n-1]) == cap(b.events[n-1]) {
		b.events = append(b.events, make([]Event, 0, eventBlockSize(n)))
	}
	last := &b.events[len(b.events)-1]
	*last = append(*last, Event{
		Phase:  phase,
		Path:   path,
		Layer:  layer,
		Rule:   rule,
		Status: status,
		Note:   note,
	})
}

// replay records from's events under path, then from's errors, as if
// they had been recorded here.
func (b *ReviewStateBuilder) replay(from *ReviewStateBuilder, path string) {
	if b == nil {
		return
	}
	for _, block := range from.events {
		for _, e := range block {
			b.AddEvent(e.Phase, path, e.Layer, e.Rule, e.Status, e.Note)
		}
	}
	b.errors = append(b.errors, from.errors...)
	b.causes = append(b.causes, from.causes...)
}

// The sizes of the builder's trace blocks: the first, and the largest.
const (
	firstEventBlock     = 64
	eventBlockDoublings = 6
	eventBlock          = firstEventBlock << eventBlockDoublings
)

// eventBlockSize is the capacity of the trace's block n (from 0). Blocks
// double from firstEventBlock up to eventBlock, so a small review allocates
// about what it records.
func eventBlockSize(n int) int {
	return firstEventBlock << min(n, eventBlockDoublings)
}

// AddError records an error encountered during any phase of the review. All
// recorded errors are visible in the final ReviewState (Errors and Err) and
// force the overall approval status to StatusError, so an error anywhere in
// the fetch/sift/approve workflow can never be silently swallowed.
func (b *ReviewStateBuilder) AddError(err error) {
	if b == nil || err == nil {
		return
	}
	b.errors = append(b.errors, err.Error())
	b.causes = append(b.causes, err)
}

// AddWarning records a condition worth reporting that does not change the
// review's status (see ReviewState.Warnings).
func (b *ReviewStateBuilder) AddWarning(w string) {
	b.warnings = append(b.warnings, w)
}

// WithFiles sets the per-file states on the state being built.
func (b *ReviewStateBuilder) WithFiles(files map[string]FileState) *ReviewStateBuilder {
	b.files = files
	return b
}

// WithOverall sets the overall status the algorithm computed.
func (b *ReviewStateBuilder) WithOverall(overall Status) *ReviewStateBuilder {
	b.overall = overall
	return b
}

// Build assembles the review state. When errors have accumulated, the
// overall status is StatusError and Err joins every one of them, so
// errors.Is and errors.As on Err see each cause.
func (b *ReviewStateBuilder) Build() *ReviewState {
	overall := b.overall
	var err error
	if len(b.errors) > 0 {
		overall = StatusError
		err = reviewErrors(b.causes)
	}
	events := slices.Concat(b.events...)
	if events == nil {
		events = []Event{} // an empty trace, never a missing one
	}
	return &ReviewState{
		Files:    b.files,
		Overall:  overall,
		Events:   events,
		Errors:   b.errors,
		Warnings: b.warnings,
		Err:      err,
	}
}
