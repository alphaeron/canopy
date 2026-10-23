// Package review runs the approval algorithm over a change: the Engine maps a
// Source's file universe to rule scopes, reports coverage, and runs reviews,
// identically for a local checkout or a pull request. A platform supplies
// both the change (Source) and the evaluation dependencies (an
// approval.Evaluator with its fetcher and resolver).
package review

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"sort"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/parallel"
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
	rules, err := e.Rules(ctx)
	if err != nil {
		return approval.RuleChain{}, err
	}
	return approval.GetFileRuleChain(ctx, rules, files, nil, logx.OrDefault(e.Log))
}

// Requirements runs the approve phase over chain with no approvals
// simulated, on the source's platform, so each file's state says who must
// approve it. Every approver counts as active and there are no repository
// owners, so a file no rule covers is reported as an unsatisfiable fallback.
func (e *Engine) Requirements(ctx context.Context, chain approval.RuleChain) *approval.ReviewState {
	return e.Source.Evaluator().Approve(ctx, approval.ReviewInput{}, chain)
}

// Rules loads the source's rules through its platform's fetcher.
func (e *Engine) Rules(ctx context.Context) (rulesfile.Set, error) {
	return e.Source.Evaluator().Fetch.Execute(ctx)
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
	res, err := e.fileScopes(ctx, opts)
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" {
		res = res.FilterScope(opts.ScopeName)
	}
	if opts.IncludeDiff {
		log := logx.OrDefault(e.Log)
		for i := range res.Files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
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

// fileScopes is every file in the universe opts selects with all of its
// scopes, before any scope filter or diff: the shared core of Match and
// Coverage. It first checks that opts.ScopeName (when set) is a rule some
// rules file declares.
func (e *Engine) fileScopes(ctx context.Context, opts Options) (*Result, error) {
	universe, err := e.Files(ctx, opts)
	if err != nil {
		return nil, err
	}
	rules, err := e.Rules(ctx)
	if err != nil {
		return nil, err
	}
	if opts.ScopeName != "" && !slices.Contains(rules.Names(), opts.ScopeName) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownScope, opts.ScopeName)
	}
	chain, err := approval.GetFileRuleChain(ctx, rules, universe, nil, logx.OrDefault(e.Log))
	if err != nil {
		return nil, err
	}
	res := &Result{RepoRoot: e.repoRoot()}
	for _, file := range universe {
		fm := FileMatch{Path: file}
		for _, layer := range chain.FileChains[file] {
			for _, rule := range layer.Rules {
				r := rule.Rule
				r.RulesFile = layer.RulesFile()
				fm.Rules = append(fm.Rules, r)
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
	m, err := e.fileScopes(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	report := CoverageOf(m)
	if opts.ScopeName != "" {
		report = report.FilterScope(opts.ScopeName)
	}
	return report, m, nil
}

// ValidateProposed validates the rules files among files (a change's
// files) as the source proposes them (ReadHead): parse errors, and the
// platform's validation of the rules (approval.Evaluator.Validate). A path
// the head does not have (a deleted rules file, or the old side of a
// rename) is skipped. Violations are keyed
// by rules file path, and a valid file is omitted. A file over
// rulesfile.MaxSize is a violation; any other file that cannot be read is
// an operational error.
//
// The rules a review enforces come from the base revision, so this is what
// keeps a pull request from merging a rules file that only fails once it
// takes effect.
func (e *Engine) ValidateProposed(ctx context.Context, files []string) (map[string][]error, error) {
	var pending []string
	for _, f := range files {
		if rulesfile.IsRulesFile(f) {
			pending = append(pending, f)
		}
	}
	// Each read is a round trip, so read the files concurrently, then
	// handle them in order.
	heads := make([]struct {
		data []byte
		err  error
	}, len(pending))
	parallel.For(len(pending), parallel.Limit, func(i int) {
		heads[i].data, heads[i].err = e.Source.ReadHead(ctx, pending[i])
	})

	out := map[string][]error{}
	proposed := rulesfile.Set{}
	for i, p := range pending {
		data, err := heads[i].data, heads[i].err
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if errors.Is(err, rulesfile.ErrTooLarge) {
			out[p] = []error{err}
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read proposed rules file %s: %w", p, err)
		}
		rules, err := rulesfile.ParseRules(data)
		if err != nil {
			out[p] = []error{err}
			continue
		}
		proposed[p] = rules
	}
	// A file that failed to parse is not in proposed, so the keys are disjoint.
	for p, errs := range e.Source.Evaluator().Validate(proposed) {
		out[p] = errs
	}
	return out, nil
}

// Review runs the full approval review (fetch, sift, approve) over files on
// the source's platform, so rules and approver resolution are local or
// hosted together. Every error the review meets is in the returned state
// (see approval.Evaluator.Execute). The state carries the source's
// repository root when it has one (see Rooted).
func (e *Engine) Review(ctx context.Context, files []string, in approval.ReviewInput) *approval.ReviewState {
	state := e.Source.Evaluator().Execute(ctx, in, files)
	state.RepoRoot = e.repoRoot()
	return state
}

// ExactPattern is the Options.Patterns entry selecting exactly the file at
// path.
func ExactPattern(path string) string { return "^" + regexp.QuoteMeta(path) + "$" }
