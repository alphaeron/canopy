package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
	"github.com/alphaeron/kit/internal/tui"
)

// kitSet is the repository's KIT files as a local review sees them: every
// KIT file in the working tree (committed ones that still exist plus new,
// non-ignored ones), read from disk. It is the single source of rules for
// every command and the TUI, and the same set the engine matches with, so
// they always agree on which rules exist.
type kitSet struct {
	root  string                   // repository root on disk
	rules map[string]kitfile.Rules // keyed by repo-relative, slash-separated path
}

// ruleEntry is one rule and the KIT file that declares it.
type ruleEntry struct {
	kitFile string
	rule    kitfile.Rule
}

// errNoKitFiles is reported when an operation needs a KIT file and the
// repository has none.
var errNoKitFiles = errors.New("no KIT files in the repository; create one with `kit init`")

// openKitSet loads the KIT files of the repository containing the current
// directory.
func openKitSet() (*kitSet, error) {
	repo, err := getRepo()
	if err != nil {
		return nil, err
	}
	root, rules, err := (&shonin.LocalFetcher{Repo: repo}).Execute()
	if err != nil {
		return nil, fmt.Errorf("load KIT files: %w", err)
	}
	return &kitSet{root: root, rules: rules}, nil
}

// paths lists the KIT files: the repository-root KIT first, then by path.
func (s *kitSet) paths() []string {
	return slices.SortedFunc(maps.Keys(s.rules), compareKitPaths)
}

// compareKitPaths orders KIT file paths: the repository-root KIT first, then
// by path.
func compareKitPaths(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == kitfile.FileName:
		return -1
	case b == kitfile.FileName:
		return 1
	}
	return strings.Compare(a, b)
}

// entries lists every rule in paths order, or only only's rules when it is
// set.
func (s *kitSet) entries(only string) []ruleEntry {
	var out []ruleEntry
	for _, p := range s.paths() {
		if only != "" && p != only {
			continue
		}
		for _, r := range s.rules[p] {
			out = append(out, ruleEntry{kitFile: p, rule: r})
		}
	}
	return out
}

// diskPath is the on-disk path of a repo-relative KIT file.
func (s *kitSet) diskPath(p string) string {
	return filepath.Join(s.root, filepath.FromSlash(p))
}

// resolve maps a --file value to a KIT file in the set. The value is a path
// from the repository root (or an absolute path inside it) naming a KIT
// file or a directory that holds one, so `--file cmd/kit` means
// cmd/kit/KIT wherever the command runs. An empty value resolves to "",
// meaning every KIT file.
func (s *kitSet) resolve(flag string) (string, error) {
	if flag == "" {
		return "", nil
	}
	p := flag
	if filepath.IsAbs(p) {
		rel, ok := relInside(s.root, p)
		if !ok {
			return "", fmt.Errorf("--file %q is outside the repository", flag)
		}
		p = rel
	}
	p = path.Clean(filepath.ToSlash(p))
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("--file %q is outside the repository", flag)
	}
	if !kitfile.IsKitFile(p) {
		p = path.Join(p, kitfile.FileName)
	}
	if _, ok := s.rules[p]; !ok {
		if len(s.rules) == 0 {
			return "", errNoKitFiles
		}
		return "", fmt.Errorf("no KIT file at %q (KIT files: %s)", p, strings.Join(s.paths(), ", "))
	}
	return p, nil
}

// relInside returns p relative to root when p is inside root, comparing
// the paths as given and then with symlinks resolved (/tmp is
// /private/tmp on macOS, and p may not exist yet).
func relInside(root, p string) (string, bool) {
	for _, pair := range [][2]string{{root, p}, {realPath(root), realPath(p)}} {
		if rel, err := filepath.Rel(pair[0], pair[1]); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			return rel, true
		}
	}
	return "", false
}

// realPath resolves symlinks so paths compare reliably (e.g. /tmp and
// /private/tmp on macOS); it returns p unchanged if that fails.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// withPattern lists the KIT files declaring a rule with pattern.
func (s *kitSet) withPattern(pattern string) []string {
	var out []string
	for _, p := range s.paths() {
		if slices.ContainsFunc(s.rules[p], func(r kitfile.Rule) bool { return r.Pattern == pattern }) {
			out = append(out, p)
		}
	}
	return out
}

// nearest is the KIT file closest to the current directory: the one in it
// or its nearest ancestor up to the repository root, falling back to the
// first KIT file. It is "" only when there are no KIT files.
func (s *kitSet) nearest() string {
	if wd, err := os.Getwd(); err == nil {
		if rel, ok := relInside(s.root, wd); ok {
			for dir := filepath.ToSlash(rel); ; dir = path.Dir(dir) {
				p := path.Join(dir, kitfile.FileName)
				if _, ok := s.rules[p]; ok {
					return p
				}
				if dir == "." {
					break
				}
			}
		}
	}
	if ps := s.paths(); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

// scopeNames lists the distinct rule names, the values --scope accepts.
func (s *kitSet) scopeNames() []string {
	seen := map[string]bool{}
	for _, rules := range s.rules {
		for _, r := range rules {
			if r.Name != "" {
				seen[r.Name] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// ruleTarget finds the KIT file holding the rule keyed by pattern, for
// edit and remove. --file names it explicitly. Otherwise every KIT file is
// searched: a single match is used; several are resolved with a picker when
// interactive and are an error otherwise, so a script never edits a file it
// did not choose.
func (s *kitSet) ruleTarget(fileFlag, pattern string, interactive bool) (string, error) {
	if fileFlag != "" {
		return s.resolve(fileFlag)
	}
	found := s.withPattern(pattern)
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) == 0:
		return "", fmt.Errorf("no rule with pattern %q in any KIT file: %w", pattern, kitfile.ErrPatternNotFound)
	case !interactive:
		return "", fmt.Errorf("pattern %q is in %d KIT files (%s); pass --file to choose one",
			pattern, len(found), strings.Join(found, ", "))
	}
	choices := make([]tui.Choice, len(found))
	for i, p := range found {
		label := p
		if j := slices.IndexFunc(s.rules[p], func(r kitfile.Rule) bool { return r.Pattern == pattern }); j >= 0 && s.rules[p][j].Name != "" {
			label += ": " + s.rules[p][j].Name
		}
		choices[i] = tui.Choice{Label: label, Value: p}
	}
	return tui.Pick(fmt.Sprintf("Rule %q is in several KIT files. Which one?", pattern), choices)
}

// mutate loads a KIT file of the set from disk, applies change, and saves
// it. Every add, edit, and remove, from the CLI or the TUI, goes through it.
func (s *kitSet) mutate(kitFile string, change func(*kitfile.KitFile) error) (*kitfile.KitFile, error) {
	disk := s.diskPath(kitFile)
	k, err := kitfile.LoadKitFile(disk)
	if err != nil {
		return nil, fmt.Errorf("load KIT file %q: %w", kitFile, err)
	}
	if err := change(k); err != nil {
		return nil, err
	}
	if err := k.Save(disk); err != nil {
		return nil, fmt.Errorf("save %s: %w", kitFile, err)
	}
	return k, nil
}
