# Installing Canopy

Each [GitHub release](https://github.com/alphaeron/canopy/releases) of the `canopy` CLI attaches, for amd64 and arm64 (macOS: one universal binary for both):

| Platform | Installer | Archive |
|---|---|---|
| Windows | `canopy_<version>_windows_<arch>.msi`: installs under Program Files and adds it to `PATH` | `.zip` |
| macOS | `canopy_<version>_darwin_all.pkg`: installs `/usr/local/bin/canopy` | `.tar.gz` |
| Linux | `.deb`, `.rpm`, `.apk` (install `/usr/bin/canopy`), and `.snap` | `.tar.gz` |

`checksums.txt` lists every file's SHA-256. Nothing is signed yet, so Windows SmartScreen and macOS Gatekeeper warn about the installers (on macOS, open the `.pkg` from Finder with Control-click → Open), and the snap, which is not in the Snap Store, installs with `sudo snap install --dangerous --classic ./canopy_<version>_linux_<arch>.snap`. `canopy --version` prints the installed version.

## What each package installs

| Platform | Program | Help and manual | Shell completion |
|---|---|---|---|
| Windows (MSI) | `canopy.exe` under `Program Files\Canopy`, on `PATH` | `canopy /?`, `canopy -?`, or `canopy help <command>`; the [documentation site](https://alphaeron.github.io/canopy/), linked from Windows' installed apps list | PowerShell: `Program Files\Canopy\completions\canopy.ps1` |
| macOS (pkg) | `/usr/local/bin/canopy` | `man canopy`, one page per command (`man canopy-rules-add`), plus `man 5 canopy-rules`, `man 5 canopy-config`, and `man 7 canopy-approval` | zsh, bash, and fish, under `/usr/local` |
| Linux (deb, rpm, apk) | `/usr/bin/canopy` | The same man pages, under `/usr/share/man` | bash, zsh, and fish, where each distribution's shells load packaged completions |
| Linux (snap) | `canopy` | `canopy --help` (a snap cannot install man pages) | bash |

zsh on macOS loads completions only once `compinit` runs, which most zsh setups (such as Oh My Zsh) already do; otherwise add `autoload -Uz compinit && compinit` to `~/.zshrc`. bash on macOS completes once Homebrew's `bash-completion@2` is installed and set up. To enable PowerShell completion, add this line to your PowerShell profile (`notepad $PROFILE`):

```powershell
. "$env:ProgramFiles\Canopy\completions\canopy.ps1"
```

The archives carry the same files: the man pages under `man/` and the completions under `completions/` (the Windows zip: `completions\canopy.ps1`). Without an installer, `canopy completion <shell>` prints any shell's completion script; `canopy completion <shell> --help` explains how to load it.

## From source


```sh
go install ./cmd/canopy      # the CLI
go build ./cmd/canopyweb     # the review server
```

See [Review server](server.md) for running `canopyweb`.
