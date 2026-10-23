package approval

import (
	"github.com/alphaeron/kit/internal/rulesfile"
)

// combineStrategy merges the targets accumulated from the rules above with
// the targets of the next rule, and describes itself for the event trace and
// for people.
type combineStrategy interface {
	apply(current, incoming ExecutableTargets) ExecutableTargets
	note() string
	// effect says what the strategy does to the approvers required by the
	// layers above.
	effect() string
}

// overrideStrategy discards the accumulated targets for the incoming ones.
type overrideStrategy struct{}

func (overrideStrategy) apply(_, incoming ExecutableTargets) ExecutableTargets { return incoming }

func (overrideStrategy) note() string { return "override strategy - existing approvers replaced" }

func (overrideStrategy) effect() string { return "replaces the approvers required above" }

// unionStrategy keeps every target occurrence, so each occurrence's
// required_count is satisfied independently against the same approvers.
type unionStrategy struct{}

func (unionStrategy) apply(current, incoming ExecutableTargets) ExecutableTargets {
	// A fresh backing array: current and incoming may have spare capacity
	// that other chains share.
	combined := make(ExecutableTargets, 0, len(current)+len(incoming))
	combined = append(combined, current...)
	return append(combined, incoming...)
}

func (unionStrategy) note() string { return "union strategy - approvers combined with OR logic" }

func (unionStrategy) effect() string { return "adds its approvers to those required above" }

// intersectStrategy keeps only the incoming targets also accumulated above.
type intersectStrategy struct{}

func (intersectStrategy) apply(current, incoming ExecutableTargets) ExecutableTargets {
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

func (intersectStrategy) note() string {
	return "intersection strategy - approvers combined with AND logic"
}

func (intersectStrategy) effect() string { return "keeps only approvers also required above" }

// newCombineStrategy is the strategy the algorithm applies for s: an omitted
// or unknown one is override (rulesfile.RuleCombineStrategy.Effective).
func newCombineStrategy(s rulesfile.RuleCombineStrategy) combineStrategy {
	switch s.Effective() {
	case rulesfile.RuleCombineStrategyUnion:
		return unionStrategy{}
	case rulesfile.RuleCombineStrategyIntersect:
		return intersectStrategy{}
	default:
		return overrideStrategy{}
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
