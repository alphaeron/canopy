package kit

import (
	"errors"
	"fmt"
	"log"

	"github.com/go-git/go-git/v6"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/alphaeron/kit/internal/gitutil"
	"github.com/alphaeron/kit/internal/kitfile"
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
		rulesGet()
	},
}

func init() {
	rulesCmd.AddCommand(rulesGetCmd)
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

	kitrepo := gitutil.KitGitRepository{ *repo }
	return &kitrepo, nil
}

func rulesGet() {
	repo, err := getRepo()
	if err == nil {
		// Successfully obtained the repository instance
		fmt.Println("Successfully opened repository root.")
	}

	// Example usage: Print the current HEAD reference
	ref, err := repo.Head()
	if err == nil {
		fmt.Printf("Current HEAD: %s\n", ref.Hash())
	} else {
		fmt.Println(err)
	}

	isAbsolute := false
	paths, err := repo.FindFiles(".*\\.go", ref, isAbsolute)
	if err == nil {
		for _, path := range paths {
			fmt.Println(path)
		}
	}

	yamlData := []byte(`
- ".*\\.go":
  name: "Core Backend Review"
  ignore_parent_dirs: false
  combine_strategy: "union"
  current_directory_only: false
  self_approve: true
  approvers:
    - target: "org/backend-team"
      required_count: 2
    - target: "org/security-leads"
      required_count: 1
- ".*\\.tf":
  name: "Infrastructure Review"
  ignore_parent_dirs: false
  combine_strategy: "union"
  current_directory_only: false
  self_approve: false
  approvers:
    - target: "org/cloud-team"
      required_count: 1
`)

	var rules kitfile.Rules
	parseerr := yaml.Unmarshal(yamlData, &rules)
	if parseerr != nil {
		log.Fatalf("error: %v", parseerr)
	}

	// Verify the results
	for _, rule := range rules {
		fmt.Printf("Pattern:              %s\n", rule.Pattern)
		fmt.Printf("Name:                 %s\n", rule.Name)
		fmt.Printf("IgnoreParentDirs:     %t\n", rule.IgnoreParentDirs)
		fmt.Printf("CombineStrategy:      %s\n", rule.CombineStrategy)
		fmt.Printf("CurrentDirectoryOnly: %t\n", rule.CurrentDirectoryOnly)
		fmt.Printf("SelfApprove:          %t\n", rule.SelfApprove)
		fmt.Printf("Approvers:\n")
		for _, approver := range rule.Approvers {
			fmt.Printf("\t%s\n", approver)
		}
		fmt.Println("")
	}
}
