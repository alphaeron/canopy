package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-git/go-git/v6"
	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/tui"
)

// kitfilesCmd groups file-level inspection commands.
var kitfilesCmd = &cobra.Command{
	Use:   "kitfiles",
	Short: "Inspect KIT files in the repository",
}

var kitfilesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every KIT file in the repository's working tree",
}

// runKitfilesList is kitfilesListCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runKitfilesList(cmd *cobra.Command, args []string) error {
	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, kitfilesRoute(valuesFrom(cmd, args)))
	}
	files, err := listKitFiles()
	if err != nil {
		return err
	}
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, map[string][]string{"kit_files": files})
	default:
		for _, f := range files {
			fmt.Println(f)
		}
	}
	return nil
}

// listKitFiles lists every KIT file in the working tree, the
// repository-root KIT first.
func listKitFiles() ([]string, error) {
	s, err := openKitSet()
	if err != nil {
		return nil, err
	}
	return s.paths(), nil
}

func init() {
	kitfilesListCmd.RunE = runKitfilesList
	kitfilesCmd.AddCommand(kitfilesListCmd)
	rootCmd.AddCommand(kitfilesCmd)
}

// getRepo opens the git repository at the current working directory, or the
// nearest parent that contains one. It is the single repository entry point
// for every CLI command; the returned wrapper carries FindFiles and other
// helpers that the commands share.
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
