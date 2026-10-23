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
)

// MinLinkKeyBytes is the shortest Options.LinkKey the server accepts.
const MinLinkKeyBytes = 32

// ReviewPagePath is the review page's route: a pull request's head
// commit, by repository owner and name, number, and SHA. Every request to it
// carries the link's signature as the query parameter "sig".
const ReviewPagePath = "/reviews/{owner}/{name}/{number}/{sha}"

// links builds and checks the signed links to review pages. A review page
// shows a private repository's paths, approvers, and errors, and the server
// serves it to anyone who can reach it, so a page is served only for a
// link the server signed: whoever can see the pull request's status, which
// carries the link, can open it, and nobody can guess another's.
type links struct {
	base *url.URL // the server's public URL, without a trailing slash
	key  []byte
}

// newLinks checks publicURL and key; both empty disables links.
func newLinks(publicURL string, key []byte) (*links, error) {
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
	u.Path = strings.TrimSuffix(u.Path, "/")
	return &links{base: u, key: append([]byte(nil), key...)}, nil
}

// reviewURL is the signed link to the review page of pr's head commit, or
// "" when links are disabled (l is nil) or pr has no repository.
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
	u.RawQuery = url.Values{"sig": {l.sign(pr.Repo, pr.Number, pr.HeadSHA)}}.Encode()
	return u.String()
}

// sign is the signature of the review page of repo's pull request number
// at headSHA, as the link carries it.
func (l *links) sign(repo string, number int, headSHA string) string {
	return base64.RawURLEncoding.EncodeToString(l.mac(repo, number, headSHA))
}

// verify reports whether sig is the signature of the review page.
func (l *links) verify(repo string, number int, headSHA, sig string) bool {
	got, err := base64.RawURLEncoding.DecodeString(sig)
	return err == nil && hmac.Equal(got, l.mac(repo, number, headSHA))
}

// mac is the HMAC-SHA256, under the link key, of the review page's fields,
// each length-prefixed so no two pages' fields run together into the same
// input.
func (l *links) mac(repo string, number int, headSHA string) []byte {
	m := hmac.New(sha256.New, l.key)
	for _, f := range []string{"review-page", repo, strconv.Itoa(number), headSHA} {
		_, _ = m.Write(binary.AppendUvarint(nil, uint64(len(f))))
		_, _ = m.Write([]byte(f))
	}
	return m.Sum(nil)
}
