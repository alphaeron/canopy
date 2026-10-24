# Canopy - A Modular Code Review Tool

Canopy decides who must approve a change from `RULES` files kept in the repository. It ships as two binaries: `canopy`, a CLI that previews and edits the rules of a local checkout, and `canopyweb`, a review server that reviews pull requests on a hosted platform (GitHub) and reports the outcome back as a status.

## Installation

Each [GitHub release](https://github.com/alphaeron/canopy/releases) attaches an MSI for Windows, a pkg for macOS, and deb, rpm, apk, and snap packages for Linux, besides plain archives. See [Installing Canopy](docs/install.md) for each platform's details, or install from source:

```sh
go install ./cmd/canopy
```

## Usage

Every `canopy` command runs against the git repository containing the current directory. Run bare on a terminal, `canopy` opens a TUI listing every command; `--json` prints any command's result as JSON instead, and `-v` enables debug logging. `canopy help <command>` and `--help` print a command's help (on Windows, so do `-?` and `/?`), and the installers add man pages (`man canopy`) and shell completions; see [Installing Canopy](docs/install.md).

| Command | Purpose |
|---|---|
| `canopy init [path]` | Create a `RULES` file, optionally with a first rule (`--pattern`, `--approver 'target count'`, ...) |
| `canopy rules list` / `files` / `get` | List the rules, list the `RULES` files, or show the rules for files or patterns |
| `canopy rules add` / `edit` / `remove` | Change a rule in a `RULES` file |
| `canopy match` | Match files against the rules (`--changed`, `--scope`, `--diff`, `--by-scope`) |
| `canopy coverage` | Report how much of the repository the rules cover |
| `canopy validate` | Validate `RULES` files as the review's platform does |
| `canopy review [pattern...]` | Evaluate who must approve each file, given `--author`, `--approver`, `--owner`, and `--label`; the exit status is the verdict |

## Documentation

| Document | Contents |
|---|---|
| [Overview](docs/index.md) | What Canopy is, a first look at RULES files, and how it compares to other code review tooling |
| [Installing Canopy](docs/install.md) | Release installers and archives, and building from source |
| [RULES files](docs/rules.md) | The `RULES` file format and how rules combine across directories, with a page per key under [docs/rules/](docs/rules/) |
| [How a review decides](docs/review.md) | Fallbacks, branch targets, blocking labels, self-approval, and the review pipeline |
| [Configuration](docs/configuration.md) | Every configuration key, for the CLI and the review server |
| [Review server](docs/server.md) | Running `canopyweb`: routes, the GitHub App, review pages, and the database |
| [Contributing](CONTRIBUTING.md) | Development tasks, adding a review platform, documentation, and releasing |

The documentation is also published at <https://alphaeron.github.io/canopy/>.
