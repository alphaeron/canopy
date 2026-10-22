package approval

import "context"

// DefaultApproverResolver is the pass-through resolver for local execution:
// it resolves each approver target to itself, so the target name is what
// gets checked against the current approver set. Local reviews have no
// platform to expand groups against or to define identity, so names are
// compared exactly; ghplatform.ApproverResolver is the GitHub counterpart.
type DefaultApproverResolver struct{}

// Resolve returns the target unchanged as its own member set.
func (r *DefaultApproverResolver) Resolve(ctx context.Context, target string) ([]string, error) {
	return []string{target}, nil
}

// Canonical returns name unchanged.
func (r *DefaultApproverResolver) Canonical(name string) string { return name }
