package review

import "github.com/alphaeron/kit/internal/rulesfile"

// CoverageReport is the pure-data outcome of Engine.Coverage: the file
// universe, the covered count and percentage, the per-rule breakdown, and
// the files matched by no rule.
type CoverageReport struct {
	Universe  int            `json:"universe"`
	Covered   int            `json:"covered"`
	Percent   float64        `json:"percent"`
	PerRule   []RuleCoverage `json:"per_rule"`
	Uncovered []string       `json:"uncovered"`
}

// FilterScope returns a copy of the report whose per-rule breakdown keeps
// only the rules named name (the --scope filter). Universe, Covered,
// Percent, and Uncovered still describe every rule: narrowing the breakdown
// does not change what the repository's rules cover.
func (r *CoverageReport) FilterScope(name string) *CoverageReport {
	per := make([]RuleCoverage, 0, len(r.PerRule))
	for _, rc := range r.PerRule {
		if rc.Name == name {
			per = append(per, rc)
		}
	}
	out := *r
	out.PerRule = per
	return &out
}

// RuleCoverage is how many files in the universe a single rule matched, and
// what fraction that is of the universe.
type RuleCoverage struct {
	rulesfile.Rule `json:"rule"`
	Files          int     `json:"files"`
	Percent        float64 `json:"percent"`
}

// CoverageOf projects a match onto coverage: the files with at least one
// rule are covered, and each rule's share of the universe is reported in
// Result.GroupByRule order.
func CoverageOf(m *Result) *CoverageReport {
	report := &CoverageReport{Universe: len(m.Files)}
	for _, fm := range m.Files {
		if len(fm.Rules) > 0 {
			report.Covered++
		} else {
			report.Uncovered = append(report.Uncovered, fm.Path)
		}
	}
	report.Percent = percentOf(report.Covered, report.Universe)
	groups := m.GroupByRule()
	report.PerRule = make([]RuleCoverage, 0, len(groups))
	for _, g := range groups {
		report.PerRule = append(report.PerRule, RuleCoverage{
			Rule:    g.Rule,
			Files:   len(g.Files),
			Percent: percentOf(len(g.Files), report.Universe),
		})
	}
	return report
}

// percentOf is n as a percentage of total, or 0 for an empty total.
func percentOf(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100.0
}
