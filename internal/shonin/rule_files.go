package shonin

import (
	"cmp"
	"slices"

	"github.com/alphaeron/kit/internal/kitfile"
)

// RuleFiles is one rule of a RuleChain with every file whose chain includes
// it: the chain inverted from "which rules apply to this file" to "which
// files does this rule apply to".
type RuleFiles struct {
	// Directory is the layer directory whose KIT file declares the rule.
	Directory string
	Rule      Rule
	// Files is sorted and holds each file once.
	Files []string
}

// ByRule inverts the chain into one entry per distinct rule (by declaring
// KIT file, pattern, and name), sorted by pattern, then KIT file, then name.
func (rc *RuleChain) ByRule() []RuleFiles {
	type key struct{ dir, pattern, name string }
	byKey := map[key]*RuleFiles{}
	for file, layers := range rc.FileChains {
		for _, layer := range layers {
			for _, rule := range layer.Rules {
				k := key{layer.DirectoryPath, rule.Pattern, rule.Name}
				rf, ok := byKey[k]
				if !ok {
					rf = &RuleFiles{Directory: layer.DirectoryPath, Rule: rule}
					byKey[k] = rf
				}
				// Files are visited one at a time, so a repeat (a pattern
				// declared twice in one KIT file) is always the last entry.
				if n := len(rf.Files); n == 0 || rf.Files[n-1] != file {
					rf.Files = append(rf.Files, file)
				}
			}
		}
	}
	out := make([]RuleFiles, 0, len(byKey))
	for _, rf := range byKey {
		slices.Sort(rf.Files)
		out = append(out, *rf)
	}
	slices.SortFunc(out, func(a, b RuleFiles) int {
		return cmp.Or(
			cmp.Compare(a.Rule.Pattern, b.Rule.Pattern),
			cmp.Compare(a.KitFile(), b.KitFile()),
			cmp.Compare(a.Rule.Name, b.Rule.Name),
		)
	})
	return out
}

// KitFile is the KIT file that declares the rule.
func (rf *RuleFiles) KitFile() string {
	return kitfile.PathInDir(rf.Directory)
}
