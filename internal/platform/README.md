# Review platforms

A **platform** is where a change under review lives: the local working tree, a GitHub pull
request, and in future a GitLab merge request or similar. The review algorithm (`internal/approval`)
and the review engine (`internal/review`) never branch on the platform, and neither does the review
server (`internal/server`). Each platform supplies these through small interfaces, and everything
else is shared:

| What | Interface | Defined in |
|---|---|---|
| The files under review and their diffs | `review.Source` | `internal/review/source.go` |
| Where rules files are read from | `approval.Tree` | `internal/approval/tree_fetcher.go` |
| What an approver target means, and who is the same user | `approval.ApproverResolver` | `internal/approval/resolver.go` |
| Turning the platform's own data into a `Source` | a constructor in the platform package | — |
| *Hosted only:* the pull request webhook flow: receiving webhooks | `server.PRWebhook` | `internal/server/pr_webhook.go` |
| *Hosted only:* the pull request under review: review input, proposed files, where it reports (its key), status | `server.PullRequest` | `internal/server/pull_request.go` (embedding `review.Change`, `internal/review/change.go`) |

The server implements one flow, the pull request webhook: a platform's `PRWebhook` turns each
verified delivery into a `PullRequest`, and the server reviews it and has it report its status.
`PullRequest` embeds `review.Change` (a `review.Source` with its review input and warnings, what
`review.Evaluate` reviews), so a hosted platform's `Source` implements it. `PRWebhook` and
`PullRequest` are defined by their consumer, the server.

`platformtest` checks a platform against all of these, so a new platform is verified to plug in
rather than assumed to.

## Existing platforms

| Package | Change under review | Rules read from | Approver targets |
|---|---|---|---|
| `local` | working tree vs. `HEAD` | the working tree, so the CLI previews the rules a branch proposes | stand for themselves; names compared case-insensitively, as on GitHub |
| `ghplatform` | a pull request vs. its base revision | the base revision (the tip of the pull request's base branch it is compared against), so a change can't edit its own requirements | `login`, or `org/team-slug` expanded to the team's members; logins case-insensitive |
| `platformtest` | — (the contract suites every platform runs) | | |

Read `ghplatform` as the model for a hosted platform and `local` as the minimal case.

## The interfaces

### `approval.Tree`: rules files

```go
type Tree interface {
    Files(ctx context.Context) ([]string, error)               // every file, sorted
    ReadFile(ctx context.Context, path string) ([]byte, error) // missing path: wraps fs.ErrNotExist
}
```

Wrap it in `approval.TreeFetcher{Tree: tree, Log: log}`. That fetcher finds the rules files
(`rulesfile.IsRulesFile`), reads and parses them, and reports per-file errors while keeping the
good files. Don't write your own fetcher.

- Paths are repository-relative and slash-separated (`svc/api/RULES`), with no leading `/`.
- Pick the revision deliberately. A hosted platform reads the change's **base** revision (its
  target branch, never the default branch), as GitHub's `baseTree` does.
- A listing the platform truncated returns an error wrapping `approval.ErrTreeTruncated`, never a
  partial tree: a missing rules file would silently drop required approvers.
- List the tree once per review and share the listing with `Source.AllFiles`. `ghplatform`
  builds one `baseTree` and passes it to both the source and the evaluator.
- A failed listing must not be cached, so a later call retries.

### `approval.ApproverResolver`: approver targets

```go
type ApproverResolver interface {
    Resolve(ctx context.Context, target string) ([]string, error)
    Canonical(name string) string
}
```

- **`Resolve`** turns a `RULES` target into the users who can approve for it. A user target returns
  itself. A group target returns its members, across every page of the platform's listing.
- **Unknown targets.** A target the platform doesn't have returns an error wrapping
  `approval.ErrUnknownTarget`. The review drops it from its rule, the same as an inactive target.
