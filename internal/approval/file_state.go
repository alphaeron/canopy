package approval

import "github.com/alphaeron/canopy/internal/rulesfile"

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
	Status     Status
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
// preserving the historical snapshot semantics of RuleState. The target is
// satisfied when Status is StatusSatisfied.
type ApproverTargetState struct {
	rulesfile.ApproverTarget
	// SatisfiedCount is how many approvals count toward the target:
	// len(ResolvedBy).
	SatisfiedCount int
	ResolvedBy     []string // individuals/teams that contributed to satisfaction
	Status         Status
	Reason         string
	// err is why a StatusUnresolved target could not be resolved, kept so
	// the review's Err still matches it with errors.Is.
	err error
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
	// because a target does not resolve.
	OwnershipBroken
)

// Ownership classifies who answers for the file.
func (fs FileState) Ownership() Ownership {
	switch {
	case fs.Status == StatusUnresolved:
		return OwnershipBroken
	case fs.UsedFallback:
		return OwnedByFallback
	}
	return OwnedByRules
}
