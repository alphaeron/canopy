# Canopy - A Modular Code Review Tool

## Architecture

### Root as Global Authority

A rule defined at the repository root directory (`/`) naturally acts globally because rules cascade downwards by default.

In the case that a file fails to meet any of the existing rules specified at the repository root, we still do not need a "global" configuration as we can reliably request the repository owner for review.

In the case an owner is inactive, the owner is removed from the list of possible approvers for a given rule. On GitHub, a user is inactive when GitHub reports their account suspended (`suspended_at` on their profile) or does not know them; a user whose lookup fails also counts as inactive, since only a user GitHub confirms is active.

If the resulting set of approvers for a given rule is empty, that rule is **skipped** — an error condition that records the skip in the review error list and forces the final review state to `error` — and evaluation falls back to the parent directory rules. If those recursively are not matched up until and including the repository root rules, we will request a review from the repository owner.

These failed matches are recorded in the review's errors, and the accumulated error propagates out of the pipeline so the review errors (blocking the merge) even when a later rule or the owner fallback satisfies the file.

### Combination Strategies
To achieve logical completeness when combining rules from parent and subdirectories, the following combination operations are supported:

* `union`: If `/` requires `Group A` and `/src/` requires `Group B`, files inside `/src/` require **both** groups.
* `override`: Subdirectory rules completely discard parent rules for any matching pattern.
* `intersect`: If `/` requires `Group A` + `Group B`, and `/src/` requires `Group B` + `Group C`, files inside `/src/` require approval from `Group B`.

### Recurring Approver Targets

When the same approver target recurs across layers (union) or is shared across layers (intersect), the count is tracked per target occurrence and every occurrence must be satisfied independently against the same pool of approvers. If the root rule requires 1 approval from a target and the `/src` rule requires 2 from that same target, a single matching approval satisfies the root requirement while `/src` still needs one more.

### Unsatisfiable Rules and the Fallback Terminal State

If a file's requirement cannot be satisfied — no matching rule at all, or every matching rule prunes down to an empty approver set — the engine falls back to the repository owners. If that fallback is itself empty (no active repository owners exist at all), the rule is **unsatisfiable**: the file and the overall evaluation terminate with an `error` status rather than remaining `unresolved`. The error propagates out of the approval pipeline so the review errors, blocking the merge.

### Branch Targets

The review rules for a pull request are read from its **base branch**, at the base revision the pull request is compared against (`pull_request.base.sha` on GitHub), not from the repository's default branch. A pull request into `release/1.x` is reviewed against `release/1.x`'s `RULES` files.

As with CODEOWNERS, a pull request cannot change its own requirements: `RULES` files it adds, modifies, or deletes take effect once it merges. The review server validates the ones it adds or modifies (as `canopy validate` does) at the pull request's head, failing the review on any violation (its report names the invalid files), so a broken `RULES` file cannot be merged.

The CLI does the opposite on purpose: it reads the working tree's `RULES` files, uncommitted changes included, to preview the rules a branch proposes. Otherwise `canopy review` judges a change as the server does: it validates the `RULES` files the working tree changes, failing the review on any violation, and its exit status is the review's verdict, zero only when the review passes. `canopy review --json` carries that verdict as its `outcome` (verdict, summary, items, and warnings) beside the review state. `canopy validate` validates `RULES` files as the review's platform does.

### Repository Owner Fallback

When a file falls back to the repository owners, each owner becomes a reviewer for that file, required once. No rule covers the file, so no rule allows self-approval: authoring the change never counts as an owner's review, even when the author is an owner. A file that no rule covers at all is also reported as a warning (`N files covered by no rule; the repository owners review them`), which does not change the verdict. On GitHub the repository owner is the repository's owner account (`owner` in `owner/repo`).

## Configuration File Schema

Each directory in the repository can host a `RULES` file to localize approval rules.

```yaml
# A regular expression matched against each file's full repository-relative
# path (e.g. "svc/api/main.go"), whichever directory this RULES file is in.
- ".*\.go":
    name: "Core Backend Review"

    # If true, blocks searching parent directories. This file acts as the new configuration root.
    ignore_parent_dirs: false

    # Controls how rules merge when multiple pattern matches collide across directories
    # Options: union, override, intersect
    combine_strategy: "union"

    # If true, this rule will NOT apply to nested subdirectories
    current_directory_only: false

    # Determines if PR authors can fulfill their own approval requirements if they belong to an approver group
    self_approve: false

    # Defined approval bodies and counts
    approvers:
      - target: "org/backend-team" # Can be 'username' or 'org/team'
        required_count: 2
      - target: "org/security-leads"
        required_count: 1
```

