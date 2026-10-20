package shonin

import (
	"reflect"
	"sort"
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
)

func TestInvertRuleChainMapsRulesAndDirectories(t *testing.T) {
	chain := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/api.go": {{
			DirectoryPath: "",
			Rules:         []Rule{{Rule: kitfile.Rule{Pattern: `.*\.go$`, Name: "root rule"}}},
		}},
		"/src/server.go": {{
			DirectoryPath: "src",
			Rules:         []Rule{{Rule: kitfile.Rule{Pattern: `.*\.go$`, Name: "src rule"}}},
		}},
		"/docs/readme.md": nil,
	}}

	got := InvertRuleChain(chain)

	want := map[string][]string{
		`.*\.go$@KIT`:     {"/src/api.go"},
		`.*\.go$@src/KIT`: {"/src/server.go"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inversion = %v, want %v", got, want)
	}
	// File lists must arrive sorted so a caller never depends on map
	// iteration order.
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
}

func TestInvertRuleChainDedupesSameFileAcrossLayers(t *testing.T) {
	// The same pattern at two layers both apply to api.go. The file appears
	// once in BOTH layer scopes (the two patterns are separate keys) but the
	// root pattern's scope lists it exactly once.
	chain := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/api.go": {
			{DirectoryPath: "", Rules: []Rule{{Rule: kitfile.Rule{Pattern: ".*.go"}}}},
			{DirectoryPath: "src", Rules: []Rule{{Rule: kitfile.Rule{Pattern: "src\\.go"}}}},
		},
	}}
	got := InvertRuleChain(chain)
	if len(got[".*.go@KIT"]) != 1 || got[".*.go@KIT"][0] != "/src/api.go" {
		t.Errorf("root pattern scope = %v, want exactly [\"/src/api.go\"]", got[".*.go@KIT"])
	}
	if len(got["src\\.go@src/KIT"]) != 1 {
		t.Errorf("nested pattern scope = %v, want exactly [\"/src/api.go\"]", got["src\\.go@src/KIT"])
	}
}

func TestLayerKeyRootAndNested(t *testing.T) {
	if got := layerKey(""); got != "KIT" {
		t.Errorf("layerKey(\"\") = %q, want KIT", got)
	}
	if got := layerKey("src"); got != "src/KIT" {
		t.Errorf("layerKey(src) = %q, want src/KIT", got)
	}
	if got := layerKey("a/b/c"); got != "a/b/c/KIT" {
		t.Errorf("layerKey(a/b/c) = %q, want a/b/c/KIT", got)
	}
}
