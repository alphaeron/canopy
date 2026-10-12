package shonin

// EvaluationContext encapsulates the dynamic runtime state of a PR check.
type EvaluationContext struct {
	// Used to enforce or bypass Rule.SelfApprove
	Author string
	// Set of usernames who have signed off on the PR
	CurrentApprovers map[string]bool
	// Dynamic callback to prune inactive owners/approvers
	IsOwnerActive func(string) bool
	// Hard fallback if all matching layers yield empty sets
	RepositoryOwners []string
}

// EvaluationResult represents the final compliance footprint of the rule chain execution.
type EvaluationResult struct {
	IsApproved bool
	// Usernames still missing to satisfy the policy
	RequiredApprovers []string
}
