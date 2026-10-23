package rulesfile

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rule is a single approval rule from a rules file. It applies to files
// whose path matches Pattern and requires approval from its Approvers. Its
// JSON form uses the rules file's field names.
type Rule struct {
	// RulesFile and Position locate the rule: the repo-relative path of the
	// rules file declaring it, and its 0-based position among that file's
	// rules. They are never written to a rules file and the review
	// algorithm does not read them; they identify the rule to the CLI (see
	// Key). ParseRules sets Position; the code that places a rule in a
	// repository sets RulesFile (rulesedit.Workspace.Entries,
	// approval.RuleChain.ByRule), and a rule parsed on its own has none.
	RulesFile string `yaml:"-" json:"rules_file,omitempty"`
	Position  int    `yaml:"-" json:"position"`

	// Pattern is the regular expression the rule matches file paths
	// against. It is the rule's mapping key in YAML, not a field.
	Pattern          string `yaml:"-" json:"pattern"`
	Name             string `yaml:"name" json:"name,omitempty"`
	IgnoreParentDirs bool   `yaml:"ignore_parent_dirs" json:"ignore_parent_dirs"`
	// CombineStrategy is omitted when unset, so a rule loaded without one
	// is saved without one (writing the zero value would produce
	// "unknown", which does not parse back).
	CombineStrategy      RuleCombineStrategy `yaml:"combine_strategy,omitempty" json:"combine_strategy,omitempty"`
	CurrentDirectoryOnly bool                `yaml:"current_directory_only" json:"current_directory_only"`
	SelfApprove          bool                `yaml:"self_approve" json:"self_approve"`
	Approvers            ApproverTargets     `yaml:"approvers" json:"approvers"`
}

// Rules is an ordered list of rules.
type Rules []Rule

// Validate lists every integrity violation in the rules of one rules file,
// each a *RuleViolation: patterns that are not valid regular expressions,
// and approver targets with no target name or no required count. A pattern
// may be declared more than once: a layer applies its rules in order, each
// combining with the ones before it (see RuleCombineStrategy).
func (rules Rules) Validate() []error {
	var all []error
	for i, rule := range rules {
		violate := func(format string, args ...any) {
			all = append(all, &RuleViolation{Position: i, Pattern: rule.Pattern, Err: fmt.Errorf(format, args...)})
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			violate("invalid pattern %q: %w", rule.Pattern, err)
		}

		for _, approver := range rule.Approvers {
			if approver.Target == "" {
				violate("rule %q has an approver with no target", rule.Pattern)
			}
			if approver.RequiredCount < 1 {
				violate("rule %q approver %q has no required count", rule.Pattern, approver.Target)
			}
		}
	}
	return all
}

// Key is the rule's repository-wide identity: where it is declared. Two
// rules with the same pattern, even in one rules file, are distinct.
func (r Rule) Key() RuleKey {
	return RuleKey{RulesFile: r.RulesFile, Position: r.Position}
}

// Location is where the rule is declared, as "KIT[2]": its rules file and
// its 0-based position there.
func (r Rule) Location() string {
	return r.RulesFile + "[" + strconv.Itoa(r.Position) + "]"
}

// String gives a human-facing reference to the rule: its pattern, where it
// is declared when known, and its name when it has one.
func (r Rule) String() string {
	out := r.Pattern
	if r.RulesFile != "" {
		out += " in " + r.Location()
	}
	if r.Name != "" {
		out += " (" + r.Name + ")"
	}
	return out
}

// MarshalJSON encodes the rules as a JSON list, empty rather than null when
// there are none.
func (rules Rules) MarshalJSON() ([]byte, error) {
	if rules == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]Rule(rules))
}

// ruleFieldKeys are the YAML keys of Rule's fields; any other key of a
// rule's mapping is its pattern.
var ruleFieldKeys = func() map[string]bool {
	keys := map[string]bool{}
	for f := range reflect.TypeFor[Rule]().Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	return keys
}()

// UnmarshalYAML implements yaml.Unmarshaler. It accepts the rule's fields
// either nested under its pattern key or alongside it, and takes the one
// key that is not a known field as the rule's Pattern.
func (r *Rule) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("expected a mapping node block")
	}
	fields := value
	var pattern string
	if len(value.Content) == 2 && !ruleFieldKeys[value.Content[0].Value] {
		// `pattern: {fields}`
		pattern, fields = value.Content[0].Value, value.Content[1]
	} else {
		// `{pattern: ~, fields...}`
		for i := 0; i < len(value.Content); i += 2 {
			if key := value.Content[i].Value; !ruleFieldKeys[key] {
				pattern = key
				break
			}
		}
	}
	// alias drops the methods, so decoding does not recurse.
	type alias Rule
	var decoded alias
	if err := fields.Decode(&decoded); err != nil {
		return err
	}
	*r = Rule(decoded)
	r.Pattern = pattern
	return nil
}

// MarshalYAML implements yaml.Marshaler: the rule is written as
// `pattern: {fields}`, the form UnmarshalYAML reads.
func (r Rule) MarshalYAML() (any, error) {
	type alias Rule // drops the methods, so encoding does not recurse
	var fields yaml.Node
	if err := fields.Encode(alias(r)); err != nil {
		return nil, fmt.Errorf("marshal rule %q fields: %w", r.Pattern, err)
	}
	if fields.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("rule %q did not marshal to a mapping", r.Pattern)
	}
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: r.Pattern},
			&fields,
		},
	}, nil
}
