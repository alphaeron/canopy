package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"
	"github.com/jackc/pgx/v5"

	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/config"
	"github.com/alphaeron/canopy/internal/platform/ghplatform"
	"github.com/alphaeron/canopy/internal/server"
)

// The world every case runs in: repository acme/widgets, whose Go files
// the acme/go-reviewers team approves, and whose pull requests dana opens
// from branch "feature".
const (
	owner  = "acme"
	repo   = owner + "/widgets"
	author = "dana"
	branch = "feature"

	appID          = 4242
	installationID = 77
	webhookSecret  = "e2e-secret"

	// stepTimeout bounds waiting for the check run each step reports.
	stepTimeout = 10 * time.Second
)

// baseFiles is "main" in every case: one rule requiring a go-reviewers
// approval for Go files, and the repository owner's for the rest.
var baseFiles = map[string]string{
	brand.RulesFileName: `- ".*\\.go":
    name: "Go"
    approvers:
      - target: "acme/go-reviewers"
        required_count: 1
`,
	"main.go":   "package main\n",
	"README.md": "# widgets\n",
}

var (
	teams = map[string][]string{"acme/go-reviewers": {"alice", "bob"}}
	users = map[string]bool{"acme": false, "dana": false, "alice": false, "bob": false, "carol": false}
)

// appKey is the GitHub App's private key, generated once for every case.
var appKey = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 2048) })

// env is one case's running world: the fake GitHub, the review server
// installed on it as the app, and the pull request the case opened.
type env struct {
	t      *testing.T
	github *fakeGitHub
	db     *pgx.Conn // the server's database; nil when it runs without one
	web    *httptest.Server
	pr     int
	stop   func()
}

// step is one thing a user does on GitHub, and the check run the app must
// report for it.
type step struct {
	name string
	do   func(ctx context.Context, e *env)
	want check
}

// check is what a reported check run must say.
type check struct {
	conclusion string
	title      string // a substring of the title
}

// TestGitHubEndToEnd runs each case in a fresh world: it stages the fake
// GitHub with the case's branch, starts the review server as the app,
// takes the case's steps, checks the check run each step reports and the
// review page it links to, and tears everything down, checking nothing
// else was reported.
func TestGitHubEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	tests := []struct {
		name string
		// feature is what branch "feature" writes over main; "" deletes.
		feature map[string]string
		steps   []step
	}{
		{
			name:    "team member approves",
			feature: map[string]string{"main.go": "package main\n\nfunc main() {}\n"},
			steps: []step{
				{name: "open", do: openPR, want: check{"action_required", "acme/go-reviewers"}},
				{name: "alice approves", do: review("alice", "APPROVE"), want: check{"success", "1 file approved"}},
			},
		},
		{
			name:    "non-member's approval does not count",
			feature: map[string]string{"util.go": "package main\n"},
			steps: []step{
				{name: "open", do: openPR, want: check{"action_required", "acme/go-reviewers"}},
				{name: "carol approves", do: review("carol", "APPROVE"), want: check{"action_required", "acme/go-reviewers"}},
			},
		},
		{
			name:    "change request cancels an approval",
			feature: map[string]string{"main.go": "package main // v2\n"},
			steps: []step{
				{name: "open", do: openPR, want: check{"action_required", "acme/go-reviewers"}},
				{name: "bob approves", do: review("bob", "APPROVE"), want: check{"success", "1 file approved"}},
				{name: "bob requests changes", do: review("bob", "REQUEST_CHANGES"), want: check{"action_required", "acme/go-reviewers"}},
			},
		},
		{
			name:    "comment leaves an approval standing",
			feature: map[string]string{"main.go": "package main // v2\n"},
			steps: []step{
				{name: "open", do: openPR, want: check{"action_required", "acme/go-reviewers"}},
				{name: "alice approves", do: review("alice", "APPROVE"), want: check{"success", "1 file approved"}},
				{name: "alice comments", do: review("alice", "COMMENT"), want: check{"success", "1 file approved"}},
			},
		},
		{
			name:    "push keeps an approval and reports on the new head",
			feature: map[string]string{"main.go": "package main // v2\n"},
			steps: []step{
				{name: "open", do: openPR, want: check{"action_required", "acme/go-reviewers"}},
				{name: "alice approves", do: review("alice", "APPROVE"), want: check{"success", "1 file approved"}},
				{name: "push", do: push(map[string]string{"util.go": "package main\n"}), want: check{"success", "all 2 files approved"}},
			},
		},
		{
			name:    "malformed proposed rules file fails",
			feature: map[string]string{brand.RulesFileName: "- [not: a rule\n"},
			steps: []step{
				{name: "open", do: openPR, want: check{"failure", brand.RulesFileName}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, tt.feature)
			for i, s := range tt.steps {
				ctx, cancel := context.WithTimeout(t.Context(), stepTimeout)
				s.do(ctx, e)
				runs, err := e.github.waitCheckRuns(ctx, repo, i+1)
				if err != nil {
					cancel()
					t.Fatalf("step %q: %v", s.name, err)
				}
				e.assertCheck(s.name, runs[i], s.want)
				e.assertReviewPage(ctx, s.name, runs[i], s.want)
				cancel()
			}

			e.waitRecorded(len(tt.steps))
			e.stop()
			// Each review marks its check running, then reports.
			runs := e.github.checkRuns(repo)
			if len(runs) != 2*len(tt.steps) {
				t.Errorf("check runs = %d after teardown, want %d (a start and a report per step)", len(runs), 2*len(tt.steps))
			}
			for i, run := range runs {
				if want := []string{"in_progress", "completed"}[i%2]; run.GetStatus() != want {
					t.Errorf("check run %d is %s, want %s", i, run.GetStatus(), want)
				}
			}
			for _, d := range e.github.deliveries() {
				if d.status != 202 {
					t.Errorf("%s %s webhook answered %d, want 202", d.event, d.action, d.status)
				}
			}
			e.assertRecorded(len(e.github.deliveries()), len(tt.steps))
		})
	}
}

