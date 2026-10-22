// Package local is the local-checkout platform: rules from the git working
// tree, approver targets that stand for themselves, and a match.Source over
// the worktree and its diff against HEAD.
package local

import (
	"log/slog"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/gitrepo"
)

// NewReviewer is the local platform: rules from repo's working tree, and
// approver targets that stand for themselves (approval.DefaultApproverResolver),
// since a local review has no platform to expand groups against.
func NewReviewer(repo *gitrepo.Repository, log *slog.Logger) *approval.Reviewer {
	return &approval.Reviewer{
		Fetch:    &Fetcher{Repo: repo, Log: log},
		Resolver: &approval.DefaultApproverResolver{},
		Log:      log,
	}
}

// logOrDefault returns l, or slog.Default() when l is nil.
func logOrDefault(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
