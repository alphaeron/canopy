package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// evaluate reviews pr: it lists the changed files, validates the rules
// files among them as pr proposes them, and runs the review over the changed
// files with the platform's review input. An invalid proposed rules file is
// Failed; anything that keeps the review from running is Errored, with its
// cause logged, never published.
func evaluate(ctx context.Context, pr PullRequest, log *slog.Logger) review.Outcome {
	fail := func(step string, err error) review.Outcome {
		log.Error("review failed", "step", step, "error", err)
		return review.Outcome{Verdict: review.Errored, Summary: errorSummary(ctx, err)}
	}

	files, err := pr.ChangedFiles(ctx)
	if err != nil {
		return fail("changed files", err)
	}
	var proposed []string
	for _, f := range files {
		if rulesfile.IsRulesFile(f) {
			proposed = append(proposed, f)
		}
	}
	violations, err := validateProposed(ctx, pr, proposed)
	if err != nil {
		return fail("validate proposed rules files", err)
	}

	in, err := pr.ReviewInput(ctx)
	if err != nil {
		return fail("review input", err)
	}
	state := review.New(pr, log).Review(ctx, files, in)
	if ctx.Err() != nil {
		return fail("review", ctx.Err())
	}
	if len(violations) > 0 {
		for _, p := range slices.Sorted(maps.Keys(violations)) {
			log.Warn("proposed rules file invalid", "path", p, "violations", fmt.Sprint(violations[p]))
		}
		return review.Outcome{Verdict: review.Failed, Summary: "invalid proposed rules file(s): " +
			strings.Join(slices.Sorted(maps.Keys(violations)), ", ")}
	}
	return review.OutcomeOf(state)
}

// validateProposed validates the pending rules files as pr proposes them,
// with the same checks as `kit validate`: parse errors and
// rulesfile.Set.Validate. A pending path pr's head does not have (a deleted
// rules file, or the old side of a rename) is skipped. Violations are keyed
// by rules file path, and a valid file is omitted. A file that cannot be
// read is an operational error, not a violation.
//
// The rules a review enforces come from the base revision, so this is what
// keeps a pull request from merging a rules file that only fails once it
// takes effect.
func validateProposed(ctx context.Context, pr PullRequest, pending []string) (map[string][]error, error) {
	out := map[string][]error{}
	proposed := rulesfile.Set{}
	for _, p := range pending {
		data, err := pr.ReadHead(ctx, p)
		if errors.Is(err, fs.ErrNotExist) {
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

// errorSummary names why a review errored, without quoting the error,
// which may carry platform API details.
func errorSummary(ctx context.Context, err error) string {
	if s := review.LimitSummary(err); s != "" {
		return s
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "review timed out"
	}
	return "review could not run; see the server logs"
}
