// Package local is the local-checkout platform: rules from the git working
// tree, approver targets that stand for themselves, and a review.Source over
// the worktree and its diff against HEAD.
package local

import (
	"log/slog"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/gitrepo"
)

// NewEvaluator is the local platform: rules from repo's working tree, and
// approver targets that stand for themselves (approval.DefaultApproverResolver),
// since a local review has no platform to expand groups against. The rules
// are read once (approval.OnceFetcher), as repo is a snapshot: every view
// built on the evaluator shares one read.
func NewEvaluator(repo *gitrepo.Repository, log *slog.Logger) *approval.Evaluator {
	return &approval.Evaluator{
		Fetch:    &approval.OnceFetcher{Fetcher: &approval.TreeFetcher{Tree: &worktree{repo: repo}, Log: log}},
		Resolver: &approval.DefaultApproverResolver{},
		Log:      log,
	}
}
