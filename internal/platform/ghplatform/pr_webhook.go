package ghplatform

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/config"
	"github.com/alphaeron/canopy/internal/server"
)

// Webhook events and actions that start a review: a pull request opened,
// updated, reopened, marked ready, moved to another base branch, labeled,
// or unlabeled, and a review submitted or dismissed.
const (
	eventPullRequest       = "pull_request"
	eventPullRequestReview = "pull_request_review"
)

var (
	pullRequestActions = []string{"opened", "synchronize", "reopened", "ready_for_review", "edited", "labeled", "unlabeled"}
	reviewActions      = []string{"submitted", "dismissed"}
)

// PRWebhook is GitHub's pull request webhook (server.PRWebhook): it
// verifies GitHub's webhook signatures and turns pull request events into
// pull requests to review, acting as a GitHub App. Each installation of
// the app gets one client and one ApproverResolver, reused across its
// reviews. It is safe for concurrent use.
type PRWebhook struct {
	secret []byte
	auth   *appAuth
	cache  *server.RepoCache // nil keeps nothing
	log    *slog.Logger

	mu       sync.Mutex
	installs map[int64]*installation
}

var _ server.PRWebhook = (*PRWebhook)(nil)

// installation is the client, resolver, and cache of one installation of
// the app.
type installation struct {
	client   *github.Client
	resolver *ApproverResolver
	cache    server.CacheScope
}

// NewPRWebhook builds the GitHub platform from its configuration section,
// which must Validate. Each installation reads base trees and rules files
// through repo, the server's repository cache, keyed by installation; nil
// keeps nothing. log may be nil (defaults to slog.Default()).
func NewPRWebhook(cfg config.GitHub, repo *server.RepoCache, log *slog.Logger) (*PRWebhook, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	id, err := cfg.AppIDNumber()
	if err != nil {
		return nil, err
	}
	key, err := cfg.RSAPrivateKey()
	if err != nil {
		return nil, err
	}
	return &PRWebhook{
		secret: []byte(cfg.WebhookSecret),
		auth: &appAuth{
			id:        id,
			key:       key,
			baseURL:   cfg.BaseURL(),
			transport: &retryTransport{base: apiTransport(cfg.APITimeout())},
			now:       time.Now,
		},
		cache:    repo,
		log:      log,
		installs: map[int64]*installation{},
	}, nil
}

// apiTransport is the HTTP transport for GitHub API requests: a copy of
// http.DefaultTransport, keeping its proxy-from-environment, dial and TLS
// handshake timeouts, and connection pooling, whose response headers must
// arrive within timeout. It never modifies http.DefaultTransport.
func apiTransport(timeout time.Duration) *http.Transport {
	t := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if d, ok := http.DefaultTransport.(*http.Transport); ok {
		t = d.Clone()
	}
	t.ResponseHeaderTimeout = timeout
	return t
}

// Name is the platform's name in logs and the OpenAPI document.
func (p *PRWebhook) Name() string { return string(config.PlatformGitHub) }

// maxPayloadBytes is GitHub's webhook payload limit: GitHub does not
// deliver a larger event.
const maxPayloadBytes = 25 << 20

// WebhookPath is where the GitHub App's webhook URL points.
func (p *PRWebhook) WebhookPath() string { return "/webhooks/" + string(config.PlatformGitHub) }

// WebhookMaxBytes is GitHub's webhook payload limit.
func (p *PRWebhook) WebhookMaxBytes() int64 { return maxPayloadBytes }

// WebhookContentTypes is JSON: a GitHub App's webhook always delivers it.
func (p *PRWebhook) WebhookContentTypes() []string { return []string{"application/json"} }

