package kenin

import (
	"context"
	"log/slog"
	"sort"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
)

// PullRequest is the webhook payload's identity for one pull request: enough
// information to fetch the base tree and its rules, list the files, and read
// each file's diff. A zero value is not usable, it must be populated from the
// webhook JSON before being handed to NewRemoteSource.
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

// RemoteSource implements Source against a remote repository. It reads the
// rules from a shonin RemoteFetcher (over the base tree of the pull
// request), and reads the file universe and per-file diffs from the pull
// request's payload. No additional API calls are needed for the changed file
// list or the per-file patch — only the AllFiles (base tree) call requires
// the GitHub client.
type RemoteSource struct {
	PR     *PullRequest
	Fetch  shonin.Fetcher // a *shonin.RemoteFetcher bound to the same owner/repo/baseSHA
	Client *github.Client // used only for AllFiles (listing the base tree)
	Log    *slog.Logger
}

// NewRemoteSource builds a RemoteSource over a pull request and a rule
// fetcher. log may be nil (defaults to slog.Default()).
func NewRemoteSource(pr *PullRequest, fetch shonin.Fetcher, client *github.Client, log *slog.Logger) *RemoteSource {
	return &RemoteSource{PR: pr, Fetch: fetch, Client: client, Log: log}
}

// RepoRoot is empty: a remote source has no on-disk repository.
func (r *RemoteSource) RepoRoot() string { return "" }

// KitFiles delegates to the RemoteFetcher, which reads rules at the pull
// request's base tree.
func (r *RemoteSource) KitFiles() (map[string]kitfile.Rules, error) {
	_, rules, err := r.Fetch.Execute()
	return rules, err
}

// AllFiles lists the pull request's base tree, i.e. the complete file set
// that the rules will match against when the PR is merged. This is the
// remote equivalent of AllFiles over the worktree: every file that could be
// affected, not just the ones the PR happens to touch.
func (r *RemoteSource) AllFiles() ([]string, error) {
	ctx := context.Background()
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
func (r *RemoteSource) ChangedFiles() ([]string, error) {
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
func (r *RemoteSource) DiffContent(path string) (string, error) {
	for _, f := range r.PR.Files {
		if f.Path == path {
			return f.Patch, nil
		}
	}
	return "", nil
}

var _ Source = (*RemoteSource)(nil)
