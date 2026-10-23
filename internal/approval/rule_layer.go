package approval

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// RuleLayer captures the exact directory context where a set of rules matched.
type RuleLayer struct {
	DirectoryPath string // e.g., "src/api"; "" for the repository root
	Rules         []ExecutableRule
}

// RulesFile is the repository path of the rules file that declared the
// layer's rules: rulesfile.PathInDir of the layer's directory.
func (rl *RuleLayer) RulesFile() string {
	return rulesfile.PathInDir(rl.DirectoryPath)
}

// Execute evaluates the rules in this layer in order and returns the layer's
// complete state: the per-rule RuleState breakdown, the per-rule self-approval
// policy, and the combined ExecutableTargets footprint that results from applying
// every rule's combination strategy to the incoming footprint. The rule chain
// consumes only this LayerState: it reads Required to feed the next layer
// and never reaches into the rule or target levels itself.
//
// Each approver target's required_count is checked independently against the
// same pool of current approvers, so a recurring target across layers is
// counted once per target occurrence.
func (rl *RuleLayer) Execute(
	ctx context.Context,
	incoming ExecutableTargets,
	p participants,
	builder *ReviewStateBuilder,
	resolver ApproverResolver,
	file string,
	log *slog.Logger,
) LayerState {
	result := incoming
	state := LayerState{DirectoryPath: rl.DirectoryPath}

	for _, rule := range rl.Rules {
		activeTargets := rule.Prune(ctx, p, resolver)
		ruleState := rule.Execute(ctx, p, resolver, file, rl.DirectoryPath, log)
		state.ResolvedBy = append(state.ResolvedBy, ruleState.ResolvedBy...)

		// A rule left with no active targets is skipped, which is an error:
		// it is recorded on the rule's state and in the review's errors
		// (forcing the overall status to StatusError), while evaluation
		// falls back to the rules before it, the parent directories, or
		// the repository owners.
		if len(activeTargets) == 0 {
			ruleState.Status = StatusError
			ruleState.Reason = "No active targets - rule is skipped"
			state.Rules = append(state.Rules, ruleState)
			skipErr := fmt.Errorf("no active targets after pruning for rule %s", rule)
			builder.AddError(skipErr)
			builder.AddEvent("approve", file, rl.DirectoryPath, rule.String(), StatusError, ruleState.Reason)
			log.Error("approve: rule has no active approvers",
				"component", "rule_layer", "phase", "approve",
				"file", file, "layer", rl.DirectoryPath, "rule", rule.String(),
				"error", skipErr)
			continue
		}
		state.Rules = append(state.Rules, ruleState)

		// The first rule with targets establishes the requirement.
		if len(result) == 0 {
			builder.AddEvent("approve", file, rl.DirectoryPath, rule.String(), StatusActive, "First rule in layer - establishing base approvers")
			result = activeTargets
			continue
		}
		result = result.combine(activeTargets, newCombineStrategy(rule.CombineStrategy), builder, file, rl.DirectoryPath, rule.String())
	}

	// The first rule that is not satisfied sets the layer's status.
	state.Status = StatusSatisfied
	if unsat, ok := firstMatching(state.Rules, func(rs RuleState) bool { return rs.Status != StatusSatisfied }); ok {
		state.Status = unsat.Status
	}
	state.Required = result
	return state
}
