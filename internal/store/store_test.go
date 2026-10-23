package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/server"
)

// testDatabaseEnv names a PostgreSQL URL the tests may create schemas in.
// Without it, the tests that need a database are skipped.
const testDatabaseEnv = "CANOPY_TEST_DATABASE_URL"

// testKey is the integrity key the tests' stores use.
var testKey = bytes.Repeat([]byte("k"), MinKeyBytes)

// Commit and blob SHAs in the form the schema requires.
const (
	sha1 = "0123456789abcdef0123456789abcdef01234567"
	sha2 = "89abcdef0123456789abcdef0123456789abcdef"
)

// testURL is the test database's URL with its tables in a schema of their
// own, dropped when the test ends, so tests never see each other's rows.
func testURL(t *testing.T) string {
	t.Helper()
	base := os.Getenv(testDatabaseEnv)
	if base == "" {
		t.Skipf("%s is not set", testDatabaseEnv)
	}
	admin, err := Open(t.Context(), Options{URL: base, Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := "canopy_test_" + strings.ToLower(rand.Text())
	if _, err := admin.pool.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.pool.Exec(context.Background(), "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Errorf("drop schema %s: %v", name, err)
		}
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("%s must be a URL: %v", testDatabaseEnv, err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	return u.String()
}

// openTest opens a Store with the schema applied on testURL, or on url
// when given.
func openTest(t *testing.T, key []byte, url ...string) *Store {
	t.Helper()
	u := ""
	if len(url) > 0 {
		u = url[0]
	} else {
		u = testURL(t)
	}
	s, err := Open(t.Context(), Options{URL: u, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.ApplySchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenDoesNotRevealTheURL(t *testing.T) {
	_, err := Open(t.Context(), Options{URL: "postgres://canopy:hunter2@localhost:5432/canopy?sslmode=nonsense", Key: testKey})
	if err == nil {
		t.Fatal("Open accepted an invalid URL")
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "localhost") {
		t.Errorf("error reveals the URL: %v", err)
	}
}

func TestOpenRequiresAKey(t *testing.T) {
	if _, err := Open(t.Context(), Options{URL: "postgres://localhost/canopy", Key: testKey[:MinKeyBytes-1]}); err == nil {
		t.Error("Open accepted a short key")
	}
}

func TestApplySchemaIsIdempotentAndConcurrent(t *testing.T) {
	s := openTest(t, testKey)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = s.ApplySchema(t.Context()) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaRejectsMalformedRows(t *testing.T) {
	s := openTest(t, testKey)
	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{"short tree commit SHA", "INSERT INTO tree_listings (scope, repo, commit_sha, blob_count, listing, mac) VALUES ('s', 'o/r', 'main', 0, '', '')", nil},
		{"upper-case blob SHA", "INSERT INTO blobs (scope, sha, content) VALUES ('s', $1, '')", []any{strings.ToUpper(sha1)}},
		{"blob over the rules file limit", "INSERT INTO blobs (scope, sha, content) VALUES ('s', $1, $2)", []any{sha1, make([]byte, 512<<10+1)}},
		{"unknown delivery disposition", "INSERT INTO webhook_deliveries (platform, delivery_id, event, disposition) VALUES ('github', 'd', 'ping', 'maybe')", nil},
		{"reported past the requests", "INSERT INTO pull_requests (scope, repo, pr_number, requested_order, reported_order) VALUES ('s', 'o/r', 1, 1, 2)", nil},
		{"unknown verdict", "INSERT INTO reviews (delivery_id, platform, scope, repo, pr_number, base_sha, head_sha, author, action, review_key, request_order, status, verdict, queued_at, finished_at) " +
			"VALUES ('d', 'github', 's', 'o/r', 1, 'b', 'h', 'a', 'x', 'k', 1, 'reported', 'approved', now(), now())", nil},
		{"finished review without a verdict", "INSERT INTO reviews (delivery_id, platform, scope, repo, pr_number, base_sha, head_sha, author, action, review_key, request_order, status, queued_at, finished_at) " +
			"VALUES ('d', 'github', 's', 'o/r', 1, 'b', 'h', 'a', 'x', 'k', 1, 'reported', now(), now())", nil},
		{"running review with an end", "INSERT INTO reviews (delivery_id, platform, scope, repo, pr_number, base_sha, head_sha, author, action, review_key, request_order, queued_at, finished_at) " +
			"VALUES ('d', 'github', 's', 'o/r', 1, 'b', 'h', 'a', 'x', 'k', 1, now(), now())", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.pool.Exec(t.Context(), tt.sql, tt.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" { // check_violation
				t.Errorf("got %v, want a check constraint violation", err)
			}
		})
	}
}

// TestStoreKeepsRepositoryContentsPerScope: listings, blobs, and responses
// round-trip, scope by scope.
func TestStoreKeepsRepositoryContentsPerScope(t *testing.T) {
	s := openTest(t, testKey)
	ctx := t.Context()
	listing := map[string]server.TreeBlob{"RULES": {SHA: sha2, Size: 12}, "src/a.go": {SHA: sha1, Size: 1 << 40}}
	if err := s.PutTree(ctx, "a", "o/r", sha1, listing); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBlob(ctx, "a", sha2, []byte("rules")); err != nil {
		t.Fatal(err)
	}
	resp := server.CachedResponse{ETag: `W/"e"`, Header: http.Header{"Link": {`<n>; rel="next"`}}, Body: []byte(`[{"login":"ann"}]`)}
	if err := s.PutResponse(ctx, "a", "https://api/orgs/o/teams/t/members", resp); err != nil {
		t.Fatal(err)
	}

	if got, ok, err := s.Tree(ctx, "a", "o/r", sha1); err != nil || !ok || !reflect.DeepEqual(got, listing) {
		t.Errorf("Tree = %v, %v, %v; want the listing as put", got, ok, err)
	}
	if got, ok, err := s.Blob(ctx, "a", sha2); err != nil || !ok || string(got) != "rules" {
		t.Errorf("Blob = %q, %v, %v; want the contents as put", got, ok, err)
	}
	if got, ok, err := s.Response(ctx, "a", "https://api/orgs/o/teams/t/members"); err != nil || !ok || !reflect.DeepEqual(got, resp) {
		t.Errorf("Response = %+v, %v, %v; want the response as put", got, ok, err)
	}
	for name, miss := range map[string]func() (bool, error){
		"tree in scope b":     func() (bool, error) { _, ok, err := s.Tree(ctx, "b", "o/r", sha1); return ok, err },
		"tree at another sha": func() (bool, error) { _, ok, err := s.Tree(ctx, "a", "o/r", sha2); return ok, err },
		"blob in scope b":     func() (bool, error) { _, ok, err := s.Blob(ctx, "b", sha2); return ok, err },
		"response in scope b": func() (bool, error) {
			_, ok, err := s.Response(ctx, "b", "https://api/orgs/o/teams/t/members")
			return ok, err
		},
	} {
		if ok, err := miss(); ok || err != nil {
			t.Errorf("%s: hit %v, err %v; want a miss", name, ok, err)
		}
	}

	resp.ETag, resp.Body = `W/"f"`, []byte(`[]`)
	if err := s.PutResponse(ctx, "a", "https://api/orgs/o/teams/t/members", resp); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.Response(ctx, "a", "https://api/orgs/o/teams/t/members"); got.ETag != `W/"f"` {
		t.Errorf("Response after replacing = %+v, want the newer one", got)
	}
}

// TestStoreRejectsRowsItDidNotSeal: a cached row changed in the database,
// moved to another scope, or sealed under another key is not served, so
// write access to the database alone cannot change the rules or members a
// review sees.
func TestStoreRejectsRowsItDidNotSeal(t *testing.T) {
	url := testURL(t)
	s := openTest(t, testKey, url)
	ctx := t.Context()
	put := func() {
		t.Helper()
		if _, err := s.pool.Exec(ctx, "DELETE FROM tree_listings; DELETE FROM responses"); err != nil {
			t.Fatal(err)
		}
		if err := s.PutTree(ctx, "a", "o/r", sha1, map[string]server.TreeBlob{"RULES": {SHA: sha2, Size: 1}}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutResponse(ctx, "a", "u", server.CachedResponse{ETag: `"e"`, Body: []byte(`["ann"]`)}); err != nil {
			t.Fatal(err)
		}
	}
	tamper := map[string]string{
		"listing changed":    "UPDATE tree_listings SET listing = listing || '\\x00'::bytea",
		"listing moved":      "UPDATE tree_listings SET scope = 'b'",
		"response changed":   `UPDATE responses SET body = '["mallory"]'`,
		"response re-tagged": `UPDATE responses SET etag = '"x"'`,
	}
	for name, sql := range tamper {
		t.Run(name, func(t *testing.T) {
			put()
			if _, err := s.pool.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
			_, okA, errA := s.Tree(ctx, "a", "o/r", sha1)
			_, okB, errB := s.Tree(ctx, "b", "o/r", sha1)
			_, okR, errR := s.Response(ctx, "a", "u")
			if strings.HasPrefix(name, "listing") && (okA || okB || !errors.Is(errors.Join(errA, errB), ErrTampered)) {
				t.Errorf("tampered listing served: %v %v, errors %v %v", okA, okB, errA, errB)
			}
			if strings.HasPrefix(name, "response") && (okR || !errors.Is(errR, ErrTampered)) {
				t.Errorf("tampered response served: %v, error %v", okR, errR)
			}
		})
	}

	put()
	other := openTest(t, bytes.Repeat([]byte("x"), MinKeyBytes), url)
	if _, ok, err := other.Tree(ctx, "a", "o/r", sha1); ok || !errors.Is(err, ErrTampered) {
		t.Errorf("a store with another key read the listing: %v, %v", ok, err)
	}
}

func TestListingEncoding(t *testing.T) {
	listing := map[string]server.TreeBlob{"": {SHA: "", Size: 0}, "a/b": {SHA: sha1, Size: 7}, "é": {SHA: sha2, Size: 1 << 33}}
	got, err := decodeListing(encodeListing(listing))
	if err != nil || !reflect.DeepEqual(got, listing) {
		t.Errorf("round trip = %v, %v; want %v", got, err, listing)
	}
	if got, err := decodeListing(encodeListing(nil)); err != nil || len(got) != 0 {
		t.Errorf("empty round trip = %v, %v", got, err)
	}
	enc := encodeListing(listing)
	for _, bad := range [][]byte{nil, {2}, enc[:len(enc)-1], append(enc[:1:1], 0xff)} {
		if _, err := decodeListing(bad); !errors.Is(err, errCorrupt) {
			t.Errorf("decodeListing(%x) err = %v, want errCorrupt", bad, err)
		}
	}
}

// TestReportInOrder: requests are ordered per pull request, whatever its
// head; a report runs only if no later request has reported, one report of
// a pull request at a time; a failed report leaves the order where it was.
func TestReportInOrder(t *testing.T) {
	s := openTest(t, testKey)
	ctx := t.Context()
	pr := server.PullRequestInfo{Scope: "a", Repo: "o/r", Number: 7, HeadSHA: sha1}
	for want := int64(1); want <= 3; want++ {
		req := pr
		if want == 3 {
			req.HeadSHA = sha2 // a push: the same pull request, ordered with its earlier head
		}
		if got, err := s.RequestReview(ctx, req); err != nil || got != want {
			t.Fatalf("RequestReview = %d, %v; want %d", got, err, want)
		}
	}
	other := pr
	other.Number = 8
	if got, _ := s.RequestReview(ctx, other); got != 1 {
		t.Errorf("another pull request's first order = %d, want 1", got)
	}

	var (
		mu  sync.Mutex
		log []int64
	)
	report := func(order int64, err error) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			log = append(log, order)
			return err
		}
	}
	if called, err := s.ReportInOrder(ctx, pr, 2, report(2, errors.New("api down"))); !called || err == nil {
		t.Fatalf("failing report = %v, %v; want called, with its error", called, err)
	}
	if called, err := s.ReportInOrder(ctx, pr, 2, report(2, nil)); !called || err != nil {
		t.Fatalf("retried report = %v, %v; want called", called, err)
	}
	if called, err := s.ReportInOrder(ctx, pr, 1, report(1, nil)); called || err != nil {
		t.Errorf("older report = %v, %v; want superseded", called, err)
	}

	// Order 3 holds the key while it reports; order 4 waits for it, then
	// reports after it.
	if _, err := s.RequestReview(ctx, pr); err != nil {
		t.Fatal(err)
	}
	holding, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = s.ReportInOrder(ctx, pr, 3, func(c context.Context) error {
			close(holding)
			<-release
			return report(3, nil)(c)
		})
	})
	<-holding
	done := make(chan bool)
	go func() {
		called, _ := s.ReportInOrder(ctx, pr, 4, report(4, nil))
		done <- called
	}()
	select {
	case <-done:
		t.Fatal("order 4 reported while order 3 held the key")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	if !<-done {
		t.Error("order 4 did not report")
	}
	if want := []int64{2, 2, 3, 4}; !reflect.DeepEqual(log, want) {
		t.Errorf("reports = %v, want %v", log, want)
	}

	// A report published as its context ends (the server stopping) is
	// still recorded, so an older review cannot report after it.
	if _, err := s.RequestReview(ctx, pr); err != nil {
		t.Fatal(err)
	}
	stopping, stop := context.WithCancel(ctx)
	called, err := s.ReportInOrder(stopping, pr, 5, func(context.Context) error { stop(); return nil })
	if !called || err != nil {
		t.Fatalf("report as the context ends = %v, %v; want called and recorded", called, err)
	}
	if called, err := s.ReportInOrder(ctx, pr, 4, report(4, nil)); called || err != nil {
		t.Errorf("older report after it = %v, %v; want superseded", called, err)
	}
}

// TestRecordsDeliveriesAndReviews: a delivery and a review's start and end
// are recorded, with the review's input, state, and targets.
func TestRecordsDeliveriesAndReviews(t *testing.T) {
	s := openTest(t, testKey)
	ctx := t.Context()
	pr := server.PullRequestInfo{Scope: "a", Repo: "o/r", Number: 7, BaseSHA: sha1, HeadSHA: sha2, Author: "dev", Action: "pull_request.opened"}
	for _, d := range []server.DeliveryRecord{
		{Platform: "github", ID: "d1", Event: "pull_request", Disposition: server.DeliveryQueued, PullRequest: &pr},
		{Platform: "github", ID: "d2", Event: "ping", Disposition: server.DeliveryIgnored},
	} {
		if err := s.RecordDelivery(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.StartReview(ctx, server.ReviewStart{Platform: "github", DeliveryID: "d1", PullRequest: pr, Key: "o/r@" + sha2, Order: 1, QueuedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	end := server.ReviewEnd{
		Status:  server.ReviewReported,
		Outcome: approval.Outcome{Verdict: approval.Pending, Summary: "1 file awaits approval", Items: []string{"acme/core"}},
		Input:   &approval.ReviewInput{Author: "dev", Approvers: []string{"ann"}, Owners: []string{"o"}},
		State:   &approval.ReviewState{Files: map[string]approval.FileState{}},
		Targets: []server.TargetRecord{{Target: "acme/core", Resolved: true}, {Target: "acme/gone", Resolved: false}},
		Queued:  time.Second, Evaluate: 2 * time.Second, Report: 3 * time.Millisecond,
	}
	if err := s.FinishReview(ctx, id, end); err != nil {
		t.Fatal(err)
	}

	var (
		status, verdict, approvers string
		items                      []string
		reportMS                   int32
		finished                   bool
	)
	err = s.pool.QueryRow(ctx, `SELECT status, verdict, items, input->'approvers'->>0, report_ms, finished_at IS NOT NULL
		FROM reviews WHERE id = $1`, id).Scan(&status, &verdict, &items, &approvers, &reportMS, &finished)
	if err != nil {
		t.Fatal(err)
	}
	if status != "reported" || verdict != "pending" || !reflect.DeepEqual(items, []string{"acme/core"}) || approvers != "ann" || reportMS != 3 || !finished {
		t.Errorf("review row = %s %s %v %s %d %v", status, verdict, items, approvers, reportMS, finished)
	}
	var unresolved string
	if err := s.pool.QueryRow(ctx, "SELECT string_agg(target, ',') FROM review_targets WHERE review_id = $1 AND NOT resolved", id).Scan(&unresolved); err != nil || unresolved != "acme/gone" {
		t.Errorf("unresolved targets = %q, %v; want acme/gone", unresolved, err)
	}
	var queued, ignored int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE disposition = 'queued' AND pr_number = 7),
		count(*) FILTER (WHERE disposition = 'ignored' AND repo IS NULL) FROM webhook_deliveries`).Scan(&queued, &ignored); err != nil || queued != 1 || ignored != 1 {
		t.Errorf("deliveries: %d queued, %d ignored, %v; want one each", queued, ignored, err)
	}
}

// TestPruneRemovesRowsPastTheirRetention: cached rows and idle pull
// request rows past their retention are removed, in batches, and newer
// ones and the review history are kept.
func TestPruneRemovesRowsPastTheirRetention(t *testing.T) {
	s := openTest(t, testKey)
	ctx := t.Context()
	now := time.Now()
	for i := range pruneBatch + 5 {
		sha := fmt.Sprintf("%040x", i)
		if err := s.PutTree(ctx, "a", "o/r", sha, map[string]server.TreeBlob{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutBlob(ctx, "a", sha1, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutResponse(ctx, "a", "u", server.CachedResponse{ETag: `"e"`}); err != nil {
		t.Fatal(err)
	}
	idle := server.PullRequestInfo{Scope: "a", Repo: "o/r", Number: 1}
	active := server.PullRequestInfo{Scope: "a", Repo: "o/r", Number: 2}
	for _, pr := range []server.PullRequestInfo{idle, active} {
		if _, err := s.RequestReview(ctx, pr); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordDelivery(ctx, server.DeliveryRecord{Platform: "github", ID: "d", Event: "ping", Disposition: server.DeliveryIgnored}); err != nil {
		t.Fatal(err)
	}
	// Age everything but one tree listing and the active pull request.
	for _, sql := range []string{
		"UPDATE tree_listings SET created_at = now() - interval '8 days'",
		"UPDATE blobs SET created_at = now() - interval '31 days'",
		"UPDATE responses SET updated_at = now() - interval '8 days'",
		"UPDATE pull_requests SET updated_at = now() - interval '31 days' WHERE pr_number = 1",
		"UPDATE webhook_deliveries SET received_at = now() - interval '400 days'",
	} {
		if _, err := s.pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutTree(ctx, "a", "o/r", sha2, map[string]server.TreeBlob{}); err != nil {
		t.Fatal(err)
	}

	pruned, err := s.Prune(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"tree_listings": pruneBatch + 5, "blobs": 1, "responses": 1, "pull_requests": 1}
	if !reflect.DeepEqual(pruned, want) {
		t.Errorf("pruned = %v, want %v", pruned, want)
	}
	if _, ok, _ := s.Tree(ctx, "a", "o/r", sha2); !ok {
		t.Error("a fresh tree listing was pruned")
	}
	var deliveries int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM webhook_deliveries").Scan(&deliveries); err != nil || deliveries != 1 {
		t.Errorf("deliveries after pruning = %d, %v; want the history kept", deliveries, err)
	}
	// The pruned pull request starts again; the active one goes on.
	if got, _ := s.RequestReview(ctx, idle); got != 1 {
		t.Errorf("pruned pull request's next order = %d, want 1", got)
	}
	if got, _ := s.RequestReview(ctx, active); got != 2 {
		t.Errorf("active pull request's next order = %d, want 2", got)
	}
}
