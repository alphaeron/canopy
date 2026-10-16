package shonin

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alphaeron/kit/internal/kitfile"
)

// GetFileRuleChain builds the chain of rules that applies for each specified
// file. Errors encountered while matching rules (e.g. invalid patterns) are
// recorded as "sift" phase events and accumulated in the supplied builder so
// they surface in the final review state; they are also logged as structured
// errors. A nil events builder is allowed for callers that only want the
// chain without a trace.
// TODO repoRoot is unused since we don't get absolute paths
//
//	for the local shonin.  Do we need it for remote, or can we
//	remove it completely?
func GetFileRuleChain(
	repoRoot string,
	repoRules map[string]kitfile.Rules,
	files []string,
	events *ReviewStateBuilder,
	log *slog.Logger,
) RuleChain {
	chainMap := make(map[string][]RuleLayer)

	for _, file := range files {
		layers := getFileRuleLayers(repoRoot, repoRules, file, events, log)
		chainMap[file] = layers
		events.AddEvent("sift", file, "", "", StatusDone,
			fmt.Sprintf("matched %d rule layer(s)", len(layers)))
	}

	return RuleChain{FileChains: chainMap}
}

func getFileRuleLayers(
	repoRoot string,
	repoRules map[string]kitfile.Rules,
	fileName string,
	events *ReviewStateBuilder,
	log *slog.Logger,
) []RuleLayer {
	var layers []RuleLayer

	subdir, _ := filepath.Split(fileName)
	subdirParts := strings.Split(filepath.ToSlash(subdir), "/")
	// A leading "/" (absolute input paths) would visit the root layer twice
	// through filepath.Join's empty-element handling, duplicating the root
	// requirements. Trailing empty segments are kept: they name the file's
	// own directory layer.
	if len(subdirParts) > 0 && subdirParts[0] == "" {
		subdirParts = subdirParts[1:]
	}

	// Loop down from the repo root to the target file's depth.
	for i := 0; i < len(subdirParts); i++ {
		currentSubPath := filepath.Join(subdirParts[:i]...)
		configFilePath := filepath.Join(currentSubPath, "KIT")

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
				patternErr := fmt.Errorf(
					"invalid rule pattern %q in %s for file %q: %w",
					rule.Pattern, configFilePath, fileName, err,
				)
				log.Error("sift: skipping rule with invalid pattern",
					"component", "sifter", "phase", "sift",
					"file", fileName, "kit_file", configFilePath,
					"rule", rule.Name, "pattern", rule.Pattern, "error", err)
				events.AddEvent("sift", fileName, currentSubPath, rule.Name, StatusError,
					fmt.Sprintf("invalid pattern %q: %v", rule.Pattern, err))
				events.AddError(patternErr)
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
			events.AddEvent("sift", fileName, currentSubPath, "", StatusActive,
				"ignore_parent_dirs: parent rules discarded")
			log.Debug("sift: truncating parent rule chain",
				"component", "sifter", "phase", "sift",
				"file", fileName, "layer", currentSubPath)
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
