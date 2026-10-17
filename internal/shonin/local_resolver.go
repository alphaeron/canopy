package shonin

// DefaultApproverResolver is the pass-through resolver for local execution:
// it resolves each approver target to itself, preserving the historical
// behavior where the target name is what gets checked against the current
// approver set.
//
// TODO Implement a remote ApproverResolver for GitHub/GitLab that expands
// group targets to their platform members and validates membership through
// the respective API, so group approvals work in the remote/webhook flow.
type DefaultApproverResolver struct{}

// Resolve returns the target unchanged as its own member set.
// The EvaluationContext has no bearing on the mapping under local execution.
func (r *DefaultApproverResolver) Resolve(target string, ctx EvaluationContext) ([]string, bool) {
	return []string{target}, true
}
