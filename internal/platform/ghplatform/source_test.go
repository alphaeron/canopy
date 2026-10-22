package ghplatform

import (
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"testing"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestGitHubAllFilesListsBlobsAtBaseTree(t *testing.T) {
	fake := &fakeGitHub{
		base: map[string]string{rulesfile.FileName: "", "src/api.go": ""},
		dirs: []string{"src"},
	}
	files, err := testSource(fake.client(t)).AllFiles(t.Context())
	if err != nil {
		t.Fatalf("AllFiles: %v", err)
	}
	// The non-blob entry ("src", a tree) must be excluded.
	if want := []string{rulesfile.FileName, "src/api.go"}; !reflect.DeepEqual(files, want) {
		t.Errorf("AllFiles = %v, want %v", files, want)
	}
}

// TestGitHubTruncatedTreeFailsClosed: a truncated tree listing is never used
// as the universe or for the rules, since a missing rules file would drop
// required approvers.
func TestGitHubTruncatedTreeFailsClosed(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{rulesfile.FileName: ""}, truncated: true}
	src := testSource(fake.client(t))
	if _, err := src.AllFiles(t.Context()); !errors.Is(err, review.ErrTreeTruncated) {
		t.Errorf("AllFiles err = %v, want review.ErrTreeTruncated", err)
	}
	if _, err := src.Evaluator().Fetch.Execute(t.Context()); !errors.Is(err, review.ErrTreeTruncated) {
		t.Errorf("Fetch err = %v, want review.ErrTreeTruncated", err)
	}
	state := review.New(src, testLogger()).Review(t.Context(), []string{"a.go"}, approval.ReviewInput{})
	if state.Overall != approval.StatusError || !errors.Is(state.Err, review.ErrTreeTruncated) {
		t.Errorf("review = %s, %v; want an error wrapping review.ErrTreeTruncated", state.Overall, state.Err)
	}
}

// TestGitHubSourceListsBaseTreeOnce: the file universe and the rules fetch
// share one base tree listing.
func TestGitHubSourceListsBaseTreeOnce(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{rulesfile.FileName: "- \".*\":\n  name: \"Root\"\n"}}
	src := testSource(fake.client(t))

	if _, err := src.AllFiles(t.Context()); err != nil {
		t.Fatalf("AllFiles: %v", err)
	}
	if _, err := src.Evaluator().Fetch.Execute(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := fake.callCount("tree"); got != 1 {
		t.Errorf("tree listings = %d, want 1", got)
	}
}

// TestGitHubSourceIsOnTheGitHubPlatform: a GitHub source's rules and
// approver resolution both come from GitHub, bound to the pull request, and
// it has no on-disk root.
func TestGitHubSourceIsOnTheGitHubPlatform(t *testing.T) {
	client, err := github.NewClient()
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	src := testSource(client)
	fetch, ok := src.Evaluator().Fetch.(*approval.TreeFetcher)
	if !ok {
		t.Fatalf("fetcher = %T, want *approval.TreeFetcher", src.Evaluator().Fetch)
	}
	if tree, ok := fetch.Tree.(*baseTree); !ok || tree != src.tree || tree.owner != "o" || tree.repo != "r" || tree.sha != "base" {
		t.Errorf("fetch tree = %#v, want the source's base tree of o/r@base", fetch.Tree)
	}
	if _, ok := src.Evaluator().Resolver.(*ApproverResolver); !ok {
		t.Errorf("resolver = %T, want *ApproverResolver", src.Evaluator().Resolver)
	}
	if _, ok := any(src).(review.Rooted); ok {
		t.Error("a GitHub source must not be review.Rooted")
	}
}

