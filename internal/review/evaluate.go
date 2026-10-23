package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/alphaeron/kit/internal/approval"
)

// Evaluate reviews pr: it lists the changed files, validates the rules
// files among them as pr proposes them, and runs the review over the changed
// files with the platform's review input. An invalid proposed rules file is
// Failed; anything that keeps the review from running is Errored, with its
// cause logged, never in the outcome. The change's warnings (see
// Change.Warnings) are added to whatever the outcome, and logged.
//
// The changed files, the review input, and the rules are independent, so
// they are read concurrently; the review then reuses the rules fetch (see
// Change).
func Evaluate(ctx context.Context, pr Change, log *slog.Logger) approval.Outcome {
	o := outcome(ctx, pr, log)
	o.Warnings = pr.Warnings()
	for _, w := range o.Warnings {
		log.Warn("review warning", "warning", w)
	}
	return o
}

// outcome is Evaluate's outcome before the change's warnings.
func outcome(ctx context.Context, pr Change, log *slog.Logger) approval.Outcome {
	fail := func(step string, err error) approval.Outcome {
		log.Error("review failed", "step", step, "error", err)
		return approval.Outcome{Verdict: approval.Errored, Summary: failureSummary(ctx, err)}
	}

	var (
		wg         sync.WaitGroup
		files      []string
		violations map[string][]error
		filesStep  string
		filesErr   error
		in         approval.ReviewInput
		inErr      error
	)
	wg.Go(func() {
		files, filesErr = pr.ChangedFiles(ctx)
		if filesErr != nil {
			filesStep = "changed files"
			return
		}
		violations, filesErr = New(pr, log).ValidateProposed(ctx, files)
		if filesErr != nil {
			filesStep = "validate proposed rules files"
		}
	})
	wg.Go(func() { in, inErr = pr.ReviewInput(ctx) })
	// Only warms the platform's shared fetch: the review below makes the
	// same fetch and records its result, error included.
	wg.Go(func() { _, _ = pr.Evaluator().Fetch.Execute(ctx) })
	wg.Wait()

	if filesErr != nil {
		return fail(filesStep, filesErr)
	}
	if inErr != nil {
		return fail("review input", inErr)
	}
	state := New(pr, log).Review(ctx, files, in)
	if ctx.Err() != nil {
		return fail("review", ctx.Err())
	}
	for _, p := range slices.Sorted(maps.Keys(violations)) {
		log.Warn("proposed rules file invalid", "path", p, "violations", fmt.Sprint(violations[p]))
	}
	return OutcomeOf(state, violations)
}

// OutcomeOf is what a review means for a change, on every platform and in
// the CLI alike: a change proposing an invalid rules file (proposed, as
// Engine.ValidateProposed reports it) is Failed whatever its approvals,
// and otherwise the review state decides (approval.ReviewState.Outcome).
func OutcomeOf(state *approval.ReviewState, proposed map[string][]error) approval.Outcome {
	if len(proposed) > 0 {
		return approval.InvalidRulesOutcome(slices.Sorted(maps.Keys(proposed)))
	}
	return state.Outcome()
}

// failureSummary names why a review could not run, without quoting the
// error, which may carry platform API details.
func failureSummary(ctx context.Context, err error) string {
	if s := approval.LimitSummary(err); s != "" {
		return s
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "review timed out"
	}
	return "review could not run; see the server logs"
}
