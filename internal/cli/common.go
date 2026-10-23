package cli

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/platform/local"
	"github.com/alphaeron/kit/internal/review"
)

// openEngine builds the match engine every command evaluates with: the
// local platform over the repository containing the current directory. It
// is the one place the CLI chooses a platform, so reviewing a change on a
// hosted platform from the CLI only changes this function. log may be nil
// (slog.Default()); the TUI passes quietLog.
func openEngine(log *slog.Logger) (*review.Engine, error) {
	_, engine, err := openLocal(log)
	return engine, err
}

// openLocal is openEngine with the repository it reads. The engine reads the
// repository as a snapshot (see gitrepo.Repository), fetching its rules
// once, so the views one page builds from it share one read of the
// working tree.
func openLocal(log *slog.Logger) (*gitRepo, *review.Engine, error) {
	repo, err := getRepo()
	if err != nil {
		return nil, nil, err
	}
	return repo, review.New(local.NewSource(repo, log), log), nil
}

// rulesFileFlag registers the shared --file flag on a command that changes a
// rules file. The value is resolved by rulesSet.resolve.
func rulesFileFlag(cmd *cobra.Command, usage string) {
	cmd.Flags().String("file", "", usage+" (path from the repository root, or its directory)")
}

// outputMode is the resolved output style for a command invocation. The CLI
// picks one of: --json emits JSON, a TTY renders a bubbletea TUI, and
// otherwise a plain table. Resolving once avoids every subcommand re-deriving
// the same policy.
type outputMode int

const (
	modeJSON outputMode = iota
	modeTUI
	modeTable
)

// resolveOutputMode inspects the command's --json flag and then the attached
// writer. It mirrors the Interactive test against os.Stdout (where bubbletea
// will render) and is the single decision point across all subcommands.
func resolveOutputMode(cmd *cobra.Command) (outputMode, error) {
	jsonFlag, err := cmd.Flags().GetBool("json")
	if err != nil {
		return modeTable, nil
	}
	if jsonFlag {
		return modeJSON, nil
	}
	if isTerminal(os.Stdout) {
		return modeTUI, nil
	}
	return modeTable, nil
}
