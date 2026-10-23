package approval

import (
	"context"
	"sync"
)

// cachedReviewResolver wraps a platform resolver for one review: each
// target is resolved once, and every member is returned in canonical form.
// Concurrent first resolutions of one target share a single platform call,
// while different targets resolve concurrently.
type cachedReviewResolver struct {
	ApproverResolver

	mu       sync.Mutex
	resolved map[string]func() ([]string, error)
}

// newCachedReviewResolver wraps r for one review.
func newCachedReviewResolver(r ApproverResolver) *cachedReviewResolver {
	return &cachedReviewResolver{ApproverResolver: r, resolved: map[string]func() ([]string, error){}}
}

// Resolve resolves target through the platform on first use, canonicalizing
// its members, and returns the same members or error for the rest of the
// review. The first call's ctx is the one the platform call runs under.
func (r *cachedReviewResolver) Resolve(ctx context.Context, target string) ([]string, error) {
	r.mu.Lock()
	resolve, ok := r.resolved[target]
	if !ok {
		resolve = sync.OnceValues(func() ([]string, error) {
			raw, err := r.ApproverResolver.Resolve(ctx, target)
			if err != nil {
				return nil, err
			}
			members := make([]string, len(raw))
			for i, m := range raw {
				members[i] = r.Canonical(m)
			}
			return members, nil
		})
		r.resolved[target] = resolve
	}
	r.mu.Unlock()
	return resolve()
}
