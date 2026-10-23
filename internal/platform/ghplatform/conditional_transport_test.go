package ghplatform

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/server"
)

// etagGitHub serves a team's members over two pages and users, each with an
// ETag of its current version, answering 304 Not Modified (without a body
// or Link) when If-None-Match names it, as GitHub does.
type etagGitHub struct {
	mu          sync.Mutex
	members     [][]string // the team's members, page by page
	version     int        // bumped to change every answer
	conditional int        // requests that carried If-None-Match
	notModified int        // requests answered 304
	trees       int        // tree requests that carried If-None-Match
}

func (g *etagGitHub) serve(t *testing.T) string {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	answer := func(w http.ResponseWriter, r *http.Request, body any, link string) {
		g.mu.Lock()
		etag := fmt.Sprintf(`W/"%s-%d"`, r.URL.RequestURI(), g.version)
		inm := r.Header.Get("If-None-Match")
		if inm != "" {
			g.conditional++
		}
		match := inm == etag
		if match {
			g.notModified++
		}
		g.mu.Unlock()
		w.Header().Set("ETag", etag)
		if match {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if link != "" {
			w.Header().Set("Link", link)
		}
		writeJSON(t, w, body)
	}
	mux.HandleFunc("GET /orgs/acme/teams/core/members", func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
		}
		g.mu.Lock()
		logins := g.members[page-1]
		last := page == len(g.members)
		g.mu.Unlock()
		users := []*github.User{}
		for _, l := range logins {
			users = append(users, &github.User{Login: new(l)})
		}
		link := ""
		if !last {
			link = fmt.Sprintf(`<%s%s?page=%d&per_page=100>; rel="next"`, srv.URL, r.URL.Path, page+1)
		}
		answer(w, r, users, link)
	})
	mux.HandleFunc("GET /users/{login}", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		v := g.version
		g.mu.Unlock()
		answer(w, r, &github.User{Login: new(r.PathValue("login")), Name: new(fmt.Sprintf("v%d", v))}, "")
	})
	mux.HandleFunc("GET /repos/o/r/git/trees/{sha}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			g.mu.Lock()
			g.trees++
			g.mu.Unlock()
		}
		w.Header().Set("ETag", `"tree"`)
		writeJSON(t, w, map[string]any{"sha": r.PathValue("sha"), "tree": []any{}})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// conditionalClient is a client for baseURL whose conditional reads keep
// their responses in scope.
func conditionalClient(t *testing.T, baseURL string, scope server.CacheScope) *github.Client {
	t.Helper()
	u := baseURL + "/"
	client, err := github.NewClient(github.WithTransport(&conditionalTransport{base: http.DefaultTransport, cache: scope}), github.WithURLs(&u, nil))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

// TestConditionalTransportRevalidatesEveryRead: a second read of a team's
// members, across pages, is answered with 304s and served from the kept
// pages, Link headers included; once the team changes, the change is read,
// never the kept answer.
func TestConditionalTransportRevalidatesEveryRead(t *testing.T) {
	g := &etagGitHub{members: [][]string{{"ann", "bob"}, {"cy"}}}
	resolver := NewApproverResolver(conditionalClient(t, g.serve(t), server.NewRepoCache(nil, nil).Scope("i1")))

	for range 2 {
		got, err := resolver.Resolve(t.Context(), "acme/core")
		if err != nil || !reflect.DeepEqual(got, []string{"ann", "bob", "cy"}) {
			t.Fatalf("Resolve = %v, %v; want every page's members", got, err)
		}
	}
	if g.conditional != 2 || g.notModified != 2 {
		t.Errorf("second read: %d conditional requests, %d not modified; want both pages revalidated", g.conditional, g.notModified)
	}

	g.mu.Lock()
	g.members = [][]string{{"ann"}, {"cy"}}
	g.version++
	g.mu.Unlock()
	got, err := resolver.Resolve(t.Context(), "acme/core")
	if err != nil || !reflect.DeepEqual(got, []string{"ann", "cy"}) {
		t.Errorf("Resolve after a member left = %v, %v; want the team as it is now", got, err)
	}
}

// TestConditionalTransportScopesAndPaths: a response kept for one
// installation is never offered for another's request, and only the paths
// whose answers can change are made conditional.
func TestConditionalTransportScopesAndPaths(t *testing.T) {
	g := &etagGitHub{}
	base := g.serve(t)
	cache := server.NewRepoCache(nil, nil)
	one := conditionalClient(t, base, cache.Scope("i1"))
	two := conditionalClient(t, base, cache.Scope("i2"))

	for _, c := range []*github.Client{one, two} {
		if _, _, err := c.Users.Get(t.Context(), "ann"); err != nil {
			t.Fatalf("Users.Get: %v", err)
		}
	}
	if g.conditional != 0 {
		t.Errorf("conditional requests = %d, want none: installation i2 must not see i1's response", g.conditional)
	}
	user, _, err := one.Users.Get(t.Context(), "ann")
	if err != nil || user.GetName() != "v0" || g.notModified != 1 {
		t.Errorf("revalidated read = %v, %v (%d not modified); want the kept user, confirmed", user, err, g.notModified)
	}

	for range 2 {
		if _, _, err := one.Git.GetTree(t.Context(), "o", "r", "abc", false); err != nil {
			t.Fatalf("GetTree: %v", err)
		}
	}
	if g.trees != 0 {
		t.Errorf("conditional tree requests = %d, want none", g.trees)
	}
}
