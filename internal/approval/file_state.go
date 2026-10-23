package approval

import "github.com/alphaeron/kit/internal/rulesfile"

// FileState represents the state of a single file in the review. Files
// whose rule chains match share their Layers and Required (see
// RuleChain.Execute), so neither may be modified.
type FileState struct {
	Path         string
	Layers       []LayerState
	Required     []ApproverTargetState // final per-target requirement for this file
	Status       Status
	UsedFallback bool
}

// LayerState represents the state of a rule layer in the review.
type LayerState struct {
	DirectoryPath string
	Rules         []RuleState
	// Required is the combined rule-target footprint that results from
	// applying this layer's rules to the incoming footprint. The chain
	// consumes it to feed the next layer down, so the layer's downstream
	// target state travels through the layer state only.
	Required   ExecutableTargets
	ResolvedBy []string // active individuals/teams that contributed
	Active     bool
	Status     Status
	Reason     string
}

// RuleState represents the state of a single rule in the review.
// It embeds the rule's configuration by value - a historical snapshot of the
// rule at the time the algorithm ran for this iteration - so a review state
// can be traced back to the rule that produced it without duplicating fields.
// The embedded copy captures the effective configuration that was actually
// applied, not the raw config: a corrupt or omitted combine_strategy is
// rewritten to the default (override) so the state is accurate about what
// ran, even if the config file later changes.
//
// The Approvers field shadows the embedded config's approver list; where the
// type differs (state, not config), read it directly, and the raw
// configuration target list is still available on Rule.Approvers.
type RuleState struct {
	rulesfile.Rule
	Approvers  []ApproverTargetState
	ResolvedBy []string // individuals/teams that satisfied this rule
	Status     Status
	Reason     string
}

// ApproverTargetState represents the state of an approver target in the
// review. It embeds the approver target's configuration by value,
// preserving the historical snapshot semantics of RuleState.
//
// Satisfied is a convenience boolean for rendering and test assertions; the
// canonical fact of satisfaction is Status == StatusSatisfied, and every
// constructor of this type keeps the two in sync. All status escalation in
// the hierarchy (rule, layer, file status) therefore reads Status, never the
// boolean, so the algorithm has exactly one definition of "unsatisfied":
// Status != StatusSatisfied.
type ApproverTargetState struct {
	rulesfile.ApproverTarget
	SatisfiedCount int
	Satisfied      bool
	ResolvedBy     []string // individuals/teams that contributed to satisfaction
	Status         Status
	Reason         string
}

// Ownership is who answers for a file when no approvals are simulated.
type Ownership int

const (
	// OwnedByRules means the file's rules name its approvers.
	OwnedByRules Ownership = iota
	// OwnedByFallback means no rule names approvers, so the repository
	// owners approve the file.
	OwnedByFallback
	// OwnershipBroken means the file's approvers cannot be determined,
	// because a target does not resolve or its evaluation failed.
	OwnershipBroken
)

// Ownership classifies who answers for the file.
func (fs FileState) Ownership() Ownership {
	switch {
	case fs.Status == StatusUnresolved || fs.Status == StatusError && !fs.UsedFallback:
		return OwnershipBroken
	case fs.UsedFallback:
		return OwnedByFallback
	}
	return OwnedByRules
}
