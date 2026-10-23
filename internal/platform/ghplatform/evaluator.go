// Package ghplatform is the GitHub platform: rules from a pull request's base
// tree through the GitHub REST API, team targets expanded to their members,
// approvals read from the pull request's reviews, outcomes reported as
// check runs, and the webhook endpoint the review server registers
// (PRWebhook), acting as a GitHub App.
package ghplatform

import (
	"log/slog"

	"github.com/alphaeron/canopy/internal/approval"
)

// newEvaluator is the GitHub platform reading rules from tree and resolving
// approvers with resolver, so a Source can share its tree's listing with the
// fetch and a PRWebhook its resolver across reviews. The fetch runs once
// per Source (approval.OnceFetcher): the server starts it while it reads the
// pull request, and the review reuses it (server.PullRequest).
func newEvaluator(tree *baseTree, resolver *ApproverResolver, log *slog.Logger) *approval.Evaluator {
	return &approval.Evaluator{
		Fetch:    &approval.OnceFetcher{Fetcher: &approval.TreeFetcher{Tree: tree, Log: log}},
		Resolver: resolver,
		Log:      log,
	}
}
