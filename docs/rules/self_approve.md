# self_approve

**Type:** boolean · **Default:** `false`

Whether the change's author can satisfy this rule's requirements themselves. Authoring a change counts as the author's approval wherever `self_approve` allows it. On GitHub, where authors cannot approve their own pull requests, this is how the setting takes effect.

| | `self_approve: false` (default) | `self_approve: true` |
|---|---|---|
| A target that is the author | Never satisfied | Satisfied by the author |
| A group the author belongs to | The author is left out; the other members can still satisfy it | The author counts as one approval |

```yaml
- 'docs/.*':
    self_approve: true   # a docs team member's own change counts toward the docs team
    approvers:
      - target: acme/docs-team
        required_count: 1
```

If the author is the only active member of a group and the rule does not allow self-approval, the group drops out of the rule (see [approvers](approvers.md)). Files no rule covers fall back to the repository owners, and there the author's change never counts as an owner's approval.
