package approval

import (
	"slices"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// RuleFiles is one rule with every file it applies to.
type RuleFiles struct {
	rulesfile.Rule `json:"rule"`
	// Files is sorted and holds each file once.
	Files []string `json:"files"`
}

// GroupByRule inverts byFile, each file's applicable rules (with RulesFile
// set), into one entry per distinct rule by Rule.Key, sorted by key. A
// pattern declared more than once in one rules file is one entry, under the
// declaration that comes first in a file's list.
func GroupByRule(byFile map[string]rulesfile.Rules) []RuleFiles {
	byKey := map[rulesfile.RuleKey]*RuleFiles{}
	for file, rules := range byFile {
		for _, r := range rules {
			rf, ok := byKey[r.Key()]
			if !ok {
				rf = &RuleFiles{Rule: r}
				byKey[r.Key()] = rf
			}
			// Files are visited one at a time, so a repeat (a key listed
			// twice for one file) is always the last entry.
			if n := len(rf.Files); n == 0 || rf.Files[n-1] != file {
				rf.Files = append(rf.Files, file)
			}
		}
	}
	out := make([]RuleFiles, 0, len(byKey))
	for _, rf := range byKey {
		slices.Sort(rf.Files)
		out = append(out, *rf)
	}
	slices.SortFunc(out, func(a, b RuleFiles) int { return a.Key().Compare(b.Key()) })
	return out
}
