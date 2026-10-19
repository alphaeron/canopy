package shonin

import (
	"github.com/alphaeron/kit/internal/kitfile"
)

// RuleTarget pairs an approver target with the self-approval policy of the
// rule that produced it. The policy travels with the target through the
// combine strategies so that an author who is also a resolved member counts
// only toward requirements whose rule allowed self-approval.
type RuleTarget struct {
	kitfile.ApproverTarget
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
func (rt *RuleTarget) Execute(ctx EvaluationContext, resolver ApproverResolver) *ApproverTargetState {
	members, ok := resolver.Resolve(rt.Target, ctx)
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
	if !rt.contributes(rt.Target, ctx) {
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
	if !rt.HasAnyEligibleMember(members, ctx) {
		return &ApproverTargetState{
			ApproverTarget: rt.ApproverTarget,
			ResolvedBy:     []string{},
			Status:         StatusInactive,
			Reason:         "Target is inactive",
		}
	}

	resolved := rt.approveMembers(members, ctx)
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
func (rt *RuleTarget) approveMembers(members []string, ctx EvaluationContext) []string {
	var resolved []string
	for _, member := range members {
		if !rt.approves(member, ctx) {
			continue
		}
		resolved = append(resolved, member)
	}
	return resolved
}

// approves reports whether a resolved member's approval counts toward this
// target. When the producing rule forbids self-approval, the author never
// contributes, even when they are a member of a resolved group.
func (rt *RuleTarget) approves(member string, ctx EvaluationContext) bool {
	return rt.contributes(member, ctx) && ctx.CurrentApprovers[member]
}

// contributes is the single implementation of the rule's self-approval
// requirement: it reports whether this author-approved member may count
// toward this target. When the rule forbids self-approval (the default),
// the author's approval never counts, whether the author is the target
// itself or a member of a resolved group. Every self-approval decision in
// the algorithm funnels through this predicate, so the requirement and its
// implementation live on one type - the RuleTarget carrying the policy.
func (rt *RuleTarget) contributes(member string, ctx EvaluationContext) bool {
	return rt.SelfApprove || member != ctx.Author
}

// HasAnyEligibleMember reports whether at least one resolved member can
// contribute toward the target: the author is ignored unless the producing
// rule permits self-approval, and inactive members never qualify. When no
// member is eligible the target is pruned from the rule.
func (rt *RuleTarget) HasAnyEligibleMember(members []string, ctx EvaluationContext) bool {
	for _, member := range members {
		if !rt.contributes(member, ctx) {
			continue
		}
		if ctx.IsOwnerActive(member) {
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
