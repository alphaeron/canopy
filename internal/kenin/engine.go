package kenin

import (
	"github.com/alphaeron/kit/internal/shonin"
)

// Match runs the shonin sifter (GetFileRuleChain) across the file universe
// selected by opts, then inverts the chain into a per-file scope mapping.
// Options.ScopeName filters after matching; Options.IncludeDiff attaches the
// diff for every file in the universe, matching or not. A per-file missing
// scope is not an error — files with no matching rule show up in the result
// with an empty scope list, so the caller can see the whole universe.
// Errors from the source (missing files, rule-loading failures) return nil,
// wrapped.
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

	// Run the sifter: the only matching implementation this analysis uses.
	repoRoot := e.Source.RepoRoot()
	chain := shonin.GetFileRuleChain(repoRoot, repoRules, universe, nil, log)

	res := &MatchResult{RepoRoot: repoRoot}
	for _, file := range universe {
		fm := FileMatch{Path: file}
		for _, layer := range chain.FileChains[file] {
			for _, rule := range layer.Rules {
				scope := Scope{
					Name:      rule.Name,
					Pattern:   rule.Pattern,
					Directory: layer.DirectoryPath,
					KITFile:   kitFilePathOf(layer.DirectoryPath),
				}
				if opts.ScopeName != "" && scope.Name != opts.ScopeName {
					continue
				}
				fm.Scopes = append(fm.Scopes, scope)
			}
		}
		if opts.IncludeDiff {
			if diff, derr := e.Source.DiffContent(file); derr == nil {
				fm.Diff = diff
			} else {
				log.Error("kenin: diff content failed",
					"component", "kenin", "path", file, "error", derr)
			}
		}
		res.Files = append(res.Files, fm)
	}
	return res, nil
}

// Coverage computes how many files each scope covers, across the full
// universe (not just the matched files), and returns the covered file count
// and the percentage. A scope that matches no files still appears, with a
// Files count of 0, so a coverage gap is a data point, not an absence.
// opts.ChangedOnly switches the universe to ChangedFiles; opts.ScopeName is
// ignored for coverage (every scope is always reported).
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
