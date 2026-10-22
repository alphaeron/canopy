package approval

import "context"

// DefaultApproverResolver is the pass-through resolver for local execution:
// it resolves each approver target to itself, so the target name is what
// gets checked against the current approver set. Local reviews have no
// platform to expand groups against; ghplatform.ApproverResolver is the GitHub
// counterpart.
type DefaultApproverResolver struct{}

// Resolve returns the target unchanged as its own member set.
// The PullRequest has no bearing on the mapping under local execution.
func (r *DefaultApproverResolver) Resolve(ctx context.Context, target string, pr PullRequest) ([]string, bool) {
	return []string{target}, true
}
