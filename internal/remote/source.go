package remote

import (
	"context"
	"log/slog"
	"sort"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/match"
)

// PullRequest is the webhook payload's identity for one pull request: enough
// information to fetch the base tree and its rules, list the files, and read
// each file's diff. A zero value is not usable, it must be populated from the
// webhook JSON before being handed to NewSource.
type PullRequest struct {
	// Owner is the repository owner login.
	Owner string
	// Repo is the repository name (slug).
	Repo string
	// BaseSHA is the pull request's base revision, the tree to evaluate rules
	// against: `pull_request.base.sha`.
	BaseSHA string
	// Files is the pull request's changed file set with each file's patch.
	// This comes directly from the webhook payload — it is what a PR review
	// needs to know, without an extra API call.
	Files []PRFile
}

// PRFile is one changed file: the path and its patch (unified diff string as
// GitHub delivers it). The patch is the same bytes used to build the diff
// view in the PR UI.
type PRFile struct {
	// Path is the repository-relative path of the changed file.
	Path string
	// Patch is the unified diff for the file, as delivered by GitHub's
	// webhook. Empty for binary files or when the payload omits it.
	Patch string
}

// Source implements match.Source against a remote repository. It reads the
// rules and resolves approvers through the GitHub review platform
// (NewReviewer, over the base tree of the pull request), and reads the file
// universe and per-file diffs from the pull request's payload. No
// additional API calls are needed for the changed file list or the per-file
// patch — only the AllFiles (base tree) call requires the GitHub client.
type Source struct {
	PR     *PullRequest
	Client *github.Client // lists the base tree, and backs the remote platform
	Log    *slog.Logger

	reviewer *approval.Reviewer
}

// NewSource builds a Source over a pull request, on the GitHub
// review platform bound to the same owner, repository, and base revision.
// log may be nil (defaults to slog.Default()).
func NewSource(pr *PullRequest, client *github.Client, log *slog.Logger) *Source {
	return &Source{
		PR:       pr,
		Client:   client,
		Log:      log,
		reviewer: NewReviewer(client, pr.Owner, pr.Repo, pr.BaseSHA, log),
	}
}

// Reviewer is the GitHub review platform for the pull request: KIT files
// from its base tree, and team targets resolved through the GitHub API.
func (r *Source) Reviewer() *approval.Reviewer { return r.reviewer }

// RepoRoot is empty: a remote source has no on-disk repository.
func (r *Source) RepoRoot() string { return "" }

// AllFiles lists the pull request's base tree, i.e. the complete file set
// that the rules will match against when the PR is merged. This is the
// remote equivalent of AllFiles over the worktree: every file that could be
// affected, not just the ones the PR happens to touch.
func (r *Source) AllFiles(ctx context.Context) ([]string, error) {
	tree, _, err := r.Client.Git.GetTree(ctx, r.PR.Owner, r.PR.Repo, r.PR.BaseSHA, true)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range tree.Entries {
		if entry.GetType() == "blob" {
			files = append(files, entry.GetPath())
		}
	}
	sort.Strings(files)
	return files, nil
}

// ChangedFiles returns the PR's modified file set, in sorted order. It reads
// from the payload, not from the API, so it costs zero API round-trips:
// GitHub already delivered these to us via the webhook.
func (r *Source) ChangedFiles(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	for _, f := range r.PR.Files {
		seen[f.Path] = true
	}
	var files []string
	for p := range seen {
		files = append(files, p)
	}
	sort.Strings(files)
	return files, nil
}

// DiffContent returns the patch the webhook payload carries for a single
// file. An empty string is returned when the file has no patch in the payload
// (binary files, or a change the webhook did not report).
func (r *Source) DiffContent(ctx context.Context, path string) (string, error) {
	for _, f := range r.PR.Files {
		if f.Path == path {
			return f.Patch, nil
		}
	}
	return "", nil
}

var _ match.Source = (*Source)(nil)
