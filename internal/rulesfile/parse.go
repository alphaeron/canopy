package rulesfile

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// MaxSize is the largest rules file read, in bytes. A rules file is a few
// kilobytes; the bound keeps a huge one, committed by mistake or proposed
// by a pull request, from being downloaded and parsed, and keeps every
// rules file within what GitHub's contents API serves.
const MaxSize = 512 << 10

// ErrTooLarge reports a rules file larger than MaxSize.
var ErrTooLarge = fmt.Errorf("rules file is larger than the %d-byte limit", MaxSize)

// ReadLimited reads r to its end, at most MaxSize bytes: a longer r is
// ErrTooLarge, found after reading only one byte past the limit.
func ReadLimited(r io.Reader) ([]byte, error) {
	dat, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(dat) > MaxSize {
		return nil, ErrTooLarge
	}
	return dat, nil
}

// ParseRules parses YAML bytes into a rules file's rule sequence. It is the
// one parser for every reader (rulesedit.Load, and approval.TreeFetcher
// over every platform's tree), so they all parse rules files identically.
// Callers add the file's path to the error.
func ParseRules(dat []byte) (Rules, error) {
	if len(dat) > MaxSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(dat))
	}
	var rules Rules
	if err := yaml.Unmarshal(dat, &rules); err != nil {
		return nil, err
	}
	for i := range rules {
		rules[i].Position = i
	}
	return rules, nil
}
