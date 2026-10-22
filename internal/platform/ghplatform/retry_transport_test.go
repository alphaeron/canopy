package ghplatform

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryTransport(t *testing.T) {
	tests := []struct {
		name      string
		responses []int  // status codes, in order
		retryHdr  string // Retry-After on rate-limited responses
		wantCalls int
		wantCode  int
	}{
		{name: "secondary rate limit is retried", responses: []int{403, 200}, retryHdr: "1", wantCalls: 2, wantCode: 200},
		{name: "429 is retried", responses: []int{429, 429, 200}, retryHdr: "1", wantCalls: 3, wantCode: 200},
		{name: "403 without Retry-After is not", responses: []int{403, 200}, wantCalls: 1, wantCode: 403},
		{name: "404 is not", responses: []int{404, 200}, retryHdr: "1", wantCalls: 1, wantCode: 404},
		{name: "a long Retry-After is not waited", responses: []int{403, 200}, retryHdr: "3600", wantCalls: 1, wantCode: 403},
		{name: "retries are bounded", responses: []int{403, 403, 403, 403, 403}, retryHdr: "1", wantCalls: maxRetries + 1, wantCode: 403},
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
				if tt.retryHdr != "" && code != 200 {
					w.Header().Set("Retry-After", tt.retryHdr)
				}
				w.WriteHeader(code)
			}))
			t.Cleanup(srv.Close)
			var waited []time.Duration
			client := &http.Client{Transport: &retryTransport{
				base: http.DefaultTransport,
				wait: func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil },
			}}
			resp, err := client.Post(srv.URL, "text/plain", strings.NewReader("payload"))
			if err != nil {
				t.Fatalf("Post: %v", err)
			}
			_ = resp.Body.Close()
			if int(calls.Load()) != tt.wantCalls || resp.StatusCode != tt.wantCode {
				t.Errorf("calls, status = %d, %d; want %d, %d", calls.Load(), resp.StatusCode, tt.wantCalls, tt.wantCode)
			}
			for _, d := range waited {
				if d != time.Second {
					t.Errorf("waited %s, want the Retry-After of 1s", d)
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
