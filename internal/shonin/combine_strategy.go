package shonin

import (
	"github.com/alphaeron/kit/internal/kitfile"
)

// CombineStrategy is the pluggable algorithm that merges the accumulated
// rule-target footprint with the incoming set of one rule. Implementing it
// as a real strategy (not a switch inside the state type) lets each combining
// algorithm be tested and evolved in isolation: the state holder keeps the
// data and the event trace, and the strategy only carries the algorithm.
// Unexported interface methods prevent strategies from being defined outside
// this package.
type CombineStrategy interface {
	apply(current, incoming RuleTargets) RuleTargets
	note() string
}

// OverrideCombineStrategy discards everything accumulated from higher levels:
// this rule's targets take total priority.
type OverrideCombineStrategy struct{}

func (OverrideCombineStrategy) apply(_ RuleTargets, incoming RuleTargets) RuleTargets {
	return incoming
}

func (OverrideCombineStrategy) note() string {
	return "override strategy - existing approvers replaced"
}

// UnionCombineStrategy keeps every target occurrence from every layer so each
// per-layer required_count is satisfied independently. A recurring target
// (e.g. root wants 1, /src wants 2) is appended once per occurrence against
// the same approver pool.
type UnionCombineStrategy struct{}

func (UnionCombineStrategy) apply(current, incoming RuleTargets) RuleTargets {
	// Allocate a fresh backing array explicitly. Current and incoming may both
	// hold views with spare capacity, so an append-based concatenation must
	// never write into either caller's array.
	combined := make(RuleTargets, 0, len(current)+len(incoming))
	combined = append(combined, current...)
	return append(combined, incoming...)
}

func (UnionCombineStrategy) note() string {
	return "union strategy - approvers combined with OR logic"
}

// IntersectCombineStrategy keeps only the targets present in BOTH the
// accumulated footprint and this rule's targets.
type IntersectCombineStrategy struct{}

func (IntersectCombineStrategy) apply(current, incoming RuleTargets) RuleTargets {
	m := make(map[string]bool, len(current))
	for _, v := range current {
		m[v.Target] = true
	}
	var res RuleTargets
	for _, v := range incoming {
		if m[v.Target] {
			res = append(res, v)
		}
	}
	return res
}

func (IntersectCombineStrategy) note() string {
	return "intersection strategy - approvers combined with AND logic"
}

// DefaultCombineStrategy applies when a rule's combine_strategy is corrupt or
// omitted. Delegates to override; carries its own note so the event trace
// stays distinguishable.
type DefaultCombineStrategy struct{}

func (d DefaultCombineStrategy) apply(current, incoming RuleTargets) RuleTargets {
	return OverrideCombineStrategy{}.apply(current, incoming)
}

func (DefaultCombineStrategy) note() string {
	return "default strategy"
}

// newCombineStrategy adapts the configuration value read from a KIT file at
// the config-to-algorithm boundary. Corrupt or omitted strategies default to
// override.
func newCombineStrategy(s kitfile.RuleCombineStrategy) CombineStrategy {
	switch s {
	case kitfile.RuleCombineStrategyOverride:
		return OverrideCombineStrategy{}
	case kitfile.RuleCombineStrategyUnion:
		return UnionCombineStrategy{}
	case kitfile.RuleCombineStrategyIntersect:
		return IntersectCombineStrategy{}
	default:
		return DefaultCombineStrategy{}
	}
}

// EffectiveCombineStrategy reports the combine strategy that
// newCombineStrategy would resolve to, expressed as a configuration value:
// valid values pass through unchanged and corrupt or omitted settings map to
// the default (override) behavior. ReviewState snapshots this so the state
// is a historical record of what was actually applied, not a copy of the raw
// configuration file; editors seed from it so re-saving a rule never changes
// who has to approve.
func EffectiveCombineStrategy(s kitfile.RuleCombineStrategy) kitfile.RuleCombineStrategy {
	switch s {
	case kitfile.RuleCombineStrategyUnion,
		kitfile.RuleCombineStrategyOverride,
		kitfile.RuleCombineStrategyIntersect:
		return s
	default:
		return kitfile.RuleCombineStrategyOverride
	}
}
