package e2e_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"
)

// fakeGitHub is an in-process GitHub for end-to-end tests: repositories
// with branches and commits, pull requests and their reviews, teams, users,
// and one GitHub App installed on every repository. It serves the REST
// endpoints the platform reads and writes, and the ones users open pull
// requests and submit reviews through, and it delivers the app's signed
// webhooks as real GitHub does: synchronously, before the API call that
// caused them returns.
//
// Requests authenticate as real GitHub's do: the app with an App JWT
// (exchanged for an installation token), a user with the token userToken
// returns. Check runs are the app's alone.
//
// A pull request's files are the difference between its base and head
// branches' tips: the fake has no merge bases.
type fakeGitHub struct {
	t   *testing.T
	url string

	mu        sync.Mutex
	app       *fakeApp
	repos     map[string]*fakeRepo // by "owner/name"
	blobs     map[string]string    // blob SHA to contents
	teams     map[string][]string  // "org/slug" to member logins
	users     map[string]bool      // known logins to whether they are suspended
	tokens    map[string]bool      // installation tokens issued
	commits   int                  // commits made so far, naming the next
	reviewIDs int64                // reviews submitted so far, numbering the next
	notify    chan struct{}        // closed and replaced whenever a check run is created
	delivered []delivery
}

// fakeApp is the GitHub App the review server acts as, installed on every
// repository as installation.
type fakeApp struct {
	id           int64
	installation int64
	key          *rsa.PublicKey
	webhookURL   string
	secret       string
}

// fakeRepo is one repository.
type fakeRepo struct {
	owner, name string
	commits     map[string]map[string]string // commit SHA to its files: path to contents
	branches    map[string]string            // branch to its tip's commit SHA
	pulls       []*fakePull                  // pulls[n-1] is #n
	checkRuns   []github.CreateCheckRunOptions
}

// fakePull is one pull request.
type fakePull struct {
	number     int
	author     string
	base, head string // branch names
	reviews    []*github.PullRequestReview
	labels     []string
}

// delivery is one webhook the fake delivered, and the app's answer.
type delivery struct {
	event, action string
	status        int
}

// appLogin is the login the app's API calls act as.
const appLogin = "canopy[bot]"

// newFakeGitHub starts a fake with the given teams ("org/slug" to member
// logins) and users (login to whether they are suspended); it stops when
// the test ends.
func newFakeGitHub(t *testing.T, teams map[string][]string, users map[string]bool) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{
		t:      t,
		repos:  map[string]*fakeRepo{},
		blobs:  map[string]string{},
		teams:  teams,
		users:  users,
		tokens: map[string]bool{},
		notify: make(chan struct{}),
	}
	srv := httptest.NewServer(f.routes())
	t.Cleanup(srv.Close)
	f.url = srv.URL + "/"
	return f
}

// installApp installs the app on every repository: the fake accepts App
// JWTs signed by key for app id, and delivers webhooks signed with secret
// to webhookURL.
func (f *fakeGitHub) installApp(app fakeApp) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.app = &app
}

// createRepo creates owner/name with one commit holding files on branch
// "main".
func (f *fakeGitHub) createRepo(owner, name string, files map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &fakeRepo{owner: owner, name: name, commits: map[string]map[string]string{}, branches: map[string]string{}}
	f.repos[owner+"/"+name] = r
	r.branches["main"] = f.commitLocked(r, files)
}

// push commits files over branch's tip (from's tip when branch is new),
// deleting each path whose contents are "", and delivers a synchronize
// event for every open pull request from branch, as a push does.
func (f *fakeGitHub) push(ctx context.Context, repo, branch, from string, files map[string]string) {
	f.t.Helper()
	f.mu.Lock()
	r := f.repos[repo]
	parent, ok := r.branches[branch]
	if !ok {
		parent = r.branches[from]
	}
	tree := maps.Clone(r.commits[parent])
	for p, content := range files {
		if content == "" {
			delete(tree, p)
		} else {
			tree[p] = content
		}
	}
	r.branches[branch] = f.commitLocked(r, tree)
	var events []any
	for _, pr := range r.pulls {
		if pr.head == branch {
			events = append(events, f.pullRequestEventLocked(r, pr, "synchronize"))
		}
	}
	f.mu.Unlock()
	for _, ev := range events {
		f.deliver(ctx, "pull_request", ev)
	}
}

// commitLocked stores a commit of files in r and returns its SHA.
func (f *fakeGitHub) commitLocked(r *fakeRepo, files map[string]string) string {
	f.commits++
	sha := fmt.Sprintf("%040x", f.commits)
	r.commits[sha] = files
	for _, content := range files {
		f.blobs[blobSHA(content)] = content
	}
	return sha
}

