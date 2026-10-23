package review

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/parallel"
	"github.com/alphaeron/kit/internal/rulesfile"
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
func Evaluate(ctx context.Context, pr Change, log *slog.Logger) Outcome {
	o := outcome(ctx, pr, log)
	o.Warnings = pr.Warnings()
	for _, w := range o.Warnings {
		log.Warn("review warning", "warning", w)
	}
	return o
}

// outcome is Evaluate's outcome before the change's warnings.
func outcome(ctx context.Context, pr Change, log *slog.Logger) Outcome {
	fail := func(step string, err error) Outcome {
		log.Error("review failed", "step", step, "error", err)
		return Outcome{Verdict: Errored, Summary: failureSummary(ctx, err)}
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
		var proposed []string
		for _, f := range files {
			if rulesfile.IsRulesFile(f) {
				proposed = append(proposed, f)
			}
		}
		violations, filesErr = validateProposed(ctx, pr, proposed)
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
	if len(violations) > 0 {
		for _, p := range slices.Sorted(maps.Keys(violations)) {
			log.Warn("proposed rules file invalid", "path", p, "violations", fmt.Sprint(violations[p]))
		}
		summary := fmt.Sprintf("%d invalid proposed rules files", len(violations))
		if len(violations) == 1 {
			summary = "1 invalid proposed rules file"
		}
		return Outcome{Verdict: Failed, Summary: summary, Items: slices.Sorted(maps.Keys(violations))}
	}
	return OutcomeOf(state)
}

// validateProposed validates the pending rules files as pr proposes them,
// with the same checks as `kit validate`: parse errors and
// rulesfile.Set.Validate. A pending path pr's head does not have (a deleted
// rules file, or the old side of a rename) is skipped. Violations are keyed
// by rules file path, and a valid file is omitted. A file over
// rulesfile.MaxSize is a violation; any other file that cannot be read is
// an operational error.
//
// The rules a review enforces come from the base revision, so this is what
// keeps a pull request from merging a rules file that only fails once it
// takes effect.
func validateProposed(ctx context.Context, pr Change, pending []string) (map[string][]error, error) {
	// Each read is a round trip, so read the files concurrently, then
	// handle them in order.
	heads := make([]struct {
		data []byte
		err  error
	}, len(pending))
	parallel.For(len(pending), parallel.Limit, func(i int) {
		heads[i].data, heads[i].err = pr.ReadHead(ctx, pending[i])
	})

	out := map[string][]error{}
	proposed := rulesfile.Set{}
	for i, p := range pending {
		data, err := heads[i].data, heads[i].err
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if errors.Is(err, rulesfile.ErrTooLarge) {
			out[p] = []error{err}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read proposed rules file %s: %w", p, err)
		}
		rules, err := rulesfile.ParseRules(data)
		if err != nil {
			out[p] = []error{err}
			continue
		}
		proposed[p] = rules
	}
	// A file that failed to parse is not in proposed, so the keys are disjoint.
	for p, errs := range proposed.Validate() {
		out[p] = errs
	}
	return out, nil
}

// failureSummary names why a review could not run, without quoting the
// error, which may carry platform API details.
func failureSummary(ctx context.Context, err error) string {
	if s := LimitSummary(err); s != "" {
		return s
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "review timed out"
	}
	return "review could not run; see the server logs"
}
