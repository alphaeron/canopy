package approval

// PullRequest encapsulates the dynamic runtime state of a PR check. It is
// the single input the approve phase consumes: the author identity, the
// current approval set, the owner-activity predicate, and the repository-owner
// fallback list. It carries no rule or target state of its own; those are
// produced from it by the rule, layer, and chain state levels.
type PullRequest struct {
	// Used to enforce or bypass Rule.SelfApprove.
	Author string
	// Set of usernames who have signed off on the PR.
	CurrentApprovers map[string]bool
	// Dynamic callback to prune inactive owners/approvers.
	IsOwnerActive func(string) bool
	// Hard fallback if all matching layers yield empty sets.
	RepositoryOwners []string
}
