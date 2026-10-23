package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alphaeron/kit/internal/brand"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesSet is the repository's rules files as a local review sees them: every
// rules file in the working tree (committed ones that still exist plus new,
// non-ignored ones), read from disk. It is the single source of rules for
// every command and the TUI, and the same set the engine matches with, so
// they always agree on which rules exist.
type rulesSet struct {
	root  string        // repository root on disk
	rules rulesfile.Set // keyed by repo-relative, slash-separated path
}

// errNoRulesFiles is reported when an operation needs a rules file and the
// repository has none.
var errNoRulesFiles = errors.New("no rules files in the repository; create one with `" + brand.Command + " init`")

// openRulesSet loads the rules files of the repository containing the current
// directory.
func openRulesSet(ctx context.Context) (*rulesSet, error) {
	repo, engine, err := openLocal(nil)
	if err != nil {
		return nil, err
	}
	return loadRulesSet(ctx, repo, engine)
}

// loadRulesSet is the rules files engine reads from repo, sharing the
// engine's fetch.
func loadRulesSet(ctx context.Context, repo *gitRepo, engine *review.Engine) (*rulesSet, error) {
	root, err := repo.Root()
	if err != nil {
		return nil, fmt.Errorf("get repository root: %w", err)
	}
	rules, err := engine.Source.Evaluator().Fetch.Execute(ctx)
	if err != nil {
		return nil, fmt.Errorf("load rules files: %w", err)
	}
	return &rulesSet{root: root, rules: rules}, nil
}

// entries lists every rule in paths order, or only only's rules when it is
// set.
func (s *rulesSet) entries(only string) []rulesfile.DeclaredRule {
	var out []rulesfile.DeclaredRule
	for _, p := range s.rules.Paths() {
		if only != "" && p != only {
			continue
		}
		for _, r := range s.rules[p] {
			out = append(out, rulesfile.DeclaredRule{RulesFile: p, Rule: r})
		}
	}
	return out
}

// diskPath is the on-disk path of a repo-relative rules file.
func (s *rulesSet) diskPath(p string) string {
	return filepath.Join(s.root, filepath.FromSlash(p))
}

// resolve maps a --file value to a rules file in the set. The value is a path
// from the repository root (or an absolute path inside it) naming a rules file
// or a directory that holds one, so `--file cmd/app` means cmd/app/<rules file>
// wherever the command runs. An empty value resolves to "", meaning every rules
// file.
func (s *rulesSet) resolve(flag string) (string, error) {
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
	if !rulesfile.IsRulesFile(p) {
		p = path.Join(p, rulesfile.FileName)
	}
	if _, ok := s.rules[p]; !ok {
		if len(s.rules) == 0 {
			return "", errNoRulesFiles
		}
		return "", fmt.Errorf("no rules file at %q (rules files: %s)", p, strings.Join(s.rules.Paths(), ", "))
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

// withPattern lists the rules files declaring a rule with pattern.
func (s *rulesSet) withPattern(pattern string) []string {
	var out []string
	for _, p := range s.rules.Paths() {
		if slices.ContainsFunc(s.rules[p], func(r rulesfile.Rule) bool { return r.Pattern == pattern }) {
			out = append(out, p)
		}
	}
	return out
}

// rule returns the rule keyed by pattern in rulesFile.
func (s *rulesSet) rule(rulesFile, pattern string) (rulesfile.Rule, error) {
	i := slices.IndexFunc(s.rules[rulesFile], func(r rulesfile.Rule) bool { return r.Pattern == pattern })
	if i < 0 {
		return rulesfile.Rule{}, fmt.Errorf("no rule with pattern %q in %s: %w", pattern, rulesFile, rulesfile.ErrPatternNotFound)
	}
	return s.rules[rulesFile][i], nil
}

// patterns lists the distinct rule patterns, the values rules edit and
// rules remove accept.
func (s *rulesSet) patterns() []string {
	seen := map[string]bool{}
	for _, rules := range s.rules {
		for _, r := range rules {
			seen[r.Pattern] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// nearest is the rules file closest to the current directory: the one in it
// or its nearest ancestor up to the repository root, falling back to the
// first rules file. It is "" only when there are no rules files.
// nearestTo is the rules file governing directory dir (slash-separated,
// repository-relative, "." for the root): dir's own, or its nearest
// ancestor's; "" when there is none.
func (s *rulesSet) nearestTo(dir string) string {
	for d := path.Clean(dir); ; d = path.Dir(d) {
		p := path.Join(d, rulesfile.FileName)
		if _, ok := s.rules[p]; ok {
			return p
		}
		if d == "." || d == "/" {
			return ""
		}
	}
}

func (s *rulesSet) nearest() string {
	if wd, err := os.Getwd(); err == nil {
		if rel, ok := relInside(s.root, wd); ok {
			for dir := filepath.ToSlash(rel); ; dir = path.Dir(dir) {
				p := path.Join(dir, rulesfile.FileName)
				if _, ok := s.rules[p]; ok {
					return p
				}
				if dir == "." {
					break
				}
			}
		}
	}
	if ps := s.rules.Paths(); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

// ruleTarget finds the rules file holding the rule keyed by pattern, for
// edit and remove. --file names it explicitly. Otherwise every rules file is
// searched: a single match is used; several are resolved with a picker when
// interactive and are an error otherwise, so a script never edits a file it
// did not choose.
func (s *rulesSet) ruleTarget(fileFlag, pattern string, interactive bool) (string, error) {
	if fileFlag != "" {
		return s.resolve(fileFlag)
	}
	found := s.withPattern(pattern)
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) == 0:
		return "", fmt.Errorf("no rule with pattern %q in any rules file: %w", pattern, rulesfile.ErrPatternNotFound)
	case !interactive:
		return "", fmt.Errorf("pattern %q is in %d rules files (%s); pass --file to choose one",
			pattern, len(found), strings.Join(found, ", "))
	}
	choices := make([]tui.Choice, len(found))
	for i, p := range found {
		label := p
		if j := slices.IndexFunc(s.rules[p], func(r rulesfile.Rule) bool { return r.Pattern == pattern }); j >= 0 && s.rules[p][j].Name != "" {
			label += ": " + s.rules[p][j].Name
		}
		choices[i] = tui.Choice{Label: label, Value: p}
	}
	return tui.Pick(fmt.Sprintf("Rule %q is in several rules files. Which one?", pattern), choices)
}

// mutate loads a rules file of the set from disk, applies change, and saves
// it. Every add, edit, and remove, from the CLI or the TUI, goes through it.
func (s *rulesSet) mutate(rulesFile string, change func(*rulesfile.File) error) (*rulesfile.File, error) {
	disk := s.diskPath(rulesFile)
	k, err := rulesfile.Load(disk)
	if err != nil {
		return nil, fmt.Errorf("load rules file %q: %w", rulesFile, err)
	}
	if err := change(k); err != nil {
		return nil, err
	}
	if err := k.Save(disk); err != nil {
		return nil, fmt.Errorf("save %s: %w", rulesFile, err)
	}
	return k, nil
}
