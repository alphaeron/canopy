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
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/logx"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/server"
)

// maxComparedFiles is the most files GitHub's comparison of two commits
// lists; a comparison listing this many may have been cut short.
const maxComparedFiles = 300

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
	// headSHA is the pull request's head revision: check runs are reported
	// on it, and the rules files it proposes are read from it.
	headSHA string
	// author is the pull request author's login.
	author string
	// action is the webhook event and action that requested the review,
	// e.g. "pull_request.synchronize", for the server's record.
	action string
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
// patches once, on first use. Its rules fetch runs once. It has no on-disk
// checkout, so it does not implement review.Rooted. It is safe for
// concurrent use.
type Source struct {
	pr        *pullRequest
	client    *github.Client
	tree      *baseTree // the base revision; the evaluator's fetch shares it
	resolver  *ApproverResolver
	evaluator *approval.Evaluator
	log       *slog.Logger

	mu       sync.Mutex
	files    *changes // nil until listed
	warnings []string // see Warnings
}

// changes is a pull request's listed changes, indexed for its readers.
type changes struct {
	// paths is the changed file set, sorted: each file's path and, for a
	// renamed file, the path it moved from.
	paths []string
	// patches maps each listed file's path to its patch.
	patches map[string]string
}

// newChanges indexes a listing of changed files.
func newChanges(files []prFile) *changes {
	c := &changes{patches: make(map[string]string, len(files))}
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		c.patches[f.path] = f.patch
		seen[f.path] = true
		if f.previousPath != "" {
			seen[f.previousPath] = true
		}
	}
	c.paths = slices.Sorted(maps.Keys(seen))
	return c
}

// newSource builds a Source over pr, on the GitHub review platform bound to
// the same owner, repository, and base revision, resolving approvers with
// resolver. cache shares base tree listings and blobs with the
// installation's other reviews.
func newSource(pr *pullRequest, client *github.Client, resolver *ApproverResolver, cache server.CacheScope, log *slog.Logger) *Source {
	tree := newBaseTree(client, pr.owner, pr.repo, pr.baseSHA, cache)
	s := &Source{
		pr:       pr,
		client:   client,
		tree:     tree,
		resolver: resolver,
		log:      log,
	}
	s.evaluator = newEvaluator(&applicableRules{tree: tree, changed: s.ChangedFiles}, resolver, log)
	return s
}

// pullRequestFrom reads a pull request's identity from a webhook event's
// repository and pull request. Every field is checked, since the event is
// untrusted input.
func pullRequestFrom(repo *github.Repository, gh *github.PullRequest) (*pullRequest, error) {
	if gh == nil {
		return nil, errors.New("no pull request")
	}
	pr := &pullRequest{
		owner:   repo.GetOwner().GetLogin(),
		repo:    repo.GetName(),
		number:  gh.GetNumber(),
		baseSHA: gh.GetBase().GetSHA(),
		headSHA: gh.GetHead().GetSHA(),
		author:  gh.GetUser().GetLogin(),
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
// first use. The files are listed only through requests addressed by the
// base and head revisions, never by the pull request's number: GitHub
// answers a request by number for the pull request's head when it is
// answered, which may already be a newer push than the head this review
// reports on, so its files would be judged as another commit's.
//
// GitHub's comparison of the two revisions lists the files, with patches,
// unless it lists maxComparedFiles, the most it lists, when the change is
// listed by comparing trees instead (changedByTrees), without patches, and
// warned of as large. A comparison listing no files is confirmed by
// comparing trees too, so a listing GitHub left out can never pass for an
// empty change. A failure is not kept, so a later call retries. The lock
// is held across the listing so concurrent first uses share one.
func (r *Source) listedFiles(ctx context.Context) (*changes, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.files != nil {
		return r.files, nil
	}
	// One commit per page: only the files and the merge base are read,
	// and GitHub lists the files on the first page.
	cmp, _, err := r.client.Repositories.CompareCommits(ctx, r.pr.owner, r.pr.repo, r.pr.baseSHA, r.pr.headSHA, &github.ListOptions{PerPage: 1})
	if err != nil {
		return nil, fmt.Errorf("compare %s with its base: %w", r.pr, err)
	}
	if n := len(cmp.Files); n > 0 && n < maxComparedFiles {
		files := make([]prFile, 0, n)
		for _, f := range cmp.Files {
			files = append(files, prFile{path: f.GetFilename(), previousPath: f.GetPreviousFilename(), patch: f.GetPatch()})
		}
		r.files = newChanges(files)
		return r.files, nil
	}
	mergeBase := cmp.GetMergeBaseCommit().GetSHA()
	if mergeBase == "" {
		return nil, fmt.Errorf("find the merge base of %s: none reported", r.pr)
	}
	files, err := r.changedByTrees(ctx, mergeBase)
	if err != nil {
		return nil, err
	}
	if len(cmp.Files) > 0 {
		r.warnings = append(r.warnings, fmt.Sprintf("large pull request (%d files)", len(files)))
	}
	r.files = newChanges(files)
	return r.files, nil
}

// changedByTrees lists the files the pull request changes as GitHub's
// comparison does, without its limit: every path whose blob differs between
// mergeBase, the merge base of the base and head revisions, and the head,
// or that only one of them has. A rename is the path it left and the path
// it arrived at. It has no patches. It costs the two tree listings (see
// listBlobs), the merge base's often the base's own.
func (r *Source) changedByTrees(ctx context.Context, mergeBase string) ([]prFile, error) {
	baseTree := r.tree
	if mergeBase != r.pr.baseSHA {
		baseTree = newBaseTree(r.client, r.pr.owner, r.pr.repo, mergeBase, r.tree.cache)
	}
	headTree := newBaseTree(r.client, r.pr.owner, r.pr.repo, r.pr.headSHA, r.tree.cache)
	var (
		wg              sync.WaitGroup
		before, after   map[string]server.TreeBlob
		beforeE, afterE error
	)
	wg.Go(func() { before, beforeE = baseTree.list(ctx) })
	wg.Go(func() { after, afterE = headTree.list(ctx) })
	wg.Wait()
	if err := errors.Join(beforeE, afterE); err != nil {
		return nil, fmt.Errorf("compare the trees of %s: %w", r.pr, err)
	}
	var files []prFile
	for path, b := range before {
		if a, ok := after[path]; !ok || a.SHA != b.SHA {
			files = append(files, prFile{path: path})
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			files = append(files, prFile{path: path})
		}
	}
	return files, nil
}

// Warnings lists what reading the pull request found worth reporting with
// its outcome: a pull request too large for GitHub's file listing.
func (r *Source) Warnings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.warnings)
}

