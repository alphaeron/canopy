package rulesfile

import "cmp"

// RuleKey identifies a rule across a repository (see Rule.Key): the rules
// file that declares it and its 0-based position there. Unlike a Rule it is
// comparable, so it can key a map.
type RuleKey struct {
	RulesFile string
	Position  int
}

// Compare orders keys by rules file (ComparePaths), then position: the
// order the rules are declared and applied in.
func (k RuleKey) Compare(o RuleKey) int {
	return cmp.Or(ComparePaths(k.RulesFile, o.RulesFile), cmp.Compare(k.Position, o.Position))
}
