package kit

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

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
		// Each arg in args is a file/regex to get rules for
		for _, arg := range args {
			rulesGet(arg)
		}
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

func rulesGet(pattern string) {
	repo, err := getRepo()
	if err == nil {
		// Successfully obtained the repository instance
		fmt.Println("Successfully opened repository root.")
	} else {
		return
	}

	repoRoot, err := repo.Root()
	if err != nil {
		fmt.Printf("Failed to get repository root.\n")
		return
	}

	// Print the current HEAD reference.
	ref, err := repo.Head()
	if err == nil {
		fmt.Printf("Current HEAD: %s\n", ref.Hash())
	} else {
		fmt.Println(err)
		return
	}

	isAbsolute := false
	paths, err := repo.FindFiles(pattern, ref, isAbsolute)
	if err == nil {
		for _, path := range paths {
			fmt.Println(path)
			rules := rulesForFile(repoRoot, path)
			for _, rule := range rules {
				printRule(rule)
			}
		}
	}
}

func rulesForFile(repoRoot string, fileName string) kitfile.Rules {
	var rules kitfile.Rules

	subdir, _ := filepath.Split(fileName)
	subdirParts := strings.Split(subdir, string(os.PathSeparator))

	for i := range subdirParts {
		f := path.Join(repoRoot, path.Join(subdirParts[:i]...), "KIT")
		exists, err := fileExistsCaseSensitive(f)
		if err != nil || !exists {
			continue
		}
		dat, err := os.ReadFile(f)
		if err != nil {
			fmt.Printf("Unable to read file '%s'.\nCannot get rules for '%s'.\n", f, fileName)
			return nil
		}
		var tmpRules kitfile.Rules
		parseerr := yaml.Unmarshal(dat, &tmpRules)
		if parseerr != nil {
			fmt.Printf("error while reading '%s': %v\nCannot get rules.\n", fileName, parseerr)
			return nil
		}
		for _, rule := range tmpRules {
			isMatch, err := regexp.MatchString(rule.Pattern, fileName)
			if err == nil {
				if isMatch {
					rules = append(rules, rule)
				}
			} else {
				fmt.Printf("error while checking rule match for '%s': %v\nCannot get rules.\n", fileName, parseerr)
				return nil
			}
		}
	}
	return rules
}

// Checks if path exists, is a regular file, and matches exact character casing on disk.
func fileExistsCaseSensitive(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if info.IsDir() {
		return false, nil
	}

	// Verify exact casing by reading parent directory
	dir, expectedFile := filepath.Split(path)
	if dir == "" {
		dir = "."
	} else {
		dir = filepath.Clean(dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() == expectedFile {
			return true, nil
		}
	}

	return false, nil
}

func printRule(rule kitfile.Rule) {
	// Verify the results
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
