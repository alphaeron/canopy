package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

// stubWebhook is a PRWebhook whose deliveries verify with the header
// "X-Token: ok" and whose events name changes in a map.
type stubWebhook struct {
	name     string
	path     string                 // "" serves at /webhooks/{name}
	maxBytes int64                  // 0 allows 1 KiB
	noTypes  bool                   // declares no content types
	changes  map[string]PullRequest // event to the change it starts
}

func (p *stubWebhook) Name() string { return p.name }

func (p *stubWebhook) WebhookPath() string {
	if p.path != "" {
		return p.path
	}
	return "/webhooks/" + p.name
}

func (p *stubWebhook) WebhookMaxBytes() int64 {
	if p.maxBytes != 0 {
		return p.maxBytes
	}
	return 1 << 10
}

func (p *stubWebhook) WebhookContentTypes() []string {
	if p.noTypes {
		return nil
	}
	return []string{"application/json"}
}

func (p *stubWebhook) WebhookDescription() string { return "The stub webhook, verified by `X-Token`." }

func (p *stubWebhook) VerifyWebhook(r *http.Request) (string, []byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", nil, err
	}
	if r.Header.Get("X-Token") != "ok" {
		return "", nil, errors.New("bad token")
	}
	return r.Header.Get("X-Event"), body, nil
}

func (p *stubWebhook) DeliveryID(r *http.Request) string { return r.Header.Get("X-Delivery") }

func (p *stubWebhook) PullRequest(event string, body []byte) (PullRequest, bool, error) {
	if event == "malformed" {
		return nil, false, errors.New("malformed event")
	}
	c, ok := p.changes[event]
	return c, ok, nil
}

// stubPullRequest is an in-memory pull request: rules at the base, rules files
// it proposes, its changed files and review input, and what it reported.
type stubPullRequest struct {
	rules      rulesfile.Set
	fetchErr   error
	warnings   []string
	head       map[string]string
	changed    []string
	changedErr error
	input      approval.ReviewInput
	key        string // Key; "" for every stub that does not set it
	// block makes ChangedFiles wait until its context is done, or release
	// is closed when it is set.
	block   bool
	release chan struct{}
	started chan struct{} // closed when ChangedFiles is first called

	// headErr makes ReadHead fail.
	headErr error

	startOnce sync.Once
	mu        sync.Mutex
	reports   []approval.Outcome
	done      chan struct{} // closed on the first report
}

func newStubPullRequest() *stubPullRequest {
	return &stubPullRequest{
		rules: rulesfile.Set{rulesfile.FileName: {{Pattern: ".*", Name: "Root",
			Approvers: rulesfile.ApproverTargets{{Target: "lead", RequiredCount: 1}}}}},
		changed: []string{"main.go"},
		done:    make(chan struct{}),
		started: make(chan struct{}),
	}
}

func (c *stubPullRequest) Evaluator() *approval.Evaluator {
	return &approval.Evaluator{
		Fetch: fetchFunc(func(context.Context) (rulesfile.Set, error) {
			if c.fetchErr != nil {
				return nil, c.fetchErr // the fetch loaded nothing
			}
			return c.rules, nil
		}),
		Resolver: &usersOnly{},
		Log:      slog.New(slog.DiscardHandler),
	}
}

func (c *stubPullRequest) AllFiles(context.Context) ([]string, error) { return c.changed, nil }

func (c *stubPullRequest) ChangedFiles(ctx context.Context) ([]string, error) {
	c.startOnce.Do(func() { close(c.started) })
	if c.block {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.release:
		}
	}
	return c.changed, c.changedErr
}

func (c *stubPullRequest) DiffContent(context.Context, string) (string, error) { return "", nil }

func (c *stubPullRequest) Warnings() []string { return c.warnings }

func (c *stubPullRequest) Key() string { return c.key }

func (c *stubPullRequest) Info() PullRequestInfo {
	return PullRequestInfo{Scope: "stub", Repo: "o/r", Number: 1, Author: c.input.Author, Action: "pr"}
}

func (c *stubPullRequest) ReviewInput(context.Context) (approval.ReviewInput, error) {
	return c.input, nil
}

func (c *stubPullRequest) ReadHead(_ context.Context, path string) ([]byte, error) {
	if c.headErr != nil {
		return nil, c.headErr
	}
	data, ok := c.head[path]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
	}
	return []byte(data), nil
}

