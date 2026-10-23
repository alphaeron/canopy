package approval

import (
	"context"
	"errors"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// ExecutableTarget pairs an approver target with the self-approval policy of
// the rule that produced it. The policy travels with the target through the
// combine strategies, so an author who is also a resolved member counts only
// toward requirements whose rule allowed self-approval.
type ExecutableTarget struct {
	rulesfile.ApproverTarget
	SelfApprove bool
}

// ExecutableTargets is the targets a rule or a chain of rules requires.
type ExecutableTargets []ExecutableTarget

// Execute evaluates this target against the review state: it resolves the
// target to its member set, enforces the self-approval constraint, and counts
// the eligible members that have approved.
func (rt *ExecutableTarget) Execute(ctx context.Context, p participants, resolver ApproverResolver) *ApproverTargetState {
	members, err := resolver.Resolve(ctx, rt.Target)
	if err != nil {
		return &ApproverTargetState{
			ApproverTarget: rt.ApproverTarget,
			ResolvedBy:     []string{},
			Status:         StatusUnresolved,
			Reason:         resolveFailure(err),
			err:            err,
		}
	}

	// A target that is the author can never count when the rule forbids
	// self-approval.
	if !rt.contributes(p.canonical(rt.Target), p) {
		return &ApproverTargetState{
			ApproverTarget: rt.ApproverTarget,
			ResolvedBy:     []string{},
			Status:         StatusInactive,
			Reason:         "Self-approval not allowed for this rule",
		}
	}

	// No eligible member: inactive individuals, groups whose members are
	// all inactive, and groups whose only active member is the excluded
	// author.
	if !rt.HasAnyEligibleMember(members, p) {
		return &ApproverTargetState{
			ApproverTarget: rt.ApproverTarget,
			ResolvedBy:     []string{},
			Status:         StatusInactive,
			Reason:         "Target is inactive",
		}
	}

	resolved := rt.approvingMembers(members, p)
	st := StatusSatisfied
	if len(resolved) < rt.RequiredCount {
		st = StatusMissingApprovers
	}
	return &ApproverTargetState{
		ApproverTarget: rt.ApproverTarget,
		SatisfiedCount: len(resolved),
		ResolvedBy:     resolved,
		Status:         st,
	}
}

// approvingMembers are the members whose approval counts toward the target:
// they approved, and the self-approval policy lets them count.
func (rt *ExecutableTarget) approvingMembers(members []string, p participants) []string {
	var resolved []string
	for _, member := range members {
		if rt.contributes(member, p) && p.approvers[member] {
			resolved = append(resolved, member)
		}
	}
	return resolved
}

// contributes is the one implementation of the rule's self-approval policy:
// whether member may count toward this target. When the rule forbids
// self-approval (the default) the author never counts, whether the author
// is the target itself or a member of a resolved group.
func (rt *ExecutableTarget) contributes(member string, p participants) bool {
	return rt.SelfApprove || member != p.author
}

// HasAnyEligibleMember reports whether at least one resolved member can
// contribute toward the target: the author is ignored unless the producing
// rule permits self-approval, and inactive members never qualify. When no
// member is eligible the target is pruned from the rule.
func (rt *ExecutableTarget) HasAnyEligibleMember(members []string, p participants) bool {
	for _, member := range members {
		if !rt.contributes(member, p) {
			continue
		}
		if p.isActive(member) {
			return true
		}
	}
	return false
}

// combine merges incoming, the targets of the next rule, into the targets
// accumulated so far with strat, recording the step in the event trace.
func (current ExecutableTargets) combine(
	incoming ExecutableTargets,
	strat combineStrategy,
	builder *ReviewStateBuilder,
	file, layer, rule string,
) ExecutableTargets {
	builder.AddEvent("approve", file, layer, rule, StatusActive, "Applied "+strat.note())
	return strat.apply(current, incoming)
}

// resolveFailure is the reason a target that could not be resolved reports:
// an unknown target, or a platform failure while resolving it.
func resolveFailure(err error) string {
	if errors.Is(err, ErrUnknownTarget) {
		return "Could not resolve target to members: " + err.Error()
	}
	return "Resolving target failed: " + err.Error()
}
