package kitfile

import (
	"fmt"
	"strings"
)

type RuleCombineStrategy int

const (
	ruleCombineStrategyUnknown RuleCombineStrategy = iota
	RuleCombineStrategyUnion
	RuleCombineStrategyOverride
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

func (rcs RuleCombineStrategy) String() string {
	if name, ok := ruleCombineStrategyNames[rcs]; ok {
		return name
	}
	return "unknown"
}

func ParseRuleCombineStrategy(text string) (RuleCombineStrategy, error) {
	i, ok := ruleCombineStrategyNameToValue[strings.ToLower(text)]
	if !ok {
		return ruleCombineStrategyUnknown, fmt.Errorf("invalid rule combine strategy: %s", text)
	}
	return i, nil
}

func (rcs RuleCombineStrategy) MarshalText() ([]byte, error) {
	return []byte(rcs.String()), nil
}

func (rcs *RuleCombineStrategy) UnmarshalText(text []byte) (err error) {
	name := string(text)
	*rcs, err = ParseRuleCombineStrategy(name)
	return
}
