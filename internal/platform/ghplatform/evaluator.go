// Package ghplatform is the GitHub platform: rules from a pull request's base
// tree through the GitHub REST API, team targets expanded to their members,
// approvals read from the pull request's reviews, outcomes reported as
// commit statuses, and the webhook endpoint the review server registers
// (PRWebhook), acting as a GitHub App.
package ghplatform

import (
	"log/slog"

	"github.com/alphaeron/kit/internal/approval"
)

// newEvaluator is the GitHub platform reading rules from tree and resolving
// approvers with resolver, so a Source can share its tree's listing with the
// fetch and a PRWebhook its resolver across reviews.
func newEvaluator(tree *baseTree, resolver *ApproverResolver, log *slog.Logger) *approval.Evaluator {
	return &approval.Evaluator{
		Fetch:    &approval.TreeFetcher{Tree: tree, Log: log},
		Resolver: resolver,
		Log:      log,
	}
}
