package rulesedit

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

// ErrNoRulesFiles reports that an operation needs a rules file and the
// workspace has none.
var ErrNoRulesFiles = errors.New("no rules files in the repository; create one with `" + brand.Command + " init`")

// ErrOutsideRepository reports a path that is not inside the workspace's
// repository.
var ErrOutsideRepository = errors.New("outside the repository")

// AmbiguousRuleError reports a rule pattern that several rules declare, in
// one rules file or several, so the pattern alone does not identify one
// rule.
type AmbiguousRuleError struct {
	Pattern string
	// Rules are the rules declaring Pattern, in rulesfile.RuleKey order,
	// each with its location set.
	Rules rulesfile.Rules
}

func (e *AmbiguousRuleError) Error() string {
	refs := make([]string, len(e.Rules))
	for i, r := range e.Rules {
		refs[i] = r.Location()
		if r.Name != "" {
			refs[i] += " (" + r.Name + ")"
		}
	}
	return fmt.Sprintf("pattern %q matches %d rules: %s", e.Pattern, len(e.Rules), strings.Join(refs, ", "))
}

// Validator reports the violations of a set of rules files, keyed by path,
// omitting valid files: the review platform's validation
// (approval.Evaluator.Validate), which adds its own checks to
// rulesfile.Set.Validate.
type Validator interface {
	Validate(rules rulesfile.Set) map[string][]error
}

// Workspace is the rules files of a repository checkout on disk: Rules is
// every rules file the working tree has that loaded, keyed as in
// rulesfile.Set; Broken is every one that could not be read or parsed,
// with why; and Root is the repository root they are read from and written
// to. Lookups answer from Rules; changes are written to disk and are not
// reflected in Rules. Validator validates the rules as the review platform
// does; nil validates with rulesfile.Set.Validate alone.
type Workspace struct {
	Root      string
	Rules     rulesfile.Set
	Broken    map[string]error
	Validator Validator
}

// Paths lists every rules file, the broken ones included, in
// rulesfile.ComparePaths order.
func (w *Workspace) Paths() []string {
	paths := slices.Concat(slices.Collect(maps.Keys(w.Rules)), slices.Collect(maps.Keys(w.Broken)))
	slices.SortFunc(paths, rulesfile.ComparePaths)
	return paths
}

