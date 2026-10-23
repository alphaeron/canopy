package gitrepo

import (
	"strings"
	"testing"
)

// headerLines counts the number of "@@ ... @@" hunk headers in a unified
// diff. A naive strings.Count("@@") would double-count, since each header
// line begins and ends with @@.
func headerLines(diff string) []string {
	var out []string
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "@@") {
			out = append(out, l)
		}
	}
	return out
}

func TestUnifiedDiffSmallChange(t *testing.T) {
	oldStr := "alpha\nbeta\ngamma\ndelta\n"
	newStr := "alpha\nbravo\ngamma\ndelta\n"
	got := UnifiedDiff("f.txt", oldStr, newStr)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	var header string
	for _, l := range lines {
		if strings.HasPrefix(l, "@@") {
			header = l
			break
		}
	}
	if header != "@@ -1,4 +1,4 @@" {
		t.Errorf("hunk header = %q, want \"@@ -1,4 +1,4 @@\"", header)
	}
	if !strings.Contains(got, "-beta\n") {
		t.Errorf("missing deletion of 'beta':\n%s", got)
	}
	if !strings.Contains(got, "+bravo\n") {
		t.Errorf("missing insertion of 'bravo':\n%s", got)
	}
	if !strings.HasPrefix(got, "--- a/f.txt\n+++ b/f.txt\n") {
		t.Errorf("bad leading headers:\n%s", got)
	}
}

func TestUnifiedDiffEmptyOldAndNew(t *testing.T) {
	if got := UnifiedDiff("f.txt", "", ""); got != "" {
		t.Errorf("empty input should produce empty output, got %q", got)
	}
}

func TestUnifiedDiffNewFile(t *testing.T) {
	got := UnifiedDiff("new.txt", "", "hello\nworld\n")
	if !strings.Contains(got, "hello\n") || !strings.Contains(got, "world\n") {
		t.Errorf("new-file diff missing content:\n%s", got)
	}
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, "@@") {
			if !strings.Contains(l, "-0,0 +1,2 @@") {
				t.Errorf("new-file hunk header = %q, want pure-insertion form", l)
			}
		}
	}
}

func TestUnifiedDiffDeletion(t *testing.T) {
	got := UnifiedDiff("del.txt", "one\ntwo\nthree\n", "")
	if !strings.Contains(got, "-one\n") || !strings.Contains(got, "-three\n") {
		t.Errorf("deletion missing removed lines:\n%s", got)
	}
}

func TestUnifiedDiffMergeNearbyChangesIntoOneHunk(t *testing.T) {
	// Two changes within three lines of each other collapse into one hunk.
	oldStr := "a\nb\nc\nd\ne\nf\ng\n"
	newStr := "a\nB\nc\nd\ne\nF\ng\n"
	got := UnifiedDiff("f.txt", oldStr, newStr)
	if n := len(headerLines(got)); n != 1 {
		t.Errorf("expected one merged hunk, got %d\n%s", n, got)
	}
}

func TestUnifiedDiffSeparateHunksForDistantChanges(t *testing.T) {
	// Two changes more than six lines apart produce two distinct hunks: with
	// three lines of context on each side, the context windows stop overlapping
	// once the change indices differ by more than 2*3=6.
	oldStr := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nm\n"
	newStr := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\nM\n"
	got := UnifiedDiff("f.txt", oldStr, newStr)
	if n := len(headerLines(got)); n != 2 {
		t.Errorf("expected two hunks, got %d\n%s", n, got)
	}
}

func TestIsBinary(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "empty", content: "", want: false},
		{name: "text", content: "package main\n", want: false},
		{name: "a NUL early", content: "PNG\x00\x01", want: true},
		{name: "a NUL past the sniffed prefix", content: strings.Repeat("a", 8000) + "\x00", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBinary(tt.content); got != tt.want {
				t.Errorf("IsBinary = %v, want %v", got, tt.want)
			}
		})
	}
}
