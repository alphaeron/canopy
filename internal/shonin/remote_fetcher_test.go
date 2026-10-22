package shonin

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/kitfile"
)

// fakeGitHubAPI stands up an httptest server that answers the two endpoints
// the RemoteFetcher depends on: GET /repos/{owner}/{repo}/git/trees/{sha} and
// GET /repos/{owner}/{repo}/git/blobs/{sha}. Blobs are addressed by an opaque
// sha string the test author controls.
type fakeGitHubAPI struct {
	blobs map[string][]byte // sha -> raw contents
	tree  []struct {
		Path string `json:"path"`
		Sha  string `json:"sha"`
		Type string `json:"type"`
	}
}

func (f *fakeGitHubAPI) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/git/trees/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := struct {
			Tree []struct {
				Path string `json:"path"`
				Sha  string `json:"sha"`
				Type string `json:"type"`
			} `json:"tree"`
		}{}
		for _, e := range f.tree {
			resp.Tree = append(resp.Tree, struct {
				Path string `json:"path"`
				Sha  string `json:"sha"`
				Type string `json:"type"`
			}{e.Path, e.Sha, e.Type})
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode tree: %v", err)
		}
	})
	mux.HandleFunc("/repos/o/r/git/blobs/", func(w http.ResponseWriter, r *http.Request) {
		sha := r.URL.Path[len("/repos/o/r/git/blobs/"):]
		data, ok := f.blobs[sha]
		if !ok {
			http.Error(w, `{"message":"Blob not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"content":  base64.StdEncoding.EncodeToString(data),
			"encoding": "base64",
			"sha":      sha,
		}); err != nil {
			t.Errorf("encode blob: %v", err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRemoteFetcherHappyPath(t *testing.T) {
	fake := &fakeGitHubAPI{
		blobs: map[string][]byte{
			"deadbeef": []byte(`- ".*\\.go$":
  name: "Backend"
  combine_strategy: union
  approvers:
    - target: "org/backend-team"
      required_count: 1
`),
		},
		tree: []struct {
			Path string `json:"path"`
			Sha  string `json:"sha"`
			Type string `json:"type"`
		}{
			{Path: "KIT", Sha: "deadbeef", Type: "blob"},
			{Path: "readme.md", Sha: "cafebabe", Type: "blob"},
		},
	}
	srv := fake.serve(t)
	client, u := github.NewClient(nil), srv.URL+"/"
	client.BaseURL, _ = url.Parse(u)

	fetcher := &RemoteFetcher{
		Client:  client,
		Owner:   "o",
		Repo:    "r",
		BaseSHA: "abc123",
		Log:     testLogger(),
	}
	root, rules, err := fetcher.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if root != "" {
		t.Errorf("root = %q, want empty for remote", root)
	}
	if len(rules) != 1 {
		t.Fatalf("rule count = %d, want 1 (only the KIT file)", len(rules))
	}
	ruleList, ok := rules["KIT"]
	if !ok {
		t.Fatalf("missing rule for the root KIT file, got keys %v", keysOf(rules))
	}
	if len(ruleList) != 1 || ruleList[0].Name != "Backend" {
		t.Errorf("unexpected rule contents: %+v", ruleList)
	}
}

// TestRemoteFetcherSkipsLookalikeNames pins whole-name KIT file matching:
// the fetcher used to match any path containing "KIT", so it fetched and
// parsed files like docs/KITCHEN.md as rules and failed the fetch.
func TestRemoteFetcherSkipsLookalikeNames(t *testing.T) {
	fake := &fakeGitHubAPI{
		blobs: map[string][]byte{"deadbeef": []byte("- \".*\":\n  name: \"Root\"\n")},
		tree: []struct {
			Path string `json:"path"`
			Sha  string `json:"sha"`
			Type string `json:"type"`
		}{
			{Path: "KIT", Sha: "deadbeef", Type: "blob"},
			// No blobs: fetching either would fail the test with a 404.
			{Path: "docs/KITCHEN.md", Sha: "kitchen", Type: "blob"},
			{Path: "svc/KIT.bak", Sha: "backup", Type: "blob"},
		},
	}
	srv := fake.serve(t)
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse(srv.URL + "/")

	fetcher := &RemoteFetcher{Client: client, Owner: "o", Repo: "r", BaseSHA: "abc123", Log: testLogger()}
	_, rules, err := fetcher.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rules) != 1 || len(rules["KIT"]) != 1 {
		t.Errorf("rules = %v, want only the root KIT file", keysOf(rules))
	}
}

func TestRemoteFetcherTreeErrorSurfaces(t *testing.T) {
	client := github.NewClient(nil)
	client.BaseURL, _ = url.Parse("http://localhost:1/")
	// A port guaranteed (in practice) to refuse connections, which forces a
	// clean transport error through Execute.
	fetcher := &RemoteFetcher{Client: client, Owner: "o", Repo: "r", BaseSHA: "abc"}
	_, _, err := fetcher.Execute()
	if err == nil {
		t.Error("Execute must report a transport error when the GitHub API is unreachable")
	}
}

// keysOf lists the map's keys for an error message; helper only.
func keysOf[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

var _ = kitfile.Rule{} // keep the kitfile import explicit in the fixture blob
