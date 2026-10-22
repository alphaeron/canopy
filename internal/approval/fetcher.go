package approval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// Fetcher retrieves the review rules that apply to a repository. Like every
// other layer of the pipeline it exposes a single Execute command, so the
// fetch phase is traced and consumed the same way as sift and approve.
type Fetcher interface {
	// Execute runs the fetch phase: it locates every rules file in the
	// platform's tree and parses its rules, returning the rules file set
	// (each repo-relative rules file path to its parsed rules) and any error
	// encountered while locating, reading, or parsing rules files.
	// Implementations must not swallow errors.
	Execute(ctx context.Context) (rulesfile.Set, error)
}

// Tree is one revision of a repository's files as a platform stores them:
// the only thing the fetch phase needs from a platform. Each platform
// implements it over its own storage (a local working tree, a hosted
// repository's tree at a revision), and TreeFetcher turns any Tree into a
// Fetcher.
type Tree interface {
	// Files lists every file path in the tree, repository-relative and
	// slash-separated, in sorted order.
	Files(ctx context.Context) ([]string, error)
	// ReadFile returns the contents of the file at path. A path that is not
	// in the tree is an error wrapping fs.ErrNotExist.
	ReadFile(ctx context.Context, path string) ([]byte, error)
}

// TreeFetcher is the Fetcher every platform shares: it lists Tree, keeps
// the rules files (rulesfile.IsRulesFile), and reads and parses each one.
// A failure to list the tree fails the fetch; a rules file that cannot be
// read or parsed is reported in the returned error while every other rules
// file is still returned, so callers can say which files failed.
type TreeFetcher struct {
	// Tree is the platform's tree to read rules files from. Required.
	Tree Tree
	// Log receives structured diagnostic output. The default logger is used
	// when nil.
	Log *slog.Logger
}

// Execute runs the fetch phase over the tree.
func (f *TreeFetcher) Execute(ctx context.Context) (rulesfile.Set, error) {
	log := logx.OrDefault(f.Log)
	rules := make(rulesfile.Set)

	paths, err := f.Tree.Files(ctx)
	if err != nil {
		log.Error("fetch: listing the tree failed",
			"component", "fetcher", "phase", "fetch", "error", err)
		return rules, fmt.Errorf("list files: %w", err)
	}

	var fetchErrors []error
	for _, path := range paths {
		if !rulesfile.IsRulesFile(path) {
			continue
		}
		fileRules, err := f.readRules(ctx, path)
		if err != nil {
			log.Error("fetch: rules file unusable",
				"component", "fetcher", "phase", "fetch", "path", path, "error", err)
			fetchErrors = append(fetchErrors, err)
			continue
		}
		rules[path] = fileRules
		log.Debug("fetch: loaded rules file",
			"component", "fetcher", "phase", "fetch", "path", path, "rules", len(fileRules))
	}
	return rules, errors.Join(fetchErrors...)
}

// readRules reads and parses one rules file.
func (f *TreeFetcher) readRules(ctx context.Context, path string) (rulesfile.Rules, error) {
	data, err := f.Tree.ReadFile(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read rules file %s: %w", path, err)
	}
	rules, err := rulesfile.ParseRules(data)
	if err != nil {
		return nil, fmt.Errorf("parse rules file %s: %w", path, err)
	}
	return rules, nil
}
