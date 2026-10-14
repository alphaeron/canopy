package shonin

import (
	"github.com/alphaeron/kit/internal/kitfile"
)

// RuleLayer captures the exact directory context where a set of rules matched.
type RuleLayer struct {
	DirectoryPath string // e.g., "/src/api"
	Rules         kitfile.Rules
}

// Execute evaluates an individual rule, to determine whether approval
// conditions are satisfied or not.
func (rl *RuleLayer) Execute(runningRequiredApprovers []kitfile.ApproverTarget, ctx EvaluationContext, builder *ReviewStateBuilder, file, layer string) []kitfile.ApproverTarget {
	result := runningRequiredApprovers

	for _, rule := range rl.Rules {

		// 1. Process active and valid approvers specifically for this
		// individual rule - now working with approver targets that preserve required counts
		ruleTargets := rl.processRuleApproversWithRequiredCount(rule, ctx)

		// Rule Fallback Logic: If pruning or an empty config yields 0 active
		// approvers for this rule, we skip it entirely, which naturally
		// allows it to fall back to the preceding rules.
		if len(ruleTargets) == 0 {
			builder.AddEvent("approve", file, layer, rule.Name, StatusUnresolved, "No active approvers found for rule")
			continue
		}

		// 2. Establish the base set if this is the first valid rule we encounter
		if len(result) == 0 {
			builder.AddEvent("approve", file, layer, rule.Name, StatusActive, "First rule in layer - establishing base approvers")
			result = ruleTargets
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
	}

	return result
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
		// For union, we need to combine targets while preserving their required counts
		// This is a simplified approach - in a real implementation, we'd want to merge
		// approvers with the same target but aggregate required counts properly
		m := make(map[string]kitfile.ApproverTarget)

		// Add all current accumulated targets
		for _, v := range currentAccumulated {
			m[v.Target] = v
		}

		// Add all incoming targets
		for _, v := range incomingRuleTargets {
			m[v.Target] = v
		}

		var res []kitfile.ApproverTarget
		for _, v := range m {
			res = append(res, v)
		}
		note = "Applied union strategy - approvers combined with OR logic"
		builder.AddEvent("approve", file, layer, ruleName, StatusActive, note)
		return res

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