## Rule Combination

When evaluating a changed target file (e.g., `/src/api/v1/auth.go`), the engine constructs a chain of rules starting from the repository root.

```
[ Root /RULES ]   ──(Union / Override / Intersect)──>  [ /src/RULES ]  ──>  [ Target File ]
```

### Additional Rules
* **Scope Exclusion:** A rule block is entirely discarded if `current_directory_only: true` and the evaluated file resides deeper in a nested subdirectory.
* **The Break Signal:** If any configuration file along the vertical path contains `ignore_parent_dirs: true`, the evaluation chain truncates instantly, throwing away all parent settings above that directory level.

## Review Algorithm

The approve phase is a chain of independent layers. Each layer consumes only the state of the layer below it and returns only its own state, so every requirement can be traced to exactly one type and one `Execute` command:

| Layer | Type | Command | State it returns |
|---|---|---|---|
| Fetch | `Fetcher` (interface), `TreeFetcher` over each platform's `Tree` | `Execute(ctx)` | parsed `RULES` rules |
| Sift | `RuleChain` builder | `GetFileRuleChain` | per-file ordered rule layers |
| Chain | `RuleChain` | `Execute` | `ReviewState` (consumes only `LayerState`) |
| Layer | `RuleLayer` | `Execute` | `LayerState` incl. per-rule states and the combined target footprint (`Required`) for the next layer |
| Rule | `ExecutableRule` | `Execute` | `RuleState` (consumes only target states) |
| Target | `ExecutableTarget` | `Execute` | `ApproverTargetState` |

State flows upward in one direction only: chain status derives from file status, file status derives from per-target states, layer status derives from its rule states, and rule status derives from its approver target states. No layer reads a state "above" itself.

### Author Self-Approval (`self_approve`)
The self-approval policy is a requirement of the rule and travels with every approver target that the rule produces. Every self-approval decision a rule makes funnels through one predicate, `ExecutableTarget.contributes`, and only two edge cases exist (a file no rule covers has no rule to allow it, so the owner fallback never counts the author):

* **The target is the author themselves.** If `self_approve: false` and a rule directly targets the PR author, the target is `inactive` and never satisfiable, even if the author has approved.
* **The author is a member of a resolved group.** Inactive members are ignored. If `self_approve: false`, the author's approval is sliced out of the group's eligible members: the group can still be satisfied by every other member, and the author never appears in `ResolvedBy`. If the group's only active member is the author, the whole target prunes away with the rule.
* Setting `self_approve: true` counts the author's approval exactly like any other member's, in both cases (one approval, counted once).
* Authoring a change is the author's approval: the platform reports who the author is, and the author counts as having approved wherever `self_approve: true` allows it. On GitHub, where an author cannot approve their own pull request, this is how `self_approve` takes effect.
* The default is `false`: authors can never fulfill their own requirements unless a rule explicitly allows it.

### Approver Targets, Resolution, and Pruning
Every target occurrence is evaluated independently against the same pool of current approvers. Targets resolve to a member set through the configured `ApproverResolver` (individual targets: 1:1 by default; group targets: expanded to their members):

* A target whose members are all inactive prunes away; the rule then falls back to earlier rules or the parent directory (see above).
* A target that cannot be resolved to any member at all is `unresolved`, which escalates the containing rule, layer, and file to `unresolved` and forces the overall status to `error`. A target the platform does not know (or that is malformed for it) prunes away like an inactive one; a platform failure while resolving (an outage, a rate limit) never prunes, so it cannot weaken a requirement, and its cause is recorded in the review's errors.
* Names are compared in the platform's canonical form: GitHub logins are case-insensitive, so `Alice` in a `RULES` file matches an approval by `alice`. Local reviews compare names case-insensitively too, so a local review agrees with GitHub on who approved.
* Each target is resolved once per review, however many files and rules use it.
* A group is `satisfied` once `required_count` of its eligible, active resolved members appear among current approvers.
* Because each occurrence is independent, recurring targets under `union` are counted once per occurrence (see Recurring Approver Targets).

