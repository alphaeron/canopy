package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
)

var (
	testLinkKey = []byte(strings.Repeat("k", MinLinkKeyBytes))
	testHead    = strings.Repeat("ab", 20)
	testNow     = time.Unix(1_800_000_000, 0)
	testLife    = 7 * 24 * time.Hour
)

func testLinks(t *testing.T) *links {
	t.Helper()
	l, err := newLinks("https://canopy.example.com/base/", testLinkKey, testLife)
	if err != nil {
		t.Fatalf("newLinks: %v", err)
	}
	l.now = func() time.Time { return testNow }
	return l
}

func TestNewLinksValidates(t *testing.T) {
	if l, err := newLinks("", nil, 0); l != nil || err != nil {
		t.Errorf("newLinks with neither = %v, %v; want no links and no error", l, err)
	}
	for _, tt := range []struct {
		name, url string
		key       []byte
		lifetime  time.Duration
	}{
		{"short key", "https://canopy.example.com", []byte("short"), testLife},
		{"no key", "https://canopy.example.com", nil, testLife},
		{"no URL", "", testLinkKey, testLife},
		{"relative URL", "canopy.example.com", testLinkKey, testLife},
		{"query", "https://canopy.example.com/?a=b", testLinkKey, testLife},
		{"other scheme", "ftp://canopy.example.com", testLinkKey, testLife},
		{"no lifetime", "https://canopy.example.com", testLinkKey, 0},
		{"short lifetime", "https://canopy.example.com", testLinkKey, MinLinkLifetime - time.Second},
	} {
		if _, err := newLinks(tt.url, tt.key, tt.lifetime); err == nil {
			t.Errorf("newLinks with %s: no error", tt.name)
		}
	}
}

// TestReviewURLIsSigned: a review page's link names the page under the
// public URL, and its signature verifies for that page alone.
func TestReviewURLIsSigned(t *testing.T) {
	l := testLinks(t)
	pr := PullRequestInfo{Repo: "o/r", Number: 7, HeadSHA: testHead}
	u, err := url.Parse(l.reviewURL(pr))
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	if want := "/base/reviews/o/r/7/" + testHead; u.Host != "canopy.example.com" || u.Path != want {
		t.Errorf("link = %s, want canopy.example.com%s", u, want)
	}
	sig := u.Query().Get("sig")
	exp, err := strconv.ParseInt(u.Query().Get("exp"), 10, 64)
	if want := testNow.Add(testLife).Unix(); err != nil || exp != want {
		t.Fatalf("link expiry = %q, want %d", u.Query().Get("exp"), want)
	}
	if !l.verify("o/r", 7, testHead, exp, sig) {
		t.Error("the link's own signature does not verify")
	}
	for name, ok := range map[string]bool{
		"another number": l.verify("o/r", 8, testHead, exp, sig),
		"another commit": l.verify("o/r", 7, strings.Repeat("cd", 20), exp, sig),
		"another repo":   l.verify("o/s", 7, testHead, exp, sig),
		"later expiry":   l.verify("o/r", 7, testHead, exp+1, sig),
		"no signature":   l.verify("o/r", 7, testHead, exp, ""),
		"not base64":     l.verify("o/r", 7, testHead, exp, "!!"),
	} {
		if ok {
			t.Errorf("signature verifies for %s", name)
		}
	}
	other, _ := newLinks("https://canopy.example.com", []byte(strings.Repeat("x", MinLinkKeyBytes)), testLife)
	other.now = l.now
	if other.verify("o/r", 7, testHead, exp, sig) {
		t.Error("signature verifies under another key")
	}
	var none *links
	if got := none.reviewURL(pr); got != "" {
		t.Errorf("reviewURL without links = %q, want none", got)
	}
}

