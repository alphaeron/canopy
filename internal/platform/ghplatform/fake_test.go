package ghplatform

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/platform/platformtest"
)

// fakeGitHub answers the REST endpoints the platform reads for repository
// o/r and pull request 7: the base tree listing, blob reads, and the
// paginated file listing. It counts requests per endpoint.
type fakeGitHub struct {
	base    map[string]string      // base tree blobs: path to contents
	dirs    []string               // non-blob entries in the tree listing
	prPages [][]*github.CommitFile // the pull request's files, page by page

	mu    sync.Mutex
	fail  bool           // answer every request with a server error
	calls map[string]int // requests per endpoint: "tree", "blob", "files"
}

// client starts the fake and returns a GitHub client pointed at it.
func (f *fakeGitHub) client(t *testing.T) *github.Client {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/git/trees/{sha}", func(w http.ResponseWriter, r *http.Request) {
		var entries []map[string]string
		for _, p := range slices.Sorted(maps.Keys(f.base)) {
			entries = append(entries, map[string]string{"path": p, "type": "blob", "sha": hex.EncodeToString([]byte(p))})
		}
		for _, d := range f.dirs {
			entries = append(entries, map[string]string{"path": d, "type": "tree", "sha": "dir"})
		}
		writeJSON(t, w, map[string]any{"sha": r.PathValue("sha"), "tree": entries})
	})
	mux.HandleFunc("GET /repos/o/r/git/blobs/{sha}", func(w http.ResponseWriter, r *http.Request) {
		path, err := hex.DecodeString(r.PathValue("sha"))
		content, ok := f.base[string(path)]
		if err != nil || !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(t, w, map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))})
	})
	mux.HandleFunc("GET /repos/o/r/pulls/7/files", func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			var err error
			if page, err = strconv.Atoi(p); err != nil || page < 1 || page > len(f.prPages) {
				http.Error(w, `{"message":"bad page"}`, http.StatusBadRequest)
				return
			}
		}
		if page < len(f.prPages) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next"`, srv.URL, r.URL.Path, page+1))
		}
		var files []*github.CommitFile
		if len(f.prPages) > 0 {
			files = f.prPages[page-1]
		}
		writeJSON(t, w, files)
	})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		fail := f.fail
		if f.calls == nil {
			f.calls = map[string]int{}
		}
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/"):
			f.calls["tree"]++
		case strings.Contains(r.URL.Path, "/git/blobs/"):
			f.calls["blob"]++
		case strings.HasSuffix(r.URL.Path, "/files"):
			f.calls["files"]++
		}
		f.mu.Unlock()
		if fail {
			http.Error(w, `{"message":"Server Error"}`, http.StatusInternalServerError)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	client := github.NewClient(nil)
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse fake URL: %v", err)
	}
	client.BaseURL = u
	return client
}

// setFail makes every later request fail, or succeed again.
func (f *fakeGitHub) setFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

// callCount is the number of requests endpoint has answered.
func (f *fakeGitHub) callCount(endpoint string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[endpoint]
}

// fakeFromFixture is a fake whose base tree is fx.Base and whose pull
// request makes fx.Change, with patches in GitHub's hunk-only form.
func fakeFromFixture(fx platformtest.Fixture) *fakeGitHub {
	var files []*github.CommitFile
	for _, p := range slices.Sorted(maps.Keys(fx.Change.Write)) {
		old, modified := fx.Base[p]
		status := "added"
		if modified {
			status = "modified"
		}
		files = append(files, &github.CommitFile{
			Filename: github.Ptr(p),
			Status:   github.Ptr(status),
			Patch:    github.Ptr("@@ @@\n" + prefixLines("-", old) + prefixLines("+", fx.Change.Write[p])),
		})
	}
	for _, p := range fx.Change.Delete {
		files = append(files, &github.CommitFile{
			Filename: github.Ptr(p),
			Status:   github.Ptr("removed"),
			Patch:    github.Ptr("@@ @@\n" + prefixLines("-", fx.Base[p])),
		})
	}
	for _, from := range slices.Sorted(maps.Keys(fx.Change.Rename)) {
		// An unchanged rename carries no patch.
		files = append(files, &github.CommitFile{
			Filename:         github.Ptr(fx.Change.Rename[from]),
			PreviousFilename: github.Ptr(from),
			Status:           github.Ptr("renamed"),
		})
	}
	return &fakeGitHub{base: fx.Base, prPages: [][]*github.CommitFile{files}}
}

// prefixLines prefixes every line of s with prefix.
func prefixLines(prefix, s string) string {
	var b strings.Builder
	for line := range strings.Lines(s) {
		b.WriteString(prefix + line)
	}
	return b.String()
}

// pullRequestEvent builds a pull_request event for o/r#7 at base revision base.
func pullRequestEvent(base string) *github.PullRequestEvent {
	return &github.PullRequestEvent{
		Repo: &github.Repository{Name: github.Ptr("r"), Owner: &github.User{Login: github.Ptr("o")}},
		PullRequest: &github.PullRequest{
			Number: github.Ptr(7),
			Base:   &github.PullRequestBranch{SHA: github.Ptr(base)},
		},
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// testLogger returns a logger that discards all output so tests stay quiet.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
