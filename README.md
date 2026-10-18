# Kit - A Modular Code Review Tool

## Architecture

### Root as Global Authority

A rule defined at the repository root directory (`/`) naturally acts globally because rules cascade downwards by default.

In the case that a file fails to meet any of the existing rules specified at the repository root, we still do not need a "global" configuration as we can reliably request the repository owner for review.

In the case an owner is inactive, the owner is removed from the list of possible approvers for a given rule.

If the resulting set of approvers for a given rule is empty, that rule is **skipped** — an error condition that records the skip in the review error list and forces the final review state to `error` — and evaluation falls back to the parent directory rules. If those recursively are not matched up until and including the repository root rules, we will request a review from the repository owner.

Warnings for these failed matches, ... will be added to the PR as a comment for visibility, and the accumulated error propagates out of the pipeline so the PR can be blocked even when a later rule or the owner fallback satisfies the file.

### Combination Strategies
To achieve logical completeness when combining rules from parent and subdirectories, the following combination operations are supported:

* `union`: If `/` requires `Group A` and `/src/` requires `Group B`, files inside `/src/` require **both** groups.
* `override`: Subdirectory rules completely discard parent rules for any matching pattern.
* `intersect`: If `/` requires `Group A` + `Group B`, and `/src/` requires `Group B` + `Group C`, files inside `/src/` require approval from `Group B`.

### Recurring Approver Targets

When the same approver target recurs across layers (union) or is shared across layers (intersect), the count is tracked per target occurrence and every occurrence must be satisfied independently against the same pool of approvers. If the root rule requires 1 approval from a target and the `/src` rule requires 2 from that same target, a single matching approval satisfies the root requirement while `/src` still needs one more.

### Unsatisfiable Rules and the Fallback Terminal State

If a file's requirement cannot be satisfied — no matching rule at all, or every matching rule prunes down to an empty approver set — the engine falls back to the repository owners. If that fallback is itself empty (no active repository owners exist at all), the rule is **unsatisfiable**: the file and the overall evaluation terminate with an `error` status rather than remaining `unresolved`. The error propagates out of the approval pipeline so the PR can be blocked, and a warning is added to the PR as a comment for visibility.

### Branch Targets

The review rules for a branch target are determined from the main branch of the repository.

## Configuration File Schema

Each directory in the repository can host a `KIT` file to localize approval rules.

