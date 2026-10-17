package shonin

import (
	"log/slog"

	"github.com/alphaeron/kit/internal/kitfile"
)

// RuleLayer captures the exact directory context where a set of rules matched.
type RuleLayer struct {
	DirectoryPath string // e.g., "/src/api"
	Rules         kitfile.Rules
}

// Execute evaluates the rules in this layer, to determine whether approval
// conditions are satisfied or not. It builds the complete LayerState, including
// its per-rule state tracking (RuleState / ApproverTargetState), so that each
// layer exposes its own satisfaction breakdown for auditability and PR comment
// rendering. Callers consume the LayerState directly rather than assembling
// it themselves.
//
// Each approver target's required_count is checked independently against the
// same pool of current approvers, so a recurring target across layers is
// counted once per target occurrence.
func (rl *RuleLayer) Execute(
	runningRequiredApprovers RuleTargets,
	ctx EvaluationContext,
	builder *ReviewStateBuilder,
	resolver ApproverResolver,
	file, layer string,
	log *slog.Logger,
) (RuleTargets, LayerState) {
	result := runningRequiredApprovers
	state := LayerState{
		DirectoryPath: rl.DirectoryPath,
		Active:        true,
	}

	for _, rule := range rl.Rules {
		// 1. Process active and valid approvers specifically for this
		// individual rule - now working with approver targets that preserve required counts
		ruleTargets := rl.processRuleApproversWithRequiredCount(rule, ctx, resolver)

		// Rule Fallback Logic: If pruning or an empty config yields 0 active
		// approvers for this rule, we skip it entirely, which naturally
		// allows it to fall back to the preceding rules.
		if len(ruleTargets) == 0 {
			builder.AddEvent("approve", file, layer, rule.Name, StatusUnresolved, "No active approvers found for rule")
			log.Debug("approve: rule has no active approvers",
				"component", "rule_layer", "phase", "approve",
				"file", file, "layer", layer, "rule", rule.Name)
			ruleState := buildRuleState(rule, ctx, resolver, file, layer, log)
			state.Rules = append(state.Rules, ruleState)
			state.ResolvedBy = append(state.ResolvedBy, ruleState.ResolvedBy...)
			continue
		}

		// 2. Establish the base set if this is the first valid rule we encounter
		if len(result) == 0 {
			builder.AddEvent("approve", file, layer, rule.Name, StatusActive, "First rule in layer - establishing base approvers")
			result = ruleTargets
			ruleState := buildRuleState(rule, ctx, resolver, file, layer, log)
			state.Rules = append(state.Rules, ruleState)
			state.ResolvedBy = append(state.ResolvedBy, ruleState.ResolvedBy...)
			continue
		}

		// 3. Apply the combination strategy specified by *this unique rule*
		// relative to the running required approver footprint accumulated so far.
		result = result.Combine(
			// The "child/incoming" rule state.
			ruleTargets,
			// The strategy to combine approvers by.
			rule.CombineStrategy,
			builder,
			file,
			layer,
			rule.Name,
		)

		ruleState := buildRuleState(rule, ctx, resolver, file, layer, log)
		state.Rules = append(state.Rules, ruleState)
		state.ResolvedBy = append(state.ResolvedBy, ruleState.ResolvedBy...)
	}

	// Determine layer-level status from its rule states.
	status := StatusSatisfied
	for _, rs := range state.Rules {
		if rs.Status != StatusSatisfied {
			status = rs.Status
			break
		}
	}
	state.Status = status

	return result, state
}

// buildRuleState constructs a detailed RuleState for a single rule, tracking
// per-target satisfaction independently. Each approver target's required_count
// is resolved to its member set and compared against the current approver pool.
func buildRuleState(rule kitfile.Rule, ctx EvaluationContext, resolver ApproverResolver, file, layer string, log *slog.Logger) RuleState {
	var approverStates []ApproverTargetState
	var resolvedBy []string

	for _, approver := range rule.Approvers {
		// The rule target carries the rule's self-approval policy with the target
		// so rule-specific state stays with the rule-target type.
		rt := RuleTarget{ApproverTarget: approver, SelfApprove: rule.SelfApprove}
		// Resolve the target to its member set and count eligible approvals
		// through the rule-target's own Execute.
		state := rt.Execute(ctx, resolver)
		if state.Status == StatusUnresolved {
			log.Error("approve: could not resolve approver target",
				"component", "rule_layer", "phase", "approve",
				"file", file, "layer", layer, "rule", rule.Name,
				"target", rt.Target)
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

	return RuleState{
		Pattern:              rule.Pattern,
		Name:                 rule.Name,
		IgnoreParentDirs:     rule.IgnoreParentDirs,
		CombineStrategy:      rule.CombineStrategy,
		CurrentDirectoryOnly: rule.CurrentDirectoryOnly,
		SelfApprove:          rule.SelfApprove,
		Approvers:            approverStates,
		ResolvedBy:           resolvedBy,
		Status:               ruleStatus,
	}
}

// processRuleApproversWithRequiredCount filters rule approvers by membership
// activity and self-approval constraints, preserving RequiredCount. Targets
// are resolved to their member set via the ApproverResolver, so group targets
// are eligible when any of their resolved members can count toward the rule,
// while individual targets (the default resolver's 1:1 mapping) keep the
// traditional direct activity check.
func (rl *RuleLayer) processRuleApproversWithRequiredCount(rule kitfile.Rule, ctx EvaluationContext, resolver ApproverResolver) RuleTargets {
	var targets RuleTargets
	for _, approver := range rule.Approvers {
		rt := RuleTarget{ApproverTarget: approver, SelfApprove: rule.SelfApprove}
		// A target that cannot be resolved to any member can never satisfy
		// the rule, so it is pruned entirely.
		members, ok := resolver.Resolve(rt.Target, ctx)
		if !ok {
			continue
		}
		// Rule Constraints: prune inactive members and enforce the
		// self-approval constraint against the author.
		if !rt.HasAnyEligibleMember(members, ctx) {
			continue
		}
		targets = append(targets, rt)
	}
	return targets
}