// headSHA is the tip of repo's branch.
func (f *fakeGitHub) headSHA(repo, branch string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repos[repo].branches[branch]
}

// checkRuns returns the check runs created on repo, in order.
func (f *fakeGitHub) checkRuns(repo string) []github.CreateCheckRunOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.repos[repo].checkRuns)
}

// deliveries returns the webhooks delivered so far, in order.
func (f *fakeGitHub) deliveries() []delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.delivered)
}

// waitCheckRuns waits until repo has at least n completed check runs, or
// ctx is done, and returns the completed ones.
func (f *fakeGitHub) waitCheckRuns(ctx context.Context, repo string, n int) ([]github.CreateCheckRunOptions, error) {
	for {
		f.mu.Lock()
		runs, notify := slices.Clone(f.repos[repo].checkRuns), f.notify
		f.mu.Unlock()
		runs = slices.DeleteFunc(runs, func(r github.CreateCheckRunOptions) bool { return r.GetStatus() != "completed" })
		if len(runs) >= n {
			return runs, nil
		}
		select {
		case <-ctx.Done():
			return runs, fmt.Errorf("waiting for check run %d: %w", n, ctx.Err())
		case <-notify:
		}
	}
}

// userToken is the API token that authenticates as login.
func userToken(login string) string { return "user-" + login }

// userClient is an API client acting as login.
func (f *fakeGitHub) userClient(login string) *github.Client {
	f.t.Helper()
	u := f.url
	client, err := github.NewClient(github.WithURLs(&u, &u), github.WithAuthToken(userToken(login)))
	if err != nil {
		f.t.Fatalf("new client: %v", err)
	}
	return client
}

// blobSHA is git's SHA of a blob of content.
func blobSHA(content string) string {
	sum := sha1.Sum(fmt.Appendf(nil, "blob %d\x00%s", len(content), content))
	return hex.EncodeToString(sum[:])
}

// caller is who authenticated a request: a user's login, or appLogin.
type caller struct{ login string }

func (c caller) isApp() bool { return c.login == appLogin }

// authenticate returns the request's caller from its installation or user
// token, or false.
func (f *fakeGitHub) authenticate(r *http.Request) (caller, bool) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return caller{}, false
	}
	if login, ok := strings.CutPrefix(tok, "user-"); ok {
		_, known := f.users[login]
		return caller{login: login}, known
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return caller{login: appLogin}, f.tokens[tok]
}

// routes is the fake's API.
func (f *fakeGitHub) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", f.createToken)
	api := func(pattern string, appOnly bool, h func(http.ResponseWriter, *http.Request, caller, *fakeRepo)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			c, ok := f.authenticate(r)
			switch {
			case !ok:
				writeError(w, http.StatusUnauthorized, "Bad credentials")
				return
			case appOnly && !c.isApp():
				writeError(w, http.StatusForbidden, "Resource not accessible by integration")
				return
			}
			var repo *fakeRepo
			if owner := r.PathValue("owner"); owner != "" {
				f.mu.Lock()
				repo = f.repos[owner+"/"+r.PathValue("repo")]
				f.mu.Unlock()
				if repo == nil {
					writeError(w, http.StatusNotFound, "Not Found")
					return
				}
			}
			h(w, r, c, repo)
		})
	}
	api("GET /repos/{owner}/{repo}/git/trees/{sha}", false, f.getTree)
	api("GET /repos/{owner}/{repo}/git/blobs/{sha}", false, f.getBlob)
	api("GET /repos/{owner}/{repo}/contents/{path...}", false, f.getContents)
	api("POST /repos/{owner}/{repo}/pulls", false, f.createPull)
	// No pull request file listing: the platform compares the event's
	// revisions instead, so requesting it is an unexpected request.
	api("GET /repos/{owner}/{repo}/compare/{basehead}", false, f.compareCommits)
	api("GET /repos/{owner}/{repo}/pulls/{number}/reviews", false, f.listReviews)
	api("POST /repos/{owner}/{repo}/pulls/{number}/reviews", false, f.createReview)
	api("GET /repos/{owner}/{repo}/issues/{number}/labels", false, f.listLabels)
	api("POST /repos/{owner}/{repo}/issues/{number}/labels", false, f.addLabels)
	api("DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}", false, f.removeLabel)
	api("POST /repos/{owner}/{repo}/check-runs", true, f.createCheckRun)
	api("GET /users/{login}", false, f.getUser)
	api("GET /orgs/{org}/teams/{slug}/members", false, f.listTeamMembers)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.t.Errorf("fake GitHub: unexpected request %s %s", r.Method, r.URL.Path)
		writeError(w, http.StatusNotFound, "Not Found")
	})
	return mux
}

