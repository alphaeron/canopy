package shonin

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alphaeron/kit/internal/kitfile"
)

// Get the chain of rules that applies for each specified file.
func GetFileRuleChain(
	repoRoot string,
	repoRules map[string]kitfile.Rules,
	files []string,
) RuleChain {
	chainMap := make(map[string][]RuleLayer)

	for _, file := range files {
		chainMap[file] = getFileRuleLayers(repoRoot, repoRules, file)
	}

	return RuleChain{FileChains: chainMap}
}

func getFileRuleLayers(
	repoRoot string,
	repoRules map[string]kitfile.Rules,
	fileName string,
) []RuleLayer {
	var layers []RuleLayer

	subdir, _ := filepath.Split(fileName)
	subdirParts := strings.Split(filepath.ToSlash(subdir), "/")

	// Loop down from the repo root to the target file's depth.
	for i := 0; i <= len(subdirParts); i++ {
		currentSubPath := filepath.Join(subdirParts[:i]...)
		configFilePath := filepath.Join(repoRoot, currentSubPath, "KIT")

		// Pull out rules registered exactly at this configuration file node
		matchedRules, exists := repoRules[configFilePath]
		if !exists || len(matchedRules) == 0 {
			continue
		}

		var activeLayerRules kitfile.Rules
		var truncateChain = false

		for _, rule := range matchedRules {
			// Check pattern matching against the file.
			isMatch, err := regexp.MatchString(rule.Pattern, fileName)
			if err != nil {
				fmt.Printf("error while checking rule match for '%s': %v\n", fileName, err)
				continue
			}
			if !isMatch {
				continue
			}

			// Constraint: Scope Exclusion (current_directory_only)
			// Discard the rule if it is scoped locally, but this current loop layer
			// is a parent folder higher up than the file's literal folder layer.
			if rule.CurrentDirectoryOnly && i < len(subdirParts) {
				continue
			}

			// Constraint: The Break Signal (ignore_parent_dirs)
			// Track if any valid matching rule explicitly orders us to throw away
			// parent rules.
			if rule.IgnoreParentDirs {
				truncateChain = true
			}

			activeLayerRules = append(activeLayerRules, rule)
		}

		// If a rule dictates truncation, discard all higher rules accumulated so far.
		if truncateChain {
			// Wipes out all parent entries collected from root down to here
			layers = nil
		}

		// Append this layer if it contains matching, non-scoped out rules
		if len(activeLayerRules) > 0 {
			layers = append(layers, RuleLayer{
				DirectoryPath: currentSubPath,
				Rules:         activeLayerRules,
			})
		}
	}

	return layers
}
