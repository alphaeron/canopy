package remote

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"

	"github.com/google/go-github/v71/github"
)

type fakeTreeEntry struct {
	Path string `json:"path"`
	Sha  string `json:"sha"`
	Type string `json:"type"`
}

// fakeTreeServer answers the base-tree listing query that
// Source.AllFiles makes (GET /repos/{owner}/{repo}/git/trees/{sha}).
type fakeTreeServer struct {
	entries []fakeTreeEntry
}

func (f *fakeTreeServer) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"sha":  "deadbeef",
			"tree": f.entries,
		}); err != nil {
			t.Errorf("encode tree: %v", err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestClient builds a bare GitHub client whose BaseURL points at the mock
// tree server so no real network traffic occurs.
func newTestClient(t *testing.T, tree *fakeTreeServer) *github.Client {
	t.Helper()
	srv := tree.serve(t)
	client := github.NewClient(nil)
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}
	client.BaseURL = u
	return client
}

func TestRemoteAllFilesListingBlobsAtBaseTree(t *testing.T) {
	tree := &fakeTreeServer{entries: []fakeTreeEntry{
		{Path: "KIT", Sha: "a", Type: "blob"},
		{Path: "src/api.go", Sha: "b", Type: "blob"},
		{Path: "nested", Sha: "c", Type: "tree"},
	}}
	client := newTestClient(t, tree)

	pr := &PullRequest{Owner: "o", Repo: "r", BaseSHA: "deadbeef"}
	src := NewSource(pr, client, nil)

	files, err := src.AllFiles(t.Context())
	if err != nil {
		t.Fatalf("AllFiles: %v", err)
	}
	// The one non-blob entry ("nested", a tree) must be excluded.
	sort.Strings(files)
	want := []string{"KIT", "src/api.go"}
	if len(files) != len(want) {
		t.Fatalf("AllFiles length = %d, want %d: %v", len(files), len(want), files)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Errorf("AllFiles[%d] = %q, want %q", i, files[i], want[i])
		}
	}
}

func TestRemoteChangedFilesAndDiffFromPayload(t *testing.T) {
	pr := &PullRequest{
		Owner: "o", Repo: "r", BaseSHA: "deadbeef",
		Files: []PRFile{
			{Path: "main.go", Patch: "-old\n+new\n"},
			{Path: "src/x.go", Patch: "+added\n"},
		},
	}
	// nil client: the changed-files and diff paths read the payload, not the network.
	src := NewSource(pr, nil, nil)

	changed, err := src.ChangedFiles(t.Context())
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	sort.Strings(changed)
	if len(changed) != 2 || changed[0] != "main.go" || changed[1] != "src/x.go" {
		t.Errorf("ChangedFiles = %v, want [main.go src/x.go]", changed)
	}
	got, err := src.DiffContent(t.Context(), "main.go")
	if err != nil {
		t.Fatalf("DiffContent: %v", err)
	}
	if got != "-old\n+new\n" {
		t.Errorf("DiffContent = %q, want the patch for that file from the payload", got)
	}
	empty, err := src.DiffContent(t.Context(), "absent.go")
	if err != nil {
		t.Fatalf("DiffContent absent: %v", err)
	}
	if empty != "" {
		t.Errorf("DiffContent for a file not in the payload should be empty, got %q", empty)
	}
}

// TestRemoteSourceIsOnTheRemotePlatform: a remote source's rules and
// approver resolution both come from GitHub, bound to the pull request.
func TestRemoteSourceIsOnTheRemotePlatform(t *testing.T) {
	pr := &PullRequest{Owner: "o", Repo: "r", BaseSHA: "x"}
	rev := NewSource(pr, github.NewClient(nil), nil).Reviewer()
	fetch, ok := rev.Fetch.(*Fetcher)
	if !ok || fetch.Owner != "o" || fetch.Repo != "r" || fetch.BaseSHA != "x" {
		t.Errorf("fetcher = %#v, want a Fetcher for o/r@x", rev.Fetch)
	}
	if _, ok := rev.Resolver.(*ApproverResolver); !ok {
		t.Errorf("resolver = %T, want *ApproverResolver", rev.Resolver)
	}
}

func TestRemoteRepoRootIsEmpty(t *testing.T) {
	src := NewSource(&PullRequest{}, nil, nil)
	if src.RepoRoot() != "" {
		t.Errorf("remote RepoRoot must be empty, got %q", src.RepoRoot())
	}
}

var _ = sort.Strings // imported for tests that sort before asserting
