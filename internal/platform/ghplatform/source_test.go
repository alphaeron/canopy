package ghplatform

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestGitHubAllFilesListsBlobsAtBaseTree(t *testing.T) {
	fake := &fakeGitHub{
		base: map[string]string{rulesfile.FileName: "", "src/api.go": ""},
		dirs: []string{"src"},
	}
	src := newSource(&pullRequest{owner: "o", repo: "r", baseSHA: "deadbeef"}, fake.client(t), nil)

	files, err := src.AllFiles(t.Context())
	if err != nil {
		t.Fatalf("AllFiles: %v", err)
	}
	// The non-blob entry ("src", a tree) must be excluded.
	if want := []string{rulesfile.FileName, "src/api.go"}; !reflect.DeepEqual(files, want) {
		t.Errorf("AllFiles = %v, want %v", files, want)
	}
}

// TestGitHubSourceListsBaseTreeOnce: the file universe and the rules fetch
// share one base tree listing.
func TestGitHubSourceListsBaseTreeOnce(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{rulesfile.FileName: "- \".*\":\n  name: \"Root\"\n"}}
	src := newSource(&pullRequest{owner: "o", repo: "r", baseSHA: "deadbeef"}, fake.client(t), nil)

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

func TestGitHubChangedFilesAndDiffFromListedFiles(t *testing.T) {
	pr := &pullRequest{
		owner: "o", repo: "r", baseSHA: "deadbeef",
		files: []prFile{
			{path: "main.go", patch: "-old\n+new\n"},
			{path: "src/x.go", patch: "+added\n"},
		},
	}
	// nil client: the changed-files and diff paths read the listed files, not the network.
	src := newSource(pr, nil, nil)

	changed, err := src.ChangedFiles(t.Context())
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if want := []string{"main.go", "src/x.go"}; !reflect.DeepEqual(changed, want) {
		t.Errorf("ChangedFiles = %v, want %v", changed, want)
	}
	if got, _ := src.DiffContent(t.Context(), "main.go"); got != "-old\n+new\n" {
		t.Errorf("DiffContent = %q, want the patch listed for that file", got)
	}
	if got, _ := src.DiffContent(t.Context(), "absent.go"); got != "" {
		t.Errorf("DiffContent for a file the PR does not change should be empty, got %q", got)
	}
}

// TestGitHubSourceIsOnTheGitHubPlatform: a GitHub source's rules and
// approver resolution both come from GitHub, bound to the pull request, and
// it has no on-disk root.
func TestGitHubSourceIsOnTheGitHubPlatform(t *testing.T) {
	src := newSource(&pullRequest{owner: "o", repo: "r", baseSHA: "x"}, github.NewClient(nil), nil)
	fetch, ok := src.Evaluator().Fetch.(*approval.TreeFetcher)
	if !ok {
		t.Fatalf("fetcher = %T, want *approval.TreeFetcher", src.Evaluator().Fetch)
	}
	if tree, ok := fetch.Tree.(*baseTree); !ok || tree != src.tree || tree.owner != "o" || tree.repo != "r" || tree.sha != "x" {
		t.Errorf("fetch tree = %#v, want the source's base tree of o/r@x", fetch.Tree)
	}
	if _, ok := src.Evaluator().Resolver.(*ApproverResolver); !ok {
		t.Errorf("resolver = %T, want *ApproverResolver", src.Evaluator().Resolver)
	}
	if _, ok := any(src).(review.Rooted); ok {
		t.Error("a GitHub source must not be review.Rooted")
	}
}

// TestNewSourceFromEventListsAllPages: the changed files and their patches
// come from every page of the file listing, a rename contributes the path it
// left as well as its new one, and the source is bound to the event's
// repository and base revision.
func TestNewSourceFromEventListsAllPages(t *testing.T) {
	fake := &fakeGitHub{prPages: [][]*github.CommitFile{
		{{Filename: github.Ptr("a.go"), Patch: github.Ptr("+a\n")}},
		{{Filename: github.Ptr("b.go"), Patch: github.Ptr("+b\n")}, {Filename: github.Ptr("bin.png")}},
		{{Filename: github.Ptr("new/c.go"), PreviousFilename: github.Ptr("old/c.go"), Status: github.Ptr("renamed"), Patch: github.Ptr("+c\n")}},
	}}

	src, err := NewSourceFromEvent(t.Context(), pullRequestEvent("deadbeef"), fake.client(t), nil)
	if err != nil {
		t.Fatalf("NewSourceFromEvent: %v", err)
	}
	changed, err := src.ChangedFiles(t.Context())
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if want := []string{"a.go", "b.go", "bin.png", "new/c.go", "old/c.go"}; !reflect.DeepEqual(changed, want) {
		t.Errorf("ChangedFiles = %v, want %v", changed, want)
	}
	if got, _ := src.DiffContent(t.Context(), "b.go"); got != "+b\n" {
		t.Errorf("DiffContent(b.go) = %q, want the second page's patch", got)
	}
	if got, _ := src.DiffContent(t.Context(), "bin.png"); got != "" {
		t.Errorf("DiffContent(bin.png) = %q, want empty for a file without a patch", got)
	}
	if got, _ := src.DiffContent(t.Context(), "new/c.go"); got != "+c\n" {
		t.Errorf("DiffContent(new/c.go) = %q, want the rename's patch", got)
	}
	if got, _ := src.DiffContent(t.Context(), "old/c.go"); got != "" {
		t.Errorf("DiffContent(old/c.go) = %q, want empty for the path a rename left", got)
	}
	if src.tree.owner != "o" || src.tree.repo != "r" || src.tree.sha != "deadbeef" {
		t.Errorf("base tree = %s/%s@%s, want o/r@deadbeef", src.tree.owner, src.tree.repo, src.tree.sha)
	}
}

func TestNewSourceFromEventRejectsIncompleteEvents(t *testing.T) {
	noRepo := pullRequestEvent("deadbeef")
	noRepo.Repo = nil
	noNumber := pullRequestEvent("deadbeef")
	noNumber.PullRequest.Number = nil
	tests := []struct {
		name  string
		event *github.PullRequestEvent
	}{
		{name: "nil event", event: nil},
		{name: "no pull request", event: &github.PullRequestEvent{}},
		{name: "no repository", event: noRepo},
		{name: "no base revision", event: pullRequestEvent("")},
		{name: "no number", event: noNumber},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// nil client: an incomplete event must fail before any API call.
			if _, err := NewSourceFromEvent(t.Context(), tt.event, nil, nil); err == nil {
				t.Error("NewSourceFromEvent succeeded, want an error")
			}
		})
	}
}

func TestNewSourceFromEventListingErrorSurfaces(t *testing.T) {
	fake := &fakeGitHub{fail: true}
	_, err := NewSourceFromEvent(t.Context(), pullRequestEvent("deadbeef"), fake.client(t), nil)
	var ghErr *github.ErrorResponse
	if !errors.As(err, &ghErr) {
		t.Fatalf("err = %v, want the wrapped *github.ErrorResponse", err)
	}
}
