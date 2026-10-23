package ghplatform

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/server"
)

// TestGitHubFetchReadsBaseRulesFiles: the evaluator fetches the rules files
// at the base revision and reads no other blob.
func TestGitHubFetchReadsBaseRulesFiles(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{
		rulesfile.FileName: `- ".*\\.go$":
  name: "Backend"
  combine_strategy: union
  approvers:
    - target: "org/backend-team"
      required_count: 1
`,
		"readme.md": "# Readme\n",
	}}
	rules, err := newEvaluator(newBaseTree(fake.client(t), "o", "r", "abc123", server.CacheScope{}), nil, testLogger()).Fetch.Execute(t.Context())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := rules[rulesfile.FileName]; len(rules) != 1 || len(got) != 1 || got[0].Name != "Backend" {
		t.Errorf("rules = %+v, want the one Backend rule", rules)
	}
	if got := fake.callCount("blob"); got != 1 {
		t.Errorf("blob reads = %d, want 1 (only the rules file)", got)
	}
}

// TestGitHubTreeListingErrorSurfacesAndRetries: a failed listing is
// reported with GitHub's error, and is not cached, so the next call lists
// again.
func TestGitHubTreeListingErrorSurfacesAndRetries(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{"a.go": ""}, fail: true}
	tree := newBaseTree(fake.client(t), "o", "r", "abc123", server.CacheScope{})

	_, err := tree.Files(t.Context())
	var ghErr *github.ErrorResponse
	if !errors.As(err, &ghErr) {
		t.Fatalf("Files err = %v, want the wrapped *github.ErrorResponse", err)
	}
	fake.setFail(false)
	files, err := tree.Files(t.Context())
	if err != nil || len(files) != 1 {
		t.Errorf("Files after recovery = %v, %v; want [a.go]", files, err)
	}
}

// TestGitHubFetchRunsOnce: a review's evaluator fetches once however many
// times it is asked, so the server's early fetch and the review's share one.
func TestGitHubFetchRunsOnce(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{rulesfile.FileName: "- \".*\":\n  name: \"Root\"\n"}}
	ev := newEvaluator(newBaseTree(fake.client(t), "o", "r", "abc123", server.CacheScope{}), nil, testLogger())
	for range 2 {
		if _, err := ev.Fetch.Execute(t.Context()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	if got := fake.callCount("tree") + fake.callCount("blob"); got != 2 {
		t.Errorf("tree and blob calls = %d, want 2 (one listing, one read)", got)
	}
}

// TestGitHubCacheSharesTreesAndBlobsAcrossReviews: reviews of one
// installation list a base revision once, and read a blob once even at
// another revision; another installation shares none of it.
func TestGitHubCacheSharesTreesAndBlobsAcrossReviews(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{rulesfile.FileName: "- \".*\":\n  name: \"Root\"\n"}}
	client, repo := fake.client(t), server.NewRepoCache()
	fetch := func(installation int64, sha string) {
		t.Helper()
		tree := newBaseTree(client, "o", "r", sha, repo.Scope(installationScope(installation)))
		if _, err := newEvaluator(tree, nil, testLogger()).Fetch.Execute(t.Context()); err != nil {
			t.Fatalf("Execute at %s: %v", sha, err)
		}
	}
	calls := func(wantTree, wantBlob int, when string) {
		t.Helper()
		if tree, blob := fake.callCount("tree"), fake.callCount("blob"); tree != wantTree || blob != wantBlob {
			t.Errorf("%s: tree listings = %d, blob reads = %d; want %d and %d", when, tree, blob, wantTree, wantBlob)
		}
	}

	fetch(1, "abc123")
	fetch(1, "abc123")
	calls(1, 1, "same revision")
	fetch(1, "def456") // the fake serves the same blobs at every revision
	calls(2, 1, "new revision")
	fetch(2, "abc123")
	calls(3, 2, "another installation")
}

// TestGitHubTreeTooLargeForOneListingIsListedBySubtree: when GitHub
// truncates the whole-tree listing, the tree is listed by its subtrees,
// with one more request per subtree, and nothing is lost.
func TestGitHubTreeTooLargeForOneListingIsListedBySubtree(t *testing.T) {
	fake := &fakeGitHub{maxListed: 3, base: map[string]string{
		"a.txt": "a", "src/x.go": "x", "src/y.go": "y", "docs/" + rulesfile.FileName: "", "docs/z.md": "z",
	}}
	files, err := newBaseTree(fake.client(t), "o", "r", "abc123", server.CacheScope{}).Files(t.Context())
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []string{"a.txt", "docs/" + rulesfile.FileName, "docs/z.md", "src/x.go", "src/y.go"}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("Files = %v, want %v", files, want)
	}
	// The whole tree, the root's own entries, then docs and src.
	if got := fake.callCount("tree"); got != 4 {
		t.Errorf("tree requests = %d, want 4", got)
	}
}

// TestGitHubOversizedRulesFileIsNotDownloaded: a rules file the listing
// sizes over rulesfile.MaxSize fails the fetch as too large, and its blob
// is never requested.
func TestGitHubOversizedRulesFileIsNotDownloaded(t *testing.T) {
	fake := &fakeGitHub{base: map[string]string{rulesfile.FileName: strings.Repeat("#", rulesfile.MaxSize+1)}}
	_, err := newEvaluator(newBaseTree(fake.client(t), "o", "r", "abc123", server.CacheScope{}), nil, testLogger()).Fetch.Execute(t.Context())
	if !errors.Is(err, rulesfile.ErrTooLarge) {
		t.Errorf("Fetch err = %v, want rulesfile.ErrTooLarge", err)
	}
	if got := fake.callCount("blob"); got != 0 {
		t.Errorf("blob reads = %d, want 0", got)
	}
}
