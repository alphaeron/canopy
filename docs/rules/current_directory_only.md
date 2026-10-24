# current_directory_only

**Type:** boolean · **Default:** `false`

When `true`, the rule applies only to files directly in its `RULES` file's directory, not to files in subdirectories. By default a rule cascades to every directory below it.

```yaml
# docs/RULES
- '.*\.md':
    name: Top-level docs
    current_directory_only: true   # docs/*.md, but not docs/guides/*.md
    approvers:
      - target: acme/tech-writers
        required_count: 1
```
