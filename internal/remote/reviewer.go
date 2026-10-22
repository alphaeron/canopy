// Package remote is the GitHub platform for one pull request: rules from the
// base tree through the GitHub REST API, team targets expanded to their
// members, and a match.Source over the pull request's payload.
package remote

import (
	"log/slog"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
)

// NewReviewer is the GitHub platform for one pull request: rules from the
// owner/repo tree at baseSHA, and approver targets resolved through the
// GitHub API (ApproverResolver). Build one per review: the resolver caches
// team membership for its lifetime.
func NewReviewer(client *github.Client, owner, repo, baseSHA string, log *slog.Logger) *approval.Reviewer {
	return &approval.Reviewer{
		Fetch:    &Fetcher{Client: client, Owner: owner, Repo: repo, BaseSHA: baseSHA, Log: log},
		Resolver: NewApproverResolver(client, log),
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
