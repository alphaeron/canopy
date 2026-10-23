// Package rulesfile defines the rules file schema: rules, approver targets, and
// combine strategies, with YAML parsing, validation, and editing.
package rulesfile

import (
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"

	"gopkg.in/yaml.v3"

	"github.com/alphaeron/kit/internal/brand"
)

// ErrPatternNotFound is returned by rule operations when no rule with the
// given pattern exists in the rules file. Callers identify it with errors.Is.
var ErrPatternNotFound = errors.New("no rule with that pattern in the rules file")

// File is the complete, order-preserving view of one rules file on disk.
// Rules keeps the file's exact rule order (YAML sequence order), so saving a
// File reproduces the original file modulo formatting.
type File struct {
	// Path records where the file came from: the path given to
	// Load, or the repo-relative key of a Set entry. It is empty for
	// files synthesized in memory.
	Path  string
	Rules Rules
}

// Load reads and parses the rules file at path. It returns an error
// when the file is missing or does not parse into the rule sequence form.
func Load(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read rules file %q: %w", path, err)
	}
	dat, err := ReadLimited(f)
	closeErr := f.Close()
	if err != nil {
		return nil, fmt.Errorf("read rules file %q: %w", path, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close rules file %q: %w", path, closeErr)
	}

	rules, err := ParseRules(dat)
	if err != nil {
		return nil, fmt.Errorf("parse rules file %q: %w", path, err)
	}
	return &File{Path: path, Rules: rules}, nil
}

// Save writes the rules to path in the dynamic-key sequence form, matching
// the form Load and UnmarshalYAML parse. The target file is created
// or truncated; the caller owns the path (rules files are named FileName).
func (k *File) Save(path string) error {
	dat, err := yaml.Marshal(k.Rules)
	if err != nil {
		return fmt.Errorf("marshal rules: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create rules file %q: %w", path, err)
	}
	if _, err := file.Write(dat); err != nil {
		_ = file.Close()
		return fmt.Errorf("write rules file %q: %w", path, err)
	}
	return file.Close()
}

// RuleByPattern returns the rule registered under the exact pattern string.
// Patterns are unique within a file (Violations reports duplicates).
func (k *File) RuleByPattern(pattern string) (*Rule, bool) {
	for i := range k.Rules {
		if k.Rules[i].Pattern == pattern {
			return &k.Rules[i], true
		}
	}
	return nil, false
}

// Add appends rule at the end of the file's rule list. It rejects rules
// whose pattern is already registered or whose pattern is not a valid
// regular expression, so Violations stays empty after every mutation.
func (k *File) Add(rule Rule) error {
	if _, exists := k.RuleByPattern(rule.Pattern); exists {
		return fmt.Errorf("add rule: pattern %q is already registered", rule.Pattern)
	}
	if _, err := regexp.Compile(rule.Pattern); err != nil {
		return fmt.Errorf("add rule: invalid pattern %q: %w", rule.Pattern, err)
	}
	k.Rules = append(k.Rules, rule)
	return nil
}

// Edit applies mutate to the rule registered under pattern. The returned
// *ErrorPatternNotFound sentinel (ErrPatternNotFound) is wrapped with the
// offending pattern so callers can identify the failure with errors.Is.
func (k *File) Edit(pattern string, mutate func(*Rule)) error {
	rule, ok := k.RuleByPattern(pattern)
	if !ok {
		return fmt.Errorf("edit: %w: %q", ErrPatternNotFound, pattern)
	}
	mutate(rule)
	return nil
}

// Remove deletes the rule registered under pattern.
func (k *File) Remove(pattern string) error {
	for i := range k.Rules {
		if k.Rules[i].Pattern == pattern {
			k.Rules = append(k.Rules[:i], k.Rules[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("remove: %w: %q", ErrPatternNotFound, pattern)
}

// Violations lists every integrity violation in the file: duplicate
// patterns, patterns that are not valid regular expressions, and approver
// targets with no target name or no required count.
func (k *File) Violations() []error {
	seen := make(map[string]bool, len(k.Rules))
	var all []error
	for _, rule := range k.Rules {
		if seen[rule.Pattern] {
			all = append(all, fmt.Errorf("duplicate pattern %q", rule.Pattern))
			continue
		}
		seen[rule.Pattern] = true

		if _, err := regexp.Compile(rule.Pattern); err != nil {
			all = append(all, fmt.Errorf("invalid pattern %q: %w", rule.Pattern, err))
		}

		for _, approver := range rule.Approvers {
			if approver.Target == "" {
				all = append(all, fmt.Errorf("rule %q has an approver with no target", rule.Pattern))
			}
			if approver.RequiredCount < 1 {
				all = append(all, fmt.Errorf("rule %q approver %q has no required count", rule.Pattern, approver.Target))
			}
		}
	}
	return all
}

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
