package ghplatform

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
)

// Rate limit retries: how often a request is retried, the longest
// Retry-After (secondary limit) the transport waits out, and the longest it
// waits for the primary limit to reset when the request has no deadline of
// its own. A longer wait fails the request instead of stalling the review.
const (
	maxRetries     = 3
	maxRetryAfter  = time.Minute
	maxPrimaryWait = 10 * time.Minute
)

// retryTransport retries requests GitHub rate limited, after the delay
// GitHub asks for: the Retry-After of a secondary limit (403 or 429), or
// the X-RateLimit-Reset of an exhausted primary limit. A wait longer than
// the request's deadline allows, or past the retries, fails the request
// with approval.ErrRateLimited. Every other response, any other 4xx
// included, is returned as is, so retries never mask an error. Rate
// limiting lives here, in the transport, rather than in the review
// algorithm.
type retryTransport struct {
	base http.RoundTripper
	// wait sleeps for d or until ctx is done; nil uses a timer.
	wait func(ctx context.Context, d time.Duration) error
	// now is the current time; nil uses time.Now.
	now func() time.Time
}

// RoundTrip sends req, retrying it while it is rate limited.
func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		delay, limit, limited := t.rateLimit(resp)
		if !limited {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		next, ok := rewind(req)
		if attempt == maxRetries || delay > t.longestWait(req.Context(), limit) || !ok {
			return nil, fmt.Errorf("%s %s: %s for %s: %w", req.Method, req.URL.Path, limit, delay, approval.ErrRateLimited)
		}
		if err := t.sleep(req.Context(), delay); err != nil {
			return nil, err
		}
		req = next
	}
}

// The rate limits GitHub applies, as rateLimit names them.
const (
	secondaryLimit = "secondary rate limit"
	primaryLimit   = "primary rate limit"
)

// rateLimit reports whether resp is rate limited, which limit, and how long
// GitHub asks to wait: a secondary limit's Retry-After, or until an
// exhausted primary limit's reset.
func (t *retryTransport) rateLimit(resp *http.Response) (time.Duration, string, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, "", false
	}
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second, secondaryLimit, true
	}
	if resp.Header.Get("X-RateLimit-Remaining") != "0" {
		return 0, "", false
	}
	reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return 0, "", false
	}
	// A second past the reset, so the retry lands after it.
	return max(time.Unix(reset, 0).Sub(t.clock())+time.Second, 0), primaryLimit, true
}

// longestWait is the longest the transport waits out limit for a request
// under ctx: up to ctx's deadline for the primary limit, which resets
// within the hour, and maxRetryAfter for a secondary limit.
func (t *retryTransport) longestWait(ctx context.Context, limit string) time.Duration {
	if limit != primaryLimit {
		return maxRetryAfter
	}
	if deadline, ok := ctx.Deadline(); ok {
		return deadline.Sub(t.clock())
	}
	return maxPrimaryWait
}

// clock is the current time.
func (t *retryTransport) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// rewind is req ready to send again, with a fresh body; false when its body
// cannot be replayed.
func rewind(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	next := req.Clone(req.Context())
	next.Body = body
	return next, true
}

// sleep waits d, or until ctx is done.
func (t *retryTransport) sleep(ctx context.Context, d time.Duration) error {
	if t.wait != nil {
		return t.wait(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
