package review

import (
	"reflect"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// TestCoverageFilterScope pins that narrowing the breakdown keeps the
// overall numbers, which describe every rule, and leaves the receiver alone.
func TestCoverageFilterScope(t *testing.T) {
	report := &CoverageReport{
		Universe: 10,
		Covered:  7,
		Percent:  70.0,
		PerRule: []RuleCoverage{
			{Rule: rulesfile.Rule{Name: "core"}, Files: 5, Percent: 50},
			{Rule: rulesfile.Rule{Name: "sec"}, Files: 2, Percent: 20},
		},
		Uncovered: []string{"x.go"},
	}
	got := report.FilterScope("sec")
	want := *report
	want.PerRule = []RuleCoverage{{Rule: rulesfile.Rule{Name: "sec"}, Files: 2, Percent: 20}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("FilterScope = %+v, want %+v", *got, want)
	}
	if len(report.PerRule) != 2 {
		t.Errorf("FilterScope must not modify its receiver: %+v", report.PerRule)
	}
}