```yaml
# RegEx matching relative to the CURRENT directory location
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

---

## Rule Combination

When evaluating a changed target file (e.g., `/src/api/v1/auth.go`), the engine constructs a chain of rules starting from the repository root.

```
[ Root /KIT ]   ──(Union / Override / Intersect)──>  [ /src/KIT ]  ──>  [ Target File ]
```

### Additional Rules
* **Scope Exclusion:** A rule block is entirely discarded if `current_directory_only: true` and the evaluated file resides deeper in a nested subdirectory.
* **The Break Signal:** If any configuration file along the vertical path contains `ignore_parent_dirs: true`, the evaluation chain truncates instantly, throwing away all parent settings above that directory level.

---

## Review Algorithm

The approve phase is a chain of independent layers. Each layer consumes only the state of the layer below it and returns only its own state, so every requirement can be traced to exactly one type and one `Execute` command:

| Layer | Type | Command | State it returns |
|---|---|---|---|
| Fetch | `Fetcher` (interface), `LocalFetcher` | `Execute()` | repository root + parsed `KIT` rules |
| Sift | `RuleChain` builder | `GetFileRuleChain` | per-file ordered rule layers |
| Chain | `RuleChain` | `Execute` | `ReviewState` (consumes only `LayerState`) |
| Layer | `RuleLayer` | `Execute` | `LayerState` incl. per-rule states and the combined target footprint (`Required`) for the next layer |
| Rule | `Rule` | `Execute` | `RuleState` (consumes only target states) |
| Target | `RuleTarget` | `Execute` | `ApproverTargetState` |

State flows upward in one direction only: chain status derives from file status, file status derives from per-target states, layer status derives from its rule states, and rule status derives from its approver target states. No layer reads a state "above" itself.

### Author Self-Approval (`self_approve`)
The self-approval policy is a requirement of the rule and travels with every approver target that the rule produces. Every self-approval decision in the algorithm funnels through one predicate, `RuleTarget.contributes`, and only two edge cases exist:

* **The target is the author themselves.** If `self_approve: false` and a rule directly targets the PR author, the target is `inactive` and never satisfiable, even if the author has approved.
* **The author is a member of a resolved group.** Inactive members are ignored. If `self_approve: false`, the author's approval is sliced out of the group's eligible members: the group can still be satisfied by every other member, and the author never appears in `ResolvedBy`. If the group's only active member is the author, the whole target prunes away with the rule.
* Setting `self_approve: true` counts the author's approval exactly like any other member's, in both cases (one approval, counted once).
* The default is `false`: authors can never fulfill their own requirements unless a rule explicitly allows it.

### Approver Targets, Resolution, and Pruning
Every target occurrence is evaluated independently against the same pool of current approvers. Targets resolve to a member set through the configured `ApproverResolver` (individual targets: 1:1 by default; group targets: expanded to their members):

* A target whose members are all inactive prunes away; the rule then falls back to earlier rules or the parent directory (see above).
* A target that cannot be resolved to any member at all is `unresolved`, which escalates the containing rule, layer, and file to `unresolved` and forces the overall status to `error`.
* A group is `satisfied` once `required_count` of its eligible, active resolved members appear among current approvers.
* Because each occurrence is independent, recurring targets under `union` are counted once per occurrence (see Recurring Approver Targets).

### Rule Fallback
Pruning drops a target only when *zero* active eligible members remain. A rule left with zero active targets is **skipped** — an error condition: the skip is marked on the rule's own state so the hierarchical breakdown remains a complete trace, and a review error is accumulated, which forces the final review state to `error` even when preceding rules, parent rules, or the repository-owner fallback afterwards still satisfy the file.

---

## PR Evaluation

The runtime application processes PR approval status utilizing a three-tiered pipeline:

```
[PR Webhook Triggered]
          │
          ▼
   1. FETCH PHASE  ──► Walk tree from target files up to root (respecting ignore_parent_dirs)
          │
          ▼
   2. SIFT PHASE   ──► Match target files against glob patterns; process configuration scoping
          │
          ▼
   3. APPROVE PHASE ──► Compute final approval metrics, check self-approval rights, verify signatures
```

### Phase Breakdown

#### Phase 1: Fetch
1. Parse the incoming GitHub PR payload to isolate all modified file pathways.
2. For each unique file path, systematically map the trajectory from its local leaf directory vertically upward to the root repository level.
3. Read the `KIT` files along the path sequentially.
4. If a file specifies `settings.ignore_parent_dirs: true`, halt tree traversal immediately.

#### Phase 2: Sift
1. Traverse the collected configurations chronologically from Top to Bottom (Root down to Leaf).
2. Assess every localized rule block against the targeted file:
   * Skip the rule block completely if `current_directory_only: true` and the file is nested.
   * Evaluate the target path against the glob `pattern` relative to the current file node.
3. If a pattern matches, reference the file's `combine_strategy`:
   * If `override`, purge all rule criteria accumulated from higher ancestral folders for this pattern space and insert the current criteria.
   * If `union`, append the rule constraints directly into the active evaluation array.
   * If `intersect`, keep only the criteria present in both the parent accumulation and the current rule.

#### Phase 3: Approve
1. Extract the unique GitHub usernames of the PR Author and all users who submitted an explicit "Approved" state review.
2. For each final rule block tied to an asset:
   * Resolve each `approvers` target to its member set (querying live organization team memberships if applicable).
   * Apply the self-approval policy: if `self_approve` is `false`, slice the PR Author out of the eligible approver matching array.
   * Increment counts when an active approver matches a member identity target.
   * If a rule's targets have all been pruned away (inactive members, excluded author, or unresolvable targets), the rule is **skipped and the skip is recorded as an error**; it still falls back to the preceding rule's criteria, but the final overall state terminates `error` regardless of that fallback.
3. Mark a rule block as **Satisfied** once each target's internal `actual_count >= required_count`.
4. Fire a passing green status payload back to the GitHub Status API if all rules across all files resolve to a **Satisfied** state.
