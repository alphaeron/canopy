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
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/platform/platformtest"
	"github.com/alphaeron/canopy/internal/server"
)

// fakeGitHub answers the REST endpoints the platform uses for repository
// o/r and pull request 7: tree listings (nested, by subtree), blob reads,
// the merge base of a comparison, the paginated
// file and review listings, contents at the head revision, team members,
// users, check runs, and installation tokens. It
// counts requests per endpoint and records what the platform publishes.
type fakeGitHub struct {
	base        map[string]string             // base tree blobs: path to contents
	trees       map[string]map[string]string  // other commits' trees: commit to path to contents
	truncated   bool                          // every tree listing is truncated
	maxListed   int                           // when positive, a recursive listing of more entries is truncated
	mergeBase   string                        // the merge base the compare endpoint reports
	head        map[string]string             // files at the head revision: path to contents
	prPages     [][]*github.CommitFile        // the pull request's files, page by page
	reviewPages [][]*github.PullRequestReview // the pull request's reviews, page by page
	teams       map[string][]string           // "org/slug" to member logins
	users       map[string]bool               // known logins to whether they are suspended

	mu        sync.Mutex
	fail      bool                           // answer every request with a server error
	calls     map[string]int                 // requests per endpoint, e.g. "tree", "files", "check-runs"
	checkRuns []github.CreateCheckRunOptions // check runs created, in order
	auth      []string                       // the Authorization header of every request
}

// blobSHA is the fake's SHA of a blob: its contents, so equal contents
// share a SHA at every commit, as in git, and the blob endpoint needs no
// lookup.
func blobSHA(content string) string { return "b" + hex.EncodeToString([]byte(content)) }

// subtreeSHA is the fake's SHA of the tree at dir (ending in "/") in
// commit's tree; treeOf reverses it.
func subtreeSHA(commit, dir string) string {
	return "t" + hex.EncodeToString([]byte(commit+"\x00"+dir))
}

// treeOf is the commit and directory prefix a tree SHA names: a subtree's,
// or a commit's root.
func treeOf(sha string) (commit, prefix string) {
	if raw, err := hex.DecodeString(strings.TrimPrefix(sha, "t")); err == nil && strings.HasPrefix(sha, "t") {
		if c, dir, ok := strings.Cut(string(raw), "\x00"); ok {
			return c, dir
		}
	}
	return sha, ""
}

// paged serves page of pages (1-based, from the request's page parameter),
// linking to the next page while there is one.
func paged[T any](t *testing.T, w http.ResponseWriter, r *http.Request, base string, pages [][]T) {
	t.Helper()
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		var err error
		if page, err = strconv.Atoi(p); err != nil || page < 1 || page > len(pages) {
			http.Error(w, `{"message":"bad page"}`, http.StatusBadRequest)
			return
		}
	}
	if page < len(pages) {
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next"`, base, r.URL.Path, page+1))
	}
	items := []T{}
	if len(pages) > 0 {
		items = pages[page-1]
	}
	writeJSON(t, w, items)
}

// client starts the fake and returns a GitHub client pointed at it.
func (f *fakeGitHub) client(t *testing.T) *github.Client {
	t.Helper()
	return newTestClient(t, f.serve(t))
}

