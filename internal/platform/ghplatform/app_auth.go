package ghplatform

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"
)

// GitHub App token lifetimes. An App JWT may live at most ten minutes, and
// is backdated a minute against clock drift. An installation token lives an
// hour; it is renewed a few minutes early so a review never starts with a
// token about to expire.
const (
	jwtLifetime  = 9 * time.Minute
	jwtBackdate  = time.Minute
	tokenRenewal = 5 * time.Minute
)

// appAuth authenticates as a GitHub App: it signs App JWTs and exchanges
// them for installation tokens, caching each installation's token until
// shortly before it expires. It is safe for concurrent use.
type appAuth struct {
	id      int64
	key     *rsa.PrivateKey
	baseURL string
	// transport carries every request, the token exchange included.
	transport http.RoundTripper
	now       func() time.Time

	mu     sync.Mutex
	tokens map[int64]*github.InstallationToken
}

// jwt is a fresh App JWT (RS256), authenticating as the app itself.
func (a *appAuth) jwt() (string, error) {
	now := a.now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]int64{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": a.id,
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign app JWT: %w", err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// token returns installation's access token, exchanging an App JWT for a
// new one when there is none or it is about to expire. The lock is held
// across the exchange, so concurrent reviews share one.
func (a *appAuth) token(ctx context.Context, installation int64) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.tokens[installation]; ok && a.now().Add(tokenRenewal).Before(t.GetExpiresAt().Time) {
		return t.GetToken(), nil
	}
	client, err := a.client(&bearer{token: func(context.Context) (string, error) { return a.jwt() }, base: a.transport})
	if err != nil {
		return "", err
	}
	t, _, err := client.Apps.CreateInstallationToken(ctx, installation, nil)
	if err != nil {
		return "", fmt.Errorf("create installation %d token: %w", installation, err)
	}
	if t.GetToken() == "" {
		return "", fmt.Errorf("create installation %d token: empty token", installation)
	}
	if a.tokens == nil {
		a.tokens = map[int64]*github.InstallationToken{}
	}
	a.tokens[installation] = t
	return t.GetToken(), nil
}

// installationClient is a client acting as the app's installation: each
// request carries the installation's token.
func (a *appAuth) installationClient(installation int64) (*github.Client, error) {
	return a.client(&bearer{
		token: func(ctx context.Context) (string, error) { return a.token(ctx, installation) },
		base:  a.transport,
	})
}

// client is a GitHub client on the configured API URL over transport.
func (a *appAuth) client(transport http.RoundTripper) (*github.Client, error) {
	base := a.baseURL
	return github.NewClient(github.WithTransport(transport), github.WithURLs(&base, &base))
}

// bearer authenticates each request with the bearer token that token
// returns for the request's context.
type bearer struct {
	token func(context.Context) (string, error)
	base  http.RoundTripper
}

// RoundTrip sends a copy of req carrying the token.
func (b *bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	tok, err := b.token(req.Context())
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+tok)
	return b.base.RoundTrip(out)
}
