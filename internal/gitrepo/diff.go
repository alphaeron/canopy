package gitrepo

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// UnifiedDiff produces a classical unified diff between two versions of a
// single file, in the form expected from `git diff`: a `--- a/path` /
// `+++ b/path` header pair followed by one or more `@@ -start,count
// +start,count @@` hunks. Context is three lines on each side of a change
// group, and overlapping context windows are merged into a single hunk.
// Lines are diffed via the diffmatchpatch library in line mode. An empty
// oldStr models a new file (every line inserted); an empty newStr models a
// deletion. The output is a pure function of the inputs — no timestamps.
func UnifiedDiff(path, oldStr, newStr string) string {
	dmp := diffmatchpatch.New()
	// Line-mode diff is composed of three documented round-trips in
	// diffmatchpatch: compress each input to a line-index string, diff the
	// two strings at the character level, then expand the result back to
	// real newlines. The checklines heuristic does not reliably force
	// line-mode in this library version, so we use the explicit round
	// trip to guarantee diff granularity stays at lines, not characters.
	text1, text2, lineArray := dmp.DiffLinesToChars(oldStr, newStr)
	var ops []op
	for _, d := range dmp.DiffCharsToLines(dmp.DiffMain(text1, text2, false), lineArray) {
		kind := ' '
		switch d.Type {
		case diffmatchpatch.DiffDelete:
			kind = '-'
		case diffmatchpatch.DiffInsert:
			kind = '+'
		}
		for _, line := range splitLines(d.Text) {
			ops = append(ops, op{kind: kind, text: line})
		}
	}

	// Contiguous change ranges: [start, end] inclusive over the ops slice,
	// one entry per cluster of changed lines separated only by other changes.
	type cRange struct{ lo, hi int }
	var changes []cRange
	for i, o := range ops {
		if o.kind == ' ' {
			continue
		}
		if len(changes) == 0 || i > changes[len(changes)-1].hi+1 {
			changes = append(changes, cRange{lo: i, hi: i})
		} else {
			changes[len(changes)-1].hi = i
		}
	}

	const ctx = 3
	// Convert each change into a candidate window with context on either
	// side. Windows are merged when the next candidate begins inside or
	// between the previous window's end.
	type window struct{ lo, hi int }
	var windows []window
	for _, c := range changes {
		lo, hi := c.lo-ctx, c.hi+ctx
		if lo < 0 {
			lo = 0
		}
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		if len(windows) == 0 || lo > windows[len(windows)-1].hi {
			windows = append(windows, window{lo, hi})
		} else {
			windows[len(windows)-1].hi = hi
		}
	}

	if len(windows) == 0 {
		return ""
	}

	// Render the windows as hunks. Line counters walk every window: the
	// old/new starting line numbers for a hunk are the counts of old-style
	// and new-style context lines that precede the first change in the hunk,
	// plus one (1-based).
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	oldLine, newLine := 0, 0
	for i, w := range windows {
		if i > 0 {
			// Skip the prior window's lines up to this window's start, since
			// the window context already covers contiguous ops. Windows are
			// disjoint, so everything prior is context or already printed.
			for j := 0; j < w.lo; j++ {
				if ops[j].kind != '+' {
					oldLine++
				}
				if ops[j].kind != '-' {
					newLine++
				}
			}
		} else {
			oldLine, newLine = 0, 0
		}
		oldStart, newStart := oldLine+1, newLine+1
		oldCount, newCount := 0, 0
		for j := w.lo; j <= w.hi; j++ {
			switch ops[j].kind {
			case ' ':
				oldCount++
				newCount++
			case '-':
				oldCount++
			case '+':
				newCount++
			}
		}
		// A side of the @@ pair that covers zero lines starts at 0, matching
		// the git convention for pure insertions / deletions.
		oldStartStr, newStartStr := strconv.Itoa(oldStart), strconv.Itoa(newStart)
		if oldCount == 0 {
			oldStartStr = "0"
		}
		if newCount == 0 {
			newStartStr = "0"
		}
		fmt.Fprintf(&b, "@@ -%s,%s +%s,%s @@\n",
			oldStartStr, strconv.Itoa(oldCount),
			newStartStr, strconv.Itoa(newCount))
		for j := w.lo; j <= w.hi; j++ {
			b.WriteRune(ops[j].kind)
			b.WriteString(ops[j].text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

type op struct {
	kind rune
	text string
}

// splitLines splits a string on newlines into a slice of lines, dropping a
// single trailing empty entry (the artifact of a trailing newline at end of
// string) so that an input of "a\nb\n" yields ["a", "b"].
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// MaxDiffBytes is the largest file a diff is computed for. Line diffing
// holds both sides in memory and slows sharply with size, so a larger file
// gets LargeFileDiff instead, as GitHub omits the patch of a large file.
const MaxDiffBytes = 1 << 20

// binarySniffBytes is how much of a file git reads to judge it binary.
const binarySniffBytes = 8000

// IsBinary reports whether content is binary as git judges it: a NUL byte
// within its first 8000 bytes.
func IsBinary(content string) bool {
	return strings.IndexByte(content[:min(len(content), binarySniffBytes)], 0) >= 0
}

// BinaryDiff is the diff git prints for a binary file that changed.
func BinaryDiff(path string) string {
	return fmt.Sprintf("Binary files a/%s and b/%s differ\n", path, path)
}

// LargeFileDiff is the diff for a changed file too large to diff (see
// MaxDiffBytes), size being its larger side.
func LargeFileDiff(path string, size int64) string {
	return fmt.Sprintf("Large files a/%s and b/%s differ (%d bytes, over the %d-byte diff limit)\n", path, path, size, MaxDiffBytes)
}
