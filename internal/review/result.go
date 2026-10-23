package review

import (
	"cmp"
	"maps"
	"slices"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

// FileMatch pairs one file in the universe with the rules that matched it.
type FileMatch struct {
	// Path is the file path, relative to the repository root.
	Path string `json:"path"`
	// Rules is every rule that matched this file (all layers and all rules
	// from a file's rules file chain), each with its RulesFile set. Empty
	// (encoded as []) when no rule matched.
	Rules rulesfile.Rules `json:"rules"`
	// Diff is the unified diff content when Options.IncludeDiff is set.
	Diff string `json:"diff,omitempty"`
}

// Result is the pure-data outcome of Engine.Match: the file universe
// with per-file rules. All downstream CLI views (by-file, by-rule, diff
// rendering) are projections of this one value.
type Result struct {
	// RepoRoot mirrors the source's repository root.
	RepoRoot string `json:"repo_root,omitempty"`
	// Files is the matched universe in sorted order.
	Files []FileMatch `json:"files"`
}

// Paths lists the result's files in order.
func (r *Result) Paths() []string {
	out := make([]string, len(r.Files))
	for i, fm := range r.Files {
		out[i] = fm.Path
	}
	return out
}

// Covered is how many of the result's files at least one rule matched.
func (r *Result) Covered() int {
	n := 0
	for _, fm := range r.Files {
		if len(fm.Rules) > 0 {
			n++
		}
	}
	return n
}

// FilterScope returns the projection of r onto the rules named name (the
// --scope filter): each file keeps only its rules with that name, and files
// left with none are dropped. Matching is unchanged; this narrows what is
// reported, so views can switch rules without re-running the engine. r is
// not modified, but the returned result shares diff strings with it.
func (r *Result) FilterScope(name string) *Result {
	out := &Result{RepoRoot: r.RepoRoot, Files: []FileMatch{}}
	for _, fm := range r.Files {
		var rules rulesfile.Rules
		for _, rule := range fm.Rules {
			if rule.Name == name {
				rules = append(rules, rule)
			}
		}
		if len(rules) > 0 {
			out.Files = append(out.Files, FileMatch{Path: fm.Path, Rules: rules, Diff: fm.Diff})
		}
	}
	return out
}

// GroupByRule returns one entry per distinct rule (by Rule.Key), each with
// its sorted file list, ordered by name, then key.
func (r *Result) GroupByRule() []approval.RuleFiles {
	byFile := make(map[string]rulesfile.Rules, len(r.Files))
	for _, fm := range r.Files {
		byFile[fm.Path] = fm.Rules
	}
	groups := approval.GroupByRule(byFile)
	// GroupByRule orders by key; a stable sort by name keeps that as the
	// tiebreak.
	slices.SortStableFunc(groups, func(a, b approval.RuleFiles) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return groups
}

// FilesOf returns the sorted, de-duplicated file paths that the rule with
// key matched.
func (r *Result) FilesOf(key rulesfile.RuleKey) []string {
	seen := map[string]bool{}
	for _, fm := range r.Files {
		if slices.ContainsFunc(fm.Rules, func(o rulesfile.Rule) bool { return o.Key() == key }) {
			seen[fm.Path] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// RuleOverlap is a rule that matches some of the same files as another, and
// how many files the two share.
type RuleOverlap struct {
	rulesfile.Rule
	Shared int
}

// Overlaps lists the other rules matching any of the files the rule with
// key matched, with how many of those files each shares, most shared first,
// then by key.
func (r *Result) Overlaps(key rulesfile.RuleKey) []RuleOverlap {
	byKey := map[rulesfile.RuleKey]*RuleOverlap{}
	for _, fm := range r.Files {
		if !slices.ContainsFunc(fm.Rules, func(o rulesfile.Rule) bool { return o.Key() == key }) {
			continue
		}
		for _, o := range fm.Rules {
			ok := o.Key()
			if ok == key {
				continue
			}
			if byKey[ok] == nil {
				byKey[ok] = &RuleOverlap{Rule: o}
			}
			byKey[ok].Shared++
		}
	}
	keys := slices.Collect(maps.Keys(byKey))
	slices.SortFunc(keys, func(a, b rulesfile.RuleKey) int {
		return cmp.Or(cmp.Compare(byKey[b].Shared, byKey[a].Shared), a.Compare(b))
	})
	out := make([]RuleOverlap, len(keys))
	for i, k := range keys {
		out[i] = *byKey[k]
	}
	return out
}
