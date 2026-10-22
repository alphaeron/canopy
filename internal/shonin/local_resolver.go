package shonin

// DefaultApproverResolver is the pass-through resolver for local execution:
// it resolves each approver target to itself, so the target name is what
// gets checked against the current approver set. Local reviews have no
// platform to expand groups against; RemoteApproverResolver is the remote
// counterpart.
type DefaultApproverResolver struct{}

// Resolve returns the target unchanged as its own member set.
// The EvaluationContext has no bearing on the mapping under local execution.
func (r *DefaultApproverResolver) Resolve(target string, ctx EvaluationContext) ([]string, bool) {
	return []string{target}, true
}
