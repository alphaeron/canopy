# Review platforms

A **platform** is where a change under review lives: the local working tree, a GitHub pull
request, and in future a GitLab merge request or similar. The review algorithm (`internal/approval`)
and the match engine (`internal/match`) never branch on the platform. Each platform supplies four
things through small interfaces, and everything else is shared:

| What | Interface | Defined in |
|---|---|---|
| The files under review and their diffs | `match.Source` | `internal/match/source.go` |
| Where rules files are read from | `approval.Tree` | `internal/approval/fetcher.go` |
| What an approver target means, and who is the same user | `approval.ApproverResolver` | `internal/approval/resolver.go` |
| Turning the platform's own data into a `Source` | a constructor in the platform package | — |

`platformtest` checks a platform against all of these, so a new platform is verified to plug in
rather than assumed to.

## Existing platforms

| Package | Change under review | Rules read from | Approver targets |
|---|---|---|---|
| `local` | working tree vs. `HEAD` | the working tree, so the CLI previews the rules a branch proposes | stand for themselves; names compared exactly |
| `ghplatform` | a pull request vs. its base revision | the base revision, so a change can't edit its own requirements | `login`, or `org/team-slug` expanded to the team's members; logins case-insensitive |
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
- Pick the revision deliberately. A hosted platform reads the **base** revision, as GitHub's
  `baseTree` does.
- List the tree once per review and share the listing with `Source.AllFiles`. `ghplatform`
  builds one `baseTree` and passes it to both the source and the reviewer.
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

### `match.Source`: the change

```go
type Source interface {
    Reviewer() *approval.Reviewer
    AllFiles(ctx context.Context) ([]string, error)
    ChangedFiles(ctx context.Context) ([]string, error)
    DiffContent(ctx context.Context, path string) (string, error)
}
```

| Method | Must return |
|---|---|
| `Reviewer` | `&approval.Reviewer{Fetch: &approval.TreeFetcher{...}, Resolver: ..., Log: log}`, built once in the constructor |
| `AllFiles` | every file at the base revision, sorted |
| `ChangedFiles` | every path the change adds, modifies, or removes, sorted. A rename lists **both** paths, so the rules over the place it left still apply. Added paths are not in `AllFiles`. Empty when nothing changed. |
| `DiffContent` | the unified diff for `path`, or `""` when the change doesn't touch it. It may also be `""` for a binary file, a diff too large for the platform, or either side of a rename. |

- Implement `match.Rooted` (`RepoRoot() string`) only if the source is an on-disk checkout. A
  hosted platform doesn't.
- Keep the `Source`'s fields private. Everything it needs comes through its constructor, so
  its reviewer can't drift out of sync with it.

### The constructor: the platform's own data to a `Source`

Each platform turns its own data into a `Source` itself, so callers never handle its payload
types. GitHub's version:

```go
func NewSourceFromEvent(ctx context.Context, event *github.PullRequestEvent,
    client *github.Client, log *slog.Logger) (*Source, error)
```

- Treat the event as untrusted. Check that every field you need (repository, change number,
  base revision) is present and return an error if one isn't, before any API call.
- Fetch what the event doesn't carry (GitHub's webhook has no file list or patches) once in the
  constructor, so `ChangedFiles` and `DiffContent` make no API calls.
- Don't paginate by hand in each call. Write one `paginate` helper for your client, as in
  `ghplatform/paginate.go`.
- Signature checks belong to the server, not this constructor.

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
4. **Implement the reviewer** in `reviewer.go`: `NewReviewer(...)` returning
   `*approval.Reviewer` with a `TreeFetcher` over your tree and your resolver. Add an unexported
   variant that accepts an existing tree, so the source can share its listing.
5. **Implement the source** in `source.go`: a `Source` with private fields, the constructor from
   the platform's own data, and `var _ match.Source = (*Source)(nil)`.
6. **Configure it**: add a section to `internal/config`:

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
   Add each key to the Configuration table in the top-level README. Never log secret values.
7. **Pass the contract**: see below.
8. **Until something calls it**, add the package to `scripts/deadcode.allow`, as `ghplatform`
   is:

   ```
   ^github\.com/alphaeron/kit/internal/platform/glplatform\..+$
   ```

   Remove the line once the server uses the package.
9. **Document it**: add a row to the table above, and the platform's specifics (where files come
   from, target syntax) to the "Change Evaluation" section of the top-level README.

## Testing: the contract

`platformtest` defines the scenarios and the checks; your tests only build your platform over
them. Put the calls in `contract_test.go`:

```go
func TestGitLabMeetsTheSourceContract(t *testing.T) {
    platformtest.RunSourceContract(t, func(t *testing.T, f platformtest.Fixture) match.Source {
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
| `RunSourceContract` | `AllFiles` is the base revision; `ChangedFiles` covers a modify, an add, a delete and both sides of a rename, and is empty with no change; diffs show each change and are `""` for untouched paths; the reviewer fetches the rules files |
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

## Not yet defined

A hosted platform will also need these, but they don't exist yet. They will be added with the
review server, as optional interfaces a hosted `Source` implements. Plan for them, but don't
invent your own versions:

- **Review input:** reading the change's author, approvals, and which users are active from the
  platform, as an `approval.ReviewInput`.
- **Status reporting:** publishing a review's result back on the change.
- **Server registration:** webhook verification and parsing, and a route per platform, registered
  explicitly in the server's `main`.
- **Offline target validation:** calling `ValidateTarget` over every rules file (server checks on
  the change's own `KIT` edits, and `kit validate`).