- **Any other failure** (network, auth, rate limit) returns a plain error. The review then fails
  with that cause. Never report an outage as `ErrUnknownTarget`: that would silently drop the
  target and could fall back to weaker rules.
- **`Canonical`** puts a name in the form the platform uses for identity (for example lowercase,
  where logins are case-insensitive). It must be idempotent. The review canonicalizes every name
  itself (targets, approvals, the author, owners, members), so `Resolve` doesn't have to.
- **Don't cache.** `approval.RuleChain.Execute` resolves each target once per review. One
  resolver can then serve every review without its group membership going stale.
  To spare the rate limit, make the requests conditional instead, below the resolver: GitHub's
  installation client keeps the last answer for a team's members, a user, and a pull request's
  reviews and labels in the server's `RepoCache` with its ETag, and serves it only when GitHub answers
  `304 Not Modified` (`ghplatform/conditional_transport.go`). Never serve such an answer without
  that confirmation.
- **Validate before calling the API.** `RULES` files are untrusted input. If your platform has
  rules for target syntax, also implement `approval.TargetValidator`:

  ```go
  ValidateTarget(target string) error // wraps approval.ErrInvalidTarget
  ```

  `Resolve` calls it first, and a malformed target returns an error wrapping both
  `ErrUnknownTarget` and `ErrInvalidTarget` without reaching the API. This matters most when
  the client library puts the target into a URL path unescaped, as go-github does.

### `review.Source`: the change

```go
type Source interface {
    Evaluator() *approval.Evaluator
    AllFiles(ctx context.Context) ([]string, error)
    ChangedFiles(ctx context.Context) ([]string, error)
    DiffContent(ctx context.Context, path string) (string, error)
    ReadHead(ctx context.Context, path string) ([]byte, error)
}
```

| Method | Must return |
|---|---|
| `Evaluator` | `&approval.Evaluator{Fetch: &approval.TreeFetcher{...}, Resolver: ..., Log: log}`, built once in the constructor |
| `AllFiles` | every file at the revision the rules are read from (a hosted platform: the base revision; local: the working tree), sorted; `approval.ErrTreeTruncated` for a truncated listing |
| `ChangedFiles` | every path the change adds, modifies, or removes, sorted. A rename lists **both** paths, so the rules over the place it left still apply. Added paths are not in `AllFiles`. Empty when nothing changed. **List it only through requests addressed by the event's base and head revisions**, never by the change's number: the platform answers a request by number for whatever the head is by then, which may be a newer push than the head the review reports on (GitHub compares `base...head` by SHA). When that listing is capped, list the files another way if you can (GitHub compares the head tree with the merge base's when the comparison lists 300 files, or none) and report it through `Warnings`; a listing you cannot complete is `approval.ErrFileListIncomplete`, never a partial list. |
| `DiffContent` | the unified diff for `path`, or `""` when the change doesn't touch it, only renames it, or the platform has no text diff for it (binary, too large). |
| `ReadHead` | a file as the change proposes it (local: the working tree); a missing path wraps `fs.ErrNotExist`, and a file over `rulesfile.MaxSize` wraps `rulesfile.ErrTooLarge` (check the size before downloading). `review.Engine.ValidateProposed` validates the rules files a change proposes with it, for the server and `canopy review` alike, skipping those the head doesn't have (deleted, or the old side of a rename). |

- Implement `review.Rooted` (`RepoRoot() string`) only if the source is an on-disk checkout. A
  hosted platform doesn't.
- Keep the `Source`'s fields private. Everything it needs comes through its constructor, so
  its evaluator can't drift out of sync with it.

### The constructor: the platform's own data to a `Source`

Each platform turns its own data into a `Source` itself, so callers never handle its payload
types. A hosted platform does it in `PRWebhook.PullRequest`; GitHub's builds the `Source` from a
`pull_request` or `pull_request_review` event with the installation's client:

```go
func (p *PRWebhook) PullRequest(event string, body []byte) (server.PullRequest, bool, error)
```

- Treat the event as untrusted. Check that every field you need (repository, change number,
  base revision) is present and return an error if one isn't, before any API call.
- Make no API calls in the constructor: the server builds the pull request while answering the
  webhook.
  Fetch what the event doesn't carry (GitHub's webhook has no file list or patches) once, on first
  use, and share it, so `ChangedFiles` and `DiffContent` list the files once. Don't
  keep a failed listing, so a later call retries.
