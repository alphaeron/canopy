# Review platforms

A **platform** is where a change under review lives: the local working tree, a GitHub pull
request, and in future a GitLab merge request or similar. The review algorithm (`internal/approval`)
and the review engine (`internal/review`) never branch on the platform, and neither does the review
server (`internal/server`). Each platform supplies these through small interfaces, and everything
else is shared:

| What | Interface | Defined in |
|---|---|---|
| The files under review and their diffs | `review.Source` | `internal/review/source.go` |
| Where rules files are read from | `approval.Tree` | `internal/approval/fetcher.go` |
| What an approver target means, and who is the same user | `approval.ApproverResolver` | `internal/approval/resolver.go` |
| Turning the platform's own data into a `Source` | a constructor in the platform package | — |
| *Hosted only:* the pull request webhook flow: receiving webhooks | `server.PRWebhook` | `internal/server/server.go` |
| *Hosted only:* the pull request under review: review input, proposed files, status | `server.PullRequest` | `internal/server/server.go` |

The server implements one flow, the pull request webhook: a platform's `PRWebhook` turns each
verified delivery into a `PullRequest`, and the server reviews it and has it report its status.
`PullRequest` embeds `review.Source`, so a hosted platform's `Source` implements it. Both are
defined by their consumer, the server; nothing hosted-only lives in `review`.

`platformtest` checks a platform against all of these, so a new platform is verified to plug in
rather than assumed to.

## Existing platforms

| Package | Change under review | Rules read from | Approver targets |
|---|---|---|---|
| `local` | working tree vs. `HEAD` | the working tree, so the CLI previews the rules a branch proposes | stand for themselves; names compared exactly |
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

- Paths are repository-relative and slash-separated (`svc/api/KIT`), with no leading `/`.
- Pick the revision deliberately. A hosted platform reads the change's **base** revision (its
  target branch, never the default branch), as GitHub's `baseTree` does.
- A listing the platform truncated returns an error wrapping `review.ErrTreeTruncated`, never a
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

- **`Resolve`** turns a `KIT` target into the users who can approve for it. A user target returns
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
- **Validate before calling the API.** `KIT` files are untrusted input. If your platform has
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
}
```

| Method | Must return |
|---|---|
| `Evaluator` | `&approval.Evaluator{Fetch: &approval.TreeFetcher{...}, Resolver: ..., Log: log}`, built once in the constructor |
| `AllFiles` | every file at the base revision, sorted; `review.ErrTreeTruncated` for a truncated listing |
| `ChangedFiles` | every path the change adds, modifies, or removes, sorted. A rename lists **both** paths, so the rules over the place it left still apply. Added paths are not in `AllFiles`. Empty when nothing changed. A listing the platform cut short is `review.ErrFileListIncomplete`, never a partial list. |
| `DiffContent` | the unified diff for `path`, or `""` when the change doesn't touch it, only renames it, or the platform has no text diff for it (binary, too large). |

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
- Don't paginate by hand in each call. Write one `paginate` helper for your client, as in
  `ghplatform/paginate.go`.
- Request authentication belongs to the platform's `PRWebhook.VerifyWebhook`, not this constructor.

### `server.PullRequest`: the pull request under review

```go
type PullRequest interface {
    review.Source
    ReviewInput(ctx context.Context) (approval.ReviewInput, error)
    ReadHead(ctx context.Context, path string) ([]byte, error)
    Report(ctx context.Context, o review.Outcome) error
}
```

- **`ReviewInput`**: the author; the approvers, each reviewer's latest decisive review (a later
  change request or dismissal cancels an approval, comments change nothing, approvals before the
  latest push still count, the author's own reviews are left to `self_approve`); `IsActive`; and
  the repository owners, the fallback reviewers. Names needn't be canonical. If an activity lookup
  fails, count the user as inactive: only a user the platform confirms is active.
- **`ReadHead`**: a file as the pull request proposes it; a missing path wraps `fs.ErrNotExist`.
  The server validates the rules files among the changed files with it, skipping those the head
  doesn't have (deleted, or the old side of a rename).
- **`Report`**: publish `o` as the pull request's status, replacing the earlier one (one status
  name per platform; GitHub: a commit status). Map `review.Passed`, `review.Pending`,
  `review.Failed`, and `review.Errored` to distinct status states, and carry `o.Summary` as the status's description,
  cut to the platform's limit rather than failing. Never decide what a review means yourself
  (`review.OutcomeOf` does). Never publish tokens, raw API errors, or file contents. The review
  reports only a status: it never comments on the pull request.

### `server.PRWebhook`: the pull request webhook

```go
type PRWebhook interface {
    Name() string                                                  // logs and the OpenAPI document
    WebhookPath() string                                           // where its webhook is served (POST)
    WebhookMaxBytes() int64                                        // the platform's payload limit
    WebhookContentTypes() []string                                 // body media types, for OpenAPI
    WebhookDescription() string                                    // auth, headers, events (Markdown)
    VerifyWebhook(r *http.Request) (event string, body []byte, err error)
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
- Register the platform's constructor in `cmd/kitweb/main.go` (`platforms`), keyed by the name
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
   `NewPRWebhook(cfg config.GitLab, log)`.
   Register it in `cmd/kitweb/main.go`'s `platforms`.
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

   Environment variables (`KIT_GITLAB_TOKEN`, ...) are bound automatically from the struct tags.
   Give the section `Enabled()` and `Validate()` as `config.GitHub` has, add its name to
   `Config.SelectedPlatform`, and add each key to the Configuration table in the top-level README.
   Never log secret values.
8. **Pass the contract**: see below.
9. **Document it**: add a row to the table above, and the platform's specifics (where files come
   from, target syntax) to the "Change Evaluation" section of the top-level README.

## Testing: the contract

`platformtest` defines the scenarios and the checks; your tests only build your platform over
them. Put the calls in `contract_test.go`:

```go
func TestGitLabMeetsTheSourceContract(t *testing.T) {
    platformtest.RunSourceContract(t, func(t *testing.T, f platformtest.Fixture) review.Source {
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
| `RunSourceContract` | `AllFiles` is the base revision; `ChangedFiles` covers a modify, an add, a delete and both sides of a rename, and is empty with no change; diffs show each change and are `""` for untouched paths; the evaluator fetches the rules files |
| `RunReviewInputContract` | *(hosted)* the author is reported; approvals are each reviewer's latest decision, sorted and deduplicated; change requests and dismissals cancel, comments don't; the author's own approval is left out |
| `RunStatusReporterContract` | *(hosted)* one report per call; a distinct state per verdict; a later report replaces the earlier one; an oversized summary is cut, not rejected |
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

**Before you're done:** `go test -race ./internal/platform/...`, `golangci-lint run` and
`make deadcode`.
