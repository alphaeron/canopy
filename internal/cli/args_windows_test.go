package cli

import (
	"slices"
	"testing"
)

func TestPlatformArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"none", nil, []string{}},
		{"question flag", []string{"-?"}, []string{"--help"}},
		{"slash question", []string{"/?"}, []string{"--help"}},
		{"subcommand", []string{"rules", "add", "/?"}, []string{"rules", "add", "--help"}},
		{"other args untouched", []string{"review", "-h", "a/?b"}, []string{"review", "-h", "a/?b"}},
		{"after terminator", []string{"review", "--", "/?"}, []string{"review", "--", "/?"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := slices.Clone(tt.args)
			got := platformArgs(args)
			if !slices.Equal(got, tt.want) {
				t.Errorf("platformArgs(%q) = %q, want %q", tt.args, got, tt.want)
			}
			if !slices.Equal(args, tt.args) {
				t.Errorf("platformArgs modified its input: %q", args)
			}
		})
	}
}
