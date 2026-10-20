package shonin

import (
	"sort"
)

// InvertRuleChain reverses a file → rule-layer mapping produced by
// GetFileRuleChain into a rule identity → files mapping. Identity is the pair
// (pattern, layer directory): the same rule pattern may be registered in two
// KIT files whose scope is different (e.g. the repository root and a nested
// directory), and an inversion that lost the layer would conflate them. A
// file appears once per (rule, layer) it matched; the returned file lists
// are de-duplicated and sorted so downstream consumers get deterministic
// output independent of map iteration order.
func InvertRuleChain(chain RuleChain) map[string][]string {
	byRule := make(map[string]map[string]bool)

	for file, layers := range chain.FileChains {
		for _, layer := range layers {
			for _, rule := range layer.Rules {
				key := identityOf(rule, layer.DirectoryPath)
				set, exists := byRule[key]
				if !exists {
					set = make(map[string]bool)
					byRule[key] = set
				}
				set[file] = true
			}
		}
	}

	out := make(map[string][]string, len(byRule))
	for key, set := range byRule {
		list := make([]string, 0, len(set))
		for f := range set {
			list = append(list, f)
		}
		sort.Strings(list)
		out[key] = list
	}
	return out
}

// identityOf returns the string identity used to key the inversion produced
// by InvertRuleChain.
func identityOf(rule Rule, directory string) string {
	return rule.Pattern + "@" + layerKey(directory)
}

// layerKey gives the file name of the KIT file whose directory the layer
// belongs to: the repository-root layer (empty directory) lives in the "KIT"
// file at the root, and every other layer in the KIT file one directory down.
func layerKey(directory string) string {
	if directory == "" {
		return "KIT"
	}
	return directory + "/KIT"
}
