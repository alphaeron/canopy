package ghplatform

import (
	"errors"
	"testing"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// TestGitHubFetchReadsBaseRulesFiles: NewReviewer fetches the rules files
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
	rules, err := NewReviewer(fake.client(t), "o", "r", "abc123", testLogger()).Fetch.Execute(t.Context())
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
	tree := newBaseTree(fake.client(t), "o", "r", "abc123")

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
