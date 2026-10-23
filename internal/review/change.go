package review

import (
	"context"

	"github.com/alphaeron/canopy/internal/approval"
)

// Change is a proposed change on a hosted platform (a pull request, a merge
// request) as the [Evaluate] function reviews it: its files and rules
// (Source, whose base revision holds the rules the review enforces), who
// wrote and approved it, and its files as it proposes them.
//
// [Evaluate] calls ChangedFiles, ReviewInput, and the evaluator's fetch
// concurrently, so its methods must be safe for concurrent use. It starts
// the fetch early and the review fetches again, so the fetch should share
// one result per change rather than read the rules twice.
type Change interface {
	Source

	// ReviewInput builds the change's review input from the platform: its
	// author, who counts as approving the change wherever a rule allows
	// self-approval (see approval.ReviewInput.Author); its approvers, worked
	// out from the reviews it has (each reviewer's latest decisive review
	// counts); the repository owners
	// who review files no rule covers; IsActive; and its labels, read when
	// the review runs (see approval.ReviewInput.Labels). Names need not be
	// canonical: the review canonicalizes them with the platform's
	// resolver.
	//
	// IsActive may look users up on the platform lazily, under ctx, while
	// the review runs, so ctx must outlive the review, and IsActive must
	// be safe for concurrent use (the review checks users concurrently).
	// An error means the input could not be built, and the review does
	// not run.
	ReviewInput(ctx context.Context) (approval.ReviewInput, error)

	// Warnings lists the conditions met while reading the change that do
	// not stop its review but are worth reporting with its outcome, such
	// as a change too large to list its files the usual way. It is called
	// once the review's reads are done.
	Warnings() []string
}