// serve starts the fake and returns its base URL.
func (f *fakeGitHub) serve(t *testing.T) string {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/git/trees/{sha}", func(w http.ResponseWriter, r *http.Request) {
		commit, prefix := treeOf(r.PathValue("sha"))
		files := f.base
		if tree, ok := f.trees[commit]; ok {
			files = tree
		}
		recursive := r.URL.Query().Get("recursive") != ""
		entries, seen := []map[string]any{}, map[string]bool{}
		for _, p := range slices.Sorted(maps.Keys(files)) {
			rel, ok := strings.CutPrefix(p, prefix)
			if !ok {
				continue
			}
			dir, _, nested := strings.Cut(rel, "/")
			if nested && !recursive {
				if !seen[dir] {
					seen[dir] = true
					entries = append(entries, map[string]any{"path": dir, "type": "tree", "sha": subtreeSHA(commit, prefix+dir+"/")})
				}
				continue
			}
			if recursive {
				for i := range len(rel) {
					if d := rel[:i]; rel[i] == '/' && !seen[d] {
						seen[d] = true
						entries = append(entries, map[string]any{"path": d, "type": "tree", "sha": subtreeSHA(commit, prefix+d+"/")})
					}
				}
			}
			entries = append(entries, map[string]any{"path": rel, "type": "blob", "sha": blobSHA(files[p]), "size": len(files[p])})
		}
		truncated := f.truncated || recursive && f.maxListed > 0 && len(entries) > f.maxListed
		writeJSON(t, w, map[string]any{"sha": r.PathValue("sha"), "tree": entries, "truncated": truncated})
	})
	mux.HandleFunc("GET /repos/o/r/git/blobs/{sha}", func(w http.ResponseWriter, r *http.Request) {
		content, err := hex.DecodeString(strings.TrimPrefix(r.PathValue("sha"), "b"))
		if err != nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		writeJSON(t, w, map[string]string{"encoding": "base64", "content": base64.StdEncoding.EncodeToString(content)})
	})
	mux.HandleFunc("GET /repos/o/r/compare/{basehead}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"merge_base_commit": map[string]string{"sha": f.mergeBase}})
	})
	mux.HandleFunc("GET /repos/o/r/pulls/7/files", func(w http.ResponseWriter, r *http.Request) {
		paged(t, w, r, srv.URL, f.prPages)
	})
	mux.HandleFunc("GET /repos/o/r/pulls/7/reviews", func(w http.ResponseWriter, r *http.Request) {
		paged(t, w, r, srv.URL, f.reviewPages)
	})
	mux.HandleFunc("GET /repos/o/r/contents/{path...}", func(w http.ResponseWriter, r *http.Request) {
		content, ok := f.head[r.PathValue("path")]
		if !ok || r.URL.Query().Get("ref") != "head" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		// As GitHub does, a file over 1 MB is sized but has no content.
		encoding, encoded := "base64", base64.StdEncoding.EncodeToString([]byte(content))
		if len(content) > 1_000_000 {
			encoding, encoded = "none", ""
		}
		writeJSON(t, w, map[string]any{
			"type": "file", "path": r.PathValue("path"), "encoding": encoding,
			"content": encoded, "size": len(content),
		})
	})
	mux.HandleFunc("GET /users/{login}", func(w http.ResponseWriter, r *http.Request) {
		suspended, ok := f.users[r.PathValue("login")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		user := map[string]any{"login": r.PathValue("login")}
		if suspended {
			user["suspended_at"] = "2026-01-02T03:04:05Z"
		}
		writeJSON(t, w, user)
	})
	mux.HandleFunc("POST /repos/o/r/check-runs", func(w http.ResponseWriter, r *http.Request) {
		var run github.CreateCheckRunOptions
		if err := json.NewDecoder(r.Body).Decode(&run); err != nil || run.Name == "" || run.HeadSHA == "" {
			http.Error(w, `{"message":"bad body"}`, http.StatusUnprocessableEntity)
			return
		}
		f.mu.Lock()
		f.checkRuns = append(f.checkRuns, run)
		f.mu.Unlock()
		writeJSON(t, w, github.CheckRun{Name: new(run.Name), HeadSHA: new(run.HeadSHA), Conclusion: run.Conclusion})
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]string{"token": "inst-" + r.PathValue("id"), "expires_at": "2099-01-01T00:00:00Z"})
	})
	mux.HandleFunc("GET /orgs/{org}/teams/{slug}/members", func(w http.ResponseWriter, r *http.Request) {
		members, ok := f.teams[r.PathValue("org")+"/"+r.PathValue("slug")]
		if !ok {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		users := make([]map[string]string, 0, len(members))
		for _, m := range members {
			users = append(users, map[string]string{"login": m})
		}
		writeJSON(t, w, users)
	})
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		fail := f.fail
		if f.calls == nil {
			f.calls = map[string]int{}
		}
		f.calls[endpointOf(r.URL.Path)]++
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if fail {
			http.Error(w, `{"message":"Server Error"}`, http.StatusInternalServerError)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// newTestClient returns a client whose API requests go to baseURL.
func newTestClient(t *testing.T, baseURL string) *github.Client {
	t.Helper()
	u := baseURL + "/"
	client, err := github.NewClient(github.WithURLs(&u, nil))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

// endpointOf names the endpoint a request path is for, for call counts.
func endpointOf(path string) string {
	for _, e := range []struct{ part, name string }{
		{"/git/trees/", "tree"}, {"/git/blobs/", "blob"}, {"/pulls/7/files", "files"},
		{"/pulls/7/reviews", "reviews"}, {"/contents/", "contents"}, {"/orgs/", "teams"},
		{"/users/", "users"}, {"/check-runs", "check-runs"},
		{"/access_tokens", "tokens"}, {"/compare/", "compare"},
	} {
		if strings.Contains(path, e.part) {
			return e.name
		}
	}
	return path
}

// testAppID is the GitHub App the tests act as.
const testAppID = 99

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
			Filename: new(p),
			Status:   new(status),
			Patch:    new("@@ @@\n" + prefixLines("-", old) + prefixLines("+", fx.Change.Write[p])),
		})
	}
	for _, p := range fx.Change.Delete {
		files = append(files, &github.CommitFile{
			Filename: new(p),
			Status:   new("removed"),
			Patch:    new("@@ @@\n" + prefixLines("-", fx.Base[p])),
		})
	}
	for _, from := range slices.Sorted(maps.Keys(fx.Change.Rename)) {
		// An unchanged rename carries no patch.
		files = append(files, &github.CommitFile{
			Filename:         new(fx.Change.Rename[from]),
			PreviousFilename: new(from),
			Status:           new("renamed"),
		})
	}
	head := maps.Clone(fx.Base)
	maps.Copy(head, fx.Change.Write)
	for _, p := range fx.Change.Delete {
		delete(head, p)
	}
	for from, to := range fx.Change.Rename {
		head[to] = head[from]
		delete(head, from)
	}
	return &fakeGitHub{base: fx.Base, head: head, prPages: [][]*github.CommitFile{files}}
}

