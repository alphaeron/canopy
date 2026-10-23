package ghplatform

import (
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/server"
)

func TestGitHubAllFilesListsBlobsAtBaseTree(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{rulesfile.FileName: "", "src/api.go": ""}}
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
	if _, err := src.AllFiles(t.Context()); !errors.Is(err, approval.ErrTreeTruncated) {
		t.Errorf("AllFiles err = %v, want approval.ErrTreeTruncated", err)
	}
	if _, err := src.Evaluator().Fetch.Execute(t.Context()); !errors.Is(err, approval.ErrTreeTruncated) {
		t.Errorf("Fetch err = %v, want approval.ErrTreeTruncated", err)
	}
	state := (&review.Engine{Source: src, Log: testLogger()}).Review(t.Context(), []string{"a.go"}, approval.ReviewInput{})
	if state.Overall != approval.StatusError || !errors.Is(state.Err, approval.ErrTreeTruncated) {
		t.Errorf("review = %s, %v; want an error wrapping approval.ErrTreeTruncated", state.Overall, state.Err)
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
	once, ok := src.Evaluator().Fetch.(*approval.OnceFetcher)
	if !ok {
		t.Fatalf("fetcher = %T, want *approval.OnceFetcher", src.Evaluator().Fetch)
	}
	fetch, ok := once.Fetcher.(*approval.TreeFetcher)
	if !ok {
		t.Fatalf("once fetcher's fetcher = %T, want *approval.TreeFetcher", once.Fetcher)
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

// TestGitHubLargePullRequestIsListedByTrees: a pull request with more
// files than GitHub lists, by the event's count or a listing cut short, is
// listed by comparing the head tree with the merge base's, as completely as
// GitHub's listing would be, and is warned of as large; one GitHub lists
// whole is listed the usual way, without a warning.
func TestGitHubLargePullRequestIsListedByTrees(t *testing.T) {
	page := func(n int) []*github.CommitFile {
		files := make([]*github.CommitFile, n)
		for i := range files {
			files[i] = &github.CommitFile{Filename: new(string(rune('a'+i%26)) + string(rune('0'+i/26%10))), Status: new("added"), Patch: new("+\n")}
		}
		return files
	}
	trees := map[string]map[string]string{
		"mb":   {"a.go": "1", "b.go": "1", "old/x.go": "x"},
		"head": {"a.go": "2", "b.go": "1", "new/x.go": "x", "c.go": "c"},
	}
	byTrees := []string{"a.go", "c.go", "new/x.go", "old/x.go"}
	tests := []struct {
		name     string
		reported int
		pages    [][]*github.CommitFile
		want     []string
		listed   bool // the pull request's file listing was read
		compared bool // the trees were compared instead
	}{
		{name: "more files reported than GitHub lists", reported: maxListedFiles + 1, want: byTrees, compared: true},
		{name: "listing short of the reported count", reported: 5, pages: [][]*github.CommitFile{page(3)}, want: byTrees, listed: true, compared: true},
		{name: "listing matches the reported count", reported: 3, pages: [][]*github.CommitFile{page(3)}, want: []string{"a0", "b0", "c0"}, listed: true},
		{name: "count unknown, under the cap", pages: [][]*github.CommitFile{page(3)}, want: []string{"a0", "b0", "c0"}, listed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeGitHub{prPages: tt.pages, trees: trees, mergeBase: "mb"}
			pr := testPR()
			pr.changedFiles = tt.reported
			src := newSource(pr, fake.client(t), nil, server.CacheScope{}, testLogger())
			got, err := src.ChangedFiles(t.Context())
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ChangedFiles = %v, %v; want %v", got, err, tt.want)
			}
			if listed := fake.callCount("files") > 0; listed != tt.listed {
				t.Errorf("listed the files: %v, want %v", listed, tt.listed)
			}
			if compared := fake.callCount("compare") > 0; compared != tt.compared {
				t.Errorf("compared the trees: %v, want %v", compared, tt.compared)
			}
			warnings := src.Warnings()
			if tt.compared && !reflect.DeepEqual(warnings, []string{"large pull request (4 files)"}) || !tt.compared && len(warnings) > 0 {
				t.Errorf("warnings = %q, want a large pull request warning only when the trees were compared", warnings)
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

// TestGitHubReadHeadOfAnOversizedFile: a proposed file over the rules file
// limit, which GitHub serves without content past 1 MB, is reported as too
// large rather than as a read failure.
func TestGitHubReadHeadOfAnOversizedFile(t *testing.T) {
	for _, size := range []int{rulesfile.MaxSize + 1, 2_000_000} {
		fake := &fakeGitHub{head: map[string]string{rulesfile.FileName: strings.Repeat("#", size)}}
		if _, err := testSource(fake.client(t)).ReadHead(t.Context(), rulesfile.FileName); !errors.Is(err, rulesfile.ErrTooLarge) {
			t.Errorf("ReadHead of %d bytes: err = %v, want rulesfile.ErrTooLarge", size, err)
		}
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
	if err := testSource(fake.client(t)).Report(t.Context(), approval.Outcome{Verdict: approval.Passed, Summary: "ok"}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(fake.checkRuns) != 1 {
		t.Fatalf("check runs = %d, want 1", len(fake.checkRuns))
	}
	run := fake.checkRuns[0]
	if run.HeadSHA != "head" || run.Name != checkName || run.GetStatus() != "completed" || run.GetConclusion() != "success" || run.CompletedAt == nil {
		t.Errorf("check run = %+v, want a completed success named %s on head", run, checkName)
	}
	if title := run.GetOutput().GetTitle(); title != "ok" {
		t.Errorf("title = %q, want the outcome's description", title)
	}
}

// TestGitHubKeyIsTheReportedRevision: the key names the repository, as
// GitHub matches it, and the head revision.
func TestGitHubKeyIsTheReportedRevision(t *testing.T) {
	pr := testPR()
	pr.owner, pr.repo = "Org", "Repo"
	src := newSource(pr, nil, nil, server.CacheScope{}, testLogger())
	if got, want := src.Key(), "org/repo@"+pr.headSHA; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
}

// TestCheckSummary: the summary lists every item as code and every
// warning, as many as fit in the limit.
func TestCheckSummary(t *testing.T) {
	tests := []struct {
		name  string
		o     approval.Outcome
		limit int
		want  string
	}{
		{
			name:  "summary alone",
			o:     approval.Outcome{Summary: "1 file approved"},
			limit: maxCheckSummary,
			want:  "**1 file approved**\n",
		},
		{
			name: "items and warnings",
			o: approval.Outcome{Summary: "2 of 3 files await approval", Items: []string{"alice", "org/backend ×2"},
				Warnings: []string{"large pull request (4012 files)"}},
			limit: maxCheckSummary,
			want: "**2 of 3 files await approval**\n\n- `alice`\n- `org/backend ×2`\n" +
				"\n**Warnings**\n\n- large pull request (4012 files)\n",
		},
		{
			name:  "an item cannot end its code span or its line",
			o:     approval.Outcome{Summary: "1 invalid proposed rules file", Items: []string{"a`[x](y)`\nb/KIT"}},
			limit: maxCheckSummary,
			want:  "**1 invalid proposed rules file**\n\n- `a'[x](y)' b/KIT`\n",
		},
		{
			name:  "items past the limit are counted",
			o:     approval.Outcome{Summary: "s", Items: []string{"a", "b", "c"}},
			limit: len("**s**\n\n- `a`\n") + len("- … and 1000000 more\n"),
			want:  "**s**\n\n- `a`\n- … and 2 more\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkSummary(tt.o, tt.limit)
			if got != tt.want {
				t.Errorf("checkSummary = %q, want %q", got, tt.want)
			}
			if len(got) > tt.limit {
				t.Errorf("checkSummary is %d bytes, over the %d limit", len(got), tt.limit)
			}
		})
	}
	long := checkSummary(approval.Outcome{Summary: strings.Repeat("ü", 100_000), Items: make([]string, 100_000)}, maxCheckSummary)
	if len(long) > maxCheckSummary {
		t.Errorf("checkSummary of a huge outcome is %d bytes, over GitHub's limit", len(long))
	}
}
