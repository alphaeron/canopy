package approval

import (
	"context"
	"errors"
	"sync"
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
// DefaultApproverResolver, and GitHub uses ghplatform.ApproverResolver, which
// expands team targets to their members (see local.NewEvaluator and
// ghplatform.NewEvaluator). Implementations need not cache: a review resolves
// each target once (see RuleChain.Execute).
type ApproverResolver interface {
	// Resolve maps a target to its member set. A target the platform does
	// not know is an error wrapping ErrUnknownTarget; any other error is an
	// operational failure.
	Resolve(ctx context.Context, target string) ([]string, error)
	// Canonical returns name in the platform's canonical form, so names
	// that denote the same user compare equal: GitHub logins are
	// case-insensitive, for example. It must be idempotent.
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

// reviewResolver wraps a platform resolver for one review: each target is
// resolved once, and every member is returned in canonical form.
type reviewResolver struct {
	ApproverResolver

	mu       sync.Mutex
	resolved map[string]resolution
}

type resolution struct {
	members []string
	err     error
}

// newReviewResolver wraps r for one review. A resolver that is already
// wrapped is returned as is.
func newReviewResolver(r ApproverResolver) *reviewResolver {
	if rr, ok := r.(*reviewResolver); ok {
		return rr
	}
	return &reviewResolver{ApproverResolver: r, resolved: map[string]resolution{}}
}

// Resolve resolves target through the platform on first use, and returns
// the same members or error for the rest of the review.
func (r *reviewResolver) Resolve(ctx context.Context, target string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if res, ok := r.resolved[target]; ok {
		return res.members, res.err
	}
	raw, err := r.ApproverResolver.Resolve(ctx, target)
	var members []string
	if err == nil {
		members = make([]string, len(raw))
		for i, m := range raw {
			members[i] = r.Canonical(m)
		}
	}
	r.resolved[target] = resolution{members: members, err: err}
	return members, err
}
