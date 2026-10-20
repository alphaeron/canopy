package kenin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
)

// stubFetch lets the tests provide the KIT rule map for a remote source
// without a GitHub client. It mirrors shonin.LocalFetcher's contract.
type stubFetch struct {
	root  string
	rules map[string]kitfile.Rules
	err   error
}

func (s *stubFetch) Execute() (string, map[string]kitfile.Rules, error) {
	return s.root, s.rules, s.err
}

var _ shonin.Fetcher = (*stubFetch)(nil)

type fakeTreeEntry struct {
	Path string `json:"path"`
	Sha  string `json:"sha"`
	Type string `json:"type"`
}

// fakeTreeServer answers the base-tree listing query that
// RemoteSource.AllFiles makes (GET /repos/{owner}/{repo}/git/trees/{sha}).
type fakeTreeServer struct {
	entries []fakeTreeEntry
}

func (f *fakeTreeServer) serve(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sha":  "deadbeef",
			"tree": f.entries,
		})
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

	fetch := &stubFetch{rules: map[string]kitfile.Rules{"KIT": {}}}
	pr := &PullRequest{Owner: "o", Repo: "r", BaseSHA: "deadbeef"}
	src := NewRemoteSource(pr, fetch, client, nil)

	files, err := src.AllFiles()
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
	fetch := &stubFetch{rules: map[string]kitfile.Rules{}}
	pr := &PullRequest{
		Owner: "o", Repo: "r", BaseSHA: "deadbeef",
		Files: []PRFile{
			{Path: "main.go", Patch: "-old\n+new\n"},
			{Path: "src/x.go", Patch: "+added\n"},
		},
	}
	// nil client: the changed-files and diff paths read the payload, not the network.
	src := NewRemoteSource(pr, fetch, nil, nil)

	changed, err := src.ChangedFiles()
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	sort.Strings(changed)
	if len(changed) != 2 || changed[0] != "main.go" || changed[1] != "src/x.go" {
		t.Errorf("ChangedFiles = %v, want [main.go src/x.go]", changed)
	}
	got, err := src.DiffContent("main.go")
	if err != nil {
		t.Fatalf("DiffContent: %v", err)
	}
	if got != "-old\n+new\n" {
		t.Errorf("DiffContent = %q, want the patch for that file from the payload", got)
	}
	empty, err := src.DiffContent("absent.go")
	if err != nil {
		t.Fatalf("DiffContent absent: %v", err)
	}
	if empty != "" {
		t.Errorf("DiffContent for a file not in the payload should be empty, got %q", empty)
	}
}

func TestRemoteKitFilesDelegatesToFetcher(t *testing.T) {
	rules := map[string]kitfile.Rules{"KIT": {{Pattern: ".go"}}}
	fetch := &stubFetch{rules: rules}
	pr := &PullRequest{Owner: "o", Repo: "r", BaseSHA: "x"}
	src := NewRemoteSource(pr, fetch, nil, nil)

	got, err := src.KitFiles()
	if err != nil {
		t.Fatalf("KitFiles: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("KitFiles = %v, want the stub's rule map", got)
	}
}

func TestRemoteRepoRootIsEmpty(t *testing.T) {
	src := NewRemoteSource(&PullRequest{}, &stubFetch{}, nil, nil)
	if src.RepoRoot() != "" {
		t.Errorf("remote RepoRoot must be empty, got %q", src.RepoRoot())
	}
}

var _ = sort.Strings // imported for tests that sort before asserting