- Don't paginate by hand in each call. Use the client library's own pagination where it has one
  (go-github's `*Iter` methods, gathered by `ghplatform/collect.go`), or write one helper.
- Let one layer own rate limits. `ghplatform` turns off go-github's own rate limit check
  (`github.WithDisableRateLimitCheck`) so its retrying transport sees every request and reports
  `approval.ErrRateLimited`.
- Request authentication belongs to the platform's `PRWebhook.VerifyWebhook`, not this constructor.

### `server.PullRequest`: the pull request under review

```go
type PullRequest interface {
    review.Change // review.Source + ReviewInput(ctx) and Warnings()
    Key() string
    Info() server.PullRequestInfo
    Start(ctx context.Context, detailsURL string) error
    Report(ctx context.Context, o approval.Outcome, detailsURL string) error
}
```

- **`ReviewInput`**: the author, who counts as approving their own change wherever a rule allows
  self-approval (so leave the author's own reviews out: authoring is their approval); the
  approvers, each reviewer's latest decisive review (a later change request or dismissal cancels an
  approval, comments change nothing, approvals before the latest push still count); `IsActive`; and
  the repository owners, the fallback reviewers; and the change's labels, read when the review runs
  (a blocking label fails the change whatever its approvals). Names needn't be canonical. If an activity lookup
  fails, count the user as inactive: only a user the platform confirms is active.
- **`Warnings`**: what reading the pull request found worth reporting that does not change the
  verdict (GitHub: a pull request too large for its file listing). The server calls it once the
  reads are done and adds the warnings to the review's own (such as files no rule covers).
- **`Key`**: where `Report` publishes, as a string, without calling the platform (GitHub: the
  repository, lower-cased, and the head commit, `owner/repo@sha`). The server runs the reviews of
  one key one at a time, and only the newest reports: a review requested while another of its key
  waits replaces it, one requested while another runs cancels it, and a review superseded after it
  ran does not report. Two pull requests whose reports would replace each other must share a key;
  two that publish to different places must not.
- **`Info`**: the pull request for the server's record (`server.PullRequestInfo`): the cache
  scope, the repository lower-cased as `Key` names it, the number, base and head revisions,
  author, and the event and action that requested the review (GitHub:
  `pull_request.synchronize`). From the event alone, without calling the platform.
- **`Start`**: publish that a review is running, in a state of its own, under the same name
  `Report` uses, so the next report replaces it (GitHub: an in-progress check run). The server
  calls it before it evaluates.
- **`detailsURL`**: the review page (see "Review pages" in docs/server.md), or `""` when
  the server serves none. `Start` and `Report` link the status to it when it is set (GitHub: the
  check run's details URL, and a note at the end of its summary).
- **`Report`**: publish `o` as the pull request's status, replacing the earlier one (one name per
  platform; GitHub: a completed check run named `canopy` on the head commit, a new one per report).
  Map `approval.Passed`, `approval.Pending`, and `approval.Failed` to distinct states, and
  `approval.Errored` to a state that neither passes nor waits (it may share the failed state, told
  apart by its text; GitHub: `success`, `action_required`, `failure`, and `failure`). Carry
  `o.Description(limit)`, with the platform's limit for a one-line summary (GitHub: the check run's
  title, 140), as the status's headline: it fits the summary, warnings, and as many items as there
  is room for, so the platform never rejects it as too long. A platform with room for more may also
  list every item and warning, bounded by its limit (GitHub: the check run's Markdown summary, up
  to 65535 bytes, each item shown as code since items come from the pull request). Never decide
  what a review means yourself (`review.OutcomeOf` and `approval.ReviewState.Outcome` do). Never publish tokens, raw API errors, or
  file contents. The review reports only a status: it never comments on the pull request.