func (c *stubPullRequest) Report(_ context.Context, o approval.Outcome) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, o)
	if len(c.reports) == 1 {
		close(c.done)
	}
	return nil
}

// wait waits for the change's review to be reported.
func (c *stubPullRequest) wait(t *testing.T) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the review was never reported")
	}
}

// fetchFunc is an approval.Fetcher from a function.
type fetchFunc func(context.Context) (rulesfile.Set, error)

func (f fetchFunc) Execute(ctx context.Context) (rulesfile.Set, error) { return f(ctx) }

// usersOnly resolves every target to itself and rejects targets with a
// slash, as a platform with target syntax does.
type usersOnly struct {
	approval.DefaultApproverResolver
}

func (*usersOnly) ValidateTarget(target string) error {
	if strings.Contains(target, "/") {
		return fmt.Errorf("%w: %q", approval.ErrInvalidTarget, target)
	}
	return nil
}

// testOptions are server options for tests: one worker, a short queue.
func testOptions() Options {
	return Options{
		Workers: 1, Queue: 4, ReviewTimeout: time.Minute, ReportTimeout: time.Second, ShutdownTimeout: time.Second,
		Version: "test", Log: slog.New(slog.DiscardHandler),
	}
}

// newTestServer starts a server over webhook whose reviews run until the
// test ends.
func newTestServer(t *testing.T, opts Options, webhook PRWebhook) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s, err := New(ctx, opts, webhook)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { cancel(); s.Wait() })
	return s
}

// post delivers a webhook to s at path, and returns the status code and
// the decoded JSON response.
func post(t *testing.T, s *Server, path, event, token string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"any":"payload"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Event", event)
	r.Header.Set("X-Token", token)
	r.Header.Set("X-Delivery", "delivery-"+event)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// TestWebhookBodyIsBoundedByThePlatform: a body over the platform's limit
// never verifies.
func TestWebhookBodyIsBoundedByThePlatform(t *testing.T) {
	s := newTestServer(t, testOptions(), &stubWebhook{name: "stub", maxBytes: 4})
	if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code == http.StatusAccepted {
		t.Errorf("webhook over the platform's limit = %d, want it refused", code)
	}
}

func TestWebhookRejectsUnverifiedRequests(t *testing.T) {
	c := newStubPullRequest()
	s := newTestServer(t, testOptions(), &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})
	if code, _ := post(t, s, "/webhooks/stub", "pr", "forged"); code != http.StatusUnauthorized {
		t.Errorf("unverified webhook = %d, want 401", code)
	}
	if len(s.reviews.pool.jobs) != 0 {
		t.Error("an unverified webhook queued a review")
	}
}

func TestWebhookIgnoresEventsThatDoNotStartAReview(t *testing.T) {
	s := newTestServer(t, testOptions(), &stubWebhook{name: "stub"})
	code, body := post(t, s, "/webhooks/stub", "ping", "ok")
	if code != http.StatusAccepted || body["queued"] != false {
		t.Errorf("ignored event = %d %v, want 202 with queued false", code, body)
	}
	if code, _ := post(t, s, "/webhooks/stub", "malformed", "ok"); code != http.StatusBadRequest {
		t.Errorf("malformed event = %d, want 400", code)
	}
}

// TestWebhookReviewsAndReportsOnce: a reviewable webhook is acknowledged
// with 202 before the review runs, and the review reports exactly once.
func TestWebhookReviewsAndReportsOnce(t *testing.T) {
	c := newStubPullRequest()
	c.input = approval.ReviewInput{Author: "dev", Approvers: []string{"lead"}}
	s := newTestServer(t, testOptions(), &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})

	code, body := post(t, s, "/webhooks/stub", "pr", "ok")
	if code != http.StatusAccepted || body["queued"] != true {
		t.Fatalf("webhook = %d %v, want 202 with queued true", code, body)
	}
	c.wait(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reports) != 1 || c.reports[0].Verdict != approval.Passed {
		t.Errorf("reports = %+v, want one passed report", c.reports)
	}
}