// Evaluator is the GitHub review platform for the pull request: rules files
// from its base tree, and team targets resolved through the GitHub API.
func (r *Source) Evaluator() *approval.Evaluator { return r.evaluator }

// AllFiles lists the pull request's base tree, i.e. the complete file set
// that the rules will match against when the PR is merged. This is the
// GitHub equivalent of AllFiles over the worktree: every file that could be
// affected, not just the ones the PR happens to touch. A truncated tree
// listing is approval.ErrTreeTruncated.
func (r *Source) AllFiles(ctx context.Context) ([]string, error) {
	return r.tree.Files(ctx)
}

// ChangedFiles returns the PR's changed file set, in sorted order. A renamed
// file contributes both its new path and the path it moved from, so the rules
// over the directory it left still apply. A pull request with more files
// than GitHub lists is listed by comparing trees (see listedFiles).
func (r *Source) ChangedFiles(ctx context.Context) ([]string, error) {
	c, err := r.listedFiles(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Clone(c.paths), nil
}

// DiffContent returns the patch GitHub reported for a single file. An empty
// string is returned when the file has no patch (binary files, diffs too
// large for GitHub to include, the path a renamed file moved from, or a file
// the pull request does not change).
func (r *Source) DiffContent(ctx context.Context, path string) (string, error) {
	c, err := r.listedFiles(ctx)
	if err != nil {
		return "", err
	}
	return c.patches[path], nil
}

// ReadHead reads path at the pull request's head revision: the rules file
// as the pull request proposes it. A file over rulesfile.MaxSize is
// rulesfile.ErrTooLarge.
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
	// GitHub omits the content of a file over 1 MB but still reports its
	// size, so an oversized rules file is reported as one, not as a read
	// failure.
	if file.GetSize() > rulesfile.MaxSize {
		return nil, fmt.Errorf("read %s at the head of %s: %d bytes: %w", path, r.pr, file.GetSize(), rulesfile.ErrTooLarge)
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
//     author's own reviews are ignored: authoring the pull request is the
//     author's approval (see approval.ReviewInput.Author), which
//     self_approve rules decide whether to count.
//   - IsActive: a user is inactive when GitHub reports their account
//     suspended (suspended_at) or does not know them. Each user is looked
//     up once, through ctx, which must outlive the review. A failed lookup
//     counts the user as inactive: only a user GitHub confirms is active.
//   - Owners: the repository's owner, who reviews the files no rule covers.
//   - Labels: the pull request's labels when the review runs, not as the
//     webhook reported them: GitHub does not order webhooks, so a label
//     removed since must not block the pull request.
//
// The reviews and the labels are listed concurrently.
func (r *Source) ReviewInput(ctx context.Context) (approval.ReviewInput, error) {
	var (
		wg                      sync.WaitGroup
		approvers, labels       []string
		approversErr, labelsErr error
	)
	wg.Go(func() { approvers, approversErr = r.approvers(ctx) })
	wg.Go(func() { labels, labelsErr = r.labels(ctx) })
	wg.Wait()
	if err := errors.Join(approversErr, labelsErr); err != nil {
		return approval.ReviewInput{}, err
	}
	return approval.ReviewInput{
		Author:    r.pr.author,
		Approvers: approvers,
		IsActive:  r.activity(ctx),
		Owners:    []string{r.pr.owner},
		Labels:    labels,
	}, nil
}

// labels lists the pull request's label names, sorted.
func (r *Source) labels(ctx context.Context) ([]string, error) {
	labels, err := collect(r.client.Issues.ListLabelsByIssueIter(ctx, r.pr.owner, r.pr.repo, r.pr.number, &github.ListOptions{PerPage: perPage}))
	if err != nil {
		return nil, fmt.Errorf("list labels of %s: %w", r.pr, err)
	}
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		out = append(out, l.GetName())
	}
	slices.Sort(out)
	return out, nil
}

// approvers lists the reviewers whose latest decisive review approves,
// sorted and in canonical form.
func (r *Source) approvers(ctx context.Context) ([]string, error) {
	reviews, err := collect(r.client.PullRequests.ListReviewsIter(ctx, r.pr.owner, r.pr.repo, r.pr.number, &github.ListOptions{PerPage: perPage}))
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
// as an active (not suspended) account, looked up once per user. Different
// users are looked up concurrently; concurrent first calls for one user
// share its lookup.
func (r *Source) activity(ctx context.Context) func(string) bool {
	var mu sync.Mutex
	lookups := map[string]func() bool{}
	return func(login string) bool {
		mu.Lock()
		lookup, ok := lookups[login]
		if !ok {
			lookup = sync.OnceValue(func() bool { return r.isActive(ctx, login) })
			lookups[login] = lookup
		}
		mu.Unlock()
		return lookup()
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

// checkName is the check run the review reports as. Each start and each
// report creates a new check run under this name on the head commit;
// GitHub shows the latest, and branch protection can require it by this
// name. The server
// reports a pull request's reviews one at a time, newest last (see
// server.PullRequest.Key), so the latest run is the newest review's.
const checkName = brand.Command

// maxCheckTitle bounds a check run's title, which the pull request's checks
// list shows on one line beside checkName.
const maxCheckTitle = 140

// maxCheckSummary is GitHub's limit on a check run's output summary.
const maxCheckSummary = 65535

// checkConclusions maps each verdict to a check run conclusion. Only a
// passed review succeeds; one waiting on approvals requires action (the
// approvals), and a change that fails the review and a review that could
// not produce a result both fail, told apart by the title.
var checkConclusions = map[approval.Verdict]string{
	approval.Passed:  "success",
	approval.Pending: "action_required",
	approval.Failed:  "failure",
	approval.Errored: "failure",
}

// Info is the pull request as the server records it: its installation's
// cache scope, repository (lower-cased, as Key names it), number,
// revisions, author, and the event that requested the review.
func (r *Source) Info() server.PullRequestInfo {
	return server.PullRequestInfo{
		Scope:   r.tree.cache.Name(),
		Repo:    strings.ToLower(r.pr.owner + "/" + r.pr.repo),
		Number:  r.pr.number,
		BaseSHA: r.pr.baseSHA,
		HeadSHA: r.pr.headSHA,
		Author:  r.pr.author,
		Action:  r.pr.action,
	}
}

// Key is the pull request's repository and head revision, where Report
// publishes: owner/repo@sha, the repository lower-cased as GitHub matches
// it.
func (r *Source) Key() string {
	return strings.ToLower(r.pr.owner+"/"+r.pr.repo) + "@" + r.pr.headSHA
}

// Titles of the check runs Queue and Start create.
const (
	queuedTitle  = "Review queued"
	runningTitle = "Review running"
)

// Queue publishes a queued check run named checkName on the pull request's
// head revision, superseding the earlier report until Start or Report
// supersedes it in turn. It links detailsURL when it is not empty.
func (r *Source) Queue(ctx context.Context, detailsURL string) error {
	run := github.CreateCheckRunOptions{
		Name:       checkName,
		HeadSHA:    r.pr.headSHA,
		Status:     new("queued"),
		DetailsURL: optional(detailsURL),
		Output: &github.CheckRunOutput{
			Title:   new(queuedTitle),
			Summary: new("The review is waiting to run" + detailsNote(detailsURL) + "."),
		},
	}
	if _, _, err := r.client.Checks.CreateCheckRun(ctx, r.pr.owner, r.pr.repo, run); err != nil {
		return fmt.Errorf("queue check run on %s: %w", r.pr, err)
	}
	return nil
}

// Start publishes an in-progress check run named checkName on the pull
// request's head revision, superseding the earlier report until Report
// supersedes it in turn. It links detailsURL when it is not empty.
func (r *Source) Start(ctx context.Context, detailsURL string) error {
	run := github.CreateCheckRunOptions{
		Name:       checkName,
		HeadSHA:    r.pr.headSHA,
		Status:     new("in_progress"),
		StartedAt:  &github.Timestamp{Time: time.Now()},
		DetailsURL: optional(detailsURL),
		Output: &github.CheckRunOutput{
			Title:   new(runningTitle),
			Summary: new("The review is running" + detailsNote(detailsURL) + "."),
		},
	}
	if _, _, err := r.client.Checks.CreateCheckRun(ctx, r.pr.owner, r.pr.repo, run); err != nil {
		return fmt.Errorf("start check run on %s: %w", r.pr, err)
	}
	return nil
}

// Report publishes o as a completed check run named checkName on the pull
// request's head revision, superseding the earlier report. The title is
// o.Description fitted to maxCheckTitle; the summary lists the outcome in
// full (see checkSummary). It links detailsURL when it is not empty.
func (r *Source) Report(ctx context.Context, o approval.Outcome, detailsURL string) error {
	conclusion, ok := checkConclusions[o.Verdict]
	if !ok {
		return fmt.Errorf("report on %s: unknown verdict %q", r.pr, o.Verdict)
	}
	run := github.CreateCheckRunOptions{
		Name:        checkName,
		HeadSHA:     r.pr.headSHA,
		Status:      new("completed"),
		Conclusion:  new(conclusion),
		CompletedAt: &github.Timestamp{Time: time.Now()},
		DetailsURL:  optional(detailsURL),
		Output: &github.CheckRunOutput{
			Title:   new(o.Description(maxCheckTitle)),
			Summary: new(checkSummary(o, maxCheckSummary-len(detailsNote(detailsURL))) + detailsNote(detailsURL)),
		},
	}
	if _, _, err := r.client.Checks.CreateCheckRun(ctx, r.pr.owner, r.pr.repo, run); err != nil {
		return fmt.Errorf("report check run on %s: %w", r.pr, err)
	}
	return nil
}

// optional is s, or nil when it is empty: GitHub rejects an empty URL.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// detailsNote is the line a check run's summary ends with to link the
// review page at detailsURL, or "" without one. GitHub shows details_url
// as the check's "Details" link only in some views, so the summary links
// it too.
func detailsNote(detailsURL string) string {
	if detailsURL == "" {
		return ""
	}
	return "\n\n[See the full review](" + detailsURL + ")"
}

// checkSummary is o as a check run's Markdown summary, in at most limit
// bytes: the summary in bold, then every item and every warning, as many
// as fit, each list ending with how many more there are. Items (file paths
// and approver names) come from the pull request, so each is shown as code,
// which Markdown does not interpret.
func checkSummary(o approval.Outcome, limit int) string {
	var b strings.Builder
	// Only a summary is cut, the way Description cuts one too long alone.
	b.WriteString("**" + approval.Outcome{Summary: o.Summary}.Description(limit/2) + "**\n")
	list := func(heading string, items []string, code bool) {
		if len(items) == 0 {
			return
		}
		if heading != "" {
			b.WriteString("\n**" + heading + "**\n")
		}
		b.WriteString("\n")
		for i, item := range items {
			if code {
				item = codeSpan(item)
			}
			line := "- " + item + "\n"
			// Leave room for the line naming the rest.
			if b.Len()+len(line)+len("- … and 1000000 more\n") > limit {
				fmt.Fprintf(&b, "- … and %d more\n", len(items)-i)
				return
			}
			b.WriteString(line)
		}
	}
	list("", o.Items, true)
	list("Warnings", o.Warnings, false)
	return b.String()
}

// codeSpan is s as a Markdown code span on one line. A backtick or line
// break in s would end the span or the list item, so each becomes a
// visible stand-in.
func codeSpan(s string) string {
	return "`" + strings.NewReplacer("`", "'", "\r", " ", "\n", " ").Replace(s) + "`"
}
