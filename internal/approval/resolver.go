package approval

import "context"

// ApproverResolver resolves approver targets (usernames or group names) to
// the concrete members who can satisfy an approval requirement. Like
// Fetcher, it is chosen by platform: local execution uses the pass-through
// DefaultApproverResolver, and GitHub uses ghplatform.ApproverResolver, which
// expands team targets to their members (see local.NewReviewer and ghplatform.NewReviewer).
type ApproverResolver interface {
	// Resolve maps a target to its member set. ok is false when the target
	// cannot be resolved at all, in which case the returned members must be
	// ignored.
	Resolve(ctx context.Context, target string, pr PullRequest) ([]string, bool)
}
