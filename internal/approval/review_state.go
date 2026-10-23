package approval

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// ReviewState is the complete, renderable outcome of one review run.
type ReviewState struct {
	// RepoRoot is the on-disk repository root when the review ran over a
	// local checkout, and empty otherwise. The approval pipeline never
	// knows it: the caller that does (review.Engine) sets it.
	RepoRoot string
	Files    map[string]FileState
	Overall  Status
	Events   []Event  // chronological trace across fetch, sift, and approve
	Errors   []string // every error encountered, so none are invisible
	// Warnings are conditions worth reporting that do not change the
	// status, such as files no rule covers.
	Warnings []string
	Err      error // every error in Errors, joined
}

// Event represents a step in the review process for auditability.
type Event struct {
	Phase  string // "fetch", "sift", or "approve"
	Path   string
	Layer  string // the layer's directory; "" for the root or no layer
	Rule   string // the rule, as Rule.String names it
	Status Status
	Note   string // e.g. "rule .*\.go combined via union; required += backend-team"
}

// reviewErrors is the errors a review accumulated, as one error that
// errors.Is and errors.As see through to each of them.
type reviewErrors []error

func (e reviewErrors) Error() string {
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return fmt.Sprintf("%d error(s) during review: %s", len(e), strings.Join(msgs, "; "))
}

func (e reviewErrors) Unwrap() []error { return e }

// SortedFiles lists the review's files, most in need of attention first,
// then by path.
func (rs *ReviewState) SortedFiles() []FileState {
	out := slices.Collect(maps.Values(rs.Files))
	slices.SortFunc(out, func(a, b FileState) int {
		return cmp.Or(cmp.Compare(attention(a.Status), attention(b.Status)), cmp.Compare(a.Path, b.Path))
	})
	return out
}

// RequiredTargets is every approver target the review's files require, each
// with the most approvals any one file needs from it, sorted by target, and
// how many files fall back to the repository owners instead.
func (rs *ReviewState) RequiredTargets() (targets rulesfile.ApproverTargets, fallback int) {
	need := map[string]int{}
	for _, fs := range rs.Files {
		if fs.UsedFallback {
			fallback++
			continue
		}
		for _, r := range fs.Required {
			need[r.Target] = max(need[r.Target], r.RequiredCount)
		}
	}
	for _, t := range slices.Sorted(maps.Keys(need)) {
		targets = append(targets, rulesfile.ApproverTarget{Target: t, RequiredCount: need[t]})
	}
	return targets, fallback
}

// StatusCounts is how many of the review's files have each status.
func (rs *ReviewState) StatusCounts() map[Status]int {
	counts := map[Status]int{}
	for _, fs := range rs.Files {
		counts[fs.Status]++
	}
	return counts
}

// FileEvents are the review's events about path, in order.
func (rs *ReviewState) FileEvents(path string) []Event {
	var out []Event
	for _, e := range rs.Events {
		if e.Path == path {
			out = append(out, e)
		}
	}
	return out
}

// Outcome maps the review state to its outcome, so platforms (and the CLI)
// cannot disagree on what a review means: StatusSatisfied and StatusDone pass,
// StatusError is Errored, and every other status is pending (approvals are
// still missing or unresolved). A pending outcome's items are the approvers
// still needed, each with how many more approvals it owes the file that
// needs the most. The state's warnings are the outcome's.
func (rs *ReviewState) Outcome() Outcome {
	o := rs.verdict()
	o.Warnings = slices.Clone(rs.Warnings)
	return o
}

// verdict is Outcome without the warnings.
func (rs *ReviewState) verdict() Outcome {
	switch rs.Overall {
	case StatusSatisfied, StatusDone:
		n := len(rs.Files)
		if n == 0 {
			return Outcome{Verdict: Passed, Summary: "no files to approve"}
		}
		if n == 1 {
			return Outcome{Verdict: Passed, Summary: "1 file approved"}
		}
		return Outcome{Verdict: Passed, Summary: fmt.Sprintf("all %d files approved", n)}
	case StatusError:
		return Outcome{Verdict: Errored, Summary: errorSummary(rs)}
	}
	waiting := 0
	owed := map[string]int{}
	for _, fs := range rs.Files {
		if fs.Status == StatusSatisfied {
			continue
		}
		waiting++
		for _, r := range fs.Required {
			if r.Status != StatusSatisfied {
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
	summary := fmt.Sprintf("%d of %d files await approval", waiting, len(rs.Files))
	if waiting == 1 {
		summary = fmt.Sprintf("1 of %d files awaits approval", len(rs.Files))
		if len(rs.Files) == 1 {
			summary = "1 file awaits approval"
		}
	}
	return Outcome{Verdict: Pending, Summary: summary, Items: items}
}

// ReviewReport is a review with what it means for the change: its state and
// its outcome, which may differ from the state's own (see
// review.OutcomeOf). It encodes as the review's JSON with an "outcome".
type ReviewReport struct {
	State   *ReviewState
	Outcome Outcome
}

// MarshalJSON encodes the report as ReviewState.MarshalJSON does, adding
// the outcome.
func (r ReviewReport) MarshalJSON() ([]byte, error) {
	out := reviewJSONOf(r.State)
	o := outcomeJSONOf(r.Outcome)
	out.Outcome = &o
	return json.Marshal(out)
}

// MarshalJSON encodes the review in its stable wire form: statuses as their
// snake_case names, files in SortedFiles order, each layer named by its rules
// file, and every list present (never null) so consumers need no nil checks.
// The CLI's --json output and any API response share this one encoding.
func (rs *ReviewState) MarshalJSON() ([]byte, error) {
	return json.Marshal(reviewJSONOf(rs))
}
