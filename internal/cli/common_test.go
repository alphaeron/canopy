package cli

import (
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestRuleTitles(t *testing.T) {
	if got := ruleTitles(nil); got != "none" {
		t.Errorf("empty set should read 'none', got %q", got)
	}
	// An unnamed rule is named by its pattern, so a file it matches never
	// reads as unmatched.
	s := rulesfile.Rules{{Name: "core"}, {Pattern: `\.go$`}}
	if got := ruleTitles(s); got != `core, \.go$` {
		t.Errorf("got %q, want the name, then the unnamed rule's pattern", got)
	}
}
