package rulesfile

import (
	"fmt"
	"strings"
)

// RuleCombineStrategy controls how a rule's approver targets merge with
// those of rules in parent directories.
type RuleCombineStrategy int

const (
	ruleCombineStrategyUnknown RuleCombineStrategy = iota
	// RuleCombineStrategyUnion requires the targets of both rules.
	RuleCombineStrategyUnion
	// RuleCombineStrategyOverride discards the parent rules' targets.
	RuleCombineStrategyOverride
	// RuleCombineStrategyIntersect requires only the targets the rules share.
	RuleCombineStrategyIntersect
	ruleCombineStrategyDone
)

var ruleCombineStrategyNames = map[RuleCombineStrategy]string{
	RuleCombineStrategyUnion:     "union",
	RuleCombineStrategyOverride:  "override",
	RuleCombineStrategyIntersect: "intersect",
}

var ruleCombineStrategyNameToValue = map[string]RuleCombineStrategy{
	"union":     RuleCombineStrategyUnion,
	"override":  RuleCombineStrategyOverride,
	"intersect": RuleCombineStrategyIntersect,
}

// String returns the strategy's name as written in rules files, or
// "unknown" for an invalid value.
func (rcs RuleCombineStrategy) String() string {
	if name, ok := ruleCombineStrategyNames[rcs]; ok {
		return name
	}
	return "unknown"
}

// IsValid reports whether rcs is one of the strategies a rules file can
// name; an omitted or unknown one is not.
func (rcs RuleCombineStrategy) IsValid() bool {
	_, ok := ruleCombineStrategyNames[rcs]
	return ok
}

// Effective is the strategy the approval algorithm applies for rcs: a valid
// strategy as is, and an omitted or unknown one as override.
func (rcs RuleCombineStrategy) Effective() RuleCombineStrategy {
	if rcs.IsValid() {
		return rcs
	}
	return RuleCombineStrategyOverride
}

// RuleCombineStrategies lists every valid strategy, in declaration order.
func RuleCombineStrategies() []RuleCombineStrategy {
	out := make([]RuleCombineStrategy, 0, len(ruleCombineStrategyNames))
	for s := RuleCombineStrategyUnion; s < ruleCombineStrategyDone; s++ {
		out = append(out, s)
	}
	return out
}

// RuleCombineStrategyNames lists every valid strategy's name, in
// declaration order.
func RuleCombineStrategyNames() []string {
	names := make([]string, 0, len(ruleCombineStrategyNames))
	for _, s := range RuleCombineStrategies() {
		names = append(names, s.String())
	}
	return names
}

// ParseRuleCombineStrategy parses a strategy name case-insensitively.
func ParseRuleCombineStrategy(text string) (RuleCombineStrategy, error) {
	i, ok := ruleCombineStrategyNameToValue[strings.ToLower(text)]
	if !ok {
		return ruleCombineStrategyUnknown, fmt.Errorf("invalid rule combine strategy: %s", text)
	}
	return i, nil
}

// MarshalText implements encoding.TextMarshaler.
func (rcs RuleCombineStrategy) MarshalText() ([]byte, error) {
	return []byte(rcs.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (rcs *RuleCombineStrategy) UnmarshalText(text []byte) (err error) {
	name := string(text)
	*rcs, err = ParseRuleCombineStrategy(name)
	return
}
