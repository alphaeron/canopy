package approval

import (
	"context"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// RuleTarget pairs an approver target with the self-approval policy of the
// rule that produced it. The policy travels with the target through the
// combine strategies so that an author who is also a resolved member counts
// only toward requirements whose rule allowed self-approval.
type RuleTarget struct {
	rulesfile.ApproverTarget
	SelfApprove bool
}

// RuleTargets is a collection of rule targets. A named slice keeps the
// rule-target-specific state management (combining, counting, eligibility)
// with the rule-target type, so each layer of the algorithm owns its own state
// and types independently.
type RuleTargets []RuleTarget

// Execute evaluates this target against the review state: it resolves the
// target to its member set, enforces the self-approval constraint, and counts
// the eligible members that have approved. The returned state carries that
// evaluation and the resolved members that contributed.
func (rt *RuleTarget) Execute(ctx context.Context, pr PullRequest, resolver ApproverResolver) *ApproverTargetState {
	members, ok := resolver.Resolve(ctx, rt.Target, pr)
	if !ok {
		return &ApproverTargetState{
			ApproverTarget: rt.ApproverTarget,
			ResolvedBy:     []string{},
			Status:         StatusUnresolved,
			Reason:         "Could not resolve target to members",
		}
	}

	// Enforce self-approval constraint against the target identity: when
	// the rule forbids self-approval, a target that IS the author can
	// never be satisfied. The policy decision is the single contributes
	// predicate shared with member-level counting.
	if !rt.contributes(rt.Target, pr) {
		return &ApproverTargetState{
			ApproverTarget: rt.ApproverTarget,
			ResolvedBy:     []string{},
			Status:         StatusInactive,
			Reason:         "Self-approval not allowed for this rule",
		}
	}

	// Prune targets with no eligible member, which covers inactive
	// individuals, groups whose members are all inactive, and groups whose
	// only active member is the author under a non-self-approve rule.
	if !rt.HasAnyEligibleMember(members, pr) {
		return &ApproverTargetState{
			ApproverTarget: rt.ApproverTarget,
			ResolvedBy:     []string{},
			Status:         StatusInactive,
			Reason:         "Target is inactive",
		}
	}

	resolved := rt.approveMembers(members, pr)
	count := len(resolved)
	st := StatusSatisfied
	if count < rt.RequiredCount {
		st = StatusMissingApprovers
	}
	return &ApproverTargetState{
		ApproverTarget: rt.ApproverTarget,
		SatisfiedCount: count,
		Satisfied:      count >= rt.RequiredCount,
		ResolvedBy:     resolved,
		Status:         st,
	}
}

// approveMembers resolves the target to its member set and returns which
// resolved members counted toward it, applying the author / self-approval
// exclusion.
func (rt *RuleTarget) approveMembers(members []string, pr PullRequest) []string {
	var resolved []string
	for _, member := range members {
		if !rt.approves(member, pr) {
			continue
		}
		resolved = append(resolved, member)
	}
	return resolved
}

// approves reports whether a resolved member's approval counts toward this
// target. When the producing rule forbids self-approval, the author never
// contributes, even when they are a member of a resolved group.
func (rt *RuleTarget) approves(member string, pr PullRequest) bool {
	return rt.contributes(member, pr) && pr.CurrentApprovers[member]
}

// contributes is the single implementation of the rule's self-approval
// requirement: it reports whether this author-approved member may count
// toward this target. When the rule forbids self-approval (the default),
// the author's approval never counts, whether the author is the target
// itself or a member of a resolved group. Every self-approval decision in
// the algorithm funnels through this predicate, so the requirement and its
// implementation live on one type - the RuleTarget carrying the policy.
func (rt *RuleTarget) contributes(member string, pr PullRequest) bool {
	return rt.SelfApprove || member != pr.Author
}

// HasAnyEligibleMember reports whether at least one resolved member can
// contribute toward the target: the author is ignored unless the producing
// rule permits self-approval, and inactive members never qualify. When no
// member is eligible the target is pruned from the rule.
func (rt *RuleTarget) HasAnyEligibleMember(members []string, pr PullRequest) bool {
	for _, member := range members {
		if !rt.contributes(member, pr) {
			continue
		}
		if pr.IsOwnerActive(member) {
			return true
		}
	}
	return false
}

// Combine merges this (accumulated) footprint with the set of rule targets
// coming in from this rule using the supplied strategy, and records the
// strategy application in the event trace. Data and its trace stay on this
// state holder; the merging algorithm itself belongs to the CombineStrategy
// implementation so each combining strategy is a standalone, testable
// behavior.
func (current *RuleTargets) Combine(
	incoming RuleTargets,
	strat CombineStrategy,
	builder *ReviewStateBuilder,
	file, layer, ruleName string,
) RuleTargets {
	builder.AddEvent("approve", file, layer, ruleName, StatusActive, "Applied "+strat.note())
	return strat.apply(*current, incoming)
}
