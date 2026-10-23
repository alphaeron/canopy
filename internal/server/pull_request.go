package server

import (
	"context"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
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

	// Report publishes the review's outcome as the pull request's status,
	// replacing the earlier one. The platform maps each verdict to its own
	// status states and carries the outcome's Description as the status's
	// description.
	Report(ctx context.Context, o approval.Outcome) error
}
