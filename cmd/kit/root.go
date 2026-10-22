package main

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/config"
	"github.com/alphaeron/kit/internal/gitutil"
	"github.com/alphaeron/kit/internal/tui"
)

// gitRepo is the repository wrapper the CLI's commands consume. It is an
// alias so the import list stays explicit and the type is documented at the
// point where it is first used.
type gitRepo = gitutil.KitGitRepository

var cfgFile string

// rootCmd is the base cobra command. All subcommands attach themselves via
// their own init().
var rootCmd = &cobra.Command{
	Use:   "kit",
	Short: "Kit code review CLI",
	Long: `The Kit CLI allows for checking code ownership using the local branch
contents before opening an official Pull Request.

The local branch KIT file contents are used rather than a remote main
branch. Every command runs against the local repository; the same analysis
is exposed by the kitweb REST server against a pull request (which is a
separate binary, not a subcommand here).`,
	// PersistentPreRunE runs after cobra has finished parsing every flag for
	// the command tree, making --config available. It is the single moment
	// configuration is loaded, so the resolved set of KIT_* environment
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
		// The kit CLI is a local tool: it does not need the GitHub app
		// credentials, but loading them is harmless and keeps the two
		// binaries (kit and kitweb) on the same resolution path.
		_ = cfg.GitHubAppID
		return nil
	},
}

// Bare `kit` on a terminal opens the TUI's home page, which lists every
// command's page; piped or with --json it prints help as it always has.
func runRoot(cmd *cobra.Command, args []string) error {
	if mode, _ := resolveOutputMode(cmd); mode != modeTUI {
		return cmd.Help()
	}
	return tui.Run(cmd.Context(), tui.Config{Routes: appRoutes()})
}

// Execute adds all child commands to the root command and sets flags
// appropriately. This is called by the main entry point. It only needs to
// happen once to the rootCmd.
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	rootCmd.RunE = runRoot
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $KIT_CONFIG or $HOME/.kit.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose (debug-level) logging; overrides the config file and KIT_VERBOSE")
	rootCmd.PersistentFlags().Bool("json", false, "emit the result as JSON instead of rendering a table or TUI")
}

// verbose is the --verbose / -v flag: when true, debug-level slog output is
// enforced after config.Load has already run, so the flag always wins.
var verbose bool