// createToken exchanges a valid App JWT for an installation token.
func (f *fakeGitHub) createToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	app := f.app
	f.mu.Unlock()
	jwt, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if app == nil || !validJWT(jwt, app.id, app.key) {
		writeError(w, http.StatusUnauthorized, "A JSON web token could not be decoded")
		return
	}
	if r.PathValue("id") != strconv.FormatInt(app.installation, 10) {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	f.mu.Lock()
	tok := fmt.Sprintf("ghs_%d", len(f.tokens)+1)
	f.tokens[tok] = true
	f.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
}

// validJWT reports whether jwt is an unexpired RS256 App JWT issued by app
// id and signed by key.
func validJWT(jwt string, id int64, key *rsa.PublicKey) bool {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct{ Iss, Exp int64 }
	return json.Unmarshal(payload, &claims) == nil && claims.Iss == id && claims.Exp > time.Now().Unix()
}

// getTree lists a commit's files recursively; the fake never truncates.
func (f *fakeGitHub) getTree(w http.ResponseWriter, r *http.Request, _ caller, repo *fakeRepo) {
	f.mu.Lock()
	files, ok := repo.commits[r.PathValue("sha")]
	f.mu.Unlock()
	if !ok || r.URL.Query().Get("recursive") == "" {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	entries := []map[string]any{}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		entries = append(entries, map[string]any{"path": p, "mode": "100644", "type": "blob", "sha": blobSHA(files[p]), "size": len(files[p])})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sha": r.PathValue("sha"), "tree": entries, "truncated": false})
}

func (f *fakeGitHub) getBlob(w http.ResponseWriter, r *http.Request, _ caller, _ *fakeRepo) {
	f.mu.Lock()
	content, ok := f.blobs[r.PathValue("sha")]
	f.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sha": r.PathValue("sha"), "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)), "size": len(content)})
}

// getContents reads a file at the ref query parameter, a commit SHA.
func (f *fakeGitHub) getContents(w http.ResponseWriter, r *http.Request, _ caller, repo *fakeRepo) {
	path := r.PathValue("path")
	f.mu.Lock()
	content, ok := repo.commits[r.URL.Query().Get("ref")][path]
	f.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"type": "file", "path": path, "sha": blobSHA(content), "size": len(content),
		"encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content)),
	})
}

// createPull opens a pull request as the caller and delivers its opened
// event.
func (f *fakeGitHub) createPull(w http.ResponseWriter, r *http.Request, c caller, repo *fakeRepo) {
	var req github.CreatePullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON")
		return
	}
	f.mu.Lock()
	_, hasBase := repo.branches[req.Base]
	_, hasHead := repo.branches[req.Head]
	if c.isApp() || !hasBase || !hasHead {
		f.mu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	pr := &fakePull{number: len(repo.pulls) + 1, author: c.login, base: req.Base, head: req.Head}
	repo.pulls = append(repo.pulls, pr)
	ev := f.pullRequestEventLocked(repo, pr, "opened")
	f.mu.Unlock()
	f.deliver(r.Context(), "pull_request", ev)
	writeJSON(w, http.StatusCreated, ev.PullRequest)
}

// pull is the pull request the request's path names, or nil.
func (f *fakeGitHub) pull(r *http.Request, repo *fakeRepo) *fakePull {
	n, err := strconv.Atoi(r.PathValue("number"))
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil || n < 1 || n > len(repo.pulls) {
		return nil
	}
	return repo.pulls[n-1]
}

// listPullFiles lists the files that differ between the pull request's
// base and head tips, in one page.
// compareCommits compares two commits by SHA (base...head), reporting the
// base as the merge base: the fake's branches never diverge from it.
func (f *fakeGitHub) compareCommits(w http.ResponseWriter, r *http.Request, _ caller, repo *fakeRepo) {
	base, head, ok := strings.Cut(r.PathValue("basehead"), "...")
	f.mu.Lock()
	from, inBase := repo.commits[base]
	to, inHead := repo.commits[head]
	f.mu.Unlock()
	if !ok || !inBase || !inHead {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"merge_base_commit": map[string]string{"sha": base}, "files": pullFiles(from, to)})
}

