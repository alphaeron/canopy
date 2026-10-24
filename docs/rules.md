# RULES files

Each directory in the repository can host a `RULES` file to localize approval rules.

```yaml
# A regular expression matched against each file's full repository-relative
# path (e.g. "svc/api/main.go"), whichever directory this RULES file is in.
- '.*\.go':
    name: "Core Backend Review"

    # If true, blocks searching parent directories. This file acts as the new configuration root.
    ignore_parent_dirs: false

    # Controls how rules merge when multiple pattern matches collide across directories
    # Options: union, override, intersect; omitted, the rule overrides
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

## Keys

| Key | Purpose |
|---|---|
| [pattern](rules/pattern.md) | The regular expression a rule matches file paths against (the rule's mapping key) |
| [name](rules/name.md) | A name for the rule, shown in output and used as its scope |
| [approvers](rules/approvers.md) | The approver targets and how many approvals each must give |
| [combine_strategy](rules/combine_strategy.md) | How the rule combines with the rules above it: `union`, `override`, or `intersect` |
| [ignore_parent_dirs](rules/ignore_parent_dirs.md) | Ignore the rules of parent directories for the files this rule matches |
| [current_directory_only](rules/current_directory_only.md) | Apply only to files directly in this directory |
| [self_approve](rules/self_approve.md) | Whether the change's author can satisfy the rule |

## Rule Combination

When evaluating a changed target file (e.g., `/src/api/v1/auth.go`), the engine constructs a chain of rules starting from the repository root.

```
[ Root /RULES ]   ──(Union / Override / Intersect)──>  [ /src/RULES ]  ──>  [ Target File ]
```

### Additional Rules
* **Scope Exclusion:** A rule block is entirely discarded if `current_directory_only: true` and the evaluated file resides deeper in a nested subdirectory.
* **The Break Signal:** If a rule matching the file sets `ignore_parent_dirs: true`, the evaluation chain truncates there, throwing away the rules matched in every directory above it.

### Combination Strategies

To achieve logical completeness when combining rules from parent and subdirectories, the following combination operations are supported:

* `union`: If `/` requires `Group A` and `/src/` requires `Group B`, files inside `/src/` require **both** groups.
* `override`: Subdirectory rules completely discard parent rules for any matching pattern.
* `intersect`: If `/` requires `Group A` + `Group B`, and `/src/` requires `Group B` + `Group C`, files inside `/src/` require approval from `Group B`.
