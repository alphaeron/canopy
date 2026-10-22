package local

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"

	"github.com/alphaeron/kit/internal/gitrepo"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// Fetcher retrieves rules from the local git repository working
// tree.
type Fetcher struct {
	Repo *gitrepo.Repository
	// Log receives structured diagnostic output. The default logger is used
	// when nil.
	Log *slog.Logger
}

// Execute runs the fetch phase for a local checkout: it locates every rules
// file in the working tree (gitrepo.Repository.WorktreeFiles: committed rules
// files that still exist plus new, non-ignored ones) and parses each from disk,
// so uncommitted rule changes are what a local review sees. Any failure to get
// the repository root or list the working tree is returned as an error;
// per-file read/parse errors are accumulated into the returned error while the
// successful rules are still provided to the caller.
func (lf *Fetcher) Execute(ctx context.Context) (string, rulesfile.Set, error) {
	log := lf.log()
	rules := make(rulesfile.Set)

	repoRoot, err := lf.Repo.Root()
	if err != nil {
		log.Error("failed to get repository root",
			"component", "fetcher", "phase", "fetch", "error", err)
		return "", nil, fmt.Errorf("get repository root: %w", err)
	}

	paths, err := lf.Repo.WorktreeFiles(rulesfile.IsRulesFile)
	if err != nil {
		log.Error("failed to find rules files",
			"component", "fetcher", "phase", "fetch", "repo_root", repoRoot, "error", err)
		return repoRoot, rules, fmt.Errorf("find rules files: %w", err)
	}

	var fetchErrors []error
	for _, path := range paths {
		log.Debug("loading rules from rules file",
			"component", "fetcher", "phase", "fetch", "path", path)
		fileRules, err := getRulesForRulesFile(repoRoot, path, log)
		if err != nil {
			fetchErrors = append(fetchErrors, err)
			continue
		}
		rules[path] = fileRules
	}

	return repoRoot, rules, errors.Join(fetchErrors...)
}

// getRulesForRulesFile loads a single rules file through rulesfile.Load
// once its name is confirmed with exact casing, returning a wrapped error
// describing the failed step instead of silently yielding nil rules.
func getRulesForRulesFile(repoRoot string, fileName string, log *slog.Logger) (rulesfile.Rules, error) {
	f := path.Join(repoRoot, fileName)

	exists, err := fileExistsCaseSensitive(f)
	if err != nil {
		log.Error("unable to stat rules file",
			"component", "fetcher", "phase", "fetch", "path", fileName, "error", err)
		return nil, fmt.Errorf("stat %s: %w", f, err)
	}
	if !exists {
		log.Error("rules file does not exist",
			"component", "fetcher", "phase", "fetch", "path", fileName)
		return nil, fmt.Errorf("rules file %s does not exist", f)
	}

	k, err := rulesfile.Load(f)
	if err != nil {
		log.Error("failed to load rules file",
			"component", "fetcher", "phase", "fetch", "path", fileName, "error", err)
		return nil, err
	}
	return k.Rules, nil
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
func (lf *Fetcher) log() *slog.Logger {
	return logOrDefault(lf.Log)
}
