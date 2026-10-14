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
func (rl *RuleLayer) Execute(runningRequiredApprovers []string, ctx EvaluationContext, builder *ReviewStateBuilder, file, layer string) {
	for _, rule := range rl.Rules {

		// 1. Process active and valid approvers specifically for this
		// individual rule
		ruleTargets := rl.processRuleApprovers(rule, ctx)

		// Rule Fallback Logic: If pruning or an empty config yields 0 active
		// approvers for this rule, we skip it entirely, which naturally
		// allows it to fall back to the preceding rules.
		if len(ruleTargets) == 0 {
			builder.AddEvent("approve", file, layer, rule.Name, StatusUnresolved, "No active approvers found for rule")
			continue
		}

		// 2. Establish the base set if this is the first valid rule we encounter
		if len(runningRequiredApprovers) == 0 {
			builder.AddEvent("approve", file, layer, rule.Name, StatusActive, "First rule in layer - establishing base approvers")
			runningRequiredApprovers = ruleTargets
			continue
		}

		// 3. Apply the combination strategy specified by *this unique rule*
		// relative to the running required approver footprint accumulated so far.
		combinedApprovers := combineRuleSets(
			// The strategy to combine approvers by.
			rule.CombineStrategy,
			// The "parent/cumulative" state
			runningRequiredApprovers,
			// The "child/incoming" rule state
			ruleTargets,
		)

		// Track the combination operation
		var note string
		switch rule.CombineStrategy {
		case kitfile.RuleCombineStrategyOverride:
			note = "Applied override strategy - existing approvers replaced"
		case kitfile.RuleCombineStrategyUnion:
			note = "Applied union strategy - approvers combined with OR logic"
		case kitfile.RuleCombineStrategyIntersect:
			note = "Applied intersection strategy - approvers combined with AND logic"
		default:
			note = "Applied default strategy"
		}

		builder.AddEvent("approve", file, layer, rule.Name, StatusActive, note)
		runningRequiredApprovers = combinedApprovers
	}
}

// Filters rule approvers based on active states and self-approval flags
func (rl *RuleLayer) processRuleApprovers(rule kitfile.Rule, ctx EvaluationContext) []string {
	var targets []string
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
		targets = append(targets, approverTarget.Target)
	}
	return uniqueStrings(targets)
}

// combineRuleSets evaluates localized rules for combination math per rule block specification.
func combineRuleSets(strat kitfile.RuleCombineStrategy, currentAccumulated []string, incomingRuleTargets []string) []string {
	switch strat {
	case kitfile.RuleCombineStrategyOverride:
		// Discard everything accumulated from higher levels.
		// This rule takes total priority.
		return incomingRuleTargets

	case kitfile.RuleCombineStrategyUnion:
		// Both existing requirements AND this rule's requirements are needed.
		m := make(map[string]bool)
		for _, v := range currentAccumulated {
			m[v] = true
		}
		for _, v := range incomingRuleTargets {
			m[v] = true
		}
		var res []string
		for k := range m {
			res = append(res, k)
		}
		return res

	case kitfile.RuleCombineStrategyIntersect:
		// Only approvers present in BOTH the parent criteria and this rule count.
		m := make(map[string]bool)
		for _, v := range currentAccumulated {
			m[v] = true
		}
		var res []string
		for _, v := range incomingRuleTargets {
			if m[v] {
				res = append(res, v)
			}
		}
		return res

	default:
		// Defaulting to override if a strategy is corrupt or omitted.
		return incomingRuleTargets
	}
}

func uniqueStrings(input []string) []string {
	m := make(map[string]bool)
	var res []string
	for _, v := range input {
		if !m[v] {
			m[v] = true
			res = append(res, v)
		}
	}
	return res
}
