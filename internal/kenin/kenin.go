package kenin

import (
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
)

// Options configure one Match or Coverage run. A zero-value struct means:
// all files, every scope, no diff content.
type Options struct {
	ChangedOnly bool
	ScopeName   string
	IncludeDiff bool
	// Patterns narrows the file universe to the files matching any of these
	// regular expressions; empty keeps every file.
	Patterns []string
}

// Engine is the single match/coverage implementation over a Source.
type Engine struct {
	Source Source
	Log    *slog.Logger
}

// New constructs an Engine on the given Source and logger.
func New(src Source, log *slog.Logger) *Engine {
	return &Engine{Source: src, Log: log}
}

// Files resolves the sorted file universe for opts: ChangedFiles when
// Options.ChangedOnly is true, AllFiles otherwise, narrowed by
// Options.Patterns. An invalid pattern is an error.
func (e *Engine) Files(opts Options) ([]string, error) {
	res := make([]*regexp.Regexp, len(opts.Patterns))
	for i, p := range opts.Patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		res[i] = re
	}
	var files []string
	var err error
	if opts.ChangedOnly {
		files, err = e.Source.ChangedFiles()
	} else {
		files, err = e.Source.AllFiles()
	}
	if err != nil {
		return nil, err
	}
	if len(res) > 0 {
		files = slices.DeleteFunc(slices.Clone(files), func(f string) bool {
			return !slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(f) })
		})
	}
	sort.Strings(files)
	return files, nil
}

// RuleChain loads the source's rules and sifts them into the rule chain for
// files. Errors from the source (missing files, rule-loading failures) are
// returned as-is.
func (e *Engine) RuleChain(files []string) (shonin.RuleChain, error) {
	rules, err := e.rules()
	if err != nil {
		return shonin.RuleChain{}, err
	}
	return shonin.GetFileRuleChain(rules, files, nil, logOrDefault(e.Log)), nil
}

// Requirements runs the approve phase over chain with no approvals
// simulated, on the source's platform, so each file's state says who must
// approve it. Every approver counts as active and there are no repository
// owners, so a file no rule covers is reported as an unsatisfiable fallback.
func (e *Engine) Requirements(chain shonin.RuleChain) *shonin.ReviewState {
	return e.Source.Reviewer().Approve(shonin.EvaluationContext{
		CurrentApprovers: map[string]bool{},
		IsOwnerActive:    func(string) bool { return true },
	}, chain)
}

// rules loads the source's KIT rules through its platform's fetcher.
func (e *Engine) rules() (map[string]kitfile.Rules, error) {
	_, rules, err := e.Source.Reviewer().Fetch.Execute()
	return rules, err
}

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

// ReviewInput is the pull-request state a review evaluates files against:
// who wrote the change, who has approved it, who is unavailable, and the
// repository owners who decide files no rule covers.
type ReviewInput struct {
	Author    string
	Approvers []string
	Inactive  []string
	Owners    []string
}

// evaluationContext is the input as the evaluation context shonin approves against.
func (in ReviewInput) evaluationContext() shonin.EvaluationContext {
	inactive := setOf(in.Inactive)
	return shonin.EvaluationContext{
		Author:           in.Author,
		CurrentApprovers: setOf(in.Approvers),
		IsOwnerActive:    func(u string) bool { return !inactive[u] },
		RepositoryOwners: in.Owners,
	}
}

// Review runs the full shonin review (fetch, sift, approve) over files on
// the source's platform, so rules and approver resolution are local or
// remote together. Every error the review meets is in the returned state
// (see shonin.Shonin.Execute).
func (e *Engine) Review(files []string, in ReviewInput) *shonin.ReviewState {
	return e.Source.Reviewer().Execute(in.evaluationContext(), files)
}

// setOf is the set of items, for membership tests.
func setOf(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, it := range items {
		set[it] = true
	}
	return set
}
