package cli

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/local"
	"github.com/alphaeron/kit/internal/match"
)

// newLocalEngine builds the match engine bound to the current repository's
// worktree. The same engine type is driven by the REST server with a remote
// source; this constructor is the CLI's fixed point. log may be nil
// (slog.Default()); the TUI passes quietLog.
func newLocalEngine(repo *gitRepo, log *slog.Logger) *match.Engine {
	source := local.NewSource(repo, log)
	return match.New(source, log)
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
