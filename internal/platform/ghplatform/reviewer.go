// Package ghplatform is the GitHub platform for one pull request: rules from the
// base tree through the GitHub REST API, team targets expanded to their
// members, and a match.Source built from a pull_request webhook event.
package ghplatform

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
	return newReviewer(newBaseTree(client, owner, repo, baseSHA), client, log)
}

// newReviewer is the GitHub platform reading rules from tree, so a Source
// can share its tree's listing with the fetch.
func newReviewer(tree *baseTree, client *github.Client, log *slog.Logger) *approval.Reviewer {
	return &approval.Reviewer{
		Fetch:    &approval.TreeFetcher{Tree: tree, Log: log},
		Resolver: NewApproverResolver(client, log),
		Log:      log,
	}
}
