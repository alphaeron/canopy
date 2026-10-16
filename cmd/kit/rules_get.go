package kit

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/go-git/go-git/v6"
	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/gitutil"
	"github.com/alphaeron/kit/internal/shonin"
)

// rulesGetCmd represents the "rules get" command
var rulesGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get review rules for a file/regex",
	Long: `The get subcommand allows for checking applicable review rules for
a file/pattern matching a set of files.

This way, review requirements can be visualized locally without opening a
pull request.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Each arg in args is a file/regex to get rules for
		for _, arg := range args {
			rulesGet(arg)
		}
	},
}

func init() {
	rulesCmd.AddCommand(rulesGetCmd)
}

func rulesGet(pattern string) {
	log := slog.Default()
	repo, err := getRepo()
	if err == nil {
		// Successfully obtained the repository instance
		fmt.Println("Successfully opened repository root.")
	} else {
		return
	}

	// Print the current HEAD reference.
	ref, err := repo.Head()
	if err == nil {
		fmt.Printf("Current HEAD: %s\n", ref.Hash())
	} else {
		log.Error("rules get: failed to read HEAD",
			"component", "cmd", "pattern", pattern, "error", err)
		return
	}

	isAbsolute := false
	paths, err := repo.FindFiles(pattern, ref, isAbsolute)
	if err == nil {
		fetch := shonin.LocalFetcher{Repo: repo}
		s := shonin.Shonin{Fetch: &fetch}
		ruleChain, err := s.GetRuleChainForFiles(paths)
		if err != nil {
			log.Error("rules get: fetching rules failed",
				"component", "cmd", "pattern", pattern, "error", err)
			return
		}
		shonin.PrintRuleChain(ruleChain)
	} else {
		log.Error("rules get: failed to find files",
			"component", "cmd", "pattern", pattern, "error", err)
	}
}

func getRepo() (*gitutil.KitGitRepository, error) {
	// "." targets the current working directory command is run from
	repo, err := git.PlainOpenWithOptions(".", &git.PlainOpenOptions{
		DetectDotGit: true,
	})

	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			fmt.Println("Not a git repository (or any of the parent directories).")
		}
		fmt.Printf("Failed to open repository: %v\n", err)
		return nil, err
	}

	kitrepo := gitutil.KitGitRepository{Repository: *repo}
	return &kitrepo, nil
}
