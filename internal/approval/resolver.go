package approval

import (
	"context"
	"errors"
)

// ErrUnknownTarget reports an approver target the platform has no user or
// group for. A rule target that is unknown is pruned like an inactive one;
// any other resolution error is an operational failure that fails the
// review, so a platform outage can never weaken a requirement.
var ErrUnknownTarget = errors.New("unknown approver target")

// ErrInvalidTarget reports an approver target that is malformed for its
// platform (see TargetValidator). It is always also ErrUnknownTarget when
// returned by Resolve.
var ErrInvalidTarget = errors.New("invalid approver target")

// ApproverResolver resolves approver targets (usernames or group names) to
// the concrete members who can satisfy an approval requirement. Like
// Fetcher, it is chosen by platform: local execution uses the pass-through
// DefaultApproverResolver, and each hosted platform under internal/platform
// supplies its own, which may expand group targets to their members.
// Implementations need not cache: a review resolves each target once (see
// RuleChain.Execute). They must be safe for concurrent use: a review
// resolves different targets concurrently.
type ApproverResolver interface {
	// Resolve maps a target to its member set. A target the platform does
	// not know is an error wrapping ErrUnknownTarget; any other error is an
	// operational failure.
	Resolve(ctx context.Context, target string) ([]string, error)
	// Canonical returns name in the platform's canonical form, so names
	// that denote the same user compare equal, e.g. by folding case on a
	// platform whose usernames are case-insensitive. It must be idempotent.
	Canonical(name string) string
}

// TargetValidator is implemented by a resolver whose platform restricts the
// syntax of approver targets. It checks a target offline, so a malformed
// target is reported when rules are validated rather than when a review
// first resolves it.
type TargetValidator interface {
	// ValidateTarget returns an error wrapping ErrInvalidTarget when target
	// is malformed for the platform.
	ValidateTarget(target string) error
}
