package approval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/alphaeron/kit/internal/logx"
	"github.com/alphaeron/kit/internal/parallel"
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
	// Implementations must not swallow errors. A set that is nil means no
	// rules could be read; a set with an error holds the rules files that
	// loaded, and the review runs over them (see Evaluator.Execute).
	Execute(ctx context.Context) (rulesfile.Set, error)
}

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
// cannot be read or parsed is reported in the returned error while every
// other rules file is still returned, so callers can say which files failed.
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
	parallel.For(len(rulesPaths), parallel.Limit, func(i int) {
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
		return nil, fmt.Errorf("read rules file %s: %w", path, err)
	}
	rules, err := rulesfile.ParseRules(data)
	if err != nil {
		return nil, fmt.Errorf("parse rules file %s: %w", path, err)
	}
	return rules, nil
}

// OnceFetcher runs Fetcher once and returns that result, error included, on
// every call; concurrent calls share the one fetch. It suits an evaluator
// over one snapshot of a repository (a pull request's base revision, a
// local Repository), whose rules cannot change under it, so the phases of a
// review, or the views of one page, read the rules once between them. The
// first call's ctx is the one the fetch runs under.
type OnceFetcher struct {
	Fetcher

	once  sync.Once
	rules rulesfile.Set
	err   error
}

// Execute runs the fetch on first use and returns its result.
func (f *OnceFetcher) Execute(ctx context.Context) (rulesfile.Set, error) {
	f.once.Do(func() { f.rules, f.err = f.Fetcher.Execute(ctx) })
	return f.rules, f.err
}