### Rule Fallback
Pruning drops a target only when *zero* active eligible members remain. A rule left with zero active targets is **skipped** — an error condition: the skip is marked on the rule's own state so the hierarchical breakdown remains a complete trace, and a review error is accumulated, which forces the final review state to `error` even when preceding rules, parent rules, or the repository-owner fallback afterwards still satisfy the file.

## Change Evaluation

The runtime application evaluates a change (a GitHub pull request, or the local working tree) with a three-tiered pipeline. Each platform supplies where files and rules come from and how approver targets resolve; the pipeline itself is the same on every platform:

```
[Change received]
          │
          ▼
   1. FETCH PHASE  ──► Walk tree from target files up to root (respecting ignore_parent_dirs)
          │
          ▼
   2. SIFT PHASE   ──► Match target files against rule patterns; process configuration scoping
          │
          ▼
   3. APPROVE PHASE ──► Compute final approval metrics, check self-approval rights, verify signatures
```

### Phase Breakdown

#### Phase 1: Fetch
1. List the change's file paths through the platform, counting a renamed file at both its old and new paths. (GitHub: the pull request's files through the API; the webhook does not carry them. Local: the working tree's status against `HEAD`.)
2. For each unique file path, systematically map the trajectory from its local leaf directory vertically upward to the root repository level.
3. Read the `RULES` files along the path sequentially.
4. If a file specifies `settings.ignore_parent_dirs: true`, halt tree traversal immediately.

#### Phase 2: Sift
1. Traverse the collected configurations chronologically from Top to Bottom (Root down to Leaf).
2. Assess every localized rule block against the targeted file:
   * Skip the rule block completely if `current_directory_only: true` and the file is nested.
   * Match the rule's `pattern`, a regular expression, against the file's full repository-relative path.
3. If a pattern matches, reference the file's `combine_strategy`:
   * If `override`, purge all rule criteria accumulated from higher ancestral folders for this pattern space and insert the current criteria.
   * If `union`, append the rule constraints directly into the active evaluation array.
   * If `intersect`, keep only the criteria present in both the parent accumulation and the current rule.

