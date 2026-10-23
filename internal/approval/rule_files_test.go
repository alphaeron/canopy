package approval

import (
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// TestGroupByRule pins the inversion: one entry per rule by its location
// (RuleKey), so a pattern repeated in one rules file is two entries, sorted
// by key, each with its files sorted and de-duplicated.
func TestGroupByRule(t *testing.T) {
	base := rulesfile.Rule{RulesFile: rulesfile.FileName, Pattern: `.*`, Name: "base"}
	override := rulesfile.Rule{RulesFile: rulesfile.FileName, Position: 1, Pattern: `.*`, Name: "override"}
	goRule := rulesfile.Rule{RulesFile: rulesfile.PathInDir("src"), Pattern: `\.go$`}
	got := GroupByRule(map[string]rulesfile.Rules{
		"src/b.go":  {base, override, goRule},
		"README.md": {base, override},
		"src/a.go":  {base, override, goRule, goRule},
	})
	want := []RuleFiles{
		{Rule: base, Files: []string{"README.md", "src/a.go", "src/b.go"}},
		{Rule: override, Files: []string{"README.md", "src/a.go", "src/b.go"}},
		{Rule: goRule, Files: []string{"src/a.go", "src/b.go"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GroupByRule() = %+v, want %+v", got, want)
	}
	if got := GroupByRule(nil); got == nil || len(got) != 0 {
		t.Errorf("GroupByRule(nil) = %#v, want empty non-nil", got)
	}
}