// PathsMatching lists the rules files, broken ones included, whose paths
// the regular expression pattern matches (every one when it is empty), in
// ComparePaths order. The result is never nil, so it encodes as a JSON list.
func (w *Workspace) PathsMatching(pattern string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	out := []string{}
	for _, p := range w.Paths() {
		if re.MatchString(p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// Entries lists every rule, or only only's rules when it is set, in
// rulesfile.RuleKey order, each with its location set.
func (w *Workspace) Entries(only string) rulesfile.Rules {
	var out rulesfile.Rules
	for _, p := range w.Rules.Paths() {
		if only != "" && p != only {
			continue
		}
		for i, r := range w.Rules[p] {
			r.RulesFile, r.Position = p, i
			out = append(out, r)
		}
	}
	return out
}

// DiskPath is the on-disk path of the repo-relative rules file p.
func (w *Workspace) DiskPath(p string) string {
	return filepath.Join(w.Root, filepath.FromSlash(p))
}

// Resolve maps a path naming a rules file to its key in Rules. The path is
// from the repository root (or absolute, inside it) and names a rules file
// or the directory holding one, so "cmd/app" means cmd/app/FileName. An
// empty path resolves to "", meaning every rules file. A broken rules file
// resolves like any other. A path outside the repository is
// ErrOutsideRepository; one naming no rules file in the workspace is an
// error listing those that exist, or ErrNoRulesFiles.
func (w *Workspace) Resolve(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	rel := p
	if filepath.IsAbs(rel) {
		var ok bool
		if rel, ok = relInside(w.Root, p); !ok {
			return "", fmt.Errorf("%q is %w", p, ErrOutsideRepository)
		}
	}
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%q is %w", p, ErrOutsideRepository)
	}
	if !rulesfile.IsRulesFile(rel) {
		rel = path.Join(rel, rulesfile.FileName)
	}
	paths := w.Paths()
	if !slices.Contains(paths, rel) {
		if len(paths) == 0 {
			return "", ErrNoRulesFiles
		}
		return "", fmt.Errorf("no rules file at %q (rules files: %s)", rel, strings.Join(paths, ", "))
	}
	return rel, nil
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

// Locate is the one rule declaring pattern, with its location set. only,
// when set, narrows the search to one rules file, and position, when not
// negative, to the rule at that 0-based position. No such rule wraps
// ErrRuleNotFound, and several are an *AmbiguousRuleError.
func (w *Workspace) Locate(pattern, only string, position int) (rulesfile.Rule, error) {
	var found rulesfile.Rules
	for _, r := range w.Entries(only) {
		if r.Pattern == pattern && (position < 0 || r.Position == position) {
			found = append(found, r)
		}
	}
	switch len(found) {
	case 0:
		where := "any rules file"
		if only != "" {
			where = only
		}
		if position >= 0 {
			where += fmt.Sprintf(" at position %d", position)
		}
		return rulesfile.Rule{}, fmt.Errorf("no rule with pattern %q in %s: %w", pattern, where, ErrRuleNotFound)
	case 1:
		return found[0], nil
	}
	return rulesfile.Rule{}, &AmbiguousRuleError{Pattern: pattern, Rules: found}
}

// Patterns lists the distinct rule patterns, sorted.
func (w *Workspace) Patterns() []string {
	seen := map[string]bool{}
	for _, rules := range w.Rules {
		for _, r := range rules {
			seen[r.Pattern] = true
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// NearestTo is the rules file governing directory dir (slash-separated,
// repository-relative, "." for the root): dir's own, or its nearest
// ancestor's; "" when there is none.
func (w *Workspace) NearestTo(dir string) string {
	for d := path.Clean(dir); ; d = path.Dir(d) {
		p := path.Join(d, rulesfile.FileName)
		if _, ok := w.Rules[p]; ok {
			return p
		}
		if d == "." || d == "/" {
			return ""
		}
	}
}

// Nearest is the rules file closest to the on-disk directory dir: the one
// NearestTo finds when dir is inside the repository, falling back to the
// first rules file. It is "" only when there are no rules files.
func (w *Workspace) Nearest(dir string) string {
	if dir != "" {
		if rel, ok := relInside(w.Root, dir); ok {
			if p := w.NearestTo(filepath.ToSlash(rel)); p != "" {
				return p
			}
		}
	}
	if ps := w.Rules.Paths(); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

// Parent is the rules file above rulesFile, whose rules it inherits unless
// a rule ignores parent directories; "" when there is none.
func (w *Workspace) Parent(rulesFile string) string {
	dir := path.Dir(rulesFile)
	if dir == "." {
		return ""
	}
	return w.NearestTo(path.Dir(dir))
}

// Validate reports the violations of every rules file, or only of only
// when it is set, as Validator reports them. A broken rules file's one
// violation is why it could not be read or parsed.
func (w *Workspace) Validate(only string) map[string][]error {
	set := w.Rules
	if only != "" {
		set = rulesfile.Set{}
		if rules, ok := w.Rules[only]; ok {
			set[only] = rules
		}
	}
	var out map[string][]error
	if w.Validator != nil {
		out = w.Validator.Validate(set)
	} else {
		out = set.Validate()
	}
	for p, e := range w.Broken {
		if only == "" || p == only {
			out[p] = []error{e}
		}
	}
	return out
}

// mutate loads rulesFile from disk, applies change, and saves the result,
// returning the rules as saved, each with its location set. Every change to
// a rules file in the workspace goes through it; a change that fails is not
// saved.
func (w *Workspace) mutate(rulesFile string, change func(rulesfile.Rules) (rulesfile.Rules, error)) (rulesfile.Rules, error) {
	disk := w.DiskPath(rulesFile)
	rules, err := Load(disk)
	if err != nil {
		return nil, fmt.Errorf("load rules file %q: %w", rulesFile, err)
	}
	if rules, err = change(rules); err != nil {
		return nil, err
	}
	if err := Save(disk, rules); err != nil {
		return nil, fmt.Errorf("save %s: %w", rulesFile, err)
	}
	for i := range rules {
		rules[i].RulesFile, rules[i].Position = rulesFile, i
	}
	return rules, nil
}

// Add appends rule to rulesFile, after every rule there (so it is applied
// last in its layer), and returns the file's rules as saved. A rule with a
// violation is rejected.
func (w *Workspace) Add(rulesFile string, rule rulesfile.Rule) (rulesfile.Rules, error) {
	return w.mutate(rulesFile, func(rules rulesfile.Rules) (rulesfile.Rules, error) { return add(rules, rule) })
}

// Edit replaces the rule at target's location with rule and returns it as
// saved. A rule's pattern is part of what the rule is, so rule keeps
// target's pattern. The rules file must still hold target, unchanged, at
// that position, or the edit fails with ErrRulesFileChanged rather than
// change another rule. A rule with a violation is rejected.
func (w *Workspace) Edit(target, rule rulesfile.Rule) (rulesfile.Rule, error) {
	rule.Pattern = target.Pattern
	rules, err := w.mutate(target.RulesFile, func(rules rulesfile.Rules) (rulesfile.Rules, error) {
		return replace(rules, target, rule)
	})
	if err != nil {
		return rulesfile.Rule{}, err
	}
	return rules[target.Position], nil
}

// Remove deletes the rule at target's location. As with Edit, the rules
// file must still hold target, unchanged, at that position.
func (w *Workspace) Remove(target rulesfile.Rule) error {
	_, err := w.mutate(target.RulesFile, func(rules rulesfile.Rules) (rulesfile.Rules, error) { return remove(rules, target) })
	return err
}
