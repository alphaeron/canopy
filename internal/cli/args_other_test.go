//go:build !windows

package cli

import (
	"slices"
	"testing"
)

func TestPlatformArgsUnchanged(t *testing.T) {
	args := []string{"-?", "/?", "rules", "--help"}
	if got := platformArgs(args); !slices.Equal(got, args) {
		t.Errorf("platformArgs(%q) = %q, want it unchanged", args, got)
	}
}
