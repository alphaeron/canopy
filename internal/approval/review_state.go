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
	Err      error    // surfaced, not swallowed
}

// Event represents a step in the review process for auditability.
type Event struct {
	Phase  string // "sift" | "approve"
	Path   string
	Layer  string
	Rule   string
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

// MarshalJSON encodes the review in its stable wire form: statuses as their
// snake_case names, files in SortedFiles order, each layer named by its rules
// file, and every list present (never null) so consumers need no nil checks.
// The CLI's --json output and any API response share this one encoding.
func (rs *ReviewState) MarshalJSON() ([]byte, error) {
	return json.Marshal(reviewJSONOf(rs))
}
