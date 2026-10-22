// Package match answers which rules govern which files: the Engine maps a
// Source's file universe to rule scopes, reports coverage, and runs reviews,
// identically for a local checkout or a pull request.
package match

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/rulesfile"
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
func (e *Engine) Files(ctx context.Context, opts Options) ([]string, error) {
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
		files, err = e.Source.ChangedFiles(ctx)
	} else {
		files, err = e.Source.AllFiles(ctx)
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
func (e *Engine) RuleChain(ctx context.Context, files []string) (approval.RuleChain, error) {
	rules, err := e.rules(ctx)
	if err != nil {
		return approval.RuleChain{}, err
	}
	return approval.GetFileRuleChain(rules, files, nil, logx.OrDefault(e.Log)), nil
}

// Requirements runs the approve phase over chain with no approvals
// simulated, on the source's platform, so each file's state says who must
// approve it. Every approver counts as active and there are no repository
// owners, so a file no rule covers is reported as an unsatisfiable fallback.
func (e *Engine) Requirements(ctx context.Context, chain approval.RuleChain) *approval.ReviewState {
	return e.Source.Reviewer().Approve(ctx, approval.PullRequest{
		CurrentApprovers: map[string]bool{},
		IsOwnerActive:    func(string) bool { return true },
	}, chain)
}

// rules loads the source's rules through its platform's fetcher.
func (e *Engine) rules(ctx context.Context) (rulesfile.Set, error) {
	return e.Source.Reviewer().Fetch.Execute(ctx)
}

// repoRoot is the source's on-disk repository root, or "" when it has none
// (see Rooted).
func (e *Engine) repoRoot() string {
	if r, ok := e.Source.(Rooted); ok {
		return r.RepoRoot()
	}
	return ""
}

// ErrUnknownScope reports a scope filter (Match or Coverage) naming a rule
// that no rules file declares, as opposed to a declared scope that matches no
// files.
var ErrUnknownScope = errors.New("unknown scope")

// Match runs the approval sifter across the file universe selected by opts,
// then inverts the chain into a per-file scope mapping. Without a scope
// filter every file in the universe is in the result, and a file no rule
// matches has an empty scope list, so the caller sees the whole universe.
//
// Options.ScopeName narrows the result after matching (see
// Result.FilterScope): the chain is always computed from every rule,
// because a file's effective rules depend on every layer above it, so the
// filter changes what is reported, never what matches. A name that no rules
// file declares is ErrUnknownScope; a declared scope that matches nothing
// yields an empty result. Options.IncludeDiff attaches diffs to the files
// in the result only, so a scope filter also skips fetching diffs for the
// files it drops. Errors from the source (missing files, rule-loading
// failures) are returned as-is.
func (e *Engine) Match(ctx context.Context, opts Options) (*Result, error) {
	res, err := e.match(ctx, opts)
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" {
		res = res.FilterScope(opts.ScopeName)
	}
	if opts.IncludeDiff {
		log := logx.OrDefault(e.Log)
		for i := range res.Files {
			path := res.Files[i].Path
			if diff, derr := e.Source.DiffContent(ctx, path); derr == nil {
				res.Files[i].Diff = diff
			} else {
				log.Error("match: diff content failed",
					"component", "match", "path", path, "error", derr)
			}
		}
	}
	return res, nil
}

// match is the shared core of Match and Coverage: the universe opts
// selects with every file's scopes, before any scope filter or diff. It
// first checks that opts.ScopeName (when set) is a rule some rules file
// declares.
func (e *Engine) match(ctx context.Context, opts Options) (*Result, error) {
	universe, err := e.Files(ctx, opts)
	if err != nil {
		return nil, err
	}
	rules, err := e.rules(ctx)
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" && !slices.Contains(rules.Names(), opts.ScopeName) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownScope, opts.ScopeName)
	}
	chain := approval.GetFileRuleChain(rules, universe, nil, logx.OrDefault(e.Log))
	res := &Result{RepoRoot: e.repoRoot()}
	for _, file := range universe {
		fm := FileMatch{Path: file}
		for _, layer := range chain.FileChains[file] {
			for _, rule := range layer.Rules {
				fm.Scopes = append(fm.Scopes, newScope(rulesfile.DeclaredRule{RulesFile: layer.RulesFile(), Rule: rule.Rule}))
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
// rules file declares is ErrUnknownScope.
func (e *Engine) Coverage(ctx context.Context, opts Options) (*CoverageReport, *Result, error) {
	m, err := e.match(ctx, opts)
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
func CoverageOf(m *Result) *CoverageReport {
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

// pullRequest is the input as the pull request state the approval package evaluates.
func (in ReviewInput) pullRequest() approval.PullRequest {
	inactive := setOf(in.Inactive)
	return approval.PullRequest{
		Author:           in.Author,
		CurrentApprovers: setOf(in.Approvers),
		IsOwnerActive:    func(u string) bool { return !inactive[u] },
		RepositoryOwners: in.Owners,
	}
}

// Review runs the full approval review (fetch, sift, approve) over files on
// the source's platform, so rules and approver resolution are local or
// GitHub together. Every error the review meets is in the returned state
// (see approval.Reviewer.Execute). The state carries the source's
// repository root when it has one (see Rooted).
func (e *Engine) Review(ctx context.Context, files []string, in ReviewInput) *approval.ReviewState {
	state := e.Source.Reviewer().Execute(ctx, in.pullRequest(), files)
	state.RepoRoot = e.repoRoot()
	return state
}

// setOf is the set of items, for membership tests.
func setOf(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, it := range items {
		set[it] = true
	}
	return set
}
