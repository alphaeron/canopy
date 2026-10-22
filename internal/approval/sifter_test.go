package approval

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
)

func TestGetFileRuleChainLayersAndByRule(t *testing.T) {
	rules := map[string]kitfile.Rules{
		"KIT":         {{Pattern: `.*`, Name: "all"}},
		"src/KIT":     {{Pattern: `\.go$`, Name: "go"}},
		"src/api/KIT": {{Pattern: `\.go$`, Name: "api"}, {Pattern: `(`, Name: "broken"}},
	}
	files := []string{"README.md", "src/api/v1.go", "src/main.go"}
	events := NewReviewStateBuilder()
	chain := GetFileRuleChain(rules, files, events, slog.New(slog.DiscardHandler))

	var dirs []string
	for _, l := range chain.FileChains["src/api/v1.go"] {
		dirs = append(dirs, l.KitFile())
	}
	if want := []string{"KIT", "src/KIT", "src/api/KIT"}; !reflect.DeepEqual(dirs, want) {
		t.Errorf("layers for src/api/v1.go = %v, want %v", dirs, want)
	}
	// The broken pattern is compiled once but reported for the one file
	// whose chain reaches its KIT file.
	if got := len(events.Build(nil).Errors); got != 1 {
		t.Errorf("sift errors = %d, want 1", got)
	}

	type entry struct {
		kitFile, name string
		files         []string
	}
	var got []entry
	for _, rf := range chain.ByRule() {
		got = append(got, entry{rf.KitFile, rf.Rule.Name, rf.Files})
	}
	want := []entry{
		{"KIT", "all", []string{"README.md", "src/api/v1.go", "src/main.go"}},
		{"src/KIT", "go", []string{"src/api/v1.go", "src/main.go"}},
		{"src/api/KIT", "api", []string{"src/api/v1.go"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ByRule() = %+v, want %+v", got, want)
	}
}

func TestEffectiveCombineStrategyDefaultsToOverride(t *testing.T) {
	if got := EffectiveCombineStrategy(kitfile.RuleCombineStrategy(0)); got != kitfile.RuleCombineStrategyOverride {
		t.Errorf("omitted strategy = %v, want override", got)
	}
	if got := EffectiveCombineStrategy(kitfile.RuleCombineStrategyUnion); got != kitfile.RuleCombineStrategyUnion {
		t.Errorf("union = %v, want union", got)
	}
}

// TestByRuleMatchesScopeIdentity pins that ByRule groups rules by
// kitfile.RuleKey, the same identity match scopes use: a pattern declared
// twice in one KIT file is one rule, whatever its names.
func TestByRuleMatchesScopeIdentity(t *testing.T) {
	rules := kitfile.Set{"KIT": {{Pattern: `.*`, Name: "first"}, {Pattern: `.*`, Name: "second"}}}
	chain := GetFileRuleChain(rules, []string{"a.go"}, nil, slog.New(slog.DiscardHandler))
	got := chain.ByRule()
	if len(got) != 1 || got[0].Rule.Name != "first" || !reflect.DeepEqual(got[0].Files, []string{"a.go"}) {
		t.Errorf("ByRule() = %+v, want one entry for the first declaration", got)
	}
}
