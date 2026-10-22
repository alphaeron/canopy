// Package ghplatform is the GitHub platform for one pull request: rules from the
// base tree through the GitHub REST API, team targets expanded to their
// members, and a review.Source built from a pull_request webhook event.
package ghplatform

import (
	"log/slog"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/kit/internal/approval"
)

// NewEvaluator is the GitHub platform for one pull request: rules from the
// owner/repo tree at baseSHA, and approver targets resolved through the
// GitHub API (ApproverResolver). Build one per review: it reads the rules
// of one base revision.
func NewEvaluator(client *github.Client, owner, repo, baseSHA string, log *slog.Logger) *approval.Evaluator {
	return newEvaluator(newBaseTree(client, owner, repo, baseSHA), client, log)
}

// newEvaluator is the GitHub platform reading rules from tree, so a Source
// can share its tree's listing with the fetch.
func newEvaluator(tree *baseTree, client *github.Client, log *slog.Logger) *approval.Evaluator {
	return &approval.Evaluator{
		Fetch:    &approval.TreeFetcher{Tree: tree, Log: log},
		Resolver: NewApproverResolver(client),
		Log:      log,
	}
}
