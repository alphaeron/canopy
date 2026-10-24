# pattern

**Type:** regular expression (the rule's mapping key) · **Required**

Each rule is a YAML mapping whose key is its pattern: a regular expression (Go's RE2 syntax) matched against each file's full repository-relative path, such as `svc/api/main.go`, whichever directory the `RULES` file is in. A rule only applies to files in its own directory and the directories below it, so a pattern in `svc/api/RULES` never matches files outside `svc/api/`.

```yaml
# svc/api/RULES
- '.*\.go':          # every Go file under svc/api/
    approvers:
      - target: acme/api-team
        required_count: 1
- 'svc/api/v1/.*':   # everything under svc/api/v1/
    approvers:
      - target: acme/api-v1-owners
        required_count: 1
```

Quote patterns with single quotes. YAML treats a backslash in double quotes as an escape, so `".*\.go"` is a parse error.

The same pattern may appear more than once, even in one file. A file's rules apply in order, each combining with the ones before it (see [combine_strategy](combine_strategy.md)). `canopy validate` reports a pattern that is not a valid regular expression.
