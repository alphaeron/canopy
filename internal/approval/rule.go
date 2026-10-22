package approval

import (
	"context"
	"errors"
	"log/slog"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// Rule is the review algorithm's view of a single configured rule. It wraps
// the rulesfile rule and owns the rule level of the pipeline: producing the
// rule's approver targets, pruning the targets that can never contribute,
// and executing the rule against the review state to build its RuleState.
// As with every other layer of the pipeline (chain, layer, target) the
// single entry point is Execute and the only state it returns is this
// level's state: RuleState, derived entirely from its target states.
type Rule struct {
	rulesfile.Rule
}

// newRule adapts a configured rules-file rule into the algorithm's Rule level,
// the boundary where configuration becomes behavior.
func newRule(rule rulesfile.Rule) Rule {
	return Rule{Rule: rule}
}

// Targets returns the rule's approver targets, each carrying the rule's
// self-approval policy so the policy travels with the target through every
// combine strategy and into every state the targets produce.
func (r *Rule) Targets() RuleTargets {
	targets := make(RuleTargets, 0, len(r.Approvers))
	for _, approver := range r.Approvers {
		targets = append(targets, RuleTarget{
			ApproverTarget: approver,
			SelfApprove:    r.SelfApprove,
		})
	}
	return targets
}

// Prune returns the rule's targets that can count at least one active,
// eligible member under the self-approval policy. Unknown targets
// (ErrUnknownTarget), and targets whose only members are inactive or the
// excluded author, are dropped: a rule left with zero active targets skips
// entirely (falling back to earlier rules or the parent directory). A target
// the platform failed to resolve for any other reason is kept, so the
// failure fails the review instead of silently weakening the requirement.
func (r *Rule) Prune(ctx context.Context, p participants, resolver ApproverResolver) RuleTargets {
	var kept RuleTargets
	for _, target := range r.Targets() {
		members, err := resolver.Resolve(ctx, target.Target)
		if errors.Is(err, ErrUnknownTarget) {
			continue
		}
		if err == nil && !target.HasAnyEligibleMember(members, p) {
			continue
		}
		kept = append(kept, target)
	}
	return kept
}

// Execute evaluates the rule against the review state and returns the rule's
// complete RuleState: each target resolved to its member set and counted
// against its required count, and the rule-level status derived from those
// target states. The rule consumes only target-level execution and the
// review state, nothing above the rule level.
func (r *Rule) Execute(
	ctx context.Context,
	p participants,
	resolver ApproverResolver,
	file, layer string,
	log *slog.Logger,
) RuleState {
	var approverStates []ApproverTargetState
	var resolvedBy []string

	for _, target := range r.Targets() {
		// Target-level execution carries its own self-approval policy and
		// its own state; the rule only aggregates.
		state := target.Execute(ctx, p, resolver)
		if state.Status == StatusUnresolved {
			log.Error("approve: could not resolve approver target",
				"component", "rule", "phase", "approve",
				"file", file, "layer", layer, "rule", r.Name,
				"target", target.Target, "reason", state.Reason)
		}
		approverStates = append(approverStates, *state)
		if len(state.ResolvedBy) > 0 {
			resolvedBy = append(resolvedBy, state.ResolvedBy...)
		}
	}

	// Determine rule-level status from its approver target states: the first
	// unsatisfied target (any status that is not StatusSatisfied) escalates the
	// rule to the target's own status.
	ruleStatus := StatusSatisfied
	if unsat, ok := firstMatching(approverStates, func(at ApproverTargetState) bool { return at.Status != StatusSatisfied }); ok {
		ruleStatus = unsat.Status
	}

	// The snapshot of the configuration captures what was effectively
	// applied for this iteration: a corrupt or omitted combine_strategy is
	// rewritten to the default (override) behavior so the state always
	// reports what actually ran.
	appliedRule := r.Rule
	appliedRule.CombineStrategy = EffectiveCombineStrategy(appliedRule.CombineStrategy)

	return RuleState{
		Rule:       appliedRule,
		Approvers:  approverStates,
		ResolvedBy: resolvedBy,
		Status:     ruleStatus,
	}
}
