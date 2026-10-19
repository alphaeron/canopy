package shonin

import (
	"log/slog"

	"github.com/alphaeron/kit/internal/kitfile"
)

// Rule is the review algorithm's view of a single configured rule. It wraps
// the kitfile rule and owns the rule level of the pipeline: producing the
// rule's approver targets, pruning the targets that can never contribute,
// and executing the rule against the review state to build its RuleState.
// As with every other layer of the pipeline (chain, layer, target) the
// single entry point is Execute and the only state it returns is this
// level's state: RuleState, derived entirely from its target states.
type Rule struct {
	kitfile.Rule
}

// newRule adapts a configured kit-file rule into the algorithm's Rule level,
// the boundary where configuration becomes behavior.
func newRule(rule kitfile.Rule) Rule {
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
// eligible member under the self-approval policy. Targets that cannot be
// resolved to any member, and targets whose only members are inactive or the
// excluded author, are dropped: a rule left with zero active targets skips
// entirely (falling back to earlier rules or the parent directory).
func (r *Rule) Prune(ctx EvaluationContext, resolver ApproverResolver) RuleTargets {
	var kept RuleTargets
	for _, target := range r.Targets() {
		members, ok := resolver.Resolve(target.Target, ctx)
		if !ok {
			continue
		}
		if !target.HasAnyEligibleMember(members, ctx) {
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
	ctx EvaluationContext,
	resolver ApproverResolver,
	file, layer string,
	log *slog.Logger,
) RuleState {
	var approverStates []ApproverTargetState
	var resolvedBy []string

	for _, target := range r.Targets() {
		// Target-level execution carries its own self-approval policy and
		// its own state; the rule only aggregates.
		state := target.Execute(ctx, resolver)
		if state.Status == StatusUnresolved {
			log.Error("approve: could not resolve approver target",
				"component", "rule", "phase", "approve",
				"file", file, "layer", layer, "rule", r.Name,
				"target", target.Target)
		}
		approverStates = append(approverStates, *state)
		if len(state.ResolvedBy) > 0 {
			resolvedBy = append(resolvedBy, state.ResolvedBy...)
		}
	}

	// Determine rule-level status from its approver target states.
	ruleStatus := StatusSatisfied
	for _, at := range approverStates {
		if !at.Satisfied {
			ruleStatus = at.Status
			break
		}
	}

	// The snapshot of the configuration captures what was effectively
	// applied for this iteration: a corrupt or omitted combine_strategy is
	// rewritten to the default (override) behavior so the state always
	// reports what actually ran.
	appliedRule := r.Rule
	appliedRule.CombineStrategy = effectiveCombineStrategy(appliedRule.CombineStrategy)

	return RuleState{
		Rule:       appliedRule,
		Approvers:  approverStates,
		ResolvedBy: resolvedBy,
		Status:     ruleStatus,
	}
}