// TestReviewURLExpires: a link opens its page until the link lifetime after
// it was signed, and not from then on.
func TestReviewURLExpires(t *testing.T) {
	l := testLinks(t)
	u, err := url.Parse(l.reviewURL(PullRequestInfo{Repo: "o/r", Number: 7, HeadSHA: testHead}))
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	exp, _ := strconv.ParseInt(u.Query().Get("exp"), 10, 64)
	sig := u.Query().Get("sig")
	for _, tt := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"when signed", testNow, true},
		{"a second before expiry", testNow.Add(testLife - time.Second), true},
		{"at expiry", testNow.Add(testLife), false},
		{"after expiry", testNow.Add(testLife + time.Hour), false},
	} {
		l.now = func() time.Time { return tt.at }
		if got := l.verify("o/r", 7, testHead, exp, sig); got != tt.want {
			t.Errorf("verify %s = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// fakeHistory is a ReviewHistory serving one record.
type fakeHistory struct {
	rec *ReviewRecord
	err error
}

func (h *fakeHistory) LatestReview(context.Context, string, int, string) (*ReviewRecord, error) {
	return h.rec, h.err
}

func pageOptions() Options {
	opts := testOptions()
	opts.PublicURL, opts.LinkKey, opts.LinkLifetime = "https://canopy.example.com", testLinkKey, testLife
	return opts
}

// getPage requests path from s and returns the status code and body.
func getPage(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Code, w.Body.String()
}

// pagePath is the signed path of o/r#1's review page at testHead, with
// suffix before its query.
func pagePath(s *Server, suffix string) string {
	u, _ := url.Parse(s.links.reviewURL(PullRequestInfo{Repo: "o/r", Number: 1, HeadSHA: testHead}))
	return u.Path + suffix + "?" + u.RawQuery
}

// progress fetches o/r#1's review progress from s.
func progress(t *testing.T, s *Server) map[string]any {
	t.Helper()
	code, body := getPage(t, s, pagePath(s, "/progress"))
	if code != http.StatusOK {
		t.Fatalf("progress = %d %s, want 200", code, body)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode progress: %v", err)
	}
	return p
}

// TestReviewPageFollowsTheReview: a review page is pending before any
// review, follows the review while it runs, and shows it in full once it
// finishes; the running status and the report link to it.
func TestReviewPageFollowsTheReview(t *testing.T) {
	c := approvedPullRequest()
	c.headSHA, c.block, c.release = testHead, true, make(chan struct{})
	s := newTestServer(t, pageOptions(), &stubWebhook{name: "stub", changes: map[string]PullRequest{"pr": c}})
	// The links the review publishes and the one checked below expire alike.
	s.links.now = func() time.Time { return testNow }

	code, body := getPage(t, s, pagePath(s, ""))
	if code != http.StatusOK || !strings.Contains(body, "No review yet") {
		t.Fatalf("page before any review = %d, want 200 and pending:\n%s", code, body)
	}
	if p := progress(t, s); p["state"] != "pending" {
		t.Errorf("progress before any review = %v, want pending", p)
	}

	if code, _ := post(t, s, "/webhooks/stub", "pr", "ok"); code != http.StatusAccepted {
		t.Fatalf("webhook = %d, want 202", code)
	}
	<-c.started
	if p := progress(t, s); p["state"] != "running" || p["stage"] != "change" {
		t.Errorf("progress while reading the change = %v, want running in stage change", p)
	}
	if _, body := getPage(t, s, pagePath(s, "")); !strings.Contains(body, "Review running") || !strings.Contains(body, "data-live") ||
		!strings.Contains(body, "/progress?exp=") {
		t.Errorf("page while running does not follow the review:\n%s", body)
	}

	close(c.release)
	c.wait(t)
	deadline := time.Now().Add(5 * time.Second)
	for progress(t, s)["state"] != "finished" {
		if time.Now().After(deadline) {
			t.Fatal("the review page never showed the review finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	code, body = getPage(t, s, pagePath(s, ""))
	if code != http.StatusOK || !strings.Contains(body, "1 file approved") || !strings.Contains(body, "main.go") || strings.Contains(body, "data-live") {
		t.Errorf("page once finished = %d, want the outcome and the full report:\n%s", code, body)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	want := s.links.reviewURL(c.Info())
	if c.starts != 1 || len(c.links) != 2 || c.links[0] != want || c.links[1] != want {
		t.Errorf("%d starts, links %q; want one start, and it and the report linking %s", c.starts, c.links, want)
	}
}

// TestReviewPageNeedsItsSignature: a page whose link the server did not
// sign, or whose link expired, is not found, and without links there are
// no pages.
func TestReviewPageNeedsItsSignature(t *testing.T) {
	s := newTestServer(t, pageOptions(), &stubWebhook{name: "stub"})
	s.links.now = func() time.Time { return testNow }
	base := "/reviews/o/r/1/" + testHead
	for _, path := range []string{base, base + "?sig=", base + "?sig=AAAA", "/reviews/o/r/2/" + testHead + "?" + strings.SplitN(pagePath(s, ""), "?", 2)[1]} {
		if code, _ := getPage(t, s, path); code == http.StatusOK {
			t.Errorf("GET %s = 200, want it refused", path)
		}
	}
	signed := pagePath(s, "")
	if code, _ := getPage(t, s, signed); code != http.StatusOK {
		t.Errorf("signed page = %d, want 200", code)
	}
	if code, _ := getPage(t, s, base+"?sig="+url.QueryEscape(s.links.sign("o/r", 1, testHead, testNow.Unix()))); code == http.StatusOK {
		t.Error("page without its expiry = 200, want it refused")
	}
	s.links.now = func() time.Time { return testNow.Add(testLife) }
	for _, path := range []string{signed, strings.Replace(signed, "?", "/progress?", 1)} {
		if code, _ := getPage(t, s, path); code != http.StatusNotFound {
			t.Errorf("GET %s once expired = %d, want 404", path, code)
		}
	}

	none := newTestServer(t, testOptions(), &stubWebhook{name: "stub"})
	if code, _ := getPage(t, none, pagePath(s, "")); code != http.StatusNotFound {
		t.Errorf("page without links = %d, want 404", code)
	}
}

// TestReviewPageReadsTheRecord: a page shows a review the record has: one
// finished in full, one another server is running as running, and is
// unavailable when the record cannot be read.
func TestReviewPageReadsTheRecord(t *testing.T) {
	state, err := json.Marshal(&approval.ReviewState{
		Files:   map[string]approval.FileState{"pkg/recorded.go": {Path: "pkg/recorded.go", Status: approval.StatusSatisfied}},
		Overall: approval.StatusSatisfied,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := &ReviewRecord{Status: ReviewReported, Outcome: approval.Outcome{Verdict: approval.Passed, Summary: "1 file approved"}, State: state}
	tests := []struct {
		name     string
		history  fakeHistory
		code     int
		state    string
		contains string
	}{
		{"finished", fakeHistory{rec: finished}, http.StatusOK, "finished", "pkg/recorded.go"},
		{"running elsewhere", fakeHistory{rec: &ReviewRecord{Status: ReviewRunning}}, http.StatusOK, "running", "Another Canopy server runs this review"},
		{"none", fakeHistory{}, http.StatusOK, "pending", "No review yet"},
		{"unreadable", fakeHistory{err: errors.New("database down")}, http.StatusServiceUnavailable, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := pageOptions()
			opts.History = &tt.history
			s := newTestServer(t, opts, &stubWebhook{name: "stub"})
			code, body := getPage(t, s, pagePath(s, ""))
			if code != tt.code || !strings.Contains(body, tt.contains) {
				t.Errorf("page = %d, want %d containing %q:\n%s", code, tt.code, tt.contains, body)
			}
			if tt.state != "" {
				if p := progress(t, s); p["state"] != tt.state {
					t.Errorf("progress = %v, want %s", p, tt.state)
				}
			}
		})
	}
}

// TestTrackerFollowsTheNewestReview: a page shows its newest review; an
// older review's progress and end do not touch it, and finished pages are
// bounded.
func TestTrackerFollowsTheNewestReview(t *testing.T) {
	tr := newTracker(false)
	ref := pageRef{"o/r", 1, testHead}
	older := tr.queue(ref, time.Now())
	newer := tr.queue(ref, time.Now())
	tr.progress(older, approval.Progress{Phase: "approve", Done: 1, Total: 2})
	tr.finish(ref, older, ReviewEnd{Status: ReviewSuperseded}, time.Now())
	if l, _ := tr.get(ref); l.Stage != stageQueued {
		t.Errorf("page stage = %s after an older review progressed and ended, want the newer one queued", l.Stage)
	}
	tr.progress(newer, approval.Progress{Phase: "sift"})
	if l, _ := tr.get(ref); l.Stage != "sift" {
		t.Errorf("page stage = %s, want sift", l.Stage)
	}
	tr.forget(ref, older)
	if _, ok := tr.get(ref); !ok {
		t.Error("forgetting an older review forgot the page's newest")
	}
	tr.finish(ref, newer, ReviewEnd{Status: ReviewReported, State: &approval.ReviewState{}}, time.Now())
	if l, _ := tr.get(ref); l.Stage != stageFinished || l.State != nil {
		t.Errorf("finished page = %+v, want finished without its state (kept only without a history)", l)
	}

	for i := range maxFinishedLive + 5 {
		r := pageRef{"o/r", i + 2, testHead}
		tr.finish(r, tr.queue(r, time.Now()), ReviewEnd{}, time.Now())
	}
	if len(tr.pages) != maxFinishedLive || len(tr.finished) != maxFinishedLive {
		t.Errorf("%d pages, %d finished; want %d of each", len(tr.pages), len(tr.finished), maxFinishedLive)
	}
}
