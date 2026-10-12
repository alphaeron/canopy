package shonin

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/alphaeron/kit/internal/gitutil"
	"github.com/alphaeron/kit/internal/kitfile"
)

type LocalFetcher struct {
	Repo *gitutil.KitGitRepository
}

func (lf *LocalFetcher) GetRules() (string, map[string]kitfile.Rules) {
	rules := make(map[string]kitfile.Rules)

	repoRoot, err := lf.Repo.Root()
	if err != nil {
		fmt.Printf("Failed to get repository root.\n")
		return "", nil
	}

	// Print the current HEAD reference.
	ref, err := lf.Repo.Head()
	if err == nil {
		// TODO Only on verbose, propper logging?
		fmt.Printf("Current HEAD: %s\n", ref.Hash())
	} else {
		fmt.Println(err)
		return repoRoot, rules
	}

	isAbsolute := false
	paths, err := lf.Repo.FindFiles("KIT", ref, isAbsolute)
	if err == nil {
		for _, path := range paths {
			fmt.Printf("Now getting rules from %s.\n", path)
			fileRules := getRulesForKitFile(repoRoot, path)
			rules[path] = fileRules
		}
	}

	return repoRoot, rules
}

func getRulesForKitFile(repoRoot string, fileName string) kitfile.Rules {
	var rules kitfile.Rules

	f := path.Join(repoRoot, fileName)
	exists, err := fileExistsCaseSensitive(f)
	if err != nil {
		fmt.Printf("Unable to read file '%s'.\nCannot get rules for '%s'.\n%s\n", f, fileName, err)
		return nil
	}
	if !exists {
		fmt.Printf("Unable to read file '%s'.\nCannot get rules for '%s'.\nFile does not exist.\n", f, fileName)
		return nil
	}
	dat, err := os.ReadFile(f)
	if err != nil {
		fmt.Printf("Unable to read file '%s'.\nCannot get rules for '%s'.\n", f, fileName)
		return nil
	}
	parseerr := yaml.Unmarshal(dat, &rules)
	if parseerr != nil {
		fmt.Printf("error while reading '%s': %v\nCannot get rules.\n", fileName, parseerr)
		return nil
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
