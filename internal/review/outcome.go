package review

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

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
// the platform-neutral form of the status a hosted platform publishes. No
// part of it carries file contents or credentials.
type Outcome struct {
	Verdict Verdict
	// Summary is the outcome's headline, e.g. "3 of 12 files await
	// approval". It names its own counts, so it stands without Items.
	Summary string
	// Items are what the summary is about (the invalid rules files, or the
	// approvers still needed), listed after it as space allows.
	Items []string
	// Warnings are conditions worth a reader's attention that do not change
	// the verdict, such as a pull request so large the platform listed its
	// files another way.
	Warnings []string
}

// Description is the outcome in one line of at most limit bytes (no limit
// when limit <= 0), as a platform publishes it: the summary, any warnings,
// and as many items as fit, e.g.
//
//	3 of 12 files await approval (org/backend ×2, alice +3 more) · warning: large pull request (4012 files)
//
// The summary always comes first. Then the warnings, shortened to a count
// when they do not fit whole, since they say something the summary does
// not; then the items, which the summary already counts. Only a summary
// too long on its own is cut, with "…".
func (o Outcome) Description(limit int) string {
	if limit <= 0 {
		limit = math.MaxInt
	}
	if len(o.Summary) > limit {
		return cut(o.Summary, limit)
	}
	warning := ""
	if len(o.Warnings) > 0 {
		full := " · warning: " + strings.Join(o.Warnings, "; ")
		short := " · " + plural(len(o.Warnings), "warning")
		switch {
		case len(o.Summary)+len(full) <= limit:
			warning = full
		case len(o.Summary)+len(short) <= limit:
			warning = short
		}
	}
	return o.Summary + listWithin(o.Items, limit-len(o.Summary)-len(warning)) + warning
}

// listWithin is " (a, b +3 more)": as many of items as fit in budget bytes,
// with how many more there are, or "" when none fit.
func listWithin(items []string, budget int) string {
	for n := len(items); n > 0; n-- {
		list := " (" + strings.Join(items[:n], ", ")
		if more := len(items) - n; more > 0 {
			list += fmt.Sprintf(" +%d more", more)
		}
		if list += ")"; len(list) <= budget {
			return list
		}
	}
	return ""
}

// cut is s in at most limit bytes, ending "…" when cut, without splitting a
// UTF-8 sequence.
func cut(s string, limit int) string {
	const ellipsis = "…"
	if len(s) <= limit {
		return s
	}
	n := max(limit-len(ellipsis), 0)
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + ellipsis
}

// plural is "1 file" or "n files".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// OutcomeOf maps a review state to its outcome, so platforms cannot
// disagree on what a review means: StatusSatisfied and StatusDone pass,
// StatusError is Errored, and every other status is pending (approvals are
// still missing or unresolved). A pending outcome's items are the approvers
// still needed, each with how many more approvals it owes the file that
// needs the most.
func OutcomeOf(state *approval.ReviewState) Outcome {
	switch state.Overall {
	case approval.StatusSatisfied, approval.StatusDone:
		n := len(state.Files)
		if n == 0 {
			return Outcome{Verdict: Passed, Summary: "no files to approve"}
		}
		if n == 1 {
			return Outcome{Verdict: Passed, Summary: "1 file approved"}
		}
		return Outcome{Verdict: Passed, Summary: fmt.Sprintf("all %d files approved", n)}
	case approval.StatusError:
		return Outcome{Verdict: Errored, Summary: errorSummary(state)}
	}
	waiting := 0
	owed := map[string]int{}
	for _, fs := range state.Files {
		if fs.Status == approval.StatusSatisfied {
			continue
		}
		waiting++
		for _, r := range fs.Required {
			if !r.Satisfied {
				owed[r.Target] = max(owed[r.Target], r.RequiredCount-r.SatisfiedCount)
			}
		}
	}
	var items []string
	for _, t := range slices.Sorted(maps.Keys(owed)) {
		if owed[t] > 1 {
			items = append(items, fmt.Sprintf("%s ×%d", t, owed[t]))
		} else {
			items = append(items, t)
		}
	}
	summary := fmt.Sprintf("%d of %d files await approval", waiting, len(state.Files))
	if waiting == 1 {
		summary = fmt.Sprintf("1 of %d files awaits approval", len(state.Files))
		if len(state.Files) == 1 {
			summary = "1 file awaits approval"
		}
	}
	return Outcome{Verdict: Pending, Summary: summary, Items: items}
}

// errorSummary names why a review errored without quoting the error, which
// may carry platform API details.
func errorSummary(state *approval.ReviewState) string {
	if s := LimitSummary(state.Err); s != "" {
		return s
	}
	return "review failed with " + plural(len(state.Errors), "error")
}

// LimitSummary names the platform limit err reports (ErrTreeTruncated,
// ErrFileListIncomplete, or ErrRateLimited) for an outcome summary, or ""
// when err reports none.
func LimitSummary(err error) string {
	switch {
	case errors.Is(err, ErrTreeTruncated):
		return "repository too large to evaluate completely"
	case errors.Is(err, ErrFileListIncomplete):
		return "too many changed files to evaluate completely"
	case errors.Is(err, ErrRateLimited):
		return "rate limited by the platform; try again later"
	}
	return ""
}
