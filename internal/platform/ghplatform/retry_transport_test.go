package ghplatform

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphaeron/kit/internal/approval"
)

func TestRetryTransport(t *testing.T) {
	// A real time (deadlines are real), whole so reset times are exact.
	now := time.Now().Truncate(time.Second)
	primary := func(resetIn time.Duration) map[string]string {
		return map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(now.Add(resetIn).Unix(), 10)}
	}
	retryAfter := func(secs string) map[string]string { return map[string]string{"Retry-After": secs} }
	tests := []struct {
		name      string
		responses []int             // status codes, in order
		limitHdr  map[string]string // headers on rate-limited responses
		deadline  time.Duration     // the request's deadline from now; 0 for none
		wantCalls int
		wantCode  int           // the response's status, when there is one
		wantWait  time.Duration // each wait the transport makes
	}{
		{name: "secondary rate limit is retried", responses: []int{403, 200}, limitHdr: retryAfter("1"), wantCalls: 2, wantCode: 200, wantWait: time.Second},
		{name: "429 is retried", responses: []int{429, 429, 200}, limitHdr: retryAfter("1"), wantCalls: 3, wantCode: 200, wantWait: time.Second},
		{name: "403 without a limit header is not", responses: []int{403, 200}, wantCalls: 1, wantCode: 403},
		{name: "404 is not", responses: []int{404, 200}, limitHdr: retryAfter("1"), wantCalls: 1, wantCode: 404},
		{name: "a long Retry-After fails as rate limited", responses: []int{403, 200}, limitHdr: retryAfter("3600"), wantCalls: 1},
		{name: "retries are bounded", responses: []int{403, 403, 403, 403, 403}, limitHdr: retryAfter("1"), wantCalls: maxRetries + 1, wantWait: time.Second},
		{name: "an exhausted primary limit is waited to its reset", responses: []int{403, 200}, limitHdr: primary(30 * time.Second), deadline: time.Minute, wantCalls: 2, wantCode: 200, wantWait: 31 * time.Second},
		{name: "a primary reset past the deadline fails as rate limited", responses: []int{403, 200}, limitHdr: primary(30 * time.Minute), deadline: time.Minute, wantCalls: 1},
		{name: "a primary reset is waited up to the cap without a deadline", responses: []int{429, 200}, limitHdr: primary(time.Minute), wantCalls: 2, wantCode: 200, wantWait: time.Minute + time.Second},
		{name: "a primary reset past the cap fails as rate limited", responses: []int{429, 200}, limitHdr: primary(time.Hour), wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != "payload" {
					t.Errorf("request body = %q, want it replayed intact", body)
				}
				code := tt.responses[calls.Add(1)-1]
				if code != 200 {
					for k, v := range tt.limitHdr {
						w.Header().Set(k, v)
					}
				}
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)
			var waited []time.Duration
			client := &http.Client{Transport: &retryTransport{
				base: http.DefaultTransport,
				wait: func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil },
				now:  func() time.Time { return now },
			}}
			ctx := t.Context()
			if tt.deadline > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, now.Add(tt.deadline))
				defer cancel()
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("payload"))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := client.Do(req)
			switch {
			case tt.wantCode == 0:
				if !errors.Is(err, approval.ErrRateLimited) {
					t.Errorf("err = %v, want approval.ErrRateLimited", err)
				}
			case err != nil:
				t.Fatalf("Do: %v", err)
			default:
				_ = resp.Body.Close()
				if resp.StatusCode != tt.wantCode {
					t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantCode)
				}
			}
			if int(calls.Load()) != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
			for _, d := range waited {
				if d != tt.wantWait {
					t.Errorf("waited %s, want %s", d, tt.wantWait)
				}
			}
		})
	}
}

// TestRetryTransportStopsWhenCancelled: waiting out a rate limit ends when
// the request's context does.
func TestRetryTransportStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusForbidden)
		cancel()
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := (&retryTransport{base: http.DefaultTransport}).RoundTrip(req); err == nil {
		t.Error("RoundTrip with a cancelled context succeeded, want the context's error")
	}
}
