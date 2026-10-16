package shonin

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/alphaeron/kit/internal/gitutil"
	"github.com/alphaeron/kit/internal/kitfile"
)

// LocalFetcher retrieves KIT rules from the local git repository working
// tree.
type LocalFetcher struct {
	Repo *gitutil.KitGitRepository
	// Log receives structured diagnostic output. The default logger is used
	// when nil.
	Log *slog.Logger
}

// GetRules locates every KIT file at HEAD and parses its rules. Any failure
// to get the repository root, read HEAD, or locate files is returned as an
// error; per-file read/parse errors are accumulated into the returned error
// while the successful rules are still provided to the caller.
func (lf *LocalFetcher) GetRules() (string, map[string]kitfile.Rules, error) {
	log := lf.log()
	rules := make(map[string]kitfile.Rules)

	repoRoot, err := lf.Repo.Root()
	if err != nil {
		log.Error("kit: failed to get repository root",
			"component", "fetcher", "phase", "fetch", "error", err)
		return "", nil, fmt.Errorf("get repository root: %w", err)
	}

	ref, err := lf.Repo.Head()
	if err != nil {
		log.Error("kit: failed to read HEAD",
			"component", "fetcher", "phase", "fetch", "repo_root", repoRoot, "error", err)
		return repoRoot, rules, fmt.Errorf("read repository HEAD: %w", err)
	}
	log.Info("kit: resolved repository HEAD",
		"component", "fetcher", "phase", "fetch", "repo_root", repoRoot, "ref", ref.Hash())

	isAbsolute := false
	paths, err := lf.Repo.FindFiles("KIT", ref, isAbsolute)
	if err != nil {
		log.Error("kit: failed to find KIT files",
			"component", "fetcher", "phase", "fetch", "repo_root", repoRoot, "error", err)
		return repoRoot, rules, fmt.Errorf("find KIT files: %w", err)
	}

	var fetchErrors []error
	for _, path := range paths {
		log.Info("kit: loading rules from KIT file",
			"component", "fetcher", "phase", "fetch", "path", path)
		fileRules, err := getRulesForKitFile(repoRoot, path, log)
		if err != nil {
			fetchErrors = append(fetchErrors, err)
			continue
		}
		rules[path] = fileRules
	}

	return repoRoot, rules, errors.Join(fetchErrors...)
}

// getRulesForKitFile reads and parses a single KIT file, returning a wrapped
// error describing the failed step instead of silently yielding nil rules.
func getRulesForKitFile(repoRoot string, fileName string, log *slog.Logger) (kitfile.Rules, error) {
	var rules kitfile.Rules

	f := path.Join(repoRoot, fileName)

	exists, err := fileExistsCaseSensitive(f)
	if err != nil {
		log.Error("kit: unable to stat KIT file",
			"component", "fetcher", "phase", "fetch", "path", fileName, "error", err)
		return nil, fmt.Errorf("stat %s: %w", f, err)
	}
	if !exists {
		log.Error("kit: KIT file does not exist",
			"component", "fetcher", "phase", "fetch", "path", fileName)
		return nil, fmt.Errorf("KIT file %s does not exist", f)
	}

	dat, err := os.ReadFile(f)
	if err != nil {
		log.Error("kit: failed to read KIT file",
			"component", "fetcher", "phase", "fetch", "path", fileName, "error", err)
		return nil, fmt.Errorf("read %s: %w", f, err)
	}

	if err := yaml.Unmarshal(dat, &rules); err != nil {
		log.Error("kit: failed to parse KIT file",
			"component", "fetcher", "phase", "fetch", "path", fileName, "error", err)
		return nil, fmt.Errorf("parse %s: %w", fileName, err)
	}

	return rules, nil
}

// fileExistsCaseSensitive checks if path exists, is a regular file, and
// matches exact character casing on disk.
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

// log returns the configured logger or the default one.
func (lf *LocalFetcher) log() *slog.Logger {
	if lf.Log != nil {
		return lf.Log
	}
	return slog.Default()
}
