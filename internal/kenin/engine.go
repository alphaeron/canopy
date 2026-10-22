package kenin

import (
	"errors"
	"fmt"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
)

// Match runs the shonin sifter (GetFileRuleChain) across the file universe
// selected by opts, then inverts the chain into a per-file scope mapping.
// Without a scope filter every file in the universe is in the result, and a
// file no rule matches has an empty scope list, so the caller sees the whole
// universe.
//
// Options.ScopeName narrows the result after matching (see
// MatchResult.FilterScope): the chain is always computed from every rule,
// because a file's effective rules depend on every layer above it, so the
// filter changes what is reported, never what matches. A name that no KIT
// file declares is ErrUnknownScope; a declared scope that matches nothing
// yields an empty result. Options.IncludeDiff attaches diffs to the files
// in the result only, so a scope filter also skips fetching diffs for the
// files it drops. Errors from the source (missing files, rule-loading
// failures) are returned as-is.
func (e *Engine) Match(opts Options) (*MatchResult, error) {
	log := logOrDefault(e.Log)
	universe, err := e.fileUniverse(opts)
	if err != nil {
		return nil, err
	}
	repoRules, err := e.Source.KitFiles()
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" && !declaresScope(repoRules, opts.ScopeName) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownScope, opts.ScopeName)
	}

	// Run the sifter: the only matching implementation this analysis uses.
	repoRoot := e.Source.RepoRoot()
	chain := shonin.GetFileRuleChain(repoRoot, repoRules, universe, nil, log)

	res := &MatchResult{RepoRoot: repoRoot}
	for _, file := range universe {
		fm := FileMatch{Path: file}
		for _, layer := range chain.FileChains[file] {
			for _, rule := range layer.Rules {
				fm.Scopes = append(fm.Scopes, Scope{
					Name:      rule.Name,
					Pattern:   rule.Pattern,
					Directory: layer.DirectoryPath,
					KITFile:   kitFilePathOf(layer.DirectoryPath),
				})
			}
		}
		res.Files = append(res.Files, fm)
	}
	if opts.ScopeName != "" {
		res = res.FilterScope(opts.ScopeName)
	}
	if opts.IncludeDiff {
		for i := range res.Files {
			path := res.Files[i].Path
			if diff, derr := e.Source.DiffContent(path); derr == nil {
				res.Files[i].Diff = diff
			} else {
				log.Error("kenin: diff content failed",
					"component", "kenin", "path", path, "error", derr)
			}
		}
	}
	return res, nil
}

// ErrUnknownScope reports a scope filter (Match or Coverage) naming a rule
// that no KIT file declares, as opposed to a declared scope that matches no
// files.
var ErrUnknownScope = errors.New("unknown scope")

// declaresScope reports whether any KIT file has a rule named name.
func declaresScope(rules map[string]kitfile.Rules, name string) bool {
	for _, list := range rules {
		for _, r := range list {
			if r.Name == name {
				return true
			}
		}
	}
	return false
}

// Coverage computes how many files each scope covers, across the full
// universe (not just the matched files), and returns the covered file count
// and the percentage. A scope that matches no files still appears, with a
// Files count of 0, so a coverage gap is a data point, not an absence.
// opts.ChangedOnly switches the universe to ChangedFiles. opts.ScopeName
// narrows the per-scope breakdown afterwards (see CoverageReport.FilterScope),
// leaving the overall numbers computed from every rule; as for Match, a name
// no KIT file declares is ErrUnknownScope.
func (e *Engine) Coverage(opts Options) (*CoverageReport, error) {
	log := logOrDefault(e.Log)
	fullUniverse, err := e.fileUniverse(Options{ChangedOnly: opts.ChangedOnly})
	if err != nil {
		return nil, err
	}
	repoRules, err := e.Source.KitFiles()
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" && !declaresScope(repoRules, opts.ScopeName) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownScope, opts.ScopeName)
	}
	chain := shonin.GetFileRuleChain(e.Source.RepoRoot(), repoRules, fullUniverse, nil, log)

	scopeHits := map[Scope]int{}
	covered := map[string]bool{}
	for _, file := range fullUniverse {
		for _, layer := range chain.FileChains[file] {
			for _, rule := range layer.Rules {
				scope := Scope{
					Name:      rule.Name,
					Pattern:   rule.Pattern,
					Directory: layer.DirectoryPath,
					KITFile:   kitFilePathOf(layer.DirectoryPath),
				}
				scopeHits[scope]++
				covered[file] = true
			}
		}
	}

	report := &CoverageReport{Universe: len(fullUniverse)}
	report.Covered = len(covered)
	if len(fullUniverse) > 0 {
		report.Percent = float64(report.Covered) / float64(len(fullUniverse)) * 100.0
	}
	report.Uncovered = setDiff(fullUniverse, coveredKeys(covered))

	report.PerScope = make([]ScopeCoverage, 0, len(scopeHits))
	for scope, hits := range scopeHits {
		pct := 0.0
		if len(fullUniverse) > 0 {
			pct = float64(hits) / float64(len(fullUniverse)) * 100.0
		}
		report.PerScope = append(report.PerScope, ScopeCoverage{
			Scope:   scope,
			Files:   hits,
			Percent: pct,
		})
	}
	if opts.ScopeName != "" {
		report = report.FilterScope(opts.ScopeName)
	}
	return report, nil
}

// coveredKeys returns the key set of a boolean-keyed map.
func coveredKeys(b map[string]bool) []string {
	var out []string
	for k := range b {
		out = append(out, k)
	}
	return out
}

// setDiff returns elements in a that are not in b.
func setDiff(a, b []string) []string {
	seen := map[string]bool{}
	for _, x := range b {
		seen[x] = true
	}
	var out []string
	for _, x := range a {
		if !seen[x] {
			out = append(out, x)
		}
	}
	return out
}
