package rulesfile

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

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
// one parser for every reader (Load, and approval.TreeFetcher over every
// platform's tree), so they all parse rules files identically. Callers add the file's path to the error.
func ParseRules(dat []byte) (Rules, error) {
	if len(dat) > MaxSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(dat))
	}
	var rules Rules
	if err := yaml.Unmarshal(dat, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// ParseApprovers parses the rule form/flag text of approver lines into
// structured targets. One target per line: the target name first, then the
// required count, separated by whitespace. Blank lines are skipped. Every
// malformed line is reported, so the combined error lists all problems
// rather than stopping at the first.
func ParseApprovers(text string) (ApproverTargets, error) {
	var targets ApproverTargets
	var all []error
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			all = append(all, fmt.Errorf("approver line %d: missing required_count", i+1))
			continue
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil {
			all = append(all, fmt.Errorf("approver line %d %q: %w", i+1, fields[1], err))
			continue
		}
		targets = append(targets, ApproverTarget{Target: fields[0], RequiredCount: count})
	}
	return targets, errors.Join(all...)
}

// ApproversToText renders approver targets into the form/flag text
// ParseApprovers consumes: one "target<TAB>required_count" line per target.
// The round trip is lossless for well-formed target lists.
func ApproversToText(targets ApproverTargets) string {
	lines := make([]string, 0, len(targets))
	for _, t := range targets {
		lines = append(lines, fmt.Sprintf("%s\t%d", t.Target, t.RequiredCount))
	}
	return strings.Join(lines, "\n")
}