// prefixLines prefixes every line of s with prefix.
func prefixLines(prefix, s string) string {
	var b strings.Builder
	for line := range strings.Lines(s) {
		b.WriteString(prefix + line)
	}
	return b.String()
}

// pullRequestEvent builds a pull_request event for o/r#7 by "author", at
// base revision base and head revision "head".
func pullRequestEvent(base string) *github.PullRequestEvent {
	return &github.PullRequestEvent{
		Action: new("opened"),
		Repo:   &github.Repository{Name: new("r"), Owner: &github.User{Login: new("o")}},
		PullRequest: &github.PullRequest{
			Number: new(7),
			User:   &github.User{Login: new("author")},
			Base:   &github.PullRequestBranch{SHA: new(base)},
			Head:   &github.PullRequestBranch{SHA: new("head")},
		},
		Installation: &github.Installation{ID: new(int64(5))},
	}
}

// testPR is the pull request the fake serves, o/r#7, at base revision
// "base" and head revision "head".
func testPR() *pullRequest {
	return &pullRequest{owner: "o", repo: "r", number: 7, baseSHA: "base", headSHA: "head", author: "author"}
}

// testSource is a Source over testPR on client, acting as testAppID.
func testSource(client *github.Client) *Source {
	return newSource(testPR(), client, NewApproverResolver(client), server.CacheScope{}, testLogger())
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
