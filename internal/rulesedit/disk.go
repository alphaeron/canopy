package rulesedit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alphaeron/kit/internal/rulesfile"
	"gopkg.in/yaml.v3"
)

// Load reads and parses the rules file at path. It returns an error
// when the file is missing or does not parse into the rule sequence form.
func Load(path string) (rulesfile.Rules, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read rules file %q: %w", path, err)
	}
	dat, err := rulesfile.ReadLimited(f)
	closeErr := f.Close()
	if err != nil {
		return nil, fmt.Errorf("read rules file %q: %w", path, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close rules file %q: %w", path, closeErr)
	}

	rules, err := rulesfile.ParseRules(dat)
	if err != nil {
		return nil, fmt.Errorf("parse rules file %q: %w", path, err)
	}
	return rules, nil
}

// Save writes rules to path in the dynamic-key sequence form, matching the
// form Load parses. The target file is created or truncated; the caller
// owns the path (rules files are named rulesfile.FileName).
func Save(path string, rules rulesfile.Rules) error {
	dat, err := yaml.Marshal(rules)
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

// PathFor is the rules file a path argument names: FileName in the current
// directory when arg is empty, FileName inside arg when it is a directory,
// and arg itself otherwise.
func PathFor(arg string) string {
	if arg == "" {
		return rulesfile.FileName
	}
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		return filepath.Join(arg, rulesfile.FileName)
	}
	return arg
}

// CheckCreate reports whether Create may write path: an existing file is
// an error wrapping fs.ErrExist unless overwrite is set.
func CheckCreate(path string, overwrite bool) error {
	if _, err := os.Stat(path); err == nil && !overwrite {
		return fmt.Errorf("rules file %q: %w", path, fs.ErrExist)
	}
	return nil
}

// Create writes a new rules file at path holding rules and returns them as
// written. An existing file is replaced only when overwrite is set (see
// CheckCreate).
func Create(path string, overwrite bool, rules ...rulesfile.Rule) (rulesfile.Rules, error) {
	if err := CheckCreate(path, overwrite); err != nil {
		return nil, err
	}
	var out rulesfile.Rules
	for _, rule := range rules {
		var err error
		if out, err = add(out, rule); err != nil {
			return nil, err
		}
	}
	if err := Save(path, out); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	for i := range out {
		out[i].Position = i
	}
	return out, nil
}
