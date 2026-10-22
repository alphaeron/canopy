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
	Rules         []Rule
}

// RulesFile is the repository path of the rules file that declared the
// layer's rules: rulesfile.PathInDir of the layer's directory.
func (rl *RuleLayer) RulesFile() string {
	return rulesfile.PathInDir(rl.DirectoryPath)
}

// Execute evaluates the rules in this layer in order and returns the layer's
// complete state: the per-rule RuleState breakdown, the per-rule self-approval
// policy, and the combined RuleTargets footprint that results from applying
// every rule's combination strategy to the incoming footprint. The rule chain
// consumes only this LayerState: it reads Required to feed the next layer
// and never reaches into the rule or target levels itself.
//
// Each approver target's required_count is checked independently against the
// same pool of current approvers, so a recurring target across layers is
// counted once per target occurrence.
func (rl *RuleLayer) Execute(
	ctx context.Context,
	incoming RuleTargets,
	p participants,
	builder *ReviewStateBuilder,
	resolver ApproverResolver,
	file string,
	log *slog.Logger,
) LayerState {
	result := incoming
	state := LayerState{
		DirectoryPath: rl.DirectoryPath,
		Active:        true,
	}

	for _, rule := range rl.Rules {
		// 1. Prune the rule's active and valid approver targets, preserving
		// their required counts. Target-level state stays at the rule level.
		activeTargets := rule.Prune(ctx, p, resolver)

		// 2. Track this rule's per-target satisfaction in the layer state.
		// The rule consumes only its own targets' execution.
		ruleState := rule.Execute(ctx, p, resolver, file, rl.DirectoryPath, log)
		state.Rules = append(state.Rules, ruleState)
		state.ResolvedBy = append(state.ResolvedBy, ruleState.ResolvedBy...)

		// Rule Fallback Logic: If pruning or an empty config yields 0 active
		// approvers for this rule, the rule is skipped — an error condition.
		// The skip is recorded in the review error list (which forces the
		// final review state to statusError) and on the rule's own state so
		// it stays visible in the hierarchical breakdown, while the
		// algorithm still falls back to preceding rules, parent directories,
		// or the repository owners.
		if len(activeTargets) == 0 {
			ruleState.Status = StatusError
			ruleState.Reason = "No active targets - rule is skipped"
			state.Rules[len(state.Rules)-1] = ruleState
			skipErr := fmt.Errorf(
				"no active targets after pruning for rule %q in layer %q",
				rule.Name, rl.DirectoryPath)
			builder.AddError(skipErr)
			builder.AddEvent("approve", file, rl.DirectoryPath, rule.Name, StatusError, "No active targets - rule is skipped")
			log.Error("approve: rule has no active approvers",
				"component", "rule_layer", "phase", "approve",
				"file", file, "layer", rl.DirectoryPath, "rule", rule.Name,
				"error", skipErr)
			continue
		}

		// 3. Establish the base set if this is the first valid rule we
		// encounter in this layer.
		if len(result) == 0 {
			builder.AddEvent("approve", file, rl.DirectoryPath, rule.Name, StatusActive, "First rule in layer - establishing base approvers")
			result = activeTargets
			continue
		}

		// 4. Apply the combination strategy specified by *this rule* relative
		// to the running required approver footprint accumulated so far. The
		// config value adapts into the strategy that the footprint delegates
		// to.
		result = result.Combine(
			// The "child/incoming" rule target set.
			activeTargets,
			// The strategy to combine approvers by.
			newCombineStrategy(rule.CombineStrategy),
			builder,
			file,
			rl.DirectoryPath,
			rule.Name,
		)
	}

	// Determine layer-level status from its rule states: the first non-satisfied
	// rule escalates the layer to that rule's own status.
	status := StatusSatisfied
	if unsat, ok := firstMatching(state.Rules, func(rs RuleState) bool { return rs.Status != StatusSatisfied }); ok {
		status = unsat.Status
	}
	state.Status = status

	// Layer-level state carries the rule-target footprint this layer
	// produced, which the chain consumes to feed the next layer down.
	state.Required = result
	return state
}
