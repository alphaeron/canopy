package main

import (
	"bytes"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// ruleKeys are the keys a rule in a RULES file can have, read from
// rulesfile.Rule's YAML tags, plus "pattern", the rule's mapping key.
func ruleKeys() []string {
	return append([]string{"pattern"}, yamlKeys(reflect.TypeFor[rulesfile.Rule]())...)
}

// yamlKeys are the YAML keys of struct type t's fields.
func yamlKeys(t reflect.Type) []string {
	var keys []string
	for f := range t.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

// checkRuleKeys fails unless the RULES reference documents exactly the keys
// the parser accepts: a page for every key of rulesfile.Rule, none for a key
// it no longer has, and every approver target key on the approvers page. A
// key added to the schema thus fails the docs build until it is documented.
func checkRuleKeys(docs map[string][]byte) error {
	documented := map[string]string{}
	for _, p := range pages {
		if p.Key != "" {
			documented[p.Key] = p.Source
		}
	}
	var problems []string
	for _, k := range ruleKeys() {
		if _, ok := documented[k]; !ok {
			problems = append(problems, fmt.Sprintf("RULES key %q has no page in the RULES reference", k))
		}
		delete(documented, k)
	}
	for _, k := range slices.Sorted(maps.Keys(documented)) {
		problems = append(problems, fmt.Sprintf("%s documents %q, which is not a RULES key", documented[k], k))
	}
	for _, k := range yamlKeys(reflect.TypeFor[rulesfile.ApproverTarget]()) {
		if !bytes.Contains(docs["docs/rules/approvers.md"], []byte("`"+k+"`")) {
			problems = append(problems, fmt.Sprintf("approver target key %q is not documented in docs/rules/approvers.md", k))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("RULES reference out of date:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
