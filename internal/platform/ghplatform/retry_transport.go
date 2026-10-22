package ghplatform

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Secondary rate limit retries: how often a request is retried, and the
// longest Retry-After the transport waits out (a longer one fails the
// request instead of stalling the review).
const (
	maxRetries    = 3
	maxRetryAfter = time.Minute
)

// retryTransport retries requests GitHub's secondary rate limit rejected
// (403 or 429 with a Retry-After header) after the delay GitHub asks for.
// Every other response, any other 4xx included, is returned as is, so
// retries never mask an error. Rate limiting lives here, in the transport,
// rather than in the review algorithm.
type retryTransport struct {
	base http.RoundTripper
	// wait sleeps for d or until ctx is done; nil uses a timer.
	wait func(ctx context.Context, d time.Duration) error
}

// RoundTrip sends req, retrying it while it is rate limited.
func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		delay, limited := retryAfter(resp)
		if !limited || attempt == maxRetries || delay > maxRetryAfter {
			return resp, nil
		}
		next, ok := rewind(req)
		if !ok {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err := t.sleep(req.Context(), delay); err != nil {
			return nil, err
		}
		req = next
	}
}

// retryAfter reports whether resp is a secondary rate limit, and how long
// GitHub asks to wait.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs) * time.Second, true
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
