// Package cli implements the command line: the cobra command tree, its
// scripted (table and JSON) output, and the TUI pages built from it.
package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/go-git/go-git/v6"
	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/brand"
	"github.com/alphaeron/kit/internal/config"
	"github.com/alphaeron/kit/internal/gitrepo"
	"github.com/alphaeron/kit/internal/tui"
)

// gitRepo is the repository wrapper the CLI's commands consume. It is an
// alias so the import list stays explicit and the type is documented at the
// point where it is first used.
type gitRepo = gitrepo.Repository

// getRepo opens the git repository at the current working directory, or the
// nearest parent that contains one. It is the single repository entry point
// for every CLI command.
func getRepo() (*gitRepo, error) {
	repo, err := git.PlainOpenWithOptions(".", &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			return nil, fmt.Errorf("not a git repository (or any of the parent directories)")
		}
		return nil, fmt.Errorf("open repository: %w", err)
	}
	return &gitRepo{Repository: *repo}, nil
}

var cfgFile string

// rootCmd is the base cobra command. All subcommands attach themselves via
// their own init().
var rootCmd = &cobra.Command{
	Use:   brand.Command,
	Short: brand.Name + " code review CLI",
	Long: "The " + brand.Name + ` CLI allows for checking code ownership using the local branch
contents before opening a pull or merge request.

The local branch's rules files are used, uncommitted changes included, so
the CLI previews the rules a branch proposes; the review server enforces the
rules of a pull request's base branch instead. Every command runs against the
local repository; the review server (kitweb, a separate binary) runs the same
review against a hosted change such as a pull request.`,
	// PersistentPreRunE runs after cobra has finished parsing every flag for
	// the command tree, making --config available. It is the single moment
	// configuration is loaded, so the resolved set of prefixed environment
	// variables, the config file, and the flag values feed every internal
	// package's slog calls immediately.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return err
		}
		if verbose {
			config.SetVerbose(true)
		}
		if cfg.Path != "" {
			slog.Debug("using config file", "component", "config", "path", cfg.Path)
		}
		return nil
	},
}

// The bare command on a terminal opens the TUI's home page, which lists every
// command's page; piped or with --json it prints help as it always has.
func runRoot(cmd *cobra.Command, args []string) error {
	if mode, _ := resolveOutputMode(cmd); mode != modeTUI {
		return cmd.Help()
	}
	return tui.Run(cmd.Context(), tui.Options{Routes: appRoutes(), Overview: repoOverview})
}

// Execute runs the command line and exits non-zero when the command fails.
// Cobra has already printed the error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.RunE = runRoot
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $"+brand.EnvConfig+" or $HOME/"+brand.ConfigName+".yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose (debug-level) logging; overrides the config file and "+brand.EnvPrefix+"VERBOSE")
	rootCmd.PersistentFlags().Bool("json", false, "emit the result as JSON instead of rendering a table or TUI")
}

// verbose is the --verbose / -v flag: when true, debug-level slog output is
// enforced after config.Load has already run, so the flag always wins.
var verbose bool
