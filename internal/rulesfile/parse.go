package rulesfile

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseRules parses YAML bytes into a rules file's rule sequence. It is the
// one parser for every reader (Load, and approval.TreeFetcher over every
// platform's tree), so they all parse rules files identically. Callers add the file's path to the error.
func ParseRules(dat []byte) (Rules, error) {
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
