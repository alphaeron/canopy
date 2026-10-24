# Contributing

The `Makefile` wraps the common tasks: `make test`, `make lint` (golangci-lint), `make e2e` (each hosted platform's end-to-end tests), `make smoke` and `make smoke-serve` (build each binary and exercise it), `make deadcode`, `make vulncheck`, and `make doc` / `make doc-serve` for the package documentation. To add a review platform, see [internal/platform/README.md](internal/platform/README.md).

## Documentation

Each kind of documentation has one source, and everything else is generated from it:

| Source | Generated |
|---|---|
| The cobra commands in `internal/cli` (`Short`, `Long`, flags) | `canopy --help`, one `canopy-<command>(1)` man page per command, the shell completions, and the site's CLI reference |
| User documents in [docs/](docs/), starting from [docs/index.md](docs/index.md) | The site's guide; `rules.md`, `configuration.md`, and `review.md` are also `canopy-rules(5)`, `canopy-config(5)`, and `canopy-approval(7)` |
| One page per `RULES` key in [docs/rules/](docs/rules/) | The site's RULES reference, and the KEYS section of `canopy-rules(5)`. docgen fails unless every key `internal/rulesfile` parses has a page, so a new key cannot ship undocumented |
| `internal/docgen/landing.tmpl` | The site's front page (`index.html`). It is HTML, not Markdown, because its layout is the content; the README stays GitHub's front page |
| Developer guides beside the code they describe, such as [internal/platform/README.md](internal/platform/README.md), and this file | The site's Developers section |
| Go doc comments | `make doc` / `make doc-serve`; not published |

`make docs` runs `internal/docgen`, which writes the man pages, completions, and site into `build/docs`; open `build/docs/site/index.html` to preview the site, which is styled with the review page's own `internal/reviewview/theme.css`. Besides the pages, docgen writes each page's "On this page" contents, previous and next links, an edit link, a 404 page, and `search.js`, the search box's script with an index of every page. A new document is published once it is listed in `internal/docgen/pages.go`. Links between documents are relative Markdown links, so they work on GitHub, and docgen points them at the site's pages (or, in a man page, at the other man page).

The release generates the same files (`.goreleaser.yaml`'s `before` hook) and packages them per platform (see [Installing Canopy](docs/install.md)), and `.github/workflows/pages.yml` publishes the site to <https://alphaeron.github.io/canopy/> on every change to its sources on `main`. Pages must be set to deploy from GitHub Actions in the repository's settings. The workflow generates the site on each change, rather than the generated HTML being committed, so the published site always matches `main` and nobody has to remember to regenerate it.

## Releasing

Pushing a `v*` tag (e.g. `git tag v1.2.0 && git push origin v1.2.0`) runs `.github/workflows/release.yml`, which releases the CLI; the review server is released separately. [GoReleaser](https://goreleaser.com) (`.goreleaser.yaml`) builds the archives, the universal macOS binary, and the deb, rpm, apk, and snap packages into a draft GitHub release, stamping the tag's version into `canopy --version`. Later jobs build the Windows MSI (`scripts/package-msi.ps1`, with WiX) and the macOS pkg (`scripts/package-pkg.sh`), attach them, add them to `checksums.txt`, and publish the release. A tag with a pre-release suffix (`v1.2.0-rc.1`) is published as a pre-release.

`make release-check` validates `.goreleaser.yaml`, and `make snapshot` builds the GoReleaser artifacts into `dist/` without publishing (the snap only where `snapcraft` is installed). The CLI's Windows-only behavior (the `-?` and `/?` help switches, in `internal/cli/args_windows.go`) is compiled only into Windows builds, so check it with `GOOS=windows go vet ./internal/cli` from other platforms.
