package kitfile

import (
	"maps"
	"path"
	"slices"
	"strings"
)

// Set is a repository's KIT files: each file's rules keyed by its
// repo-relative, slash-separated KIT file path ("KIT" at the root,
// "<dir>/KIT" otherwise). It is what a fetcher loads and what the sifter
// matches files against.
type Set map[string]Rules

// Paths lists the set's KIT files in ComparePaths order.
func (s Set) Paths() []string {
	return slices.SortedFunc(maps.Keys(s), ComparePaths)
}

// ComparePaths orders KIT file paths: the repository-root KIT first, then
// by path.
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

// Validate reports each KIT file's violations (see KitFile.Violations),
// keyed by path. A file with none is omitted, so an empty map means every
// file in the set is valid.
func (s Set) Validate() map[string][]error {
	out := map[string][]error{}
	for p, rules := range s {
		k := KitFile{Path: p, Rules: rules}
		if errs := k.Violations(); len(errs) > 0 {
			out[p] = errs
		}
	}
	return out
}

// RuleKey identifies a rule across a repository: the KIT file that declares
// it and its pattern, which a KIT file declares at most once (Validate
// enforces this).
type RuleKey struct {
	KitFile string
	Pattern string
}

// Compare orders keys by pattern, then KIT file.
func (k RuleKey) Compare(o RuleKey) int {
	if c := strings.Compare(k.Pattern, o.Pattern); c != 0 {
		return c
	}
	return ComparePaths(k.KitFile, o.KitFile)
}

// DeclaredRule is a rule together with the KIT file that declares it.
type DeclaredRule struct {
	// KitFile is the repo-relative path of the declaring KIT file.
	KitFile string
	Rule    Rule
}

// Key is the rule's repository-wide identity.
func (d DeclaredRule) Key() RuleKey {
	return RuleKey{KitFile: d.KitFile, Pattern: d.Rule.Pattern}
}

// Dir is the directory of the KIT file at the repo-relative path p, the
// inverse of PathInDir: "" for the repository-root KIT file.
func Dir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}
