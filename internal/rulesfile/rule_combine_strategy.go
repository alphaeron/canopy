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