// TestServerServesThePlatformsPath: the webhook is served at the path the
// platform chooses, and New refuses a webhook it cannot serve.
func TestServerServesThePlatformsPath(t *testing.T) {
	pr := newStubPullRequest()
	s := newTestServer(t, testOptions(), &stubWebhook{name: "stub", path: "/hooks/stub/events", changes: map[string]PullRequest{"pr": pr}})
	if code, _ := post(t, s, "/hooks/stub/events", "pr", "ok"); code != http.StatusAccepted {
		t.Fatalf("webhook at the platform's path = %d, want 202", code)
	}
	pr.wait(t)
	if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code != http.StatusNotFound {
		t.Errorf("webhook at another path = %d, want 404", code)
	}
	for _, w := range []*stubWebhook{
		{name: ""},
		{name: "stub", path: "webhooks/stub"},
		{name: "stub", path: "/swagger"},
		{name: "stub", path: "/openapi.json"},
		{name: "stub", maxBytes: -1},
		{name: "stub", noTypes: true},
	} {
		if _, err := New(t.Context(), testOptions(), w); err == nil {
			t.Errorf("New with %+v succeeded, want an error", w)
		}
	}
	if _, err := New(t.Context(), testOptions(), nil); err == nil {
		t.Error("New with no platform succeeded, want an error")
	}
}

// TestNewRefusesNonPositiveOptions: every size and timeout must be positive.
func TestNewRefusesNonPositiveOptions(t *testing.T) {
	for name, zero := range map[string]func(*Options){
		"workers":          func(o *Options) { o.Workers = 0 },
		"queue":            func(o *Options) { o.Queue = 0 },
		"review timeout":   func(o *Options) { o.ReviewTimeout = 0 },
		"report timeout":   func(o *Options) { o.ReportTimeout = 0 },
		"shutdown timeout": func(o *Options) { o.ShutdownTimeout = 0 },
	} {
		opts := testOptions()
		zero(&opts)
		if _, err := New(t.Context(), opts, &stubWebhook{name: "stub"}); err == nil {
			t.Errorf("New with zero %s succeeded, want an error", name)
		}
	}
}

