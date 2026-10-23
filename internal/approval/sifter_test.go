package approval

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

func TestGetFileRuleChainLayersAndByRule(t *testing.T) {
	rules := map[string]rulesfile.Rules{
		rulesfile.FileName:             {{Pattern: `.*`, Name: "all"}},
		rulesfile.PathInDir("src"):     {{Pattern: `\.go$`, Name: "go"}},
		rulesfile.PathInDir("src/api"): {{Pattern: `\.go$`, Name: "api"}, {Pattern: `(`, Name: "broken"}},
	}
	files := []string{"README.md", "src/api/v1.go", "src/main.go"}
	events := NewReviewStateBuilder()
	chain, err := GetFileRuleChain(t.Context(), rules, files, events, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("GetFileRuleChain: %v", err)
	}

	var dirs []string
	for _, l := range chain.FileChains["src/api/v1.go"] {
		dirs = append(dirs, l.RulesFile())
	}
	if want := []string{rulesfile.FileName, rulesfile.PathInDir("src"), rulesfile.PathInDir("src/api")}; !reflect.DeepEqual(dirs, want) {
		t.Errorf("layers for src/api/v1.go = %v, want %v", dirs, want)
	}
	// The broken pattern is compiled once but reported for the one file
	// whose chain reaches its rules file.
	if got := len(events.Build().Errors); got != 1 {
		t.Errorf("sift errors = %d, want 1", got)
	}

	type entry struct {
		rulesFile, name string
		files           []string
	}
	var got []entry
	for _, rf := range chain.ByRule() {
		got = append(got, entry{rf.RulesFile, rf.Name, rf.Files})
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

// TestByRuleKeepsRepeatedPatterns pins that ByRule groups rules by
// rulesfile.RuleKey, their location, the same identity match results use: a
// pattern declared twice in one rules file is two rules, in declaration
// order. (Positions are set as ParseRules sets them.)
func TestByRuleKeepsRepeatedPatterns(t *testing.T) {
	rules := rulesfile.Set{rulesfile.FileName: {{Pattern: `.*`, Name: "first"}, {Pattern: `.*`, Name: "second", Position: 1}}}
	chain, err := GetFileRuleChain(t.Context(), rules, []string{"a.go"}, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("GetFileRuleChain: %v", err)
	}
	got := chain.ByRule()
	if len(got) != 2 || got[0].Name != "first" || got[1].Name != "second" || got[1].Location() != rulesfile.FileName+"[1]" {
		t.Errorf("ByRule() = %+v, want both declarations, in order", got)
	}
}

// TestGetFileRuleChainStopsWhenCancelled: a sift under a done context
// stops with its error.
func TestGetFileRuleChainStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rules := rulesfile.Set{rulesfile.FileName: {{Pattern: `.*`, Name: "all"}}}
	if _, err := GetFileRuleChain(ctx, rules, []string{"a.go"}, nil, slog.New(slog.DiscardHandler)); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// TestGetFileRuleChainSharesChains: files that match the same rules share
// one chain, wherever they are; files that match different rules do not.
func TestGetFileRuleChainSharesChains(t *testing.T) {
	rules := map[string]rulesfile.Rules{
		rulesfile.FileName:         {{Pattern: `.*`, Name: "all"}},
		rulesfile.PathInDir("src"): {{Pattern: `\.go$`, Name: "go"}},
	}
	files := []string{"a/x.go", "b/y.go", "src/r.md", "src/m.go", "src/n.go"}
	chain, err := GetFileRuleChain(t.Context(), rules, files, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("GetFileRuleChain: %v", err)
	}
	key := func(file string) chainKey { return keyOf(chain.FileChains[file]) }
	for _, same := range [][2]string{{"a/x.go", "b/y.go"}, {"a/x.go", "src/r.md"}, {"src/m.go", "src/n.go"}} {
		if key(same[0]) != key(same[1]) {
			t.Errorf("%s and %s do not share a chain", same[0], same[1])
		}
	}
	if key("a/x.go") == key("src/m.go") {
		t.Errorf("a/x.go and src/m.go share a chain, want distinct")
	}
	if got := len(chain.FileChains["src/m.go"]); got != 2 {
		t.Errorf("src/m.go layers = %d, want 2", got)
	}
}
