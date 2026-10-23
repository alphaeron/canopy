package ghplatform

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/alphaeron/canopy/internal/server"
)

// conditionalPaths are the API paths whose answers can change between
// reviews and that the platform reads on every review: a team's members, a
// user, and a pull request's reviews. Only GETs of these are made
// conditional. Everything else the platform reads is either addressed by
// SHA, and so cached as is (trees, blobs), or read once.
var conditionalPaths = regexp.MustCompile(`/(orgs/[^/]+/teams/[^/]+/members|users/[^/]+|repos/[^/]+/[^/]+/pulls/[0-9]+/reviews)$`)

// keptHeaders are the response headers kept with a body: what go-github
// reads besides it (the next page of a listing, and how to decode it).
var keptHeaders = []string{"Link", "Content-Type"}

// conditionalTransport makes the GETs of conditionalPaths conditional on
// the response it kept last time, per installation (its cache scope): it
// sends that response's ETag as If-None-Match, and when GitHub answers
// 304 Not Modified, serves the kept body as a 200. GitHub confirms the
// kept body is current before every use, so it is never stale, and a 304
// does not count against the installation's rate limit. A response that
// is not kept, a cache miss, or any other answer passes through untouched.
type conditionalTransport struct {
	base  http.RoundTripper
	cache server.CacheScope
}

// RoundTrip sends req, conditionally when it is a GET of a conditional
// path with a kept response.
func (t *conditionalTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || !conditionalPaths.MatchString(req.URL.Path) {
		return t.base.RoundTrip(req)
	}
	url := req.URL.String()
	kept, ok := t.cache.Response(req.Context(), url)
	if ok {
		req = req.Clone(req.Context())
		req.Header.Set("If-None-Match", kept.ETag)
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	switch {
	case ok && resp.StatusCode == http.StatusNotModified:
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return notModified(resp, kept), nil
	case resp.StatusCode == http.StatusOK && resp.Header.Get("ETag") != "":
		return t.keep(url, resp)
	}
	return resp, nil
}

// keep keeps resp's body, ETag, and keptHeaders for url, and returns resp
// with its body intact for the caller.
func (t *conditionalTransport) keep(url string, resp *http.Response) (*http.Response, error) {
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	header := http.Header{}
	for _, name := range keptHeaders {
		if v := resp.Header.Values(name); len(v) > 0 {
			header[name] = v
		}
	}
	t.cache.PutResponse(url, server.CachedResponse{ETag: resp.Header.Get("ETag"), Header: header, Body: body})
	return resp, nil
}

// notModified is the 200 a 304 confirms: the kept body and headers, with
// the 304's own headers (rate limits, request ID) otherwise.
func notModified(resp *http.Response, kept server.CachedResponse) *http.Response {
	header := resp.Header.Clone()
	for _, name := range keptHeaders {
		header.Del(name)
		for _, v := range kept.Header.Values(name) {
			header.Add(name, v)
		}
	}
	header.Set("Content-Length", strconv.Itoa(len(kept.Body)))
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         resp.Proto,
		ProtoMajor:    resp.ProtoMajor,
		ProtoMinor:    resp.ProtoMinor,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(kept.Body)),
		ContentLength: int64(len(kept.Body)),
		Request:       resp.Request,
	}
}
