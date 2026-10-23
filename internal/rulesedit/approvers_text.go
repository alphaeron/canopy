package rulesedit

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// ParseApprovers parses the rule form/flag text of approver lines into
// structured targets. One target per line: the target name first, then the
// required count, separated by whitespace. Blank lines are skipped. Every
// malformed line is reported, so the combined error lists all problems
// rather than stopping at the first.
func ParseApprovers(text string) (rulesfile.ApproverTargets, error) {
	var targets rulesfile.ApproverTargets
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
		targets = append(targets, rulesfile.ApproverTarget{Target: fields[0], RequiredCount: count})
	}
	return targets, errors.Join(all...)
}

// ApproversToText renders approver targets into the form/flag text
// ParseApprovers consumes: one "target<TAB>required_count" line per target.
// The round trip is lossless for well-formed target lists.
func ApproversToText(targets rulesfile.ApproverTargets) string {
	lines := make([]string, 0, len(targets))
	for _, t := range targets {
		lines = append(lines, fmt.Sprintf("%s\t%d", t.Target, t.RequiredCount))
	}
	return strings.Join(lines, "\n")
}

// ParseApproverPairs reads approver targets from alternating target and
// required count values ("team", "2", "lead", "1"). An odd number of values
// or a count that is not an integer is an error.
func ParseApproverPairs(pairs []string) (rulesfile.ApproverTargets, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("approvers take 'target required_count' pairs; got %d value(s)", len(pairs))
	}
	targets := make(rulesfile.ApproverTargets, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		count, err := strconv.Atoi(pairs[i+1])
		if err != nil {
			return nil, fmt.Errorf("required count %q of %q: %w", pairs[i+1], pairs[i], err)
		}
		targets = append(targets, rulesfile.ApproverTarget{Target: pairs[i], RequiredCount: count})
	}
	return targets, nil
}
