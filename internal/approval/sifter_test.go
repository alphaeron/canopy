package approval

import (
	"log/slog"
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestGetFileRuleChainLayersAndByRule(t *testing.T) {
	rules := map[string]rulesfile.Rules{
		rulesfile.FileName:             {{Pattern: `.*`, Name: "all"}},
		rulesfile.PathInDir("src"):     {{Pattern: `\.go$`, Name: "go"}},
		rulesfile.PathInDir("src/api"): {{Pattern: `\.go$`, Name: "api"}, {Pattern: `(`, Name: "broken"}},
	}
	files := []string{"README.md", "src/api/v1.go", "src/main.go"}
	events := NewReviewStateBuilder()
	chain := GetFileRuleChain(rules, files, events, slog.New(slog.DiscardHandler))

	var dirs []string
	for _, l := range chain.FileChains["src/api/v1.go"] {
		dirs = append(dirs, l.RulesFile())
	}
	if want := []string{rulesfile.FileName, rulesfile.PathInDir("src"), rulesfile.PathInDir("src/api")}; !reflect.DeepEqual(dirs, want) {
		t.Errorf("layers for src/api/v1.go = %v, want %v", dirs, want)
	}
	// The broken pattern is compiled once but reported for the one file
	// whose chain reaches its rules file.
	if got := len(events.Build(nil).Errors); got != 1 {
		t.Errorf("sift errors = %d, want 1", got)
	}

	type entry struct {
		rulesFile, name string
		files           []string
	}
	var got []entry
	for _, rf := range chain.ByRule() {
		got = append(got, entry{rf.RulesFile, rf.Rule.Name, rf.Files})
	}
	want := []entry{
		{rulesfile.FileName, "all", []string{"README.md", "src/api/v1.go", "src/main.go"}},
		{rulesfile.PathInDir("src"), "go", []string{"src/api/v1.go", "src/main.go"}},
		{rulesfile.PathInDir("src/api"), "api", []string{"src/api/v1.go"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ByRule() = %+v, want %+v", got, want)
	}
}

func TestEffectiveCombineStrategyDefaultsToOverride(t *testing.T) {
	if got := EffectiveCombineStrategy(rulesfile.RuleCombineStrategy(0)); got != rulesfile.RuleCombineStrategyOverride {
		t.Errorf("omitted strategy = %v, want override", got)
	}
	if got := EffectiveCombineStrategy(rulesfile.RuleCombineStrategyUnion); got != rulesfile.RuleCombineStrategyUnion {
		t.Errorf("union = %v, want union", got)
	}
}

// TestByRuleMatchesScopeIdentity pins that ByRule groups rules by
// rulesfile.RuleKey, the same identity match scopes use: a pattern declared
// twice in one rules file is one rule, whatever its names.
func TestByRuleMatchesScopeIdentity(t *testing.T) {
	rules := rulesfile.Set{rulesfile.FileName: {{Pattern: `.*`, Name: "first"}, {Pattern: `.*`, Name: "second"}}}
	chain := GetFileRuleChain(rules, []string{"a.go"}, nil, slog.New(slog.DiscardHandler))
	got := chain.ByRule()
	if len(got) != 1 || got[0].Rule.Name != "first" || !reflect.DeepEqual(got[0].Files, []string{"a.go"}) {
		t.Errorf("ByRule() = %+v, want one entry for the first declaration", got)
	}
}
