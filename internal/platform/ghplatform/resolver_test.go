package ghplatform

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v93/github"

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
	return newTestClient(t, srv.URL), &calls
}

func TestGitHubApproverResolverExpandsTeams(t *testing.T) {
	client, calls := teamAPI(t)
	members, err := NewApproverResolver(client).Resolve(t.Context(), "acme/backend")
	if err != nil || !reflect.DeepEqual(members, []string{"alice", "bob", "carol"}) {
		t.Fatalf("acme/backend = %v, %v; want every page's members", members, err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("API calls = %d, want 2 (two pages)", got)
	}
}

func TestGitHubApproverResolverTargets(t *testing.T) {
	client, calls := teamAPI(t)
	r := NewApproverResolver(client)

	if members, err := r.Resolve(t.Context(), "dana"); err != nil || !reflect.DeepEqual(members, []string{"dana"}) {
		t.Errorf("a login must resolve to itself, got %v, %v", members, err)
	}
	if _, err := r.Resolve(t.Context(), "acme/ghosts"); !errors.Is(err, approval.ErrUnknownTarget) {
		t.Errorf("an unknown team err = %v, want approval.ErrUnknownTarget", err)
	}
	before := calls.Load()
	for _, bad := range []string{"acme/../../user", "acme/", "/team", "acme/a/b", "ac me/team", "da na"} {
		_, err := r.Resolve(t.Context(), bad)
		if !errors.Is(err, approval.ErrUnknownTarget) || !errors.Is(err, approval.ErrInvalidTarget) {
			t.Errorf("malformed target %q err = %v, want unknown and invalid", bad, err)
		}
		if err := r.ValidateTarget(bad); !errors.Is(err, approval.ErrInvalidTarget) {
			t.Errorf("ValidateTarget(%q) = %v, want approval.ErrInvalidTarget", bad, err)
		}
	}
	if calls.Load() != before {
		t.Error("a malformed target must never reach the API")
	}
}

// TestGitHubApproverResolverFailureIsNotUnknown: an API failure other than
// a missing team is returned as an operational error, so the review fails
// instead of pruning the team.
func TestGitHubApproverResolverFailureIsNotUnknown(t *testing.T) {
	fake := &fakeGitHub{fail: true}
	_, err := NewApproverResolver(fake.client(t)).Resolve(t.Context(), "acme/backend")
	if err == nil || errors.Is(err, approval.ErrUnknownTarget) {
		t.Errorf("err = %v, want an operational error, not approval.ErrUnknownTarget", err)
	}
}

func TestGitHubLoginsCompareCaseInsensitively(t *testing.T) {
	r := NewApproverResolver(nil)
	if r.Canonical("Alice") != r.Canonical("alice") {
		t.Error("logins differing only in case must be the same user")
	}
}
