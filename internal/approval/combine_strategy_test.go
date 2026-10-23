package approval

import (
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

func targetSet(names ...string) ExecutableTargets {
	targets := make(ExecutableTargets, 0, len(names))
	for _, n := range names {
		targets = append(targets, ExecutableTarget{ApproverTarget: approver(n, 1), SelfApprove: true})
	}
	return targets
}

func targetNames(targets ExecutableTargets) []string {
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.Target)
	}
	return names
}

func TestOverrideCombineReplacesAccumulatedTargets(t *testing.T) {
	current, incoming := targetSet("a", "b"), targetSet("c")
	result := overrideStrategy{}.apply(current, incoming)
	if !reflect.DeepEqual(targetNames(result), []string{"c"}) {
		t.Errorf("override should leave only incoming targets, got %v", targetNames(result))
	}
}

func TestUnionCombinePreservesOccurrencesAndBackingArray(t *testing.T) {
	current, incoming := targetSet("a", "b"), targetSet("b", "c")
	result := unionStrategy{}.apply(current, incoming)
	if !reflect.DeepEqual(targetNames(result), []string{"a", "b", "b", "c"}) {
		t.Fatalf("union should preserve every occurrence (incl. recurring b), got %v", targetNames(result))
	}
	// The result carries its own backing array: appending after the combine
	// must not mutate the accumulated footprint.
	_ = append(result, ExecutableTarget{})
	if len(current) != 2 {
		t.Errorf("union must not write into the current backing array, current grew to %d", len(current))
	}
}

func TestIntersectCombineKeepsSharedTargetsOnly(t *testing.T) {
	current, incoming := targetSet("a", "b"), targetSet("b", "c")
	result := intersectStrategy{}.apply(current, incoming)
	if !reflect.DeepEqual(targetNames(result), []string{"b"}) {
		t.Errorf("intersect should leave only the shared target, got %v", targetNames(result))
	}
}

func TestNewCombineStrategyMapsConfigValues(t *testing.T) {
	tests := []struct {
		s    rulesfile.RuleCombineStrategy
		want combineStrategy
	}{
		{rulesfile.RuleCombineStrategy(0), overrideStrategy{}}, // omitted or unknown
		{rulesfile.RuleCombineStrategyOverride, overrideStrategy{}},
		{rulesfile.RuleCombineStrategyUnion, unionStrategy{}},
		{rulesfile.RuleCombineStrategyIntersect, intersectStrategy{}},
	}
	for _, tc := range tests {
		if got := newCombineStrategy(tc.s); got != tc.want {
			t.Errorf("newCombineStrategy(%v) = %T, want %T", tc.s, got, tc.want)
		}
	}
}

func TestCombineStrategyEffect(t *testing.T) {
	tests := []struct {
		s     rulesfile.RuleCombineStrategy
		first bool
		want  string
	}{
		{rulesfile.RuleCombineStrategyIntersect, true, "establishes the required approvers"},
		{rulesfile.RuleCombineStrategyUnion, false, "adds its approvers to those required above"},
		{rulesfile.RuleCombineStrategyIntersect, false, "keeps only approvers also required above"},
		{rulesfile.RuleCombineStrategyOverride, false, "replaces the approvers required above"},
		{rulesfile.RuleCombineStrategy(0), false, "replaces the approvers required above"},
	}
	for _, tc := range tests {
		if got := CombineStrategyEffect(tc.s, tc.first); got != tc.want {
			t.Errorf("CombineStrategyEffect(%v, %v) = %q, want %q", tc.s, tc.first, got, tc.want)
		}
	}
}
