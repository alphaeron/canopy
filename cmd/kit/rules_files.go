package main

import (
	"fmt"
	"os"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/tui"
)

// rulesFilesCmd is the reverse lookup of rules get: given a rule's pattern
// (or any file regex), it lists the files that pattern matches at a given
// file-set revision. The revision defaults to HEAD and is resolved through
// gitutil.FindFilesAt before any file walk, so a bad revision errors here,
// before the CLI tries to enumerate the tree and silently produce an empty
// result.
var rulesFilesCmd = &cobra.Command{
	Use:         "files <pattern>",
	Short:       "List the files a rule pattern matches",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"arg": "regular expression of file paths"},
}

// runRulesFiles is rulesFilesCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runRulesFiles(cmd *cobra.Command, args []string) error {
	pattern := args[0]
	revision, _ := cmd.Flags().GetString("revision")

	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, rulesFilesRoute(valuesFrom(cmd, args)))
	}
	files, err := findPatternFiles(pattern, revision)
	if err != nil {
		return err
	}
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, map[string][]string{"pattern": {pattern}, "files": files})
	default:
		for _, f := range files {
			fmt.Println(f)
		}
		if len(files) == 0 {
			fmt.Println("(no files match)")
		}
	}
	return nil
}

// findPatternFiles lists the files pattern matches at revision (HEAD when
// empty).
func findPatternFiles(pattern, revision string) ([]string, error) {
	repo, err := getRepo()
	if err != nil {
		return nil, err
	}
	files, err := repo.FindFilesAt(pattern, plumbing.Revision(revision), false)
	if err != nil {
		// No commit yet: no tracked files exist to match, so an empty
		// result is valid rather than a fatal missing-HEAD error.
		if revision == "" && !repo.HasCommits() {
			return nil, nil
		}
		return nil, fmt.Errorf("find files for pattern %q: %w", pattern, err)
	}
	return files, nil
}

func init() {
	rulesFilesCmd.RunE = runRulesFiles
	rulesFilesCmd.Flags().String("revision", "", "tree revision to resolve (defaults to HEAD)")
	rulesCmd.AddCommand(rulesFilesCmd)
}
