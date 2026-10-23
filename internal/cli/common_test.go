package cli

import (
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestRuleTitles(t *testing.T) {
	if got := ruleTitles(nil); got != "none" {
		t.Errorf("empty set should read 'none', got %q", got)
	}
	s := rulesfile.Rules{{Name: "core"}, {Name: "sec"}}
	if got := ruleTitles(s); got != "core sec" {
		t.Errorf("got %q, want 'core sec'", got)
	}
}
