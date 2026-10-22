package shonin

import (
	"cmp"
	"maps"
	"slices"

	"github.com/alphaeron/kit/internal/kitfile"
)

// attention ranks a file status by how much it needs attention; a lower
// rank needs more: errors, then unresolved targets, then missing approvals,
// then anything else, then satisfied.
func attention(s Status) int {
	switch s {
	case StatusError:
		return 0
	case StatusUnresolved:
		return 1
	case StatusMissingApprovers:
		return 2
	case StatusSatisfied:
		return 4
	default:
		return 3
	}
}

// SortedFiles lists the review's files, most in need of attention first,
// then by path.
func (rs *ReviewState) SortedFiles() []FileState {
	out := slices.Collect(maps.Values(rs.Files))
	slices.SortFunc(out, func(a, b FileState) int {
		return cmp.Or(cmp.Compare(attention(a.Status), attention(b.Status)), cmp.Compare(a.Path, b.Path))
	})
	return out
}

// RequiredTargets is every approver target the review's files require, each
// with the most approvals any one file needs from it, sorted by target, and
// how many files fall back to the repository owners instead.
func (rs *ReviewState) RequiredTargets() (targets kitfile.ApproverTargets, fallback int) {
	need := map[string]int{}
	for _, fs := range rs.Files {
		if fs.UsedFallback {
			fallback++
			continue
		}
		for _, r := range fs.Required {
			need[r.Target] = max(need[r.Target], r.RequiredCount)
		}
	}
	for _, t := range slices.Sorted(maps.Keys(need)) {
		targets = append(targets, kitfile.ApproverTarget{Target: t, RequiredCount: need[t]})
	}
	return targets, fallback
}
