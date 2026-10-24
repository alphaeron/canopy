# combine_strategy

**Type:** `union`, `override`, or `intersect` · **Default:** `override`

How the rule combines with the rules already accumulated for a file: those from parent directories, and earlier rules in the same file. Rules are evaluated from the repository root down, so each rule combines with everything above it.

| Value | The file requires |
|---|---|
| `union` | The accumulated approvals **and** this rule's |
| `override` | Only this rule's approvals, discarding what was accumulated |
| `intersect` | Only the approver targets both the accumulated rules and this rule name |

```yaml
# RULES (the root)
- '.*':
    approvers:
      - target: acme/maintainers
        required_count: 1

# src/RULES
- '.*\.go':
    combine_strategy: union    # Go files under src/ need maintainers and the Go team
    approvers:
      - target: acme/go-team
        required_count: 1
```

With `intersect`, if the root requires groups A and B and `src/` requires B and C, files under `src/` require only B.

Leaving `combine_strategy` out overrides, so a nested rule replaces its parents' requirements unless it says otherwise. To discard parent rules whatever their strategy, use [ignore_parent_dirs](ignore_parent_dirs.md).
