package approval

import (
	"context"
	"errors"
	"log/slog"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// ExecutableRule is a rulesfile.Rule as the review algorithm runs it: it
// produces the rule's approver targets, prunes those that can never
// contribute, and evaluates the rule into its RuleState.
type ExecutableRule struct {
	rulesfile.Rule
}

// Targets returns the rule's approver targets, each carrying the rule's
// self-approval policy so the policy travels with the target through every
// combine strategy and into every state the targets produce.
func (r *ExecutableRule) Targets() ExecutableTargets {
	targets := make(ExecutableTargets, 0, len(r.Approvers))
	for _, approver := range r.Approvers {
		targets = append(targets, ExecutableTarget{
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
func (r *ExecutableRule) Prune(ctx context.Context, p participants, resolver ApproverResolver) ExecutableTargets {
	var kept ExecutableTargets
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
func (r *ExecutableRule) Execute(
	ctx context.Context,
	p participants,
	resolver ApproverResolver,
	file, layer string,
	log *slog.Logger,
) RuleState {
	var approverStates []ApproverTargetState
	var resolvedBy []string

	for _, target := range r.Targets() {
		state := target.Execute(ctx, p, resolver)
		if state.Status == StatusUnresolved {
			log.Error("approve: could not resolve approver target",
				"component", "rule", "phase", "approve",
				"file", file, "layer", layer, "rule", r.String(),
				"target", target.Target, "reason", state.Reason)
		}
		approverStates = append(approverStates, *state)
		if len(state.ResolvedBy) > 0 {
			resolvedBy = append(resolvedBy, state.ResolvedBy...)
		}
	}

	// The first unsatisfied target sets the rule's status.
	ruleStatus := StatusSatisfied
	if unsat, ok := firstMatching(approverStates, func(at ApproverTargetState) bool { return at.Status != StatusSatisfied }); ok {
		ruleStatus = unsat.Status
	}

	// The state records the strategy that ran: an omitted one is override.
	appliedRule := r.Rule
	appliedRule.CombineStrategy = appliedRule.CombineStrategy.Effective()

	return RuleState{
		Rule:       appliedRule,
		Approvers:  approverStates,
		ResolvedBy: resolvedBy,
		Status:     ruleStatus,
	}
}
