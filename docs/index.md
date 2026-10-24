# Overview

Canopy decides who must approve a change. The rules live in the repository itself, as small `RULES` files beside the code they cover, and every pull request is reviewed against them. Nothing is configured in a central file or a settings page.

Most code review tools are monolithic. They keep one ownership file at the root that every team edits, or approval rules in a hosting platform's settings, evaluated by an engine tied to that one host. Canopy is modular from the files up:

- **Rules live where the code lives.** Any directory can have a `RULES` file, owned and changed by the team that owns the directory, through the same review as its code.
- **Each rule chooses how it combines with its parents.** A rule can add to the rules above it (`union`), replace them (`override`), or narrow them to the approvers they share (`intersect`). It can also stop inheritance altogether (`ignore_parent_dirs`).
- **The same engine runs everywhere.** `canopy review` in your terminal decides exactly as the review server does on a pull request, so you can see who must approve before you open one.
- **A complete CLI.** The alternatives below give you a file format and a pull request check. Canopy also has a command line for every part of the rules: `init` and `rules add`, `edit`, and `remove` to write them; `rules list`, `files`, and `get` to inspect them; `match` and `coverage` to see what they cover; `validate` to check them; and `review` to run the full review. Every command prints JSON with `--json`, and the installers add man pages and shell completions.
- **Platforms plug in.** The review algorithm never depends on where a change lives. GitHub is supported today, and each platform supplies the same small interfaces (see [Review platforms](../internal/platform/README.md)).

## RULES at a glance

A `RULES` file is a YAML list of rules. Each rule is a regular expression matched against repository-relative paths, and the approvals that files matching it need:

```yaml
# svc/api/RULES
- '.*\.go':
    name: API code
    combine_strategy: union   # require these approvals as well as the root's
    approvers:
      - target: acme/api-team # a GitHub team, or a username
        required_count: 2
```

A rule applies to the files in its own directory and every directory below it. Rules are evaluated from the root down, and each combines with the ones above it. [RULES files](rules.md) describes the format, with a page for each key under RULES reference.

## Preview a review

The CLI reads the working tree's `RULES` files, uncommitted changes included, so you can try rules before you propose them:

```sh
canopy match --changed                                   # which rules cover the files you changed
canopy review --changed --author carol --approver alice  # who must still approve them
```

`canopy review` exits zero only when the review passes, so it fits in scripts and pre-push hooks. On a pull request, the review server reports the same verdict as a check run, with a page that shows each file's full decision path (see [Review server](server.md)).

## Comparison of code review tooling

| | Canopy | GitHub | GitLab | Bitbucket | Kubernetes (Prow) |
|---|---|---|---|---|---|
| Where rules live | A `RULES` file in any directory | One `CODEOWNERS` file | One `CODEOWNERS` file, plus approval rules in project settings | Default reviewers and merge checks in repository settings | An `OWNERS` file in any directory |
| How nested rules combine | Chosen per rule: union, override, or intersect | The last matching pattern wins | The last matching pattern wins within each section | Not applicable: rules are set per repository or branch | Approvers are inherited from parent directories |
| Stop inheriting parent rules | `ignore_parent_dirs` | Not applicable | Not applicable | Not applicable | `no_parent_owners` |
| Approvals required | A count per approver target | One code owner | A count per section or approval rule | A minimum count per repository or branch | One approver for each file |
| Author self-approval | Chosen per rule (`self_approve`) | Not allowed | A project setting | Not allowed | An author who is an approver approves implicitly |
| A CLI to write, inspect, validate, and test rules | Yes: `init`, `rules`, `match`, `coverage`, `validate`, `review` | No | No | No | No |
| Preview a review locally before opening a pull request | `canopy review` | No | No | No | No |
| A pull request can change its own rules | No: proposed `RULES` files take effect once merged, and are validated first | No | No | Not applicable | No |
| Platforms | GitHub today; others plug in | GitHub | GitLab | Bitbucket | GitHub |
