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
	runningRequiredApprovers []kitfile.ApproverTarget,
	ctx EvaluationContext,
	builder *ReviewStateBuilder,
	resolver ApproverResolver,
	file, layer string,
	log *slog.Logger,
) ([]kitfile.ApproverTarget, LayerState) {
	result := runningRequiredApprovers
	state := LayerState{
		DirectoryPath: rl.DirectoryPath,
		Active:        true,
	}

	for _, rule := range rl.Rules {
		// 1. Process active and valid approvers specifically for this
		// individual rule - now working with approver targets that preserve required counts
		ruleTargets := rl.processRuleApproversWithRequiredCount(rule, ctx)

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
		combinedApprovers := combineRuleSets(
			// The strategy to combine approvers by.
			rule.CombineStrategy,
			// The "parent/cumulative" state
			result,
			// The "child/incoming" rule state
			ruleTargets,
			builder,
			file,
			layer,
			rule.Name,
		)

		result = combinedApprovers
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

	for _, target := range rule.Approvers {
		// Prune inactive targets early.
		if !ctx.IsOwnerActive(target.Target) {
			approverStates = append(approverStates, ApproverTargetState{
				Target:         target.Target,
				RequiredCount:  target.RequiredCount,
				SatisfiedCount: 0,
				Satisfied:      false,
				ResolvedBy:     []string{},
				Status:         StatusInactive,
				Reason:         "Target is inactive",
			})
			continue
		}

		// Enforce self-approval constraint.
		if target.Target == ctx.Author && !rule.SelfApprove {
			approverStates = append(approverStates, ApproverTargetState{
				Target:         target.Target,
				RequiredCount:  target.RequiredCount,
				SatisfiedCount: 0,
				Satisfied:      false,
				ResolvedBy:     []string{},
				Status:         StatusInactive,
				Reason:         "Self-approval not allowed for this rule",
			})
			continue
		}

		// Resolve the target to individual members and count how many
		// of those members appear in the current approver set.
		members, ok := resolver.Resolve(target.Target, ctx)
		if !ok {
			log.Error("approve: could not resolve approver target",
				"component", "rule_layer", "phase", "approve",
				"file", file, "layer", layer, "rule", rule.Name,
				"target", target.Target)
			approverStates = append(approverStates, ApproverTargetState{
				Target:         target.Target,
				RequiredCount:  target.RequiredCount,
				SatisfiedCount: 0,
				Satisfied:      false,
				ResolvedBy:     []string{},
				Status:         StatusUnresolved,
				Reason:         "Could not resolve target to members",
			})
			continue
		}

		satisfiedCount := 0
		var resolved []string
		for _, member := range members {
			if ctx.CurrentApprovers[member] {
				satisfiedCount++
				resolved = append(resolved, member)
			}
		}

		satisfied := satisfiedCount >= target.RequiredCount
		st := StatusSatisfied
		if !satisfied {
			st = StatusMissingApprovers
		}

		approverStates = append(approverStates, ApproverTargetState{
			Target:         target.Target,
			RequiredCount:  target.RequiredCount,
			SatisfiedCount: satisfiedCount,
			Satisfied:      satisfied,
			ResolvedBy:     resolved,
			Status:         st,
			Reason:         "",
		})

		if len(resolved) > 0 {
			resolvedBy = append(resolvedBy, resolved...)
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

// Filters rule approvers based on active states and self-approval flags, preserving RequiredCount
func (rl *RuleLayer) processRuleApproversWithRequiredCount(rule kitfile.Rule, ctx EvaluationContext) []kitfile.ApproverTarget {
	var targets []kitfile.ApproverTarget
	for _, approverTarget := range rule.Approvers {
		// Rule Constraint: Prune inactive accounts early
		// TODO Handle group vs. individual users.
		if !ctx.IsOwnerActive(approverTarget.Target) {
			continue
		}
		// Rule Constraint: Handle self-approval checks
		if approverTarget.Target == ctx.Author && !rule.SelfApprove {
			continue
		}
		targets = append(targets, approverTarget)
	}
	return targets
}

// combineRuleSets evaluates localized rules for combination math per rule block specification,
// working with approver targets that include required counts.
func combineRuleSets(strat kitfile.RuleCombineStrategy, currentAccumulated []kitfile.ApproverTarget, incomingRuleTargets []kitfile.ApproverTarget, builder *ReviewStateBuilder, file, layer, ruleName string) []kitfile.ApproverTarget {
	var note string

	switch strat {
	case kitfile.RuleCombineStrategyOverride:
		// Discard everything accumulated from higher levels.
		// This rule takes total priority.
		note = "Applied override strategy - existing approvers replaced"
		builder.AddEvent("approve", file, layer, ruleName, StatusActive, note)
		return incomingRuleTargets

	case kitfile.RuleCombineStrategyUnion:
		// Union keeps every target occurrence from every layer so each
		// per-layer required_count is satisfied independently. A recurring
		// target (e.g. root wants 1, /src wants 2) is counted once per
		// target occurrence against the same approver pool: root would be
		// satisfied while /src still needs one more.
		note := "Applied union strategy - approvers combined with OR logic"
		builder.AddEvent("approve", file, layer, ruleName, StatusActive, note)
		return append(append([]kitfile.ApproverTarget{}, currentAccumulated...), incomingRuleTargets...)

	case kitfile.RuleCombineStrategyIntersect:
		// Only approvers present in BOTH the parent criteria and this rule count.
		m := make(map[string]bool)
		for _, v := range currentAccumulated {
			m[v.Target] = true
		}
		var res []kitfile.ApproverTarget
		for _, v := range incomingRuleTargets {
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
		return incomingRuleTargets
	}
}
