package remote

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
)

// teamAPI serves GET /orgs/acme/teams/backend/members in two pages, and 404
// for any other team, counting the requests it answers.
func teamAPI(t *testing.T) (*github.Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/orgs/acme/teams/backend/members", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		body := `[{"login":"carol"}]`
		if r.URL.Query().Get("page") != "2" {
			w.Header().Set("Link", fmt.Sprintf(`<%s/orgs/acme/teams/backend/members?page=2>; rel="next"`, srv.URL))
			body = `[{"login":"alice"},{"login":"bob"}]`
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("write members: %v", err)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(srv.URL + "/")
	return client, &calls
}

func TestRemoteApproverResolverExpandsTeams(t *testing.T) {
	client, calls := teamAPI(t)
	r := NewApproverResolver(client, testLogger())

	for range 2 {
		members, ok := r.Resolve(t.Context(), "acme/backend", approval.PullRequest{})
		if !ok || !reflect.DeepEqual(members, []string{"alice", "bob", "carol"}) {
			t.Fatalf("acme/backend = %v, %v; want every page's members", members, ok)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("API calls = %d, want 2 (two pages, then cached)", got)
	}
}

func TestRemoteApproverResolverTargets(t *testing.T) {
	client, calls := teamAPI(t)
	r := NewApproverResolver(client, testLogger())

	if members, ok := r.Resolve(t.Context(), "dana", approval.PullRequest{}); !ok || !reflect.DeepEqual(members, []string{"dana"}) {
		t.Errorf("a login must resolve to itself, got %v, %v", members, ok)
	}
	if _, ok := r.Resolve(t.Context(), "acme/ghosts", approval.PullRequest{}); ok {
		t.Error("an unknown team must be unresolved")
	}
	before := calls.Load()
	for _, bad := range []string{"acme/../../user", "acme/", "/team", "acme/a/b", "ac me/team"} {
		if _, ok := r.Resolve(t.Context(), bad, approval.PullRequest{}); ok {
			t.Errorf("malformed team %q must be unresolved", bad)
		}
	}
	if calls.Load() != before {
		t.Error("a malformed team name must never reach the API")
	}
}
