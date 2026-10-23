package rulesfile

import (
	"path"
	"strings"

	"github.com/alphaeron/kit/internal/brand"
)

// FileName is the name every rules file has, in any directory.
const FileName = brand.RulesFileName

// IsRulesFile reports whether the slash-separated repository path names a
// rules file: its final element is exactly FileName. Matching the whole
// name keeps files whose names merely start or end with FileName from being
// read as rules.
func IsRulesFile(p string) bool {
	return path.Base(p) == FileName
}

// PathInDir is the slash-separated repository path of the rules file in dir:
// FileName for the repository root ("" or "."), dir/FileName otherwise.
// It is the key a fetcher's rule map uses for that directory's rules.
func PathInDir(dir string) string {
	return path.Join(dir, FileName)
}

// ComparePaths orders rules file paths: the repository-root rules file
// first, then by path.
func ComparePaths(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == FileName:
		return -1
	case b == FileName:
		return 1
	}
	return strings.Compare(a, b)
}

// Dir is the directory of the rules file at the repo-relative path p, the
// inverse of PathInDir: "" for the repository-root rules file.
func Dir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}
