package kenin

import (
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
