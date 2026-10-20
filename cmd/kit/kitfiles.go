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
	Short: "List every KIT file in the repository at the current HEAD",
	RunE: func(cmd *cobra.Command, args []string) error {
		repo, err := getRepo()
		if err != nil {
			return err
		}
		var files []string
		if repo.HasCommits() {
			ref, err := repo.Head()
			if err != nil {
				return fmt.Errorf("read HEAD: %w", err)
			}
			files, err = repo.FindFiles(`KIT`, ref, false)
			if err != nil {
				return fmt.Errorf("find KIT files: %w", err)
			}
		}
		mode, _ := resolveOutputMode(cmd)
		switch mode {
		case modeJSON:
			return tui.JSONEncoder(os.Stdout, map[string][]string{"kit_files": files})
		case modeTUI:
			rows := make([]tui.Row, 0, len(files))
			for _, f := range files {
				rows = append(rows, tui.Row{Title: f, Subtitle: "KIT file", Data: f})
			}
			if _, err := tui.RunListView(rows); err != nil {
				return fmt.Errorf("run list: %w", err)
			}
		default:
			for _, f := range files {
				fmt.Println(f)
			}
		}
		return nil
	},
}

func init() {
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
