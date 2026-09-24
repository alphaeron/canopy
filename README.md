# Kit - A Modular Code Review Tool

## Architecture

### Root as Global Authority

A rule defined at the repository root directory (`/`) naturally acts globally because rules cascade downwards by default.

In the case that a file fails to meet any of the exitsing rules specified at the repository root, we still do not need a "global" configuration as we can reliably request the repository owner for review.

In the case an owner is inactive, the owner is removed from the list of possible approvers for a given rule.

If the resulting set of approvers for a given rule is empty, we will fall back to the parent directory rules.  If those recursively are not matched up until and including the repository root rules, we will request a review from the repository owner.

Warnings for these failed matches, ... will be added to the PR as a comment for visibility.

### Combination Strategies
To achieve logical completeness when combining rules from parent and subdirectories, the following combination operations are supported:

* `union`: If `/` requires `Group A` and `/src/` requires `Group B`, files inside `/src/` require **both** groups.
* `override`: Subdirectory rules completely discard parent rules for any matching pattern.
* `intersect`: If `/` requires `Group A` + `Group B`, and `/src/` requires `Group B` + `Group C`, files inside `/src/` require approval from `Group B`.

### Branch Targets

The review rules for a branch target are determined from the main branch of the repository.

## Configuration File Schema

Each directory in the repository can host a `KIT` file to localize approval ruls.

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
[ Root /owners.yaml ]   ──(Union / Override / Intersect)──>  [ /src/owners.yaml ]  ──>  [ Target File ]
```

### Additional Rules
* **Scope Exclusion:** A rule block is entirely discarded if `current_directory_only: true` and the evaluated file resides deeper in a nested subdirectory.
* **The Break Signal:** If any configuration file along the vertical path contains `ignore_parent_dirs: true`, the evaluation chain truncates instantly, throwing away all parent settings above that directory level.

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
3. Read the `owners.yaml` files along the path sequentially.
4. If a file specifies `settings.ignore_parent_dirs: true`, halt tree traversal immediately.

#### Phase 2: Sift
1. Traverse the collected configurations chronologically from Top to Bottom (Root down to Leaf).
2. Assess every localized rule block against the targeted file:
   * Skip the rule block completely if `current_directory_only: true` and the file is nested.
   * Evaluate the target path against the glob `pattern` relative to the current file node.
3. If a pattern matches, reference the file's `combine_strategy`:
   * If `override`, purge all rule criteria accumulated from higher ancestral folders for this pattern space and insert the current criteria.
   * If `union`, append the rule constraints directly into the active evaluation array.

#### Phase 3: Approve
1. Extract the unique GitHub usernames of the PR Author and all users who submitted an explicit "Approved" state review.
2. For each final rule block tied to an asset:
   * Validate self-approval rules: If `can_author_self_approve` is `false`, slice the PR Author out of the valid approver matching array.
   * Cross-reference the active approved user list against the designated `approvers` targets (querying live organization team memberships if applicable).
   * Increment counts when an active approver matches an identity target.
3. Mark a rule block as **Satisfied** once its internal `actual_count >= required_count`.
4. Fire a passing green status payload back to the GitHub Status API if all rules across all files resolve to a **Satisfied** state.
