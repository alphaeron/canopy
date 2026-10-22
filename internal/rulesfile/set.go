package rulesfile

import (
	"maps"
	"path"
	"slices"
	"strings"
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

// ComparePaths orders rules file paths: the repository-root rules file
// first, then by path.
func ComparePaths(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == FileName:
		return -1
	case b == FileName:
		return 1
	}
	return strings.Compare(a, b)
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

// Validate reports each rules file's violations (see File.Violations),
// keyed by path. A file with none is omitted, so an empty map means every
// file in the set is valid.
func (s Set) Validate() map[string][]error {
	out := map[string][]error{}
	for p, rules := range s {
		k := File{Path: p, Rules: rules}
		if errs := k.Violations(); len(errs) > 0 {
			out[p] = errs
		}
	}
	return out
}

// RuleKey identifies a rule across a repository: the rules file that declares
// it and its pattern, which a rules file declares at most once (Validate
// enforces this).
type RuleKey struct {
	RulesFile string
	Pattern   string
}

// Compare orders keys by pattern, then rules file.
func (k RuleKey) Compare(o RuleKey) int {
	if c := strings.Compare(k.Pattern, o.Pattern); c != 0 {
		return c
	}
	return ComparePaths(k.RulesFile, o.RulesFile)
}

// DeclaredRule is a rule together with the rules file that declares it.
type DeclaredRule struct {
	// RulesFile is the repo-relative path of the declaring rules file.
	RulesFile string
	Rule      Rule
}

// Key is the rule's repository-wide identity.
func (d DeclaredRule) Key() RuleKey {
	return RuleKey{RulesFile: d.RulesFile, Pattern: d.Rule.Pattern}
}

// Dir is the directory of the rules file at the repo-relative path p, the
// inverse of PathInDir: "" for the repository-root rules file.
func Dir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}
