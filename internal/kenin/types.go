package kenin

import (
	"cmp"
	"maps"
	"slices"
	"sort"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
)

// Scope is one match between a file and a rule, carrying rule identity and
// provenance. Two rules with the same pattern registered in two different
// KIT files are two distinct scopes, because they cover different layers.
type Scope struct {
	// Name is the user-visible rule name from the KIT file.
	Name string `json:"name"`
	// Pattern is the rule's regular expression; the matching identity.
	Pattern string `json:"pattern"`
	// Directory is the layer directory this rule lives in (empty string for
	// a rule registered in the repository-root KIT file).
	Directory string `json:"directory,omitempty"`
	// KITFile is the path of the KIT file that registered the rule: "KIT" at
	// the repository root, "<dir>/KIT" otherwise.
	KITFile string `json:"kit_file"`
}

// newScope is the scope of rule as declared by the KIT file in dir: the
// one place a shonin rule layer becomes a kenin scope.
func newScope(dir string, rule shonin.Rule) Scope {
	return Scope{
		Name:      rule.Name,
		Pattern:   rule.Pattern,
		Directory: dir,
		KITFile:   kitfile.PathInDir(dir),
	}
}

// String gives a human-facing reference to the scope, used in TUI rendering.
func (s Scope) String() string {
	out := s.Pattern + " in " + s.KITFile
	if s.Name != "" {
		out += " (" + s.Name + ")"
	}
	return out
}

// Identity returns a stable key for grouping scopes: the KIT file and the
// pattern, which a KIT file declares at most once. Two scopes compare equal
// by identity when they came from the same rule in the same KIT file.
func (s Scope) Identity() string {
	return s.KITFile + "\x00" + s.Pattern
}

// FileMatch pairs one file in the universe with the scopes that matched it.
type FileMatch struct {
	// Path is the file path, relative to the repository root.
	Path string `json:"path"`
	// Scopes is the list of scopes that matched this file (all layers and
	// all rules from a file's KIT chain). Empty when no scope matched.
	Scopes []Scope `json:"scopes"`
	// Diff is the unified diff content when Options.IncludeDiff is set.
	Diff string `json:"diff,omitempty"`
}

// MatchResult is the pure-data outcome of Engine.Match: the file universe
// with per-file scopes. All downstream CLI views (by-file, by-scope, diff
// rendering) are projections of this one value.
type MatchResult struct {
	// RepoRoot mirrors the source's repository root.
	RepoRoot string `json:"repo_root,omitempty"`
	// Files is the matched universe in sorted order.
	Files []FileMatch `json:"files"`
}

// FilterScope returns the projection of r onto the scope named name: each
// file keeps only its scopes with that name, and files left with none are
// dropped. Matching is unchanged; this narrows what is reported, so views
// can switch scopes without re-running the engine. r is not modified, but
// the returned result shares diff strings with it.
func (r *MatchResult) FilterScope(name string) *MatchResult {
	out := &MatchResult{RepoRoot: r.RepoRoot, Files: []FileMatch{}}
	for _, fm := range r.Files {
		var scopes []Scope
		for _, s := range fm.Scopes {
			if s.Name == name {
				scopes = append(scopes, s)
			}
		}
		if len(scopes) > 0 {
			out.Files = append(out.Files, FileMatch{Path: fm.Path, Scopes: scopes, Diff: fm.Diff})
		}
	}
	return out
}

// GroupByScope groups the result by Scope identity and returns one entry
// per distinct scope, each with its sorted file list. Scopes are listed in
// stable order (by Name, KITFile, Directory).
func (r *MatchResult) GroupByScope() []ScopeGroup {
	byID := map[string]*ScopeGroup{}
	var order []string
	for _, fm := range r.Files {
		for _, s := range fm.Scopes {
			id := s.Identity()
			if _, exists := byID[id]; !exists {
				byID[id] = &ScopeGroup{Scope: s}
				order = append(order, id)
			}
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := byID[order[i]].Scope, byID[order[j]].Scope
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.KITFile != b.KITFile {
			return a.KITFile < b.KITFile
		}
		return a.Directory < b.Directory
	})
	out := make([]ScopeGroup, 0, len(byID))
	for _, id := range order {
		g := byID[id]
		g.Files = r.filesFor(func(s Scope) bool { return s.Identity() == id })
		out = append(out, *g)
	}
	return out
}

// FilesOf returns the sorted, de-duplicated file paths that s (by
// identity) matched.
func (r *MatchResult) FilesOf(s Scope) []string {
	id := s.Identity()
	return r.filesFor(func(o Scope) bool { return o.Identity() == id })
}

// ScopeOverlap is a scope that matches some of the same files as another,
// and how many files the two share.
type ScopeOverlap struct {
	Scope  Scope
	Shared int
}

// Overlaps lists the other scopes matching any of the files s (by identity)
// matched, with how many of those files each shares, most shared first,
// then by identity.
func (r *MatchResult) Overlaps(s Scope) []ScopeOverlap {
	id := s.Identity()
	byID := map[string]*ScopeOverlap{}
	for _, fm := range r.Files {
		if !slices.ContainsFunc(fm.Scopes, func(o Scope) bool { return o.Identity() == id }) {
			continue
		}
		for _, o := range fm.Scopes {
			oid := o.Identity()
			if oid == id {
				continue
			}
			if byID[oid] == nil {
				byID[oid] = &ScopeOverlap{Scope: o}
			}
			byID[oid].Shared++
		}
	}
	ids := slices.Collect(maps.Keys(byID))
	slices.SortFunc(ids, func(a, b string) int {
		return cmp.Or(cmp.Compare(byID[b].Shared, byID[a].Shared), cmp.Compare(a, b))
	})
	out := make([]ScopeOverlap, len(ids))
	for i, oid := range ids {
		out[i] = *byID[oid]
	}
	return out
}

// filesFor returns the sorted, de-duplicated file paths where at least one
// scope satisfies the predicate.
func (r *MatchResult) filesFor(pred func(Scope) bool) []string {
	seen := map[string]bool{}
	for _, fm := range r.Files {
		for _, s := range fm.Scopes {
			if pred(s) {
				seen[fm.Path] = true
				break
			}
		}
	}
	return sortedKeys(seen)
}

// ScopeGroup is one distinct scope plus the files it matched.
type ScopeGroup struct {
	Scope Scope    `json:"scope"`
	Files []string `json:"files"`
}

// FilterScope returns a copy of the report whose per-scope breakdown keeps
// only the scopes named name. Universe, Covered, Percent, and Uncovered still
// describe every rule: narrowing the breakdown does not change what the
// repository's rules cover.
func (r *CoverageReport) FilterScope(name string) *CoverageReport {
	per := make([]ScopeCoverage, 0, len(r.PerScope))
	for _, sc := range r.PerScope {
		if sc.Scope.Name == name {
			per = append(per, sc)
		}
	}
	out := *r
	out.PerScope = per
	return &out
}

// ScopeCoverage is how many files in the universe a single scope matched,
// and what fraction that is of the universe.
type ScopeCoverage struct {
	Scope   Scope   `json:"scope"`
	Files   int     `json:"files"`
	Percent float64 `json:"percent"`
}

// CoverageReport is the pure-data outcome of Engine.Coverage: the file
// universe, the covered count and percentage, the per-scope breakdown, and
// the files matched by no scope.
type CoverageReport struct {
	Universe  int             `json:"universe"`
	Covered   int             `json:"covered"`
	Percent   float64         `json:"percent"`
	PerScope  []ScopeCoverage `json:"per_scope"`
	Uncovered []string        `json:"uncovered"`
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