// TestGitHubChangeListsAllPagesOnce: the changed files and their
// patches come from every page of the file listing, listed once on first
// use; a rename contributes the path it left as well as its new one; and
// the source is bound to the event's repository and base revision.
func TestGitHubChangeListsAllPagesOnce(t *testing.T) {
	fake := &fakeGitHub{prPages: [][]*github.CommitFile{
		{{Filename: new("a.go"), Status: new("added"), Patch: new("+a\n")}},
		{{Filename: new("b.go"), Status: new("modified"), Patch: new("+b\n")}, {Filename: new("bin.png"), Status: new("added")}},
		{
			{Filename: new("new/c.go"), PreviousFilename: new("old/c.go"), Status: new("renamed"), Changes: new(1), Patch: new("+c\n")},
			{Filename: new("new/d.go"), PreviousFilename: new("old/d.go"), Status: new("renamed")},
		},
	}}
	src := changeFromEvent(t, fake, pullRequestEvent("deadbeef")).(*Source)
	if got := fake.callCount("files"); got != 0 {
		t.Errorf("file listings before use = %d, want none", got)
	}
	changed, err := src.ChangedFiles(t.Context())
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if want := []string{"a.go", "b.go", "bin.png", "new/c.go", "new/d.go", "old/c.go", "old/d.go"}; !reflect.DeepEqual(changed, want) {
		t.Errorf("ChangedFiles = %v, want %v", changed, want)
	}
	tests := []struct {
		path string
		want string
	}{
		{path: "b.go", want: "+b\n"},
		{path: "bin.png"},
		{path: "new/c.go", want: "+c\n"},
		{path: "new/d.go"},
		{path: "old/c.go"},
		{path: "absent.go"},
	}
	for _, tt := range tests {
		got, err := src.DiffContent(t.Context(), tt.path)
		if err != nil || got != tt.want {
			t.Errorf("DiffContent(%s) = %q, %v; want %q", tt.path, got, err, tt.want)
		}
	}
	if got := fake.callCount("files"); got != 3 {
		t.Errorf("file listing requests = %d, want 3 (one listing, three pages)", got)
	}
	if src.tree.owner != "o" || src.tree.repo != "r" || src.tree.sha != "deadbeef" {
		t.Errorf("base tree = %s/%s@%s, want o/r@deadbeef", src.tree.owner, src.tree.repo, src.tree.sha)
	}
}

func TestGitHubChangeRejectsIncompleteEvents(t *testing.T) {
	noRepo := pullRequestEvent("deadbeef")
	noRepo.Repo = nil
	noNumber := pullRequestEvent("deadbeef")
	noNumber.PullRequest.Number = nil
	noHead := pullRequestEvent("deadbeef")
	noHead.PullRequest.Head = nil
	tests := []struct {
		name  string
		event *github.PullRequestEvent
	}{
		{name: "no pull request", event: &github.PullRequestEvent{Action: new("opened"), Installation: &github.Installation{ID: new(int64(5))}}},
		{name: "no repository", event: noRepo},
		{name: "no base revision", event: pullRequestEvent("")},
		{name: "no head revision", event: noHead},
		{name: "no number", event: noNumber},
	}
	p, _ := testPRWebhook(t, &fakeGitHub{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if _, _, err := p.PullRequest("pull_request", body); err == nil {
				t.Error("Change succeeded, want an error")
			}
		})
	}
}

// TestGitHubListingErrorSurfacesAndRetries: an API failure listing the
// files is returned and not kept, so a later call lists again.
func TestGitHubListingErrorSurfacesAndRetries(t *testing.T) {
	fake := &fakeGitHub{fail: true, prPages: [][]*github.CommitFile{{{Filename: new("a.go"), Status: new("added"), Patch: new("+a\n")}}}}
	src := testSource(fake.client(t))
	_, err := src.ChangedFiles(t.Context())
	var ghErr *github.ErrorResponse
	if !errors.As(err, &ghErr) {
		t.Fatalf("err = %v, want the wrapped *github.ErrorResponse", err)
	}
	fake.setFail(false)
	if got, err := src.ChangedFiles(t.Context()); err != nil || !reflect.DeepEqual(got, []string{"a.go"}) {
		t.Errorf("ChangedFiles after recovery = %v, %v; want [a.go]", got, err)
	}
}