func TestServerServesItsOpenAPIDocument(t *testing.T) {
	s := newTestServer(t, testOptions(), &stubWebhook{name: "stub"})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, OpenAPIPath+".json", nil))
	var doc struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			Description string `json:"description"`
			RequestBody struct {
				Content map[string]any `json:"content"`
			} `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	post, ok := doc.Paths["/webhooks/stub"]["post"]
	if !strings.HasPrefix(doc.OpenAPI, "3.1") || !ok {
		t.Fatalf("OpenAPI %s paths %v, want 3.1 with POST /webhooks/stub", doc.OpenAPI, doc.Paths)
	}
	if !strings.Contains(post.Description, "verified by `X-Token`") || post.RequestBody.Content["application/json"] == nil {
		t.Errorf("webhook operation = %+v, want the platform's description and content types", post)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, DocsPath, nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "swagger-ui") {
		t.Errorf("docs page = %d, want 200 with Swagger UI", w.Code)
	}
}

func TestWebhookRefusesWhenTheQueueIsFull(t *testing.T) {
	changes := map[string]PullRequest{}
	for i := range 4 {
		blocked := newStubPullRequest()
		blocked.block = true
		blocked.key = fmt.Sprint(i)
		changes[blocked.key] = blocked
	}
	opts := testOptions()
	opts.Queue = 1
	s := newTestServer(t, opts, &stubWebhook{name: "stub", changes: changes})
	codes := map[int]int{}
	// One pull request occupies the worker and one waits; the rest are
	// refused.
	for i := range 4 {
		code, _ := post(t, s, "/webhooks/stub", fmt.Sprint(i), "ok")
		codes[code]++
	}
	if codes[http.StatusServiceUnavailable] == 0 {
		t.Errorf("responses = %v, want 503 once the queue is full", codes)
	}
}

// TestShutdownCancelsInFlightReviews: cancelling the server's context stops
// a running review, which then reports nothing.
func TestShutdownCancelsInFlightReviews(t *testing.T) {
	c := newStubPullRequest()
	c.block = true
	ctx, cancel := context.WithCancel(context.Background())
	s, err := New(ctx, testOptions(), &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code != http.StatusAccepted {
		t.Fatalf("webhook = %d, want 202", code)
	}
	cancel()
	stopped := make(chan struct{})
	go func() { s.Wait(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the context was cancelled")
	}
	if len(c.reports) != 0 {
		t.Errorf("reports = %v, want none from a cancelled review", c.reports)
	}
	if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code != http.StatusServiceUnavailable {
		t.Errorf("webhook after shutdown = %d, want 503", code)
	}
}

// TestReviewTimeoutIsReported: a review that runs out of time still
// reports, as failed.
func TestReviewTimeoutIsReported(t *testing.T) {
	c := newStubPullRequest()
	c.block = true
	opts := testOptions()
	opts.ReviewTimeout = 10 * time.Millisecond
	s := newTestServer(t, opts, &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})
	if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code != http.StatusAccepted {
		t.Fatalf("webhook = %d, want 202", code)
	}
	c.wait(t)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reports) != 1 || !reflect.DeepEqual(c.reports[0], approval.Outcome{Verdict: approval.Errored, Summary: "review timed out"}) {
		t.Errorf("reports = %+v, want one failed for the timeout", c.reports)
	}
}

// TestNewerReviewCancelsTheRunningOne: a review requested while another of
// the same key runs cancels it, and only the newer one reports.
func TestNewerReviewCancelsTheRunningOne(t *testing.T) {
	older, newer := newStubPullRequest(), newStubPullRequest()
	older.block, older.key, newer.key = true, "k", "k"
	s := newTestServer(t, testOptions(), &stubWebhook{name: "stub", changes: map[string]PullRequest{"older": older, "newer": newer}})

	if code, _ := post(t, s, "/webhooks/stub", "older", "ok"); code != http.StatusAccepted {
		t.Fatalf("older webhook = %d, want 202", code)
	}
	<-older.started
	if code, _ := post(t, s, "/webhooks/stub", "newer", "ok"); code != http.StatusAccepted {
		t.Fatalf("newer webhook = %d, want 202", code)
	}
	newer.wait(t)
	older.mu.Lock()
	defer older.mu.Unlock()
	if len(older.reports) != 0 {
		t.Errorf("older reports = %+v, want none from a superseded review", older.reports)
	}
}

// TestNewerReviewReplacesAWaitingOne: of the reviews of one key requested
// while it waits for a worker, only the newest runs, and it takes one
// place in the queue.
func TestNewerReviewReplacesAWaitingOne(t *testing.T) {
	busy := newStubPullRequest()
	busy.block, busy.key, busy.release = true, "busy", make(chan struct{})
	first, second, third := newStubPullRequest(), newStubPullRequest(), newStubPullRequest()
	first.key, second.key, third.key = "k", "k", "k"
	opts := testOptions()
	opts.Queue = 1
	s := newTestServer(t, opts, &stubWebhook{name: "stub", changes: map[string]PullRequest{
		"busy": busy, "first": first, "second": second, "third": third,
	}})

	if code, _ := post(t, s, "/webhooks/stub", "busy", "ok"); code != http.StatusAccepted {
		t.Fatalf("busy webhook = %d, want 202", code)
	}
	<-busy.started // the only worker is taken
	for _, event := range []string{"first", "second", "third"} {
		if code, _ := post(t, s, "/webhooks/stub", event, "ok"); code != http.StatusAccepted {
			t.Fatalf("%s webhook = %d, want 202 (one queue place for the key)", event, code)
		}
	}
	close(busy.release)
	third.wait(t)
	for name, c := range map[string]*stubPullRequest{"first": first, "second": second} {
		select {
		case <-c.started:
			t.Errorf("the %s review ran, want it replaced before it started", name)
		default:
		}
	}
}

// TestSupersededReviewDoesNotReport: a review that a newer one supersedes
// after it ran, but before it reports, is no longer current; the newer one
// is.
func TestSupersededReviewDoesNotReport(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	newer := newStubPullRequest()
	newer.key = "k"
	var r *reviews
	got := make(chan []bool, 1)
	var seen []bool
	r = newReviews(ctx, 1, 1, time.Minute, func(_ context.Context, j job, current func() bool) {
		if j.pr != PullRequest(newer) {
			r.submit(job{pr: newer}) // arrives while the older one finishes
		}
		seen = append(seen, current())
		if len(seen) == 2 {
			got <- seen
		}
	})
	older := newStubPullRequest()
	older.key = "k"
	if !r.submit(job{pr: older}) {
		t.Fatal("submit refused")
	}
	select {
	case seen := <-got:
		if !reflect.DeepEqual(seen, []bool{false, true}) {
			t.Errorf("current = %v, want the older review superseded and the newer current", seen)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the newer review never ran")
	}
	cancel()
	r.wait()
}
