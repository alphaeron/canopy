package ghplatform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/brand"
	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/server"
)

// maxListedFiles is the most files GitHub lists for one pull request; a
// pull request with more is listed only partially.
const maxListedFiles = 3000

// pullRequest is what a Source needs to know about one pull request: enough
// to fetch the base tree and its rules, list the changed files, read the
// review input, and report back. It is built from a webhook event, never by
// callers, so GitHub's shapes stay inside this package.
type pullRequest struct {
	owner  string
	repo   string
	number int
	// baseSHA is the pull request's base revision, the tip of its base
	// branch the pull request is compared against: `pull_request.base.sha`.
	// Rules are read from it.
	baseSHA string
	// headSHA is the pull request's head revision: statuses are reported
	// on it, and the rules files it proposes are read from it.
	headSHA string
	// author is the pull request author's login.
	author string
	// changedFiles is the number of files GitHub says the pull request
	// changes, or 0 when the event does not say.
	changedFiles int
}

// String is the pull request's reference, owner/repo#number, used in errors.
func (pr *pullRequest) String() string {
	return fmt.Sprintf("%s/%s#%d", pr.owner, pr.repo, pr.number)
}

// prFile is one changed file as GitHub lists it.
type prFile struct {
	path string
	// previousPath is the path a renamed file moved from, empty otherwise.
	// The rename removes the file from there, so it is a changed path too.
	previousPath string
	// patch is the unified diff GitHub reports, empty for binary files and
	// diffs too large for GitHub to include.
	patch string
}

// Source implements server.PullRequest against a GitHub pull request. It reads
// the rules and resolves approvers through the GitHub review platform,
// lists the file universe from the pull request's base tree (one listing,
// shared with the rules fetch), and lists the changed files and their
// patches once, on first use. It has no on-disk checkout, so it does not
// implement review.Rooted. It is safe for concurrent use.
type Source struct {
	pr        *pullRequest
	client    *github.Client
	tree      *baseTree // the base revision; the evaluator's fetch shares it
	resolver  *ApproverResolver
	evaluator *approval.Evaluator
	log       *slog.Logger

	mu       sync.Mutex
	files    []prFile // nil until listed
	filesErr error    // a listing found incomplete; API failures are not kept
}

// newSource builds a Source over pr, on the GitHub review platform bound to
// the same owner, repository, and base revision, resolving approvers with
// resolver.
func newSource(pr *pullRequest, client *github.Client, resolver *ApproverResolver, log *slog.Logger) *Source {
	tree := newBaseTree(client, pr.owner, pr.repo, pr.baseSHA)
	return &Source{
		pr:        pr,
		client:    client,
		tree:      tree,
		resolver:  resolver,
		evaluator: newEvaluator(tree, resolver, log),
		log:       log,
	}
}

// pullRequestFrom reads a pull request's identity from a webhook event's
// repository and pull request. Every field is checked, since the event is
// untrusted input.
func pullRequestFrom(repo *github.Repository, gh *github.PullRequest) (*pullRequest, error) {
	if gh == nil {
		return nil, errors.New("no pull request")
	}
	pr := &pullRequest{
		owner:        repo.GetOwner().GetLogin(),
		repo:         repo.GetName(),
		number:       gh.GetNumber(),
		baseSHA:      gh.GetBase().GetSHA(),
		headSHA:      gh.GetHead().GetSHA(),
		author:       gh.GetUser().GetLogin(),
		changedFiles: gh.GetChangedFiles(),
	}
	switch {
	case pr.owner == "" || pr.repo == "":
		return nil, errors.New("no repository owner or name")
	case pr.number <= 0:
		return nil, errors.New("no pull request number")
	case pr.baseSHA == "":
		return nil, errors.New("no base revision")
	case pr.headSHA == "":
		return nil, errors.New("no head revision")
	}
	return pr, nil
}

// listedFiles returns the pull request's changed files, listing them on
// first use. A listing GitHub cut short is ErrFileListIncomplete, kept so
// every later call reports it too; an API failure is not kept, so a later
// call retries. The lock is held across the listing so concurrent first
// uses share one.
func (r *Source) listedFiles(ctx context.Context) ([]prFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.files != nil || r.filesErr != nil {
		return r.files, r.filesErr
	}
	files, err := listFiles(ctx, r.client, r.pr)
	if err != nil {
		return nil, err
	}
	if incomplete(r.pr.changedFiles, len(files)) {
		r.filesErr = fmt.Errorf("%s: listed %d of %d changed files: %w",
			r.pr, len(files), max(r.pr.changedFiles, len(files)), review.ErrFileListIncomplete)
		return nil, r.filesErr
	}
	r.files = files
	return files, nil
}

