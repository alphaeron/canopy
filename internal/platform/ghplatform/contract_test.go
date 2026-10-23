package ghplatform

import (
	"testing"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/platform/platformtest"
	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/server"
)

// TestGitHubMeetsTheSourceContract runs the platform contract against a fake
// GitHub serving the fixture's base tree and a pull request making its
// change, built the way the server builds it: from a pull_request event.
func TestGitHubMeetsTheSourceContract(t *testing.T) {
	platformtest.RunSourceContract(t, platformtest.BaseRevision, func(t *testing.T, f platformtest.Fixture) review.Source {
		return changeFromEvent(t, fakeFromFixture(f), pullRequestEvent("base"))
	})
}

func TestGitHubBaseTreeMeetsTheTreeContract(t *testing.T) {
	platformtest.RunTreeContract(t, func(t *testing.T, files map[string]string) approval.Tree {
		return newBaseTree((&fakeGitHub{base: files}).client(t), "o", "r", "base", server.CacheScope{})
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

// reviewStates are GitHub's review states for each contract decision. A
// dismissal is the dismissed approval itself, listed in its place.
var reviewStates = map[platformtest.Decision]string{
	platformtest.Approve:        "APPROVED",
	platformtest.RequestChanges: "CHANGES_REQUESTED",
	platformtest.Dismiss:        "DISMISSED",
	platformtest.Comment:        "COMMENTED",
}

func TestGitHubMeetsTheReviewInputContract(t *testing.T) {
	platformtest.RunReviewInputContract(t, func(t *testing.T, r platformtest.Reviews) server.PullRequest {
		var reviews []*github.PullRequestReview
		for _, rev := range r.Reviews {
			reviews = append(reviews, &github.PullRequestReview{User: &github.User{Login: new(rev.Reviewer)}, State: new(reviewStates[rev.Decision])})
		}
		// Served over two pages, so the latest decision spans pages.
		half := len(reviews) / 2
		fake := &fakeGitHub{reviewPages: [][]*github.PullRequestReview{reviews[:half], reviews[half:]}}
		pr := testPR()
		pr.author = r.Author
		return newSource(pr, fake.client(t), NewApproverResolver(fake.client(t)), server.CacheScope{}, testLogger())
	})
}

func TestGitHubMeetsTheStatusReporterContract(t *testing.T) {
	platformtest.RunStatusReporterContract(t, func(t *testing.T) (server.PullRequest, func() []platformtest.StatusReport) {
		fake := &fakeGitHub{}
		observe := func() []platformtest.StatusReport {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			out := make([]platformtest.StatusReport, len(fake.checkRuns))
			for i, run := range fake.checkRuns {
				out[i] = platformtest.StatusReport{Name: run.Name, State: run.GetStatus() + "/" + run.GetConclusion(), Summary: run.GetOutput().GetTitle(), Link: run.GetDetailsURL()}
			}
			return out
		}
		return testSource(fake.client(t)), observe
	})
}