// newEnv stages the fake GitHub with acme/widgets and branch "feature"
// writing feature over main, starts the review server as the app, and
// installs the app. Everything stops when the test ends, or at e.stop.
func newEnv(t *testing.T, feature map[string]string) *env {
	t.Helper()
	key, err := appKey()
	if err != nil {
		t.Fatalf("generate app key: %v", err)
	}
	gh := newFakeGitHub(t, teams, users)
	gh.createRepo(owner, "widgets", baseFiles)
	gh.push(t.Context(), repo, branch, "main", feature)

	var (
		repoStore server.RepoStore
		recorder  server.Recorder
	)
	db, conn := testDatabase(t)
	if db != nil {
		repoStore, recorder = db, db
	}
	var history server.ReviewHistory
	if db != nil {
		history = db
	}
	cache := server.NewRepoCache(repoStore, discardLogger())
	platform, err := ghplatform.NewPRWebhook(config.GitHub{
		AppID:         strconv.Itoa(appID),
		PrivateKey:    string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		WebhookSecret: webhookSecret,
		APIURL:        gh.url,
	}, cache, discardLogger())
	if err != nil {
		t.Fatalf("NewPRWebhook: %v", err)
	}
	// The server's public URL is the test server's, which must exist first.
	var handler http.Handler
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	ctx, cancel := context.WithCancel(context.Background())
	srv, err := server.New(ctx, server.Options{
		Workers:         2,
		Queue:           8,
		ReviewTimeout:   stepTimeout,
		ReportTimeout:   stepTimeout,
		ShutdownTimeout: stepTimeout,
		Version:         "e2e",
		Log:             discardLogger(),
		Recorder:        recorder,
		History:         history,
		PublicURL:       web.URL,
		LinkKey:         []byte(strings.Repeat("l", server.MinLinkKeyBytes)),
		LinkLifetime:    server.MinLinkLifetime,
	}, platform)
	if err != nil {
		cancel()
		web.Close()
		t.Fatalf("server.New: %v", err)
	}
	handler = srv.Handler()

	// Stop the webhook endpoint first, then the reviews, before the fake
	// GitHub (whose cleanup newFakeGitHub registered earlier) closes.
	stop := sync.OnceFunc(func() {
		web.Close()
		cancel()
		srv.Wait()
		cache.Wait()
	})
	t.Cleanup(stop)

	gh.installApp(fakeApp{
		id:           appID,
		installation: installationID,
		key:          &key.PublicKey,
		webhookURL:   web.URL + platform.WebhookPath(),
		secret:       webhookSecret,
	})
	return &env{t: t, github: gh, db: conn, web: web, stop: stop}
}

