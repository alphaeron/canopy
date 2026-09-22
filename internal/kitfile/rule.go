package kitfile

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

type Rule struct {
	Pattern string        `yaml:"-"`
	Name    string        `yaml:"name"`
	IgnoreParentDirs bool `yaml:"ignore_parent_dirs"`
	CombineStrategy RuleCombineStrategy `yaml:"combine_strategy"`
	CurrentDirectoryOnly bool `yaml:"current_directory_only"`
	SelfApprove bool `yaml:"self_approve"`
	Approvers ApproverTargets `yaml:"approvers"`
}

type Rules []Rule

func (r Rule) String() string {
	return fmt.Sprintf(`Rule{
  Pattern: "%v",
  Name: "%v",
  IgnoreParentDirs: %v,
  CombineStrategy: %v,
  CurrentDirectoryOnly: %v,
  SelfApprove: %v,
  Approvers: %v,
}`,
		r.Pattern,
		r.Name,
		r.IgnoreParentDirs,
		r.CombineStrategy,
		r.CurrentDirectoryOnly,
		r.SelfApprove,
		r.Approvers)
}

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
	var targetNode *yaml.Node = value
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
