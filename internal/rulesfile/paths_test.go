package rulesfile

import (
	"strings"
	"testing"
)

// TestIsRulesFile pins whole-name matching: the GitHub fetcher used to match
// any path containing FileName, so look-alike files were parsed as rules.
func TestIsRulesFile(t *testing.T) {
	tests := map[string]bool{
		FileName:                           true,
		PathInDir("cmd/app"):               true,
		FileName + "CHEN.md":               false,
		"docs/" + FileName + "CHEN.md":     false,
		FileName + "/readme.md":            false,
		PathInDir("svc") + ".bak":          false,
		"svc/" + strings.ToLower(FileName): false,
		"svc/MY" + FileName:                false,
	}
	for p, want := range tests {
		if got := IsRulesFile(p); got != want {
			t.Errorf("IsRulesFile(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestPathInDir(t *testing.T) {
	for dir, want := range map[string]string{
		"":        FileName,
		".":       FileName,
		"src":     PathInDir("src"),
		"src/api": PathInDir("src/api"),
	} {
		if got := PathInDir(dir); got != want {
			t.Errorf("PathInDir(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestDir(t *testing.T) {
	for p, want := range map[string]string{FileName: "", PathInDir("svc"): "svc", PathInDir("a/b"): "a/b"} {
		if got := Dir(p); got != want {
			t.Errorf("Dir(%q) = %q, want %q", p, got, want)
		}
		if want != "" && PathInDir(want) != p {
			t.Errorf("PathInDir(Dir(%q)) = %q", p, PathInDir(want))
		}
	}
}
