package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MinLinkKeyBytes is the shortest Options.LinkKey the server accepts.
const MinLinkKeyBytes = 32

// MinLinkLifetime is the shortest Options.LinkLifetime the server accepts:
// a link must outlast the review it is published for.
const MinLinkLifetime = time.Hour

// ReviewPagePath is the review page's route: a pull request's head
// commit, by repository owner and name, number, and SHA. Every request to it
// carries the link's expiry, in Unix seconds, as the query parameter "exp"
// and its signature as "sig".
const ReviewPagePath = "/reviews/{owner}/{name}/{number}/{sha}"

// links builds and checks the signed links to review pages. A review page
// shows a private repository's paths, approvers, and errors, and the server
// serves it to anyone who can reach it, so a page is served only for a
// link the server signed: whoever can see the pull request's status, which
// carries the link, can open it until it expires, and nobody can guess
// another's.
type links struct {
	base     *url.URL // the server's public URL, without a trailing slash
	key      []byte
	lifetime time.Duration // how long a link opens its page once signed
	now      func() time.Time
}

// newLinks checks publicURL, key, and lifetime; publicURL and key both
// empty disables links, and lifetime is then not checked.
func newLinks(publicURL string, key []byte, lifetime time.Duration) (*links, error) {
	if publicURL == "" && len(key) == 0 {
		return nil, nil
	}
	u, err := url.Parse(publicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("review page links: the public URL must be an absolute http(s) URL without a query or fragment")
	}
	if len(key) < MinLinkKeyBytes {
		return nil, fmt.Errorf("review page links: the link key must be at least %d bytes, got %d", MinLinkKeyBytes, len(key))
	}
	if lifetime < MinLinkLifetime {
		return nil, fmt.Errorf("review page links: the link lifetime must be at least %s, got %s", MinLinkLifetime, lifetime)
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return &links{base: u, key: append([]byte(nil), key...), lifetime: lifetime, now: time.Now}, nil
}

// reviewURL is the signed link to the review page of pr's head commit,
// expiring the link lifetime from now, or "" when links are disabled (l is nil)
// or pr has no repository.
func (l *links) reviewURL(pr PullRequestInfo) string {
	if l == nil {
		return ""
	}
	owner, name, ok := strings.Cut(pr.Repo, "/")
	if !ok {
		return ""
	}
	u := *l.base
	u.Path += "/reviews/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/" + strconv.Itoa(pr.Number) + "/" + url.PathEscape(pr.HeadSHA)
	exp := l.now().Add(l.lifetime).Unix()
	u.RawQuery = linkQuery(exp, l.sign(pr.Repo, pr.Number, pr.HeadSHA, exp))
	return u.String()
}

// linkQuery is the query a review page's link carries: its expiry and its
// signature.
func linkQuery(exp int64, sig string) string {
	return url.Values{"exp": {strconv.FormatInt(exp, 10)}, "sig": {sig}}.Encode()
}

// sign is the signature of the review page of repo's pull request number
// at headSHA, for a link expiring at exp (Unix seconds), as the link
// carries it.
func (l *links) sign(repo string, number int, headSHA string, exp int64) string {
	return base64.RawURLEncoding.EncodeToString(l.mac(repo, number, headSHA, exp))
}

// verify reports whether sig is the signature of the review page for a
// link expiring at exp, and the link has not expired.
func (l *links) verify(repo string, number int, headSHA string, exp int64, sig string) bool {
	if !l.now().Before(time.Unix(exp, 0)) {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	return err == nil && hmac.Equal(got, l.mac(repo, number, headSHA, exp))
}

// mac is the HMAC-SHA256, under the link key, of the review page's fields
// and the link's expiry, each length-prefixed so no two links' fields run
// together into the same input.
func (l *links) mac(repo string, number int, headSHA string, exp int64) []byte {
	m := hmac.New(sha256.New, l.key)
	for _, f := range []string{"review-page", repo, strconv.Itoa(number), headSHA, strconv.FormatInt(exp, 10)} {
		_, _ = m.Write(binary.AppendUvarint(nil, uint64(len(f))))
		_, _ = m.Write([]byte(f))
	}
	return m.Sum(nil)
}
