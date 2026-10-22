package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesFilesCmd lists the KIT files (the files that declare rules), every
// one or only those whose paths a regex matches. Without --revision it reads
// the working tree, so uncommitted KIT files count; with one, it lists the
// files committed at that revision, which gitutil.FindFilesAt resolves
// before any file walk, so a bad revision is an error rather than a
// silently empty result.
var rulesFilesCmd = &cobra.Command{
	Use:         "files [pattern]",
	Short:       "List the KIT files, or those whose paths match a regex",
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{"arg": "regular expression of KIT file paths (default: every KIT file)"},
}

// runRulesFiles is rulesFilesCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runRulesFiles(cmd *cobra.Command, args []string) error {
	var pattern string
	if len(args) > 0 {
		pattern = args[0]
	}
	revision, _ := cmd.Flags().GetString("revision")

	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, rulesFilesRoute(valuesFrom(cmd, args)))
	}
	files, err := findKitFiles(pattern, revision)
	if err != nil {
		return err
	}
	switch mode {
	case modeJSON:
		return tui.JSONEncoder(os.Stdout, map[string][]string{"pattern": append([]string{}, args...), "files": files})
	default:
		for _, f := range files {
			fmt.Println(f)
		}
		if len(files) == 0 {
			fmt.Println("(no KIT files match)")
		}
	}
	return nil
}

// findKitFiles lists the KIT files whose paths match pattern (every KIT file
// when empty), the repository-root KIT first. An empty revision reads the
// working tree; any other lists the files committed at that revision. The
// result is never nil, so it encodes as a JSON list.
func findKitFiles(pattern, revision string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	repo, err := getRepo()
	if err != nil {
		return nil, err
	}
	var files []string
	if revision == "" {
		files, err = repo.WorktreeFiles(func(p string) bool { return kitfile.IsKitFile(p) && re.MatchString(p) })
		if err != nil {
			return nil, fmt.Errorf("find KIT files for pattern %q: %w", pattern, err)
		}
	} else {
		files, err = repo.FindFilesAt(pattern, plumbing.Revision(revision), false)
		if err != nil {
			return nil, fmt.Errorf("find KIT files for pattern %q at %s: %w", pattern, revision, err)
		}
		files = slices.DeleteFunc(files, func(f string) bool { return !kitfile.IsKitFile(f) })
	}
	if files == nil {
		files = []string{}
	}
	slices.SortFunc(files, compareKitPaths)
	return files, nil
}

func init() {
	rulesFilesCmd.RunE = runRulesFiles
	rulesFilesCmd.Flags().String("revision", "", "list the KIT files committed at this revision instead of the working tree")
	rulesCmd.AddCommand(rulesFilesCmd)
}
