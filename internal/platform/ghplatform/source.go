package ghplatform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/match"
)

// pullRequest is what a Source needs to know about one pull request: enough
// to fetch the base tree and its rules, list the changed files, and read each
// file's diff. It is built by NewSourceFromEvent, never by callers, so
// GitHub's shapes stay inside this package.
type pullRequest struct {
	owner string
	repo  string
	// baseSHA is the pull request's base revision, the tree to evaluate
	// rules against: `pull_request.base.sha`.
	baseSHA string
	// files is the pull request's changed file set with each file's patch.
	files []prFile
}

// prFile is one changed file: its path and its patch, the unified diff
// GitHub reports for it. The patch is empty for binary files and for diffs
// too large for GitHub to include.
type prFile struct {
	path string
	// previousPath is the path a renamed file moved from, empty otherwise.
	// The rename removes the file from there, so it is a changed path too.
	previousPath string
	patch        string
}

// Source implements match.Source against a GitHub pull request. It reads the
// rules and resolves approvers through the GitHub review platform, lists
// the file universe from the pull request's base tree (one listing, shared
// with the rules fetch), and reads the changed files and their patches as
// listed when the Source was built. It has no on-disk checkout, so it does
// not implement match.Rooted.
type Source struct {
	pr       *pullRequest
	tree     *baseTree // the base revision; the reviewer's fetch shares it
	reviewer *approval.Reviewer
}

// NewSourceFromEvent converts a pull_request webhook event into a Source on
// the GitHub review platform, bound to the event's repository and base
// revision. The webhook does not carry the changed files, so they are listed
// here through client, once; the Source then serves ChangedFiles and
// DiffContent without further API calls.
//
// The caller validates the webhook signature and parses the event (for
// example with github.ValidatePayload and github.ParseWebHook); an event
// missing the repository, pull request number, or base revision is an error.
// log may be nil (defaults to slog.Default()).
func NewSourceFromEvent(ctx context.Context, event *github.PullRequestEvent, client *github.Client, log *slog.Logger) (*Source, error) {
	pr, number, err := pullRequestFromEvent(event)
	if err != nil {
		return nil, err
	}
	pr.files, err = listFiles(ctx, client, pr.owner, pr.repo, number)
	if err != nil {
		return nil, err
	}
	return newSource(pr, client, log), nil
}

// newSource builds a Source over pr, on the GitHub review platform bound to
// the same owner, repository, and base revision.
func newSource(pr *pullRequest, client *github.Client, log *slog.Logger) *Source {
	tree := newBaseTree(client, pr.owner, pr.repo, pr.baseSHA)
	return &Source{
		pr:       pr,
		tree:     tree,
		reviewer: newReviewer(tree, client, log),
	}
}

// pullRequestFromEvent reads the pull request's identity from a webhook
// event: its repository, base revision, and number.
func pullRequestFromEvent(event *github.PullRequestEvent) (*pullRequest, int, error) {
	if event == nil || event.PullRequest == nil {
		return nil, 0, errors.New("pull_request event has no pull request")
	}
	pr := &pullRequest{
		owner:   event.GetRepo().GetOwner().GetLogin(),
		repo:    event.GetRepo().GetName(),
		baseSHA: event.GetPullRequest().GetBase().GetSHA(),
	}
	number := event.GetPullRequest().GetNumber()
	switch {
	case pr.owner == "" || pr.repo == "":
		return nil, 0, errors.New("pull_request event has no repository owner or name")
	case pr.baseSHA == "":
		return nil, 0, errors.New("pull_request event has no base revision")
	case number <= 0:
		return nil, 0, errors.New("pull_request event has no pull request number")
	}
	return pr, number, nil
}

// listFiles lists every file a pull request changes, with its patch.
func listFiles(ctx context.Context, client *github.Client, owner, repo string, number int) ([]prFile, error) {
	opts := &github.ListOptions{PerPage: perPage}
	page, err := paginate(opts, func() ([]*github.CommitFile, *github.Response, error) {
		return client.PullRequests.ListFiles(ctx, owner, repo, number, opts)
	})
	if err != nil {
		return nil, fmt.Errorf("list files of %s/%s#%d: %w", owner, repo, number, err)
	}
	files := make([]prFile, 0, len(page))
	for _, f := range page {
		files = append(files, prFile{
			path:         f.GetFilename(),
			previousPath: f.GetPreviousFilename(),
			patch:        f.GetPatch(),
		})
	}
	return files, nil
}

// Reviewer is the GitHub review platform for the pull request: rules files
// from its base tree, and team targets resolved through the GitHub API.
func (r *Source) Reviewer() *approval.Reviewer { return r.reviewer }

// AllFiles lists the pull request's base tree, i.e. the complete file set
// that the rules will match against when the PR is merged. This is the
// GitHub equivalent of AllFiles over the worktree: every file that could be
// affected, not just the ones the PR happens to touch.
func (r *Source) AllFiles(ctx context.Context) ([]string, error) {
	return r.tree.Files(ctx)
}

// ChangedFiles returns the PR's changed file set, in sorted order. A renamed
// file contributes both its new path and the path it moved from, so the rules
// over the directory it left still apply. It reads the files listed when the
// Source was built, so it makes no API calls.
func (r *Source) ChangedFiles(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	for _, f := range r.pr.files {
		seen[f.path] = true
		if f.previousPath != "" {
			seen[f.previousPath] = true
		}
	}
	var files []string
	for p := range seen {
		files = append(files, p)
	}
	sort.Strings(files)
	return files, nil
}

// DiffContent returns the patch GitHub reported for a single file. An empty
// string is returned when the file has no patch (binary files, diffs too
// large for GitHub to include, the path a renamed file moved from, or a file
// the pull request does not change).
func (r *Source) DiffContent(ctx context.Context, path string) (string, error) {
	for _, f := range r.pr.files {
		if f.path == path {
			return f.patch, nil
		}
	}
	return "", nil
}

var _ match.Source = (*Source)(nil)
