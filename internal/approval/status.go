package approval

// Status represents the state of a review node in the evaluation tree.
type Status int

const (
	// StatusUnknown is the zero value: no status has been assigned.
	StatusUnknown Status = iota
	// StatusUnresolved means approvers could not be determined, such as
	// when an approver target fails to resolve or no rule applies.
	StatusUnresolved
	// StatusError means evaluation failed or a rule was skipped; it forces
	// the overall review status to StatusError.
	StatusError
	// StatusSatisfied means every requirement for the node is met.
	StatusSatisfied
	// StatusMissingApprovers means at least one requirement still needs
	// approvals.
	StatusMissingApprovers
	// StatusActive marks a step that took effect, such as an applied rule.
	StatusActive
	// StatusInactive marks a target that can never be satisfied, such as
	// the author when self-approval is disallowed.
	StatusInactive
	// StatusDone marks a completed evaluation phase.
	StatusDone
)

// String returns the lowercase name of the status.
func (s Status) String() string {
	switch s {
	case StatusUnknown:
		return "unknown"
	case StatusUnresolved:
		return "unresolved"
	case StatusError:
		return "error"
	case StatusSatisfied:
		return "satisfied"
	case StatusMissingApprovers:
		return "missing_approvers"
	case StatusActive:
		return "active"
	case StatusInactive:
		return "inactive"
	case StatusDone:
		return "done"
	default:
		return "unknown"
	}
}

// firstMatching returns the first element of items for which pred holds, or
// the zero value and false when none do.
func firstMatching[T any](items []T, pred func(T) bool) (T, bool) {
	for _, item := range items {
		if pred(item) {
			return item, true
		}
	}
	var zero T
	return zero, false
}

// attention ranks a file status by how much it needs attention; a lower
// rank needs more: errors, then unresolved targets, then missing approvals,
// then anything else, then satisfied.
func attention(s Status) int {
	switch s {
	case StatusError:
		return 0
	case StatusUnresolved:
		return 1
	case StatusMissingApprovers:
		return 2
	case StatusSatisfied:
		return 4
	default:
		return 3
	}
}
