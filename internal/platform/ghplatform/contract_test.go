package ghplatform

import (
	"testing"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/platform/platformtest"
	"github.com/alphaeron/kit/internal/review"
)

// TestGitHubMeetsTheSourceContract runs the platform contract against a fake
// GitHub serving the fixture's base tree and a pull request making its
// change, built the way the server builds it: from a pull_request event.
func TestGitHubMeetsTheSourceContract(t *testing.T) {
	platformtest.RunSourceContract(t, func(t *testing.T, f platformtest.Fixture) review.Source {
		src, err := NewSourceFromEvent(t.Context(), pullRequestEvent("base"), fakeFromFixture(f).client(t), testLogger())
		if err != nil {
			t.Fatalf("NewSourceFromEvent: %v", err)
		}
		return src
	})
}

func TestGitHubBaseTreeMeetsTheTreeContract(t *testing.T) {
	platformtest.RunTreeContract(t, func(t *testing.T, files map[string]string) approval.Tree {
		return newBaseTree((&fakeGitHub{base: files}).client(t), "o", "r", "base")
	})
}

func TestGitHubResolverMeetsTheResolverContract(t *testing.T) {
	platformtest.RunResolverContract(t, func(t *testing.T, members []string) platformtest.ResolverHarness {
		fake := &fakeGitHub{teams: map[string][]string{"acme/backend": members}}
		return platformtest.ResolverHarness{
			Resolver:     NewApproverResolver(fake.client(t)),
			Group:        "acme/backend",
			UnknownGroup: "acme/ghosts",
			Invalid:      "acme/../admin",
			Calls:        func() int { return fake.callCount("teams") },
		}
	})
}
