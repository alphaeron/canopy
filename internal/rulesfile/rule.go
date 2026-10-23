package rulesfile

import (
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

// UnmarshalYAML implements yaml.Unmarshaler. It accepts the rule's fields
// either nested under its pattern key or alongside it, and takes the one
// key that is not a known field as the rule's Pattern.
func (r *Rule) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("expected a mapping node block")
	}

	// 1. Dynamically collect all known YAML tags from the Rule struct fields
	knownKeys := make(map[string]bool)
	t := reflect.TypeOf(Rule{})
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("yaml")
		if tag != "" && tag != "-" {
			if idx := strings.Index(tag, ","); idx != -1 {
				tag = tag[:idx]
			}
			knownKeys[tag] = true
		}
	}

	// 2. Determine if the fields are flat or nested.
	// If there is exactly one key-value pair (len == 2) and that key is NOT a known field,
	// it means this is a single object root wrapper: `".*\\.go": { name: ... }`
	targetNode := value
	var dynamicKey string

	if len(value.Content) == 2 && !knownKeys[value.Content[0].Value] {
		dynamicKey = value.Content[0].Value
		targetNode = value.Content[1] // Pivot down to the nested block where the fields actually live
	} else {
		// It's a flat structure (like inside the slice sequence). Loop to find the unknown key.
		for i := 0; i < len(value.Content); i += 2 {
			keyStr := value.Content[i].Value
			if !knownKeys[keyStr] {
				dynamicKey = keyStr
				break
			}
		}
	}

	// 3. Decode the actual content fields from the target node
	type alias Rule
	var dummy alias
	if err := targetNode.Decode(&dummy); err != nil {
		return err
	}

	// 4. Cast and copy the parsed struct back
	*r = Rule(dummy)
	r.Pattern = dynamicKey // Assign the dynamic key to your field

	return nil
}

// MarshalYAML serializes the rule dynamically in the format {pattern: { name: ..., ... }}.
// It automatically handles new fields added to the Rule struct without needing code updates.
func (r Rule) MarshalYAML() (interface{}, error) {
	// 1. Create a local type alias to bypass this custom MarshalYAML method
	// and avoid infinite recursive calling loops.
	type alias Rule

	// 2. Marshal the aliased struct. The `yaml:"-"` on the Pattern field
	// will automatically prevent it from leaking into the child node fields.
	content, err := yaml.Marshal(alias(r))
	if err != nil {
		return nil, fmt.Errorf("marshal rule %q fields: %w", r.Pattern, err)
	}

	// 3. Unmarshal the generated YAML payload into a generic node tree
	var docNode yaml.Node
	if err := yaml.Unmarshal(content, &docNode); err != nil {
		return nil, fmt.Errorf("decode rule %q fields node: %w", r.Pattern, err)
	}

	// 4. Pivot from the DocumentNode wrapper down to the underlying MappingNode
	var fieldsNode *yaml.Node
	if docNode.Kind == yaml.DocumentNode && len(docNode.Content) == 1 {
		fieldsNode = docNode.Content[0]
	} else {
		fieldsNode = &docNode
	}

	if fieldsNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("rule %q did not marshal to a mapping", r.Pattern)
	}

	// 5. Build and return the wrapper map mapping [Pattern -> Fields]
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: r.Pattern},
			fieldsNode,
		},
	}, nil
}
