package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
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
// takes the case's steps, checks the check run each step reports, and
// tears everything down, checking nothing else was reported.
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
				cancel()
				if err != nil {
					t.Fatalf("step %q: %v", s.name, err)
				}
				e.assertCheck(s.name, runs[i], s.want)
			}

			e.waitRecorded(len(tt.steps))
			e.stop()
			if runs := e.github.checkRuns(repo); len(runs) != len(tt.steps) {
				t.Errorf("check runs = %d after teardown, want %d (one per step)", len(runs), len(tt.steps))
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
	}, platform)
	if err != nil {
		cancel()
		t.Fatalf("server.New: %v", err)
	}
	web := httptest.NewServer(srv.Handler())

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
	return &env{t: t, github: gh, db: conn, stop: stop}
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
