package approval

import (
	"github.com/alphaeron/kit/internal/rulesfile"
)

// CombineStrategy is the pluggable algorithm that merges the accumulated
// rule-target footprint with the incoming set of one rule. Implementing it
// as a real strategy (not a switch inside the state type) lets each combining
// algorithm be tested and evolved in isolation: the state holder keeps the
// data and the event trace, and the strategy only carries the algorithm.
// Unexported interface methods prevent strategies from being defined outside
// this package.
type CombineStrategy interface {
	apply(current, incoming ExecutableTargets) ExecutableTargets
	note() string
	// effect says what the strategy does to the approvers required by the
	// layers above.
	effect() string
}

// OverrideCombineStrategy discards everything accumulated from higher levels:
// this rule's targets take total priority.
type OverrideCombineStrategy struct{}

func (OverrideCombineStrategy) apply(_ ExecutableTargets, incoming ExecutableTargets) ExecutableTargets {
	return incoming
}

func (OverrideCombineStrategy) note() string {
	return "override strategy - existing approvers replaced"
}

func (OverrideCombineStrategy) effect() string {
	return "replaces the approvers required above"
}

// UnionCombineStrategy keeps every target occurrence from every layer so each
// per-layer required_count is satisfied independently. A recurring target
// (e.g. root wants 1, /src wants 2) is appended once per occurrence against
// the same approver pool.
type UnionCombineStrategy struct{}

func (UnionCombineStrategy) apply(current, incoming ExecutableTargets) ExecutableTargets {
	// Allocate a fresh backing array explicitly. Current and incoming may both
	// hold views with spare capacity, so an append-based concatenation must
	// never write into either caller's array.
	combined := make(ExecutableTargets, 0, len(current)+len(incoming))
	combined = append(combined, current...)
	return append(combined, incoming...)
}

func (UnionCombineStrategy) note() string {
	return "union strategy - approvers combined with OR logic"
}

func (UnionCombineStrategy) effect() string {
	return "adds its approvers to those required above"
}

// IntersectCombineStrategy keeps only the targets present in BOTH the
// accumulated footprint and this rule's targets.
type IntersectCombineStrategy struct{}

func (IntersectCombineStrategy) apply(current, incoming ExecutableTargets) ExecutableTargets {
	m := make(map[string]bool, len(current))
	for _, v := range current {
		m[v.Target] = true
	}
	var res ExecutableTargets
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

func (IntersectCombineStrategy) effect() string {
	return "keeps only approvers also required above"
}

// DefaultCombineStrategy applies when a rule's combine_strategy is corrupt or
// omitted. Delegates to override; carries its own note so the event trace
// stays distinguishable.
type DefaultCombineStrategy struct{}

func (d DefaultCombineStrategy) apply(current, incoming ExecutableTargets) ExecutableTargets {
	return OverrideCombineStrategy{}.apply(current, incoming)
}

func (DefaultCombineStrategy) note() string {
	return "default strategy"
}

func (DefaultCombineStrategy) effect() string {
	return OverrideCombineStrategy{}.effect()
}

// newCombineStrategy adapts the configuration value read from a rules file at
// the config-to-algorithm boundary. Corrupt or omitted strategies default to
// override.
func newCombineStrategy(s rulesfile.RuleCombineStrategy) CombineStrategy {
	switch s {
	case rulesfile.RuleCombineStrategyOverride:
		return OverrideCombineStrategy{}
	case rulesfile.RuleCombineStrategyUnion:
		return UnionCombineStrategy{}
	case rulesfile.RuleCombineStrategyIntersect:
		return IntersectCombineStrategy{}
	default:
		return DefaultCombineStrategy{}
	}
}

// CombineStrategyEffect says what a rule with strategy s does to the
// approvers required by the layers above it. The first rule of a chain has
// nothing above it, so it establishes them whatever its strategy.
func CombineStrategyEffect(s rulesfile.RuleCombineStrategy, first bool) string {
	if first {
		return "establishes the required approvers"
	}
	return newCombineStrategy(s).effect()
}
