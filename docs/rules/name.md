# name

**Type:** string · **Default:** none

A human-readable name for the rule, shown wherever the rule is: in the CLI's output, in the review page's decision path, and in validation errors. It also names the rule as a scope: `canopy match --scope "<name>"` matches only files the named rule covers.

```yaml
- '.*\.sql':
    name: Database migrations
    approvers:
      - target: acme/dba
        required_count: 1
```

A rule without a name is referred to by its pattern and where it is declared, such as `.*\.sql in db/RULES[0]`.
