package review

import (
	"errors"
	"fmt"

	"github.com/alphaeron/kit/internal/approval"
)

// Verdict is what a review means for a change, in the terms every platform
// reports: each hosted platform maps each verdict to one of its status
// states.
type Verdict string

// The verdicts a review can reach.
const (
	// Passed: every file is approved.
	Passed Verdict = "passed"
	// Pending: the review ran cleanly but approvals are still missing.
	Pending Verdict = "pending"
	// Failed: the review ran, and the change itself is unacceptable: it
	// proposes an invalid rules file. Only a change to it fixes that.
	Failed Verdict = "failed"
	// Errored: the review could not produce a valid result
	// (approval.StatusError: a platform failure, a listing too large to
	// evaluate, a skipped or unsatisfiable rule; or the review could not
	// run at all). It is not a judgement of the change.
	Errored Verdict = "errored"
)

// Outcome is a review's verdict with a one-line, human-readable summary,
// the platform-neutral form of the status a hosted platform publishes. The
// summary never carries file contents or credentials.
type Outcome struct {
	Verdict Verdict
	Summary string
}

// OutcomeOf maps a review state to its outcome, so platforms cannot
// disagree on what a review means: StatusSatisfied and StatusDone pass,
// StatusError is Errored, and every other status is pending (approvals are
// still missing or unresolved).
func OutcomeOf(state *approval.ReviewState) Outcome {
	switch state.Overall {
	case approval.StatusSatisfied, approval.StatusDone:
		return Outcome{Verdict: Passed, Summary: fmt.Sprintf("all %d file(s) approved", len(state.Files))}
	case approval.StatusError:
		return Outcome{Verdict: Errored, Summary: errorSummary(state)}
	}
	waiting := 0
	for _, fs := range state.Files {
		if fs.Status != approval.StatusSatisfied {
			waiting++
		}
	}
	return Outcome{Verdict: Pending, Summary: fmt.Sprintf("%d of %d file(s) awaiting approval", waiting, len(state.Files))}
}

// errorSummary names why a review errored without quoting the error, which
// may carry platform API details.
func errorSummary(state *approval.ReviewState) string {
	if s := LimitSummary(state.Err); s != "" {
		return s
	}
	if len(state.Errors) == 1 {
		return "review failed with 1 error"
	}
	return fmt.Sprintf("review failed with %d errors", len(state.Errors))
}

// LimitSummary names the platform listing limit err reports
// (ErrTreeTruncated or ErrFileListIncomplete) for an outcome summary, or ""
// when err reports neither.
func LimitSummary(err error) string {
	switch {
	case errors.Is(err, ErrTreeTruncated):
		return "repository too large to evaluate completely"
	case errors.Is(err, ErrFileListIncomplete):
		return "too many changed files to evaluate completely"
	}
	return ""
}
