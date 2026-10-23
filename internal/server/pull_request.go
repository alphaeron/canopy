package server

import (
	"context"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/review"
)

// PullRequest is a pull request (or merge request) on a hosted platform,
// as the server reviews it: the change review.Evaluate reviews, and where
// its outcome is reported.
type PullRequest interface {
	review.Change

	// Key identifies where Report publishes (GitHub: the repository and
	// head commit). Reviews with the same key run one at a time and only
	// the newest reports, so a review that finishes late never replaces a
	// newer one. It must not call the platform's API.
	Key() string

	// Info describes the pull request for the server's record (see
	// Recorder). It must not call the platform's API.
	Info() PullRequestInfo

	// Start publishes that a review of the pull request is running, as
	// its status, replacing the earlier one until Report replaces it in
	// turn. detailsURL, when not empty, is the page showing the review,
	// which the status links to.
	Start(ctx context.Context, detailsURL string) error

	// Report publishes the review's outcome as the pull request's status,
	// replacing the earlier one. The platform maps each verdict to its own
	// status states and carries the outcome's Description as the status's
	// description. detailsURL is as for Start.
	Report(ctx context.Context, o approval.Outcome, detailsURL string) error
}

// PullRequestInfo describes a pull request for the server's record. Every
// field comes from the verified webhook, not from the platform's API.
type PullRequestInfo struct {
	// Scope is the platform's cache scope for the pull request (see
	// RepoCache.Scope): whatever limits what its reader may see.
	Scope string
	// Repo is the repository, "owner/name", lower-cased.
	Repo    string
	Number  int
	BaseSHA string
	HeadSHA string
	Author  string
	// Action is the webhook event and action that requested the review,
	// e.g. "pull_request.synchronize".
	Action string
}
