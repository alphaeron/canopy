package kitfile

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// ErrPatternNotFound is returned by rule operations when no rule with the
// given pattern exists in the KIT file. Callers identify it with errors.Is.
var ErrPatternNotFound = errors.New("no rule with that pattern in the KIT file")

// KitFile is the complete, order-preserving view of one KIT file on disk.
// Rules keeps the file's exact rule order (YAML sequence order), so saving a
// KitFile reproduces the original file modulo formatting.
type KitFile struct {
	// Path records where the file was loaded from, when loaded via Load;
	// it is empty for files synthesized in memory.
	Path  string
	Rules Rules
}

// LoadKitFile reads and parses the KIT file at path. It returns an error
// when the file is missing or does not parse into the rule sequence form.
func LoadKitFile(path string) (*KitFile, error) {
	dat, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read KIT file %q: %w", path, err)
	}

	var rules Rules
	if err := yaml.Unmarshal(dat, &rules); err != nil {
		return nil, fmt.Errorf("parse KIT file %q: %w", path, err)
	}
	return &KitFile{Path: path, Rules: rules}, nil
}

// Save writes the rules to path in the dynamic-key sequence form, matching
// the form LoadKitFile and UnmarshalYAML parse. The target file is created
// or truncated; the caller owns the path (KIT files are named "KIT").
func (k *KitFile) Save(path string) error {
	dat, err := yaml.Marshal(k.Rules)
	if err != nil {
		return fmt.Errorf("marshal KIT rules: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create KIT file %q: %w", path, err)
	}
	if _, err := file.Write(dat); err != nil {
		_ = file.Close()
		return fmt.Errorf("write KIT file %q: %w", path, err)
	}
	return file.Close()
}

// RuleByPattern returns the rule registered under the exact pattern string.
// Patterns are unique within a file (Validate enforces this).
func (k *KitFile) RuleByPattern(pattern string) (*Rule, bool) {
	for i := range k.Rules {
		if k.Rules[i].Pattern == pattern {
			return &k.Rules[i], true
		}
	}
	return nil, false
}

// Add appends rule at the end of the file's rule list. It rejects rules
// whose pattern is already registered or whose pattern is not a valid
// regular expression, so Validate invariants hold after every mutation.
func (k *KitFile) Add(rule Rule) error {
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
func (k *KitFile) Edit(pattern string, mutate func(*Rule)) error {
	rule, ok := k.RuleByPattern(pattern)
	if !ok {
		return fmt.Errorf("edit: %w: %q", ErrPatternNotFound, pattern)
	}
	mutate(rule)
	return nil
}

// Remove deletes the rule registered under pattern.
func (k *KitFile) Remove(pattern string) error {
	for i := range k.Rules {
		if k.Rules[i].Pattern == pattern {
			k.Rules = append(k.Rules[:i], k.Rules[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("remove: %w: %q", ErrPatternNotFound, pattern)
}

// Validate reports every integrity violation in the file: duplicate
// patterns, patterns that are not valid regular expressions, and approver
// targets with no target name or no required count. It returns a combined
// error covering all violations, never just the first one, so a user can
// fix every problem in one pass.
func (k *KitFile) Validate() error {
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
	return errors.Join(all...)
}
