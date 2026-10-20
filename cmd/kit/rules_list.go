package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesListCmd lists the rules stored in a KIT file. Without --file it targets
// the repository-root KIT. --all lists every tracked KIT file's rules in one
// grouped render.
var rulesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the rules in a KIT file",
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		all, _ := cmd.Flags().GetBool("all")
		mode, _ := resolveOutputMode(cmd)

		if all {
			repo, err := getRepo()
			if err != nil {
				return err
			}
			grouped, err := allKitContent(repo)
			if err != nil {
				return err
			}
			return renderAllKitFiles(mode, grouped)
		}

		k, err := loadKitFileContent(kitFilePathFor(filePath))
		if err != nil {
			return err
		}
		return renderKitFileRules(mode, k)
	},
}

// loadKitFileContent reads a KIT file by path, falling back to the repository
// root if it is not found on disk (so that a worktree-relative path works
// from any subdirectory).
func loadKitFileContent(path string) (*kitfile.KitFile, error) {
	k, err := kitfile.LoadKitFile(path)
	if err != nil {
		return nil, fmt.Errorf("load KIT file %q: %w", path, err)
	}
	return k, nil
}

func renderKitFileRules(mode outputMode, k *kitfile.KitFile) error {
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, k.Rules)
	case modeTUI:
		rows := make([]tui.Row, 0, len(k.Rules))
		for _, r := range k.Rules {
			rows = append(rows, tui.Row{Title: r.Pattern, Subtitle: r.Name, Data: r})
		}
		if _, err := tui.RunListView(rows); err != nil {
			return fmt.Errorf("render list: %w", err)
		}
		return nil
	default:
		for _, r := range k.Rules {
			fmt.Printf("%s\t%s\n", r.Pattern, r.Name)
		}
		return nil
	}
}

func renderAllKitFiles(mode outputMode, grouped map[string]kitfile.Rules) error {
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, grouped)
	case modeTUI:
		var rows []tui.Row
		for file := range grouped {
			for _, r := range grouped[file] {
				rows = append(rows, tui.Row{Title: r.Pattern, Subtitle: file, Data: r.Pattern})
			}
		}
		if _, err := tui.RunListView(rows); err != nil {
			return fmt.Errorf("render list: %w", err)
		}
		return nil
	default:
		for file := range grouped {
			fmt.Printf("%s\n", file)
			for _, r := range grouped[file] {
				fmt.Printf("  %s\t%s\n", r.Pattern, r.Name)
			}
		}
		return nil
	}
}

func init() {
	rulesListCmd.Flags().String("file", "", "target a specific KIT file")
	rulesListCmd.Flags().Bool("all", false, "list rules from every KIT file in the repository")
	rulesCmd.AddCommand(rulesListCmd)
}
