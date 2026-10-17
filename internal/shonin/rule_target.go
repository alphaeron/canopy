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
			Target:        rt.Target,
			RequiredCount: rt.RequiredCount,
			ResolvedBy:    []string{},
			Status:        StatusUnresolved,
			Reason:        "Could not resolve target to members",
		}
	}

	// Enforce self-approval constraint against the target identity.
	if rt.Target == ctx.Author && !rt.SelfApprove {
		return &ApproverTargetState{
			Target:        rt.Target,
			RequiredCount: rt.RequiredCount,
			ResolvedBy:    []string{},
			Status:        StatusInactive,
			Reason:        "Self-approval not allowed for this rule",
		}
	}

	// Prune targets with no eligible member, which covers inactive
	// individuals, groups whose members are all inactive, and groups whose
	// only active member is the author under a non-self-approve rule.
	if !rt.HasAnyEligibleMember(members, ctx) {
		return &ApproverTargetState{
			Target:        rt.Target,
			RequiredCount: rt.RequiredCount,
			ResolvedBy:    []string{},
			Status:        StatusInactive,
			Reason:        "Target is inactive",
		}
	}

	resolved := rt.approveMembers(members, ctx)
	count := len(resolved)
	st := StatusSatisfied
	if count < rt.RequiredCount {
		st = StatusMissingApprovers
	}
	return &ApproverTargetState{
		Target:         rt.Target,
		RequiredCount:  rt.RequiredCount,
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
	if member == ctx.Author && !rt.SelfApprove {
		return false
	}
	return ctx.CurrentApprovers[member]
}

// HasAnyEligibleMember reports whether at least one resolved member can
// contribute toward the target: the author is ignored unless the producing
// rule permits self-approval, and inactive members never qualify. When no
// member is eligible the target is pruned from the rule.
func (rt *RuleTarget) HasAnyEligibleMember(members []string, ctx EvaluationContext) bool {
	for _, member := range members {
		if member == ctx.Author && !rt.SelfApprove {
			continue
		}
		if ctx.IsOwnerActive(member) {
			return true
		}
	}
	return false
}

// Combine applies the rule's combination strategy to this (accumulated) set
// by merging in the incoming target set. It always returns a slice with its
// own backing array so later appends by the caller can never mutate the
// accumulated footprint.
func (current *RuleTargets) Combine(
	incoming RuleTargets,
	strat kitfile.RuleCombineStrategy,
	builder *ReviewStateBuilder,
	file, layer, ruleName string,
) RuleTargets {
	var note string

	switch strat {
	case kitfile.RuleCombineStrategyOverride:
		// Discard everything accumulated from higher levels.
		// This rule takes total priority.
		note = "Applied override strategy - existing approvers replaced"
		builder.AddEvent("approve", file, layer, ruleName, StatusActive, note)
		return incoming

	case kitfile.RuleCombineStrategyUnion:
		// Union keeps every target occurrence from every layer so each
		// per-layer required_count is satisfied independently. A recurring
		// target (e.g. root wants 1, /src wants 2) is counted once per
		// target occurrence against the same approver pool: root would be
		// satisfied while /src still needs one more.
		note := "Applied union strategy - approvers combined with OR logic"
		builder.AddEvent("approve", file, layer, ruleName, StatusActive, note)
		// Allocate a fresh backing array explicitly. *current and incoming
		// may both hold views with spare capacity, so an append-based
		// concatenation must never write into either caller's array.
		combined := make(RuleTargets, 0, len(*current)+len(incoming))
		combined = append(combined, *current...)
		return append(combined, incoming...)

	case kitfile.RuleCombineStrategyIntersect:
		// Only approvers present in BOTH the parent criteria and this rule count.
		m := make(map[string]bool)
		for _, v := range *current {
			m[v.Target] = true
		}
		var res RuleTargets
		for _, v := range incoming {
			if m[v.Target] {
				res = append(res, v)
			}
		}
		note = "Applied intersection strategy - approvers combined with AND logic"
		builder.AddEvent("approve", file, layer, ruleName, StatusActive, note)
		return res

	default:
		// Defaulting to override if a strategy is corrupt or omitted.
		note = "Applied default strategy"
		builder.AddEvent("approve", file, layer, ruleName, StatusActive, note)
		return incoming
	}
}
