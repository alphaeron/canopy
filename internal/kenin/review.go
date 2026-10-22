package kenin

import (
	"github.com/alphaeron/kit/internal/shonin"
)

// ReviewInput is the pull-request state a review evaluates files against:
// who wrote the change, who has approved it, who is unavailable, and the
// repository owners who decide files no rule covers.
type ReviewInput struct {
	Author    string
	Approvers []string
	Inactive  []string
	Owners    []string
}

// evaluationContext is the input as the evaluation context shonin approves against.
func (in ReviewInput) evaluationContext() shonin.EvaluationContext {
	inactive := setOf(in.Inactive)
	return shonin.EvaluationContext{
		Author:           in.Author,
		CurrentApprovers: setOf(in.Approvers),
		IsOwnerActive:    func(u string) bool { return !inactive[u] },
		RepositoryOwners: in.Owners,
	}
}

// Review runs the full shonin review (fetch, sift, approve) over files on
// the source's platform, so rules and approver resolution are local or
// remote together. Every error the review meets is in the returned state
// (see shonin.Shonin.Execute).
func (e *Engine) Review(files []string, in ReviewInput) *shonin.ReviewState {
	return e.Source.Reviewer().Execute(in.evaluationContext(), files)
}

// setOf is the set of items, for membership tests.
func setOf(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, it := range items {
		set[it] = true
	}
	return set
}