### `server.PRWebhook`: the pull request webhook

```go
type PRWebhook interface {
    Name() string                                                  // logs and the OpenAPI document
    WebhookPath() string                                           // where its webhook is served (POST)
    WebhookMaxBytes() int64                                        // the platform's payload limit
    WebhookContentTypes() []string                                 // body media types, for OpenAPI
    WebhookDescription() string                                    // auth, headers, events (Markdown)
    VerifyWebhook(r *http.Request) (event string, body []byte, err error)
    DeliveryID(r *http.Request) string                             // for the server's record; "" if none
    PullRequest(event string, body []byte) (server.PullRequest, bool, error) // false: not reviewable
}
```

- Everything particular to the webhook is the platform's: `WebhookPath` (the URL configured on
  the platform's side; GitHub: `/webhooks/github`), `WebhookMaxBytes` (its payload limit; GitHub:
  25 MB), `WebhookContentTypes`, and `WebhookDescription`, the OpenAPI text saying how a request
  is authenticated, which headers it carries, and which events start a review. The server
  registers the route as given, bounds the body by the limit, and refuses a path without a
  leading `/`, a path already served, or a platform without a limit or content types.
- `VerifyWebhook` authenticates the request (signature or token) before anything reads it.
- `PullRequest` parses the verified body, returns false for events that don't start a review, and
  makes no API calls. It owns the platform's client construction (GitHub: an App JWT exchanged for
  an installation token per installation) and reuses one resolver per installation.
- Register the platform's constructor in `cmd/canopyweb/main.go` (`platforms`), keyed by the name
  `config.Config.SelectedPlatform` returns. Nothing else in the server changes.

## Adding a platform

The steps use GitLab as the example.

1. **Create the package**: `internal/platform/glplatform`. Name it after the platform with a
   `platform` suffix, so it doesn't clash with the client library's package name (as `ghplatform`
   avoids `github`). It must not import another platform package.
2. **Implement the tree** in `tree.go`: an unexported type implementing `approval.Tree` over the
   base revision, listed once.
3. **Implement the resolver** in `resolver.go`: an exported `ApproverResolver` with `Resolve`,
   `Canonical` and, if targets have a syntax, `ValidateTarget`. Map "not found" to
   `approval.ErrUnknownTarget`, using `errors.As` on the client's error type and never matching
   strings.
4. **Implement the evaluator** in `evaluator.go`: a function returning `*approval.Evaluator` with
   a `TreeFetcher` over your tree and your resolver, accepting an existing tree so the source can
   share its listing.
5. **Implement the source** in `source.go`: a `Source` with private fields and
   `var _ server.PullRequest = (*Source)(nil)`: the files, base revision, review input, head
   reads, and status reporting. Keep every `Source` method in `source.go`, with its type.
6. **Implement the webhook** in `pr_webhook.go`: a `PRWebhook` implementing `server.PRWebhook`
   (route, verification, event parsing, client construction), with
   `NewPRWebhook(cfg config.GitLab, repo *server.RepoCache, log)`. Read base trees and rules files
   through a `repo.Scope(...)` of the server's repository cache (`server.RepoCache`), named
   for whatever limits what a reader may see (e.g. one installation). Register it in `cmd/canopyweb/main.go`'s `platforms`.
7. **Configure it**: add a section to `internal/config`:

   ```go
   type Config struct {
       // ...
       GitLab GitLab `mapstructure:"gitlab"`
   }
   type GitLab struct {
       URL   string `mapstructure:"url"`   // self-hosted instances
       Token string `mapstructure:"token"` // never logged
   }
   ```

   Environment variables (`CANOPY_GITLAB_TOKEN`, ...) are bound automatically from the struct tags.
   Give the section `Enabled()` and `Validate()` as `config.GitHub` has, add its name to
   `Config.SelectedPlatform`, and add each key to the table in docs/configuration.md.
   Never log secret values.