#### Phase 3: Approve
1. Read the change's author and approvers from the platform (or, locally, from `canopy review` flags), in the platform's canonical name form.
2. For each final rule block tied to an asset:
   * Resolve each `approvers` target to its member set through the platform (GitHub: `org/team-slug` targets expand to the team's members).
   * Apply the self-approval policy: if `self_approve` is `false`, slice the PR Author out of the eligible approver matching array.
   * Increment counts when an active approver matches a member identity target.
   * If a rule's targets have all been pruned away (inactive members, excluded author, or unresolvable targets), the rule is **skipped and the skip is recorded as an error**; it still falls back to the preceding rule's criteria, but the final overall state terminates `error` regardless of that fallback.
3. Mark a rule block as **Satisfied** once each target's internal `actual_count >= required_count`.
4. Report the result back on the platform — passing only if all rules across all files resolve to a **Satisfied** state. GitHub: a completed check run named `canopy` on the pull request's head commit. Its title summarizes the outcome in one line, and its summary lists in full what the outcome is about (the approvers still needed, or the invalid `RULES` files) and any warnings, such as files no rule covers or a very large pull request. Its conclusion is `success` when the review is satisfied, `action_required` while approvals are missing, and `failure` when the pull request proposes an invalid `RULES` file or the review could not produce a valid result: an internal `error` status (a GitHub failure, a listing too large to evaluate, a skipped or unsatisfiable rule) or a review that could not run or timed out; the title tells these apart. Each report is a new check run, and GitHub shows the latest. Require the `canopy` check in branch protection to block merging until it succeeds. The review never comments on the pull request.

On GitHub, approvals are each reviewer's latest decisive review: a later change request or dismissal cancels an approval, and comments change nothing. An approval made before the latest push still counts. The author's own reviews are ignored here: authoring the pull request is the author's approval, and `self_approve` decides whether it counts.

## Configuration

The CLI and the review server read the same configuration, in decreasing precedence: environment variables prefixed `CANOPY_`, the file given with `--config`, the file named by `CANOPY_CONFIG`, then `.canopy.yaml` in the current directory or `$HOME`. Each hosted platform has its own section.

| Key | Environment variable | Default | Purpose |
|---|---|---|---|
| `verbose` | `CANOPY_VERBOSE` | `false` | Debug-level logging |
| `serve_addr` | `CANOPY_SERVE_ADDR` | `:8080` | Review server listen address |
| `review_timeout` | `CANOPY_REVIEW_TIMEOUT` | `50s` | Longest one review may run |
| `report_timeout` | `CANOPY_REPORT_TIMEOUT` | `30s` | Longest reporting the outcome of a timed-out review may take |
| `review_workers` | `CANOPY_REVIEW_WORKERS` | `4` | Reviews the server runs at once |
| `review_queue_per_worker` | `CANOPY_REVIEW_QUEUE_PER_WORKER` | `16` | Pull requests that may wait per worker; beyond that the server answers 503. More webhooks for a pull request already waiting or under review take no more room. `review_workers` × `review_queue_per_worker` may be at most 4096 |
| `shutdown_timeout` | `CANOPY_SHUTDOWN_TIMEOUT` | `10s` | Longest the server waits for open requests when stopping |
| `database_url` | `CANOPY_DATABASE_URL` | | Review server's PostgreSQL connection string; unset runs without a database (see [Database](#database)). The server creates its tables at startup. Never logged |
| `public_url` | `CANOPY_PUBLIC_URL` | | Review server's URL as users reach it, e.g. `https://canopy.example.com`; with `link_key`, serves review pages and links each check run to its own (see [Review pages](#review-pages)) |
| `link_key` | `CANOPY_LINK_KEY` | | Required with `public_url`: at least 32 bytes (e.g. `openssl rand -hex 32`), the same on every replica. Signs review page links; changing it breaks every link already published. Never logged |
| `database_key` | `CANOPY_DATABASE_KEY` | | Required with `database_url`: at least 32 bytes (e.g. `openssl rand -hex 32`), the same on every replica, never stored in the database. Authenticates the GitHub data cached there; changing it discards that cache. Never logged |
| `github.app_id` | `CANOPY_GITHUB_APP_ID` | | GitHub App ID |
| `github.private_key` | `CANOPY_GITHUB_PRIVATE_KEY` | | GitHub App private key (PEM, PKCS #1 or #8) |
| `github.webhook_secret` | `CANOPY_GITHUB_WEBHOOK_SECRET` | | GitHub webhook signing secret |
| `github.api_url` | `CANOPY_GITHUB_API_URL` | `https://api.github.com/` | REST API base URL, for GitHub Enterprise Server |
| `github.request_timeout` | `CANOPY_GITHUB_REQUEST_TIMEOUT` | `30s` | Longest one REST API request may wait for a response |

```yaml
verbose: false
github:
  app_id: "12345"
  private_key: |
    -----BEGIN RSA PRIVATE KEY-----
    ...
  webhook_secret: "..."
```

The platform is deployment configuration: a repository under review has no say in it. The review server reviews on the platform whose section has any key set. That section must then be complete and valid, so setting only some of a platform's keys stops the server at startup, as does configuring more than one platform.

## Review Server

`canopyweb` is the review server, a separate binary from the CLI (`go build ./cmd/canopyweb`). At startup it loads the configuration, selects the platform, and refuses to start unless that platform's section is complete and valid (GitHub: `app_id`, `private_key`, and `webhook_secret` all set, `app_id` numeric, `private_key` an RSA key), and unless `review_workers` × `review_queue_per_worker` is at most 4096. `--addr` overrides `serve_addr`, and `--config` names the configuration file.

| Route | Purpose |
|---|---|
| `POST /webhooks/github` | The GitHub App's webhook; each platform chooses its own webhook path. The signature is verified before anything else; unsigned or mis-signed requests get 401. A reviewable event is acknowledged with 202 and reviewed in the background; a full queue gets 503. Only the newest review of a pull request's head commit reports (see below). |
| `GET /openapi.json`, `GET /openapi.yaml` | The OpenAPI 3.1 document, generated from the routes |
| `GET /swagger` | Interactive API documentation |
| `GET /reviews/{owner}/{name}/{number}/{sha}?sig=…` | The review page of a pull request's head commit (with `public_url` and `link_key`; see [Review pages](#review-pages)) |
| `GET /reviews/{owner}/{name}/{number}/{sha}/progress?sig=…` | How far that commit's newest review has come, as JSON, which the review page polls |

On GitHub, the server runs as a GitHub App. It needs these repository permissions: *Contents* (read), *Pull requests* (read), *Checks* (read and write), and *Members* (organization, read) to expand `org/team` targets. It reviews on `pull_request` (`opened`, `synchronize`, `reopened`, `ready_for_review`, and `edited` when the base branch changes) and `pull_request_review` (`submitted`, `dismissed`) events, except those of a closed or merged pull request (a reopened one is reviewed again). Requests that GitHub rate-limits are retried after the delay it asks for: up to a minute for a secondary limit (`Retry-After`), and for an exhausted primary limit until it resets, within the review's deadline (at most 10 minutes). A review still rate-limited reports `rate limited by the platform; try again later`; other errors are not retried.

Each review runs with a timeout (`review_timeout`); a review that times out reports `review timed out`, allowing up to `report_timeout` to post it. Stopping the server (SIGINT or SIGTERM) cancels reviews in flight and waits up to `shutdown_timeout` for open requests.

Webhooks can arrive in bursts (several reviews submitted at once, pushes in a row), and GitHub does not order them. The server reviews one pull request's head commit at a time, and only its newest review reports: a webhook for a head commit already waiting for a worker replaces the waiting review, one for a head commit under review cancels that review and is reviewed next, and a review that a newer one supersedes after it ran does not report. Each review reads the approvals when it runs, so the last review to report has seen the latest. Without a database this holds within one server process, and replicas behind a load balancer do not coordinate. With a database (`database_url`), replicas order their reports through it: a review reports only if no review of its pull request requested later has reported (for any head commit), one at a time per pull request. A review whose order cannot be recorded (the database is unavailable) reports anything but a pass as is, and a pass as `review could not be recorded; see the server logs`, so a review that may be stale never passes a pull request.

### Review pages

With `public_url` and `link_key` set, every review has a page, and the check runs the server creates link to it (as the check's details URL, and at the end of its summary). The page follows the newest review of the pull request's head commit: before any review it says none has been recorded yet; while the review is queued or running it shows its stage (waiting for a worker, reading the pull request, fetching rules files, matching rules to files, resolving approver targets, evaluating approvals file by file, publishing the check) and updates itself; once the review finishes it shows the verdict and the full decision path of every file (rules file → rule → approver target), the errors, and the event trace (the first 2,000 events). The page is self-contained (inline styles and scripts, nothing loaded from elsewhere) and readable without JavaScript, which only adds search, filters, and the live updates.

Live progress comes from the server running the review; another replica's page shows the review as running, from the database, until it finishes. Without a database a server shows only the reviews it queued or ran recently (its 32 most recently finished).

A page shows a repository's paths, approvers, and review errors, so it is served only for a link the server signed with `link_key`: anyone who can see the check run can open its page, and a link for any other pull request or commit is not found. A link does not expire; treat it like the check run it came from.

Each review also marks its check run in progress before it evaluates, so a pull request shows the review running rather than the previous result. With a database this is ordered like reports: a review never marks its pull request running after a newer review reported, and a review whose order is unknown does not mark it at all. A review cancelled because the server is stopping leaves its check in progress until the next review of the pull request.

### Database

With `database_url` set, the server keeps what it reads from GitHub that cannot go stale (base tree listings by commit, rules files by blob SHA) in PostgreSQL, shared by every replica, behind its in-memory cache, and records every verified webhook delivery and every review: its pull request and revisions, approvals and owners, the full review state, the outcome it published, the approver targets it evaluated, and its timings. The record is never read back to decide a review. A team's members, a user, and a pull request's reviews can change, so they are read from GitHub on every review; the last answer is kept with its ETag and used only when GitHub confirms it is unchanged (a `304 Not Modified`, which does not count against the rate limit). Cached rows are authenticated with `database_key`, which never reaches the database, and a rules file is checked against its SHA, so write access to the database alone cannot change the rules or team members a review sees. The server prunes the cache hourly (tree listings and team, user, and review responses after a week, rules files after a month; a pruned entry costs one more read from GitHub) and the ordering of a pull request after a month without reviews; the record of deliveries and reviews is kept. The server creates its tables at startup; while the project is in development, a schema change means recreating the database.

A pull request's changed files come from GitHub's comparison of the base and head commits the webhook names, never from the pull request's file listing, which GitHub answers for whatever the head is by then. A comparison listing as many files as GitHub lists (300), or none, is confirmed from the merge-base and head trees, with a warning that it is large when it was cut short; a base tree too large for one listing is listed a directory at a time. One that still cannot be listed completely errors rather than being reviewed incompletely.
