# ignore_parent_dirs

**Type:** boolean · **Default:** `false`

When `true`, a file this rule matches ignores every rule matched in the directories above this `RULES` file, whatever their [combine_strategy](combine_strategy.md). This directory acts as the file's new root: its rules, and any in directories below it, still apply.

```yaml
# vendor/RULES
- '.*':
    name: Vendored code
    ignore_parent_dirs: true   # the root's rules do not apply here
    approvers:
      - target: acme/dependency-owners
        required_count: 1
```

It takes effect only for files the rule matches. Other files in the directory keep their parents' rules.