// WebhookDescription describes the GitHub App webhook for the OpenAPI
// document.
func (p *PRWebhook) WebhookDescription() string {
	return "The GitHub App's webhook. `X-Hub-Signature-256` authenticates each request: an HMAC-SHA256 of the body " +
		"with the webhook secret. `X-GitHub-Event` names the event. `" + eventPullRequest + "` events (" +
		strings.Join(pullRequestActions, ", ") + "; `edited` only when the base branch changes) and `" +
		eventPullRequestReview + "` events (" + strings.Join(reviewActions, ", ") + ") start a review of the " +
		"pull request, unless it is closed; every other event is acknowledged and ignored."
}

// VerifyWebhook checks the request's X-Hub-Signature-256 against the
// webhook secret and returns the event type and the verified body. Nothing
// in an unverified request is trusted.
func (p *PRWebhook) VerifyWebhook(r *http.Request) (string, []byte, error) {
	body, err := github.ValidatePayload(r, p.secret)
	if err != nil {
		return "", nil, fmt.Errorf("verify GitHub webhook: %w", err)
	}
	return github.WebHookType(r), body, nil
}

// DeliveryID is the delivery's GitHub-assigned ID (X-GitHub-Delivery),
// which a redelivery repeats.
func (p *PRWebhook) DeliveryID(r *http.Request) string { return r.Header.Get("X-GitHub-Delivery") }

// PullRequest turns a verified webhook into the pull request to review. It
// reports false, with no error, for an event or action that does not start
// a review. It makes no API calls: the pull request reads what it needs on
// first use.
func (p *PRWebhook) PullRequest(event string, body []byte) (server.PullRequest, bool, error) {
	var (
		repo   *github.Repository
		pr     *github.PullRequest
		inst   *github.Installation
		action string
	)
	switch event {
	case eventPullRequest:
		var ev github.PullRequestEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			return nil, false, fmt.Errorf("parse %s event: %w", event, err)
		}
		if ev.GetAction() == "edited" && ev.GetChanges().GetBase() == nil {
			return nil, false, nil // a title or body edit
		}
		repo, pr, inst, action = ev.GetRepo(), ev.GetPullRequest(), ev.GetInstallation(), ev.GetAction()
		if !slices.Contains(pullRequestActions, action) {
			return nil, false, nil
		}
	case eventPullRequestReview:
		var ev github.PullRequestReviewEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			return nil, false, fmt.Errorf("parse %s event: %w", event, err)
		}
		repo, pr, inst, action = ev.GetRepo(), ev.GetPullRequest(), ev.GetInstallation(), ev.GetAction()
		if !slices.Contains(reviewActions, action) {
			return nil, false, nil
		}
	default:
		return nil, false, nil
	}
	// A closed (or merged) pull request has nothing left to review; one
	// reopened is open again in its reopened event.
	if pr.GetState() == "closed" {
		return nil, false, nil
	}
	if inst.GetID() <= 0 {
		return nil, false, fmt.Errorf("%s event has no app installation", event)
	}
	gh, err := pullRequestFrom(repo, pr)
	if err != nil {
		return nil, false, fmt.Errorf("%s event: %w", event, err)
	}
	gh.action = event + "." + action
	in, err := p.installation(inst.GetID())
	if err != nil {
		return nil, false, err
	}
	return newSource(gh, in.client, in.resolver, in.cache, p.log), true, nil
}

// installation returns the client, resolver, and cache of an installation,
// creating them on first use.
func (p *PRWebhook) installation(id int64) (*installation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if in, ok := p.installs[id]; ok {
		return in, nil
	}
	cache := p.cache.Scope(installationScope(id))
	client, err := p.auth.installationClient(id, cache)
	if err != nil {
		return nil, fmt.Errorf("client for installation %d: %w", id, err)
	}
	in := &installation{client: client, resolver: NewApproverResolver(client), cache: cache}
	p.installs[id] = in
	return in, nil
}

// installationScope names an installation's scope of the server's cache:
// an installation reads only the repositories it is installed on.
func installationScope(id int64) string { return fmt.Sprintf("github/installation/%d", id) }
