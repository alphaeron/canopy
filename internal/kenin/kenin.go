package kenin

import (
	"log/slog"
	"regexp"
	"sort"
)

// Options configure one Match or Coverage run. A zero-value struct means:
// all files, every scope, no diff content.
type Options struct {
	ChangedOnly bool
	ScopeName   string
	IncludeDiff bool
}

// Engine is the single match/coverage implementation over a Source.
type Engine struct {
	Source Source
	Log    *slog.Logger
}

// New constructs an Engine on the given Source and logger.
func New(src Source, log *slog.Logger) *Engine {
	return &Engine{Source: src, Log: log}
}

func (e *Engine) log() *slog.Logger {
	if e.Log == nil {
		return slog.Default()
	}
	return e.Log
}

// fileUniverse resolves the file set for the current option state:
// ChangedFiles when Options.ChangedOnly is true, AllFiles otherwise.
// Both are sorted before being returned.
func (e *Engine) fileUniverse(opts Options) ([]string, error) {
	var files []string
	var err error
	if opts.ChangedOnly {
		files, err = e.Source.ChangedFiles()
	} else {
		files, err = e.Source.AllFiles()
	}
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// compileRegex caches the regex used to match rule patterns against the
// user's filter.
func compileRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}