// assertCheck checks run is the app's check on the pull request's current
// head, as want says.
func (e *env) assertCheck(step string, run github.CreateCheckRunOptions, want check) {
	e.t.Helper()
	if run.Name != brand.Command {
		e.t.Errorf("step %q: check run name = %q, want %q", step, run.Name, brand.Command)
	}
	if head := e.github.headSHA(repo, branch); run.HeadSHA != head {
		e.t.Errorf("step %q: check run on %s, want the head %s", step, run.HeadSHA, head)
	}
	if run.GetStatus() != "completed" || run.GetConclusion() != want.conclusion {
		e.t.Errorf("step %q: check run %s/%s, want completed/%s", step, run.GetStatus(), run.GetConclusion(), want.conclusion)
	}
	if title := run.GetOutput().GetTitle(); !strings.Contains(title, want.title) {
		e.t.Errorf("step %q: check run title = %q, want it to contain %q", step, title, want.title)
	}
}

// assertReviewPage follows run's details link to its review page, waits
// for the page to show the review finished, and checks it shows the title
// the check reported.
func (e *env) assertReviewPage(ctx context.Context, step string, run github.CreateCheckRunOptions, want check) {
	e.t.Helper()
	link := run.GetDetailsURL()
	if !strings.HasPrefix(link, e.web.URL+"/reviews/") {
		e.t.Errorf("step %q: check run links %q, want a review page on the server", step, link)
		return
	}
	if summary := run.GetOutput().GetSummary(); !strings.Contains(summary, link) {
		e.t.Errorf("step %q: check run summary does not link the review page", step)
	}
	progress := strings.Replace(link, "?", "/progress?", 1)
	for {
		var p struct{ State string }
		if err := getJSON(ctx, progress, &p); err != nil {
			e.t.Errorf("step %q: review progress: %v", step, err)
			return
		}
		if p.State == "finished" {
			break
		}
		select {
		case <-ctx.Done():
			e.t.Errorf("step %q: review page shows %s, never finished", step, p.State)
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
	body, err := getBody(ctx, link)
	if err != nil {
		e.t.Errorf("step %q: review page: %v", step, err)
		return
	}
	if !strings.Contains(body, want.title) || strings.Contains(body, "data-live") {
		e.t.Errorf("step %q: review page does not show the finished review's outcome %q", step, want.title)
	}
	tampered := strings.Replace(link, "sig=", "sig=A", 1)
	if resp, err := http.Get(tampered); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			e.t.Errorf("step %q: tampered review page link = %d, want 404", step, resp.StatusCode)
		}
	}
}

// getBody is the body of a successful GET of url.
func getBody(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err == nil && resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return string(body), err
}

// getJSON decodes the body of a successful GET of url into v.
func getJSON(ctx context.Context, url string, v any) error {
	body, err := getBody(ctx, url)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(body), v)
}

// openPR opens the pull request from "feature" into "main" as the author.
func openPR(ctx context.Context, e *env) {
	e.t.Helper()
	pr, _, err := e.github.userClient(author).PullRequests.Create(ctx, owner, "widgets",
		github.CreatePullRequest{Title: new("Change widgets"), Head: branch, Base: "main"})
	if err != nil {
		e.t.Fatalf("open pull request: %v", err)
	}
	e.pr = pr.GetNumber()
}

// review submits a review of the pull request as login: APPROVE,
// REQUEST_CHANGES, or COMMENT.
func review(login, event string) func(context.Context, *env) {
	return func(ctx context.Context, e *env) {
		e.t.Helper()
		if _, _, err := e.github.userClient(login).PullRequests.CreateReview(ctx, owner, "widgets", e.pr,
			&github.PullRequestReviewRequest{Event: new(event)}); err != nil {
			e.t.Fatalf("%s %s: %v", login, event, err)
		}
	}
}

// push pushes files over "feature" ("" deletes).
func push(files map[string]string) func(context.Context, *env) {
	return func(ctx context.Context, e *env) {
		e.github.push(ctx, repo, branch, "main", files)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
