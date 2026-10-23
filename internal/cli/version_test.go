package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	withMain := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}
	tests := []struct {
		name   string
		linked string
		info   *debug.BuildInfo
		want   string
	}{
		{"linked wins", "1.2.3", withMain("v0.9.0"), "1.2.3"},
		{"module version", "", withMain("v0.9.0"), "v0.9.0"},
		{"local checkout", "", withMain("(devel)"), "dev"},
		{"empty module version", "", withMain(""), "dev"},
		{"no build info", "", nil, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.linked, tt.info); got != tt.want {
				t.Errorf("resolveVersion(%q, ...) = %q, want %q", tt.linked, got, tt.want)
			}
		})
	}
}
