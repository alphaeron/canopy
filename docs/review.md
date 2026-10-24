# How a review decides

How a review turns the `RULES` files (see [RULES files](rules.md)) and a change's approvals into a verdict.

## Root as Global Authority

A rule defined at the repository root directory (`/`) naturally acts globally because rules cascade downwards by default.

In the case that a file fails to meet any of the existing rules specified at the repository root, we still do not need a "global" configuration as we can reliably request the repository owner for review.

In the case an owner is inactive, the owner is removed from the list of possible approvers for a given rule. On GitHub, a user is inactive when GitHub reports their account suspended (`suspended_at` on their profile) or does not know them; a user whose lookup fails also counts as inactive, since only a user GitHub confirms is active.

If the resulting set of approvers for a given rule is empty, that rule is **skipped** — an error condition that records the skip in the review error list and forces the final review state to `error` — and evaluation falls back to the parent directory rules. If those recursively are not matched up until and including the repository root rules, we will request a review from the repository owner.

These failed matches are recorded in the review's errors, and the accumulated error propagates out of the pipeline so the review errors (blocking the merge) even when a later rule or the owner fallback satisfies the file.

## Recurring Approver Targets

When the same approver target recurs across layers (union) or is shared across layers (intersect), the count is tracked per target occurrence and every occurrence must be satisfied independently against the same pool of approvers. If the root rule requires 1 approval from a target and the `/src` rule requires 2 from that same target, a single matching approval satisfies the root requirement while `/src` still needs one more.

## Unsatisfiable Rules and the Fallback Terminal State

If a file's requirement cannot be satisfied — no matching rule at all, or every matching rule prunes down to an empty approver set — the engine falls back to the repository owners. If that fallback is itself empty (no active repository owners exist at all), the rule is **unsatisfiable**: the file and the overall evaluation terminate with an `error` status rather than remaining `unresolved`. The error propagates out of the approval pipeline so the review errors, blocking the merge.

## Branch Targets

The review rules for a pull request are read from its **base branch**, at the base revision the pull request is compared against (`pull_request.base.sha` on GitHub), not from the repository's default branch. A pull request into `release/1.x` is reviewed against `release/1.x`'s `RULES` files.

As with CODEOWNERS, a pull request cannot change its own requirements: `RULES` files it adds, modifies, or deletes take effect once it merges. The review server validates the ones it adds or modifies (as `canopy validate` does) at the pull request's head, failing the review on any violation (its report names the invalid files), so a broken `RULES` file cannot be merged.

The CLI does the opposite on purpose: it reads the working tree's `RULES` files, uncommitted changes included, to preview the rules a branch proposes. Otherwise `canopy review` judges a change as the server does: it validates the `RULES` files the working tree changes, failing the review on any violation, and its exit status is the review's verdict, zero only when the review passes. `canopy review --json` carries that verdict as its `outcome` (verdict, summary, items, and warnings) beside the review state. `canopy validate` validates `RULES` files as the review's platform does.

## Blocking Labels

A label named in `blocking_labels` (see [Configuration](configuration.md)) fails the change whatever its approvals, as an override does in PullApprove: the review still runs and its page shows the full decision path, but the outcome is `failed` with the summary `"do not merge" label found` (every blocking label it carries is named), ahead of an invalid proposed `RULES` file or missing approvals. Removing the label reviews the change again, and the review decides as usual. Labels are compared case-insensitively, ignoring surrounding space, so `Do Not Merge` on a pull request matches `do not merge` in the configuration; a label must match a configured name whole.

The blocking labels are deployment configuration, like the platform: a repository under review cannot change them. On GitHub the labels are read when the review runs, not taken from the webhook, since GitHub does not order webhooks and a label removed since must not block. `canopy review --label` simulates a change's labels against the same configuration.

## Repository Owner Fallback

When a file falls back to the repository owners, each owner becomes a reviewer for that file, required once. No rule covers the file, so no rule allows self-approval: authoring the change never counts as an owner's review, even when the author is an owner. A file that no rule covers at all is also reported as a warning (`N files covered by no rule; the repository owners review them`), which does not change the verdict. On GitHub the repository owner is the repository's owner account (`owner` in `owner/repo`).

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
   3. APPROVE PHASE ──► Resolve approver targets, check self-approval rights, compute final approval metrics
```

### Phase Breakdown

#### Phase 1: Fetch
1. List the change's file paths through the platform, counting a renamed file at both its old and new paths. (GitHub: the comparison of the base and head commits the webhook names, which does not carry the files itself; see [Database](server.md#database) for large pull requests. Local: the working tree's status against `HEAD`.)
2. For each unique file path, systematically map the trajectory from its local leaf directory vertically upward to the root repository level.
3. Read the `RULES` files along the path sequentially.
4. If a rule specifies `ignore_parent_dirs: true`, halt tree traversal immediately.

#### Phase 2: Sift
1. Traverse the collected configurations chronologically from Top to Bottom (Root down to Leaf).
2. Assess every localized rule block against the targeted file:
   * Skip the rule block completely if `current_directory_only: true` and the file is nested.
   * Match the rule's `pattern`, a regular expression, against the file's full repository-relative path.
3. If a pattern matches, reference the rule's `combine_strategy`:
   * If `override`, purge all rule criteria accumulated from higher ancestral folders for this pattern space and insert the current criteria.
   * If `union`, append the rule constraints directly into the active evaluation array.
   * If `intersect`, keep only the criteria present in both the parent accumulation and the current rule.

#### Phase 3: Approve
1. Read the change's author and approvers from the platform (or, locally, from `canopy review` flags), in the platform's canonical name form.
2. For each final rule block tied to an asset:
   * Resolve each `approvers` target to its member set through the platform (GitHub: `org/team-slug` targets expand to the team's members).
   * Apply the self-approval policy: if `self_approve` is `false`, slice the PR Author out of the eligible approver matching array.
   * Increment counts when an active approver matches a member identity target.
   * If a rule's targets have all been pruned away (inactive members, excluded author, or targets the platform does not know), the rule is **skipped and the skip is recorded as an error**; it still falls back to the preceding rule's criteria, but the final overall state terminates `error` regardless of that fallback.
3. Mark a rule block as **Satisfied** once each target's internal `actual_count >= required_count`.
4. Report the result back on the platform — passing only if all rules across all files resolve to a **Satisfied** state. GitHub: a completed check run named `canopy` on the pull request's head commit. Its title summarizes the outcome in one line, and its summary lists in full what the outcome is about (the approvers still needed, or the invalid `RULES` files) and any warnings, such as files no rule covers or a very large pull request. Its conclusion is `success` when the review is satisfied, `action_required` while approvals are missing, and `failure` when the pull request carries a blocking label, proposes an invalid `RULES` file, or the review could not produce a valid result: an internal `error` status (a GitHub failure, a listing too large to evaluate, a skipped or unsatisfiable rule) or a review that could not run or timed out; the title tells these apart. Each report is a new check run, and GitHub shows the latest. Require the `canopy` check in branch protection to block merging until it succeeds. The review never comments on the pull request.

On GitHub, approvals are each reviewer's latest decisive review: a later change request or dismissal cancels an approval, and comments change nothing. An approval made before the latest push still counts. The author's own reviews are ignored here: authoring the pull request is the author's approval, and `self_approve` decides whether it counts.
