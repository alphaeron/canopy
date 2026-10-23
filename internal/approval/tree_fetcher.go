package approval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/parallel"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// Tree is one revision of a repository's files as a platform stores them:
// the only thing the fetch phase needs from a platform. Each platform
// implements it over its own storage (a local working tree, a hosted
// repository's tree at a revision), and TreeFetcher turns any Tree into a
// Fetcher. TreeFetcher reads files concurrently, so ReadFile must be safe
// for concurrent use.
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
// A failure to list the tree fails the fetch (a nil set); a rules file that
// cannot be read or parsed is reported in the returned error, as a
// *RulesFileError, while every other rules file is still returned, so
// callers can say which files failed (RulesFileErrors).
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
		return nil, fmt.Errorf("list files: %w", err)
	}

	var rulesPaths []string
	for _, path := range paths {
		if rulesfile.IsRulesFile(path) {
			rulesPaths = append(rulesPaths, path)
		}
	}
	// Each read is a round trip on a hosted platform, so read the files
	// concurrently, then report them in path order.
	read := make([]struct {
		rules rulesfile.Rules
		err   error
	}, len(rulesPaths))
	parallel.For(len(rulesPaths), func(i int) {
		read[i].rules, read[i].err = f.readRules(ctx, rulesPaths[i])
	})

	var fetchErrors []error
	for i, path := range rulesPaths {
		if err := read[i].err; err != nil {
			log.Error("fetch: rules file unusable",
				"component", "fetcher", "phase", "fetch", "path", path, "error", err)
			fetchErrors = append(fetchErrors, err)
			continue
		}
		rules[path] = read[i].rules
		log.Debug("fetch: loaded rules file",
			"component", "fetcher", "phase", "fetch", "path", path, "rules", len(read[i].rules))
	}
	return rules, errors.Join(fetchErrors...)
}

// readRules reads and parses one rules file.
func (f *TreeFetcher) readRules(ctx context.Context, path string) (rulesfile.Rules, error) {
	data, err := f.Tree.ReadFile(ctx, path)
	if err != nil {
		return nil, &RulesFileError{Path: path, Op: "read", Err: err}
	}
	rules, err := rulesfile.ParseRules(data)
	if err != nil {
		return nil, &RulesFileError{Path: path, Op: "parse", Err: err}
	}
	return rules, nil
}