// pullFiles is GitHub's file listing of the change from base to head:
// every path added, modified, or removed, with a hunk-only patch.
func pullFiles(base, head map[string]string) []*github.CommitFile {
	paths := slices.Collect(maps.Keys(base))
	for p := range head {
		if _, ok := base[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	files := []*github.CommitFile{}
	for _, p := range paths {
		old, inBase := base[p]
		cur, inHead := head[p]
		status := "modified"
		switch {
		case inBase && inHead && old == cur:
			continue
		case !inBase:
			status = "added"
		case !inHead:
			status = "removed"
		}
		files = append(files, &github.CommitFile{
			Filename: new(p),
			Status:   new(status),
			Patch:    new("@@ @@\n" + prefixLines("-", old) + prefixLines("+", cur)),
		})
	}
	return files
}

// prefixLines prefixes every line of s with prefix.
func prefixLines(prefix, s string) string {
	var b strings.Builder
	for line := range strings.Lines(s) {
		b.WriteString(prefix + strings.TrimSuffix(line, "\n") + "\n")
	}
	return b.String()
}

func (f *fakeGitHub) listReviews(w http.ResponseWriter, r *http.Request, _ caller, repo *fakeRepo) {
	pr := f.pull(r, repo)
	if pr == nil {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	f.mu.Lock()
	reviews := slices.Clone(pr.reviews)
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, reviews)
}

// listLabels lists the pull request's labels, in one page.
func (f *fakeGitHub) listLabels(w http.ResponseWriter, r *http.Request, _ caller, repo *fakeRepo) {
	pr := f.pull(r, repo)
	if pr == nil {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	f.mu.Lock()
	labels := labelsOf(pr.labels)
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, labels)
}

// addLabels adds labels to the pull request as a user, delivering a
// labeled event for each label it did not have.
func (f *fakeGitHub) addLabels(w http.ResponseWriter, r *http.Request, c caller, repo *fakeRepo) {
	pr := f.pull(r, repo)
	var names []string
	if pr == nil || c.isApp() || json.NewDecoder(r.Body).Decode(&names) != nil {
		writeError(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	var events []*github.PullRequestEvent
	f.mu.Lock()
	for _, l := range names {
		if !slices.Contains(pr.labels, l) {
			pr.labels = append(pr.labels, l)
			ev := f.pullRequestEventLocked(repo, pr, "labeled")
			ev.Label = &github.Label{Name: l}
			events = append(events, ev)
		}
	}
	labels := labelsOf(pr.labels)
	f.mu.Unlock()
	for _, ev := range events {
		f.deliver(r.Context(), "pull_request", ev)
	}
	writeJSON(w, http.StatusOK, labels)
}

// removeLabel removes a label from the pull request as a user, delivering
// an unlabeled event.
func (f *fakeGitHub) removeLabel(w http.ResponseWriter, r *http.Request, c caller, repo *fakeRepo) {
	pr := f.pull(r, repo)
	name := r.PathValue("name")
	f.mu.Lock()
	i := -1
	if pr != nil {
		i = slices.Index(pr.labels, name)
	}
	if i < 0 || c.isApp() {
		f.mu.Unlock()
		writeError(w, http.StatusNotFound, "Label does not exist")
		return
	}
	pr.labels = slices.Delete(pr.labels, i, i+1)
	ev := f.pullRequestEventLocked(repo, pr, "unlabeled")
	ev.Label = &github.Label{Name: name}
	labels := labelsOf(pr.labels)
	f.mu.Unlock()
	f.deliver(r.Context(), "pull_request", ev)
	writeJSON(w, http.StatusOK, labels)
}

// labelsOf is names as GitHub lists labels.
func labelsOf(names []string) []*github.Label {
	labels := make([]*github.Label, len(names))
	for i, n := range names {
		labels[i] = &github.Label{Name: n}
	}
	return labels
}

// reviewStates maps a review request's event to the review's state.
var reviewStates = map[string]string{"APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED"}

// createReview submits a review as the caller and delivers its submitted
// event. As on GitHub, an author cannot approve or request changes on
// their own pull request.
func (f *fakeGitHub) createReview(w http.ResponseWriter, r *http.Request, c caller, repo *fakeRepo) {
	pr := f.pull(r, repo)
	if pr == nil {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	var req github.PullRequestReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON")
		return
	}
	state, ok := reviewStates[req.GetEvent()]
	if !ok || c.isApp() || (c.login == pr.author && state != "COMMENTED") {
		writeError(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	f.mu.Lock()
	f.reviewIDs++
	review := &github.PullRequestReview{
		ID:       new(f.reviewIDs),
		User:     &github.User{Login: new(c.login)},
		State:    new(state),
		CommitID: new(repo.branches[pr.head]),
	}
	pr.reviews = append(pr.reviews, review)
	prev := f.pullRequestEventLocked(repo, pr, "")
	ev := &github.PullRequestReviewEvent{
		Action: new("submitted"), Review: review,
		Repo: prev.Repo, PullRequest: prev.PullRequest, Installation: prev.Installation, Sender: review.User,
	}
	f.mu.Unlock()
	f.deliver(r.Context(), "pull_request_review", ev)
	writeJSON(w, http.StatusOK, review)
}

// createCheckRun records a check run on a commit of the repository.
func (f *fakeGitHub) createCheckRun(w http.ResponseWriter, r *http.Request, _ caller, repo *fakeRepo) {
	var run github.CreateCheckRunOptions
	if err := json.NewDecoder(r.Body).Decode(&run); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := repo.commits[run.HeadSHA]; !ok || run.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "Validation Failed")
		return
	}
	repo.checkRuns = append(repo.checkRuns, run)
	close(f.notify)
	f.notify = make(chan struct{})
	writeJSON(w, http.StatusCreated, github.CheckRun{ID: new(int64(len(repo.checkRuns))), Name: new(run.Name), HeadSHA: new(run.HeadSHA), Conclusion: run.Conclusion})
}

func (f *fakeGitHub) getUser(w http.ResponseWriter, r *http.Request, _ caller, _ *fakeRepo) {
	login := r.PathValue("login")
	suspended, ok := f.users[login]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	user := map[string]any{"login": login, "type": "User"}
	if suspended {
		user["suspended_at"] = "2026-01-02T03:04:05Z"
	}
	writeJSON(w, http.StatusOK, user)
}

// listTeamMembers lists a team's members in one page.
func (f *fakeGitHub) listTeamMembers(w http.ResponseWriter, r *http.Request, _ caller, _ *fakeRepo) {
	members, ok := f.teams[r.PathValue("org")+"/"+r.PathValue("slug")]
	if !ok {
		writeError(w, http.StatusNotFound, "Not Found")
		return
	}
	users := []map[string]string{}
	for _, m := range members {
		users = append(users, map[string]string{"login": m})
	}
	writeJSON(w, http.StatusOK, users)
}

// pullRequestEventLocked is pr's webhook event for action, at its base
// and head branches' current tips.
func (f *fakeGitHub) pullRequestEventLocked(repo *fakeRepo, pr *fakePull, action string) *github.PullRequestEvent {
	author := &github.User{Login: new(pr.author)}
	ev := &github.PullRequestEvent{
		Number: new(pr.number),
		Repo: &github.Repository{
			Name: new(repo.name), FullName: new(repo.owner + "/" + repo.name),
			Owner: &github.User{Login: new(repo.owner)},
		},
		PullRequest: &github.PullRequest{
			Number: new(pr.number),
			State:  new("open"),
			User:   author,
			Base:   &github.PullRequestBranch{Ref: new(pr.base), SHA: new(repo.branches[pr.base])},
			Head:   &github.PullRequestBranch{Ref: new(pr.head), SHA: new(repo.branches[pr.head])},
		},
		Sender: author,
	}
	if action != "" {
		ev.Action = new(action)
	}
	if f.app != nil {
		ev.Installation = &github.Installation{ID: new(f.app.installation)}
	}
	return ev
}

// deliver posts payload to the app's webhook as event, signed with the
// webhook secret, and records the delivery. Without an app installed,
// nothing is delivered.
func (f *fakeGitHub) deliver(ctx context.Context, event string, payload any) {
	f.mu.Lock()
	app := f.app
	f.mu.Unlock()
	if app == nil {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		f.t.Errorf("marshal %s webhook: %v", event, err)
		return
	}
	var action struct{ Action string }
	_ = json.Unmarshal(body, &action) // every payload is an object

	mac := hmac.New(sha256.New, []byte(app.secret))
	mac.Write(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, app.webhookURL, bytes.NewReader(body))
	if err != nil {
		f.t.Errorf("build %s webhook: %v", event, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("delivery-%d", len(f.deliveries())+1))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	status := 0
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Errorf("deliver %s webhook: %v", event, err)
	} else {
		status = resp.StatusCode
		_ = resp.Body.Close()
	}
	f.mu.Lock()
	f.delivered = append(f.delivered, delivery{event: event, action: action.Action, status: status})
	f.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // a failed write is the client's to see
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}