// TestGitHubIncompleteFileListing: a pull request with more files than
// GitHub lists is never presented as complete.
func TestGitHubIncompleteFileListing(t *testing.T) {
	page := func(n int) []*github.CommitFile {
		files := make([]*github.CommitFile, n)
		for i := range files {
			files[i] = &github.CommitFile{Filename: new(string(rune('a'+i%26)) + string(rune('0'+i/26%10))), Status: new("added"), Patch: new("+\n")}
		}
		return files
	}
	tests := []struct {
		name     string
		reported int
		pages    [][]*github.CommitFile
		wantErr  bool
	}{
		{name: "listing short of the reported count", reported: 5, pages: [][]*github.CommitFile{page(3)}, wantErr: true},
		{name: "listing matches the reported count", reported: 3, pages: [][]*github.CommitFile{page(3)}},
		{name: "count unknown, under the cap", pages: [][]*github.CommitFile{page(3)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeGitHub{prPages: tt.pages}
			pr := testPR()
			pr.changedFiles = tt.reported
			src := newSource(pr, fake.client(t), nil, testLogger())
			_, err := src.ChangedFiles(t.Context())
			if got := errors.Is(err, review.ErrFileListIncomplete); got != tt.wantErr {
				t.Fatalf("ChangedFiles err = %v, want ErrFileListIncomplete %v", err, tt.wantErr)
			}
		})
	}
	if !incomplete(0, maxListedFiles) {
		t.Error("a listing at GitHub's cap with no count to confirm it must count as incomplete")
	}
}

func TestGitHubReadHead(t *testing.T) {
	fake := &fakeGitHub{head: map[string]string{"svc/" + rulesfile.FileName: "- \"x\":\n  name: X\n"}}
	src := testSource(fake.client(t))
	got, err := src.ReadHead(t.Context(), "svc/"+rulesfile.FileName)
	if err != nil || string(got) != "- \"x\":\n  name: X\n" {
		t.Errorf("ReadHead = %q, %v; want the head revision's contents", got, err)
	}
	if _, err := src.ReadHead(t.Context(), "missing/"+rulesfile.FileName); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadHead(missing) err = %v, want fs.ErrNotExist", err)
	}
}

// TestGitHubReviewInput: GitHub specifics past the contract: the
// repository owner is the fallback reviewer, suspended or unknown users are
// inactive, each user is looked up once, and a failed lookup makes the user
// inactive.
func TestGitHubReviewInput(t *testing.T) {
	fake := &fakeGitHub{
		reviewPages: [][]*github.PullRequestReview{{
			{User: &github.User{Login: new("Ann")}, State: new("APPROVED")},
		}},
		users: map[string]bool{"ann": false, "sus": true},
	}
	in, err := testSource(fake.client(t)).ReviewInput(t.Context())
	if err != nil {
		t.Fatalf("ReviewInput: %v", err)
	}
	if in.Author != "author" || !reflect.DeepEqual(in.Approvers, []string{"ann"}) || !reflect.DeepEqual(in.Owners, []string{"o"}) {
		t.Errorf("ReviewInput = author %q, approvers %v, owners %v; want author, [ann], [o]", in.Author, in.Approvers, in.Owners)
	}
	for login, want := range map[string]bool{"ann": true, "sus": false, "ghost": false, "org/team": false} {
		if got := in.IsActive(login); got != want {
			t.Errorf("IsActive(%s) = %v, want %v", login, got, want)
		}
	}
	in.IsActive("ann")
	if got := fake.callCount("users"); got != 3 {
		t.Errorf("user lookups = %d, want 3 (one per well-formed login, cached)", got)
	}

	fake.setFail(true)
	if in.IsActive("down") {
		t.Error("IsActive with GitHub failing = true, want false: only a user GitHub confirms is active")
	}
}

func TestGitHubReportsOnTheHeadRevision(t *testing.T) {
	fake := &fakeGitHub{}
	if err := testSource(fake.client(t)).Report(t.Context(), review.Outcome{Verdict: review.Passed, Summary: "ok"}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(fake.statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(fake.statuses))
	}
	s := fake.statuses[0]
	if s.GetURL() != "head" || s.GetState() != "success" || s.GetContext() != statusContext {
		t.Errorf("status = %s on %s under %s, want success on head under %s", s.GetState(), s.GetURL(), s.GetContext(), statusContext)
	}
	if len(fake.statuses[0].GetDescription()) > maxStatusDescription {
		t.Errorf("description over GitHub's limit: %q", s.GetDescription())
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s     string
		limit int
		want  string
	}{
		{s: "short", limit: 10, want: "short"},
		{s: "exactly10!", limit: 10, want: "exactly10!"},
		{s: "abcdefghijkl", limit: 10, want: "abcdefg…"},
		{s: "ééééé", limit: 8, want: "éé…"}, // never splits a two-byte é
	}
	for _, tt := range tests {
		if got := truncate(tt.s, tt.limit, "…"); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.limit, got, tt.want)
		}
	}
}