8. **Pass the contract**: see below.
9. **Document it**: add a row to the table above, and the platform's specifics (where files come
   from, target syntax) to the "Change Evaluation" section of docs/review.md.

## Testing: the contract

`platformtest` defines the scenarios and the checks; your tests only build your platform over
them. Put the calls in `contract_test.go`:

```go
func TestGitLabMeetsTheSourceContract(t *testing.T) {
    platformtest.RunSourceContract(t, platformtest.BaseRevision, func(t *testing.T, f platformtest.Fixture) review.Source {
        // Serve f.Base as the base revision and f.Change as the merge request
        // from a fake API, then build the Source the way production does.
    })
}

func TestGitLabTreeMeetsTheTreeContract(t *testing.T) {
    platformtest.RunTreeContract(t, func(t *testing.T, files map[string]string) approval.Tree { ... })
}

func TestGitLabResolverMeetsTheResolverContract(t *testing.T) {
    platformtest.RunResolverContract(t, func(t *testing.T, members []string) platformtest.ResolverHarness {
        return platformtest.ResolverHarness{
            Resolver:     ...,           // over a fake API holding one group with members
            Group:        "acme/backend", // that group, in your target syntax
            UnknownGroup: "acme/ghosts",
            Invalid:      "acme/../x",   // "" if every target is well-formed
            Calls:        ...,           // API request count; nil if there is no API
        }
    })
}
```

| Suite | Checks |
|---|---|
| `RunSourceContract` | `AllFiles` is the revision the platform reads rules from (`BaseRevision` or `HeadRevision`); `ChangedFiles` covers a modify, an add, a delete and both sides of a rename, and is empty with no change; diffs show each change and are `""` for untouched paths; the evaluator fetches the rules files |
| `RunReviewInputContract` | *(hosted)* the author is reported; approvals are each reviewer's latest decision, sorted and deduplicated; change requests and dismissals cancel, comments don't; the author's own approval is left out |
| `RunStatusReporterContract` | *(hosted)* one report per call; passed, pending, and failed in distinct states, and errored neither passed nor pending; a later report replaces the earlier one; a start is a running state of its own that a report replaces, both linking the review page; an oversized summary is cut, not rejected |
| `RunTreeContract` | `Files` is complete and sorted; `ReadFile` round-trips contents; a missing path is `fs.ErrNotExist` |
| `RunResolverContract` | a user resolves to itself; `Canonical` is idempotent; a group resolves to its members; an unknown group is `ErrUnknownTarget`; a malformed target is rejected offline, without an API call |

**Fake the platform's API with `httptest`, not the network.** Build one fake server per platform
that can be configured from a `platformtest.Fixture` and that counts requests per endpoint, like
`ghplatform/fake_test.go`. Point the client's base URL at it.

**Also test what the contract can't know**, as `ghplatform`'s own tests do:
- pagination across several pages;
- "not found" vs. other API errors;
- case rules in `Canonical`;
- that one tree listing is shared between `AllFiles` and the fetch;
- rejecting incomplete events.

**A hosted platform has end-to-end tests too**, in `internal/platform/<platform>/e2e` (a `doc.go`
and test files; `make e2e` runs every platform's). They run the real review server as the platform's app
against an in-process fake of the platform that keeps state and sends signed webhooks. Each case
stages a repository, starts the server, takes steps as users would (open a pull request, push,
review), and checks the status the server reports after each one. Then it tears everything down. See
`ghplatform/e2e`: its fake GitHub handles App JWTs, installation tokens and user tokens, and only
the app may create check runs.

**Before you're done:** `go test -race ./internal/platform/...`, `golangci-lint run` and
`make deadcode`.
