package rulesfile

import (
	"reflect"
	"testing"
)

// TestRuleCombineStrategyEffective pins that an omitted or unknown strategy
// is invalid and applies as override, while a valid one applies as is.
func TestRuleCombineStrategyEffective(t *testing.T) {
	tests := []struct {
		in        RuleCombineStrategy
		valid     bool
		effective RuleCombineStrategy
	}{
		{ruleCombineStrategyUnknown, false, RuleCombineStrategyOverride},
		{ruleCombineStrategyDone, false, RuleCombineStrategyOverride},
		{RuleCombineStrategyUnion, true, RuleCombineStrategyUnion},
		{RuleCombineStrategyOverride, true, RuleCombineStrategyOverride},
		{RuleCombineStrategyIntersect, true, RuleCombineStrategyIntersect},
	}
	for _, tt := range tests {
		if got := tt.in.IsValid(); got != tt.valid {
			t.Errorf("%v.IsValid() = %v, want %v", tt.in, got, tt.valid)
		}
		if got := tt.in.Effective(); got != tt.effective {
			t.Errorf("%v.Effective() = %v, want %v", tt.in, got, tt.effective)
		}
	}
}

func TestRuleCombineStrategyNames(t *testing.T) {
	if got, want := RuleCombineStrategyNames(), []string{"union", "override", "intersect"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names = %v, want %v", got, want)
	}
}

func TestStrategyValidity(t *testing.T) {
	if !RuleCombineStrategyUnion.IsValid() || RuleCombineStrategy(0).IsValid() {
		t.Error("IsValid must hold exactly for the named strategies")
	}
}
