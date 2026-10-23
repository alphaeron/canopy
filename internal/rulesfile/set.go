package rulesfile

import (
	"maps"
	"slices"
)

// Set is a repository's rules files: each file's rules keyed by its
// repo-relative, slash-separated rules file path (PathInDir of its
// directory). It is what a fetcher loads and what the sifter matches files
// against.
type Set map[string]Rules

// Paths lists the set's rules files in ComparePaths order.
func (s Set) Paths() []string {
	return slices.SortedFunc(maps.Keys(s), ComparePaths)
}

// Names lists the distinct, non-empty rule names across the set, sorted.
func (s Set) Names() []string {
	seen := map[string]bool{}
	for _, rules := range s {
		for _, r := range rules {
			if r.Name != "" {
				seen[r.Name] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// Validate reports each rules file's violations (see Rules.Validate),
// keyed by path. A file with none is omitted, so an empty map means every
// file in the set is valid.
func (s Set) Validate() map[string][]error {
	out := map[string][]error{}
	for p, rules := range s {
		if errs := rules.Validate(); len(errs) > 0 {
			out[p] = errs
		}
	}
	return out
}
