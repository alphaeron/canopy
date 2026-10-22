package kenin

import (
	"errors"
	"fmt"

	"github.com/alphaeron/kit/internal/shonin"
)

// ErrUnknownScope reports a scope filter (Match or Coverage) naming a rule
// that no KIT file declares, as opposed to a declared scope that matches no
// files.
var ErrUnknownScope = errors.New("unknown scope")

// Match runs the shonin sifter across the file universe selected by opts,
// then inverts the chain into a per-file scope mapping. Without a scope
// filter every file in the universe is in the result, and a file no rule
// matches has an empty scope list, so the caller sees the whole universe.
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
	res, err := e.match(opts)
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" {
		res = res.FilterScope(opts.ScopeName)
	}
	if opts.IncludeDiff {
		log := logOrDefault(e.Log)
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

// match is the shared core of Match and Coverage: the universe opts
// selects with every file's scopes, before any scope filter or diff. It
// first checks that opts.ScopeName (when set) is a rule some KIT file
// declares.
func (e *Engine) match(opts Options) (*MatchResult, error) {
	universe, err := e.Files(opts)
	if err != nil {
		return nil, err
	}
	rules, err := e.rules()
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" && !declaresScope(rules, opts.ScopeName) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownScope, opts.ScopeName)
	}
	chain := shonin.GetFileRuleChain(rules, universe, nil, logOrDefault(e.Log))
	res := &MatchResult{RepoRoot: e.Source.RepoRoot()}
	for _, file := range universe {
		fm := FileMatch{Path: file}
		for _, layer := range chain.FileChains[file] {
			for _, rule := range layer.Rules {
				fm.Scopes = append(fm.Scopes, newScope(layer.DirectoryPath, rule))
			}
		}
		res.Files = append(res.Files, fm)
	}
	return res, nil
}

// Coverage computes how many files each scope covers, across the full
// universe (not just the matched files), and returns the covered file count
// and the percentage, together with the match it was computed from: every
// file in the universe with its scopes, for views that list each scope's
// files. A scope that matches no files does not appear. opts.ChangedOnly and
// opts.Patterns select the universe. opts.ScopeName narrows only the
// per-scope breakdown (see CoverageReport.FilterScope), leaving the overall
// numbers and the match computed from every rule; as for Match, a name no
// KIT file declares is ErrUnknownScope.
func (e *Engine) Coverage(opts Options) (*CoverageReport, *MatchResult, error) {
	m, err := e.match(opts)
	if err != nil {
		return nil, nil, err
	}
	report := CoverageOf(m)
	if opts.ScopeName != "" {
		report = report.FilterScope(opts.ScopeName)
	}
	return report, m, nil
}

// CoverageOf projects a match onto coverage: the files with at least one
// scope are covered, and each scope's share of the universe is reported in
// GroupByScope order.
func CoverageOf(m *MatchResult) *CoverageReport {
	report := &CoverageReport{Universe: len(m.Files)}
	for _, fm := range m.Files {
		if len(fm.Scopes) > 0 {
			report.Covered++
		} else {
			report.Uncovered = append(report.Uncovered, fm.Path)
		}
	}
	report.Percent = percentOf(report.Covered, report.Universe)
	groups := m.GroupByScope()
	report.PerScope = make([]ScopeCoverage, 0, len(groups))
	for _, g := range groups {
		report.PerScope = append(report.PerScope, ScopeCoverage{
			Scope:   g.Scope,
			Files:   len(g.Files),
			Percent: percentOf(len(g.Files), report.Universe),
		})
	}
	return report
}

// percentOf is n as a percentage of total, or 0 for an empty total.
func percentOf(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100.0
}
