package ghplatform

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/config"
	"github.com/alphaeron/canopy/internal/server"
)

// testKey is the app's private key, generated once for every test.
var testKey = sync.OnceValues(func() (*rsa.PrivateKey, error) { return rsa.GenerateKey(rand.Reader, 2048) })

// testPRWebhook is a PRWebhook acting as app testAppID against fake, with
// webhook secret "s3cret", and the app's private key.
func testPRWebhook(t *testing.T, fake *fakeGitHub) (*PRWebhook, *rsa.PrivateKey) {
	t.Helper()
	key, err := testKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	p, err := NewPRWebhook(config.GitHub{
		AppID:         "99",
		PrivateKey:    string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		WebhookSecret: "s3cret",
		APIURL:        fake.serve(t),
	}, nil, testLogger())
	if err != nil {
		t.Fatalf("NewPRWebhook: %v", err)
	}
	return p, key
}

// changeFromEvent is the change fake serves for event, built the way the
// server builds it: through the platform, from the event's JSON.
func changeFromEvent(t *testing.T, fake *fakeGitHub, event *github.PullRequestEvent) server.PullRequest {
	t.Helper()
	p, _ := testPRWebhook(t, fake)
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	c, ok, err := p.PullRequest("pull_request", body)
	if err != nil || !ok {
		t.Fatalf("Change = %v, %v; want a reviewable change", ok, err)
	}
	return c
}

// TestAPITransportKeepsTheDefaults: the API transport keeps the default
// transport's proxy and timeouts, adds the response header timeout, and
// leaves the default transport untouched.
func TestAPITransportKeepsTheDefaults(t *testing.T) {
	def := http.DefaultTransport.(*http.Transport)
	got := apiTransport(7 * time.Second)
	if got == def {
		t.Fatal("apiTransport returned http.DefaultTransport, want a copy")
	}
	if got.ResponseHeaderTimeout != 7*time.Second {
		t.Errorf("ResponseHeaderTimeout = %v, want 7s", got.ResponseHeaderTimeout)
	}
	if got.Proxy == nil || got.TLSHandshakeTimeout != def.TLSHandshakeTimeout || got.MaxIdleConns != def.MaxIdleConns {
		t.Error("apiTransport dropped the default transport's proxy, TLS handshake timeout, or connection pooling")
	}
	if def.ResponseHeaderTimeout != 0 {
		t.Errorf("http.DefaultTransport.ResponseHeaderTimeout = %v, want it unmodified", def.ResponseHeaderTimeout)
	}
}

func TestNewPRWebhookValidatesItsSection(t *testing.T) {
	if _, err := NewPRWebhook(config.GitHub{AppID: "99"}, nil, nil); err == nil {
		t.Error("NewPRWebhook with an incomplete section succeeded, want an error")
	}
}

// TestPRWebhookDescribesItsWebhook: the webhook's route, limit, format, and
// documentation are GitHub's, supplied by the platform.
func TestPRWebhookDescribesItsWebhook(t *testing.T) {
	p, _ := testPRWebhook(t, &fakeGitHub{})
	if p.WebhookPath() != "/webhooks/github" || p.WebhookMaxBytes() != 25<<20 || !slices.Equal(p.WebhookContentTypes(), []string{"application/json"}) {
		t.Errorf("webhook = %s, %d bytes, %v; want /webhooks/github, 25 MB, JSON", p.WebhookPath(), p.WebhookMaxBytes(), p.WebhookContentTypes())
	}
	for _, want := range []string{"X-Hub-Signature-256", "X-GitHub-Event", "pull_request", "pull_request_review"} {
		if !strings.Contains(p.WebhookDescription(), want) {
			t.Errorf("WebhookDescription = %q, want it to mention %s", p.WebhookDescription(), want)
		}
	}
}

// webhookRequest is a webhook delivery of event with payload, signed with
// secret ("" leaves it unsigned).
func webhookRequest(t *testing.T, event, secret string, payload any) *http.Request {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-GitHub-Event", event)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return r
}

func TestPRWebhookVerifyWebhook(t *testing.T) {
	p, _ := testPRWebhook(t, &fakeGitHub{})
	event, body, err := p.VerifyWebhook(webhookRequest(t, "pull_request", "s3cret", pullRequestEvent("base")))
	if err != nil || event != "pull_request" || len(body) == 0 {
		t.Errorf("VerifyWebhook(signed) = %q, %d bytes, %v; want the event and its body", event, len(body), err)
	}
	for name, secret := range map[string]string{"unsigned": "", "wrong secret": "other"} {
		if _, _, err := p.VerifyWebhook(webhookRequest(t, "pull_request", secret, pullRequestEvent("base"))); err == nil {
			t.Errorf("VerifyWebhook(%s) succeeded, want an error", name)
		}
	}
}

