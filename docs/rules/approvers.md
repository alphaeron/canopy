# approvers

**Type:** list of approver targets · **Required**

The approvals a file matching the rule needs. Each entry names a target and how many of its members must approve:

| Key | Type | Meaning |
|---|---|---|
| `target` | string | A username (`alice`), or a group as `org/team` (on GitHub, a team, expanded to its members) |
| `required_count` | integer, at least 1 | How many of the target's members must approve |

```yaml
- '.*\.go':
    approvers:
      - target: acme/backend-team
        required_count: 2
      - target: acme/security-leads
        required_count: 1
```

Every target must be satisfied: this rule needs two approvals from the backend team and one from the security leads. Names are compared as the platform compares them. GitHub logins are case-insensitive, so `Alice` matches an approval by `alice`.

## How targets are resolved

- **Inactive members are ignored.** On GitHub, a suspended or unknown account is inactive. A target whose members are all inactive drops out of the rule.
- **The author is excluded** unless the rule sets [self_approve](self_approve.md).
- **A rule left with no targets is skipped.** The file falls back to earlier rules, then its parent directories' rules, then the repository owners. The skip is still recorded as an error, so the review fails rather than passing on a weaker requirement.
- **A target that cannot be resolved at all** (the platform fails, or is rate limiting) never drops out: the review errors instead.
- **Each occurrence counts on its own.** When rules combine with `union` and two of them name the same target, each occurrence must be satisfied separately against the same approvals. A root rule needing one approval from a team and a `src/` rule needing two from it need two in all, not three.

See [How a review decides](../review.md) for the full algorithm.