// incomplete reports whether a listing of listed files is short of the
// reported count, or reached GitHub's cap without a count to confirm it.
func incomplete(reported, listed int) bool {
	return listed < reported || (reported == 0 && listed >= maxListedFiles)
}

// listFiles lists every file a pull request changes, with its patch.
func listFiles(ctx context.Context, client *github.Client, pr *pullRequest) ([]prFile, error) {
	opts := &github.ListOptions{PerPage: perPage}
	page, err := paginate(opts, func() ([]*github.CommitFile, *github.Response, error) {
		return client.PullRequests.ListFiles(ctx, pr.owner, pr.repo, pr.number, opts)
	})
	if err != nil {
		return nil, fmt.Errorf("list files of %s: %w", pr, err)
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

// Evaluator is the GitHub review platform for the pull request: rules files
// from its base tree, and team targets resolved through the GitHub API.
func (r *Source) Evaluator() *approval.Evaluator { return r.evaluator }

// AllFiles lists the pull request's base tree, i.e. the complete file set
// that the rules will match against when the PR is merged. This is the
// GitHub equivalent of AllFiles over the worktree: every file that could be
// affected, not just the ones the PR happens to touch. A truncated tree
// listing is review.ErrTreeTruncated.
func (r *Source) AllFiles(ctx context.Context) ([]string, error) {
	return r.tree.Files(ctx)
}

// ChangedFiles returns the PR's changed file set, in sorted order. A renamed
// file contributes both its new path and the path it moved from, so the rules
// over the directory it left still apply. A pull request with more files
// than GitHub lists is review.ErrFileListIncomplete.
func (r *Source) ChangedFiles(ctx context.Context) ([]string, error) {
	files, err := r.listedFiles(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.path] = true
		if f.previousPath != "" {
			seen[f.previousPath] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// DiffContent returns the patch GitHub reported for a single file. An empty
// string is returned when the file has no patch (binary files, diffs too
// large for GitHub to include, the path a renamed file moved from, or a file
// the pull request does not change).
func (r *Source) DiffContent(ctx context.Context, path string) (string, error) {
	files, err := r.listedFiles(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.path == path {
			return f.patch, nil
		}
	}
	return "", nil
}

// ReadHead reads path at the pull request's head revision: the rules file
// as the pull request proposes it.
func (r *Source) ReadHead(ctx context.Context, path string) ([]byte, error) {
	file, _, _, err := r.client.Repositories.GetContents(ctx, r.pr.owner, r.pr.repo, path,
		&github.RepositoryContentGetOptions{Ref: r.pr.headSHA})
	if isNotFound(err) {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
	}
	if err != nil {
		return nil, fmt.Errorf("read %s at the head of %s: %w", path, r.pr, err)
	}
	if file == nil {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
	}
	content, err := file.GetContent()
	if err != nil {
		return nil, fmt.Errorf("decode %s at the head of %s: %w", path, r.pr, err)
	}
	return []byte(content), nil
}

// isNotFound reports whether err is a GitHub 404.
func isNotFound(err error) bool {
	var ghErr *github.ErrorResponse
	return errors.As(err, &ghErr) && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound
}

// logger is the source's logger, or the default one.
func (r *Source) logger() *slog.Logger { return logx.OrDefault(r.log) }

var _ server.PullRequest = (*Source)(nil)

// The review states that decide whether a reviewer approves. Any other
// state (COMMENTED, PENDING) leaves a reviewer's earlier decision standing.
const (
	reviewApproved         = "APPROVED"
	reviewChangesRequested = "CHANGES_REQUESTED"
	reviewDismissed        = "DISMISSED"
)

// ReviewInput reads the pull request's review input from GitHub:
//
//   - Author: the pull request's author.
//   - Approvers: every reviewer whose latest decisive review approves. A
//     later change request or dismissal cancels an earlier approval;
//     comments change nothing. Approvals count whatever commit they were
//     made on, so pushing after an approval does not cancel it. The
//     author's own reviews are ignored: self_approve rules decide whether
//     the author counts.
//   - IsActive: a user is inactive when GitHub reports their account
//     suspended (suspended_at) or does not know them. Each user is looked
//     up once, through ctx, which must outlive the review. A failed lookup
//     counts the user as inactive: only a user GitHub confirms is active.
//   - Owners: the repository's owner, who reviews the files no rule covers.
func (r *Source) ReviewInput(ctx context.Context) (approval.ReviewInput, error) {
	approvers, err := r.approvers(ctx)
	if err != nil {
		return approval.ReviewInput{}, err
	}
	return approval.ReviewInput{
		Author:    r.pr.author,
		Approvers: approvers,
		IsActive:  r.activity(ctx),
		Owners:    []string{r.pr.owner},
	}, nil
}

// approvers lists the reviewers whose latest decisive review approves,
// sorted and in canonical form.
func (r *Source) approvers(ctx context.Context) ([]string, error) {
	opts := &github.ListOptions{PerPage: perPage}
	reviews, err := paginate(opts, func() ([]*github.PullRequestReview, *github.Response, error) {
		return r.client.PullRequests.ListReviews(ctx, r.pr.owner, r.pr.repo, r.pr.number, opts)
	})
	if err != nil {
		return nil, fmt.Errorf("list reviews of %s: %w", r.pr, err)
	}
	canonical := r.resolver.Canonical
	author := canonical(r.pr.author)
	// GitHub lists reviews in the order they were submitted, so the last
	// decisive review per reviewer wins.
	approved := map[string]bool{}
	for _, rev := range reviews {
		login := canonical(rev.GetUser().GetLogin())
		if login == "" || login == author {
			continue
		}
		switch rev.GetState() {
		case reviewApproved:
			approved[login] = true
		case reviewChangesRequested, reviewDismissed:
			approved[login] = false
		}
	}
	out := []string{}
	for _, login := range slices.Sorted(maps.Keys(approved)) {
		if approved[login] {
			out = append(out, login)
		}
	}
	return out, nil
}

// activity is the IsActive of one review: whether GitHub reports each user
// as an active (not suspended) account, looked up once per user.
func (r *Source) activity(ctx context.Context) func(string) bool {
	var mu sync.Mutex
	active := map[string]bool{}
	return func(login string) bool {
		mu.Lock()
		defer mu.Unlock()
		if a, ok := active[login]; ok {
			return a
		}
		a := r.isActive(ctx, login)
		active[login] = a
		return a
	}
}

// isActive looks login up: active only when GitHub reports an account that
// is not suspended. A suspended or unknown user, or one whose lookup fails,
// is inactive.
func (r *Source) isActive(ctx context.Context, login string) bool {
	// Names reach the API path unescaped (see ValidateTarget); anything
	// that is not a login is no user GitHub has.
	if !githubName.MatchString(login) {
		return false
	}
	user, _, err := r.client.Users.Get(ctx, login)
	switch {
	case isNotFound(err):
		return false
	case err != nil:
		r.logger().Warn("review input: user lookup failed; counting the user as inactive",
			"component", "ghplatform", "pull_request", r.pr.String(), "user", login, "error", err)
		return false
	}
	return user.SuspendedAt == nil
}

// statusContext is the commit status context the review reports under. A
// new report replaces the previous one under the same context, and branch
// protection can require it by this name.
const statusContext = brand.Command

// maxStatusDescription is GitHub's limit on a commit status description.
const maxStatusDescription = 140

// statusStates maps each verdict to a commit status state: a passed review
// is a check mark, a change that fails it a cross, a review that could not
// produce a result an error, and one still waiting on approvals pending.
var statusStates = map[review.Verdict]string{
	review.Passed:  "success",
	review.Pending: "pending",
	review.Failed:  "failure",
	review.Errored: "error",
}

// Report publishes o as a commit status on the pull request's head revision
// under statusContext, replacing the earlier report. The description is
// o.Summary, cut to GitHub's 140-character limit.
func (r *Source) Report(ctx context.Context, o review.Outcome) error {
	state, ok := statusStates[o.Verdict]
	if !ok {
		return fmt.Errorf("report on %s: unknown verdict %q", r.pr, o.Verdict)
	}
	status := github.RepoStatus{
		State:       new(state),
		Description: new(truncate(o.Summary, maxStatusDescription, "…")),
		Context:     new(statusContext),
	}
	if _, _, err := r.client.Repositories.CreateStatus(ctx, r.pr.owner, r.pr.repo, r.pr.headSHA, status); err != nil {
		return fmt.Errorf("report status on %s: %w", r.pr, err)
	}
	return nil
}

// truncate cuts s to at most limit bytes, ending it with suffix when cut,
// without splitting a UTF-8 sequence.
func truncate(s string, limit int, suffix string) string {
	if len(s) <= limit {
		return s
	}
	cut := limit - len(suffix)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}
