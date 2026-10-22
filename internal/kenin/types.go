package kenin

import (
	"regexp"
	"sort"
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

// String gives a human-facing reference to the scope, used in TUI rendering.
func (s Scope) String() string {
	out := s.Pattern + " in " + kitFilePathOf(s.Directory)
	if s.Name != "" {
		out += " (" + s.Name + ")"
	}
	return out
}

// Identity returns a stable key for grouping scopes: pattern, layer, and
// KIT file path. Two scopes compare equal by identity when they came from
// the same rule in the same KIT file.
func (s Scope) Identity() string {
	return s.Pattern + "\x00" + s.KITFile + "\x00" + s.Directory
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

// FilesForScope returns the sorted, de-duplicated file paths that have at
// least one scope whose rule name is exactly name.
func (r *MatchResult) FilesForScope(name string) []string {
	return r.filesFor(func(s Scope) bool { return s.Name == name })
}

// FilesForPattern returns the sorted, de-duplicated file paths that have at
// least one scope whose Pattern regex matches the given filter pattern.
// An invalid filter regex yields an empty list — the caller decides whether
// to report that as an error.
func (r *MatchResult) FilesForPattern(pattern string) []string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	return r.filesFor(func(s Scope) bool { return re.MatchString(s.Pattern) })
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

// kitFilePathOf returns the KIT file path (relative to the repo root) that
// registers a rule for a layer directory: "KIT" at the repository root and
// "<dir>/KIT" for any other directory.
func kitFilePathOf(dir string) string {
	if dir == "" {
		return "KIT"
	}
	return dir + "/KIT"
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