func TestPRWebhookChange(t *testing.T) {
	p, _ := testPRWebhook(t, &fakeGitHub{})
	marshal := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	withAction := func(action string) []byte {
		ev := pullRequestEvent("base")
		ev.Action = new(action)
		return marshal(ev)
	}
	baseChanged := pullRequestEvent("base")
	baseChanged.Action = new("edited")
	baseChanged.Changes = &github.EditChange{Base: &github.EditBase{Ref: &github.EditRef{From: new("old")}}}
	noInstallation := pullRequestEvent("base")
	noInstallation.Installation = nil
	ev := pullRequestEvent("base")
	review := &github.PullRequestReviewEvent{Action: new("submitted"), Repo: ev.Repo, PullRequest: ev.PullRequest, Installation: ev.Installation}
	closedPR := pullRequestEvent("base").PullRequest
	closedPR.State = new("closed")
	reviewOfClosed := &github.PullRequestReviewEvent{Action: new("submitted"), Repo: ev.Repo, PullRequest: closedPR, Installation: ev.Installation}
	editOfClosed := pullRequestEvent("base")
	editOfClosed.Action, editOfClosed.Changes, editOfClosed.PullRequest = new("edited"), baseChanged.Changes, closedPR

	tests := []struct {
		name       string
		event      string
		body       []byte
		reviewable bool
		wantErr    bool
	}{
		{name: "opened", event: "pull_request", body: withAction("opened"), reviewable: true},
		{name: "synchronize", event: "pull_request", body: withAction("synchronize"), reviewable: true},
		{name: "labeled", event: "pull_request", body: withAction("labeled"), reviewable: true},
		{name: "unlabeled", event: "pull_request", body: withAction("unlabeled"), reviewable: true},
		{name: "closed", event: "pull_request", body: withAction("closed")},
		{name: "title edited", event: "pull_request", body: withAction("edited")},
		{name: "base branch changed", event: "pull_request", body: marshal(baseChanged), reviewable: true},
		{name: "review submitted", event: "pull_request_review", body: marshal(review), reviewable: true},
		{name: "review of a closed pull request", event: "pull_request_review", body: marshal(reviewOfClosed)},
		{name: "base branch of a closed pull request changed", event: "pull_request", body: marshal(editOfClosed)},
		{name: "ping", event: "ping", body: []byte(`{"zen":"hi"}`)},
		{name: "no installation", event: "pull_request", body: marshal(noInstallation), wantErr: true},
		{name: "malformed", event: "pull_request", body: []byte(`{`), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok, err := p.PullRequest(tt.event, tt.body)
			if (err != nil) != tt.wantErr || ok != tt.reviewable || (c != nil) != tt.reviewable {
				t.Errorf("Change = %v, %v, %v; want reviewable %v, error %v", c, ok, err, tt.reviewable, tt.wantErr)
			}
		})
	}
}

// TestPRWebhookAuthenticatesAsTheInstallation: the change's API calls carry
// the installation's token, which the platform obtained with an App JWT
// signed by the app's key, once per installation.
func TestPRWebhookAuthenticatesAsTheInstallation(t *testing.T) {
	fake := &fakeGitHub{prPages: [][]*github.CommitFile{{{Filename: new("a.go"), Status: new("added"), Patch: new("+a\n")}}}}
	p, key := testPRWebhook(t, fake)
	body, err := json.Marshal(pullRequestEvent("base"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for range 2 {
		c, ok, err := p.PullRequest("pull_request", body)
		if err != nil || !ok {
			t.Fatalf("Change = %v, %v", ok, err)
		}
		if _, err := c.ChangedFiles(t.Context()); err != nil {
			t.Fatalf("ChangedFiles: %v", err)
		}
	}
	if got := fake.callCount("tokens"); got != 1 {
		t.Errorf("token exchanges = %d, want 1 (cached per installation)", got)
	}
	if fake.auth[0] == "" || !strings.HasPrefix(fake.auth[0], "Bearer ") {
		t.Fatalf("token exchange authorization = %q, want an App JWT", fake.auth[0])
	}
	verifyJWT(t, strings.TrimPrefix(fake.auth[0], "Bearer "), &key.PublicKey)
	for _, a := range fake.auth[1:] {
		if a != "Bearer inst-5" {
			t.Errorf("API call authorization = %q, want the installation token", a)
		}
	}
}

// verifyJWT checks jwt is an RS256 token signed by key, issued by app
// testAppID.
func verifyJWT(t *testing.T, jwt string, key *rsa.PublicKey) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d parts, want 3", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("JWT signature: %v", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims struct{ Iss, Iat, Exp int64 }
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("parse claims: %v", err)
	}
	if claims.Iss != testAppID || claims.Exp <= claims.Iat || claims.Exp-claims.Iat > 600 {
		t.Errorf("claims = %+v, want issuer %d and a lifetime of at most ten minutes", claims, testAppID)
	}
}
