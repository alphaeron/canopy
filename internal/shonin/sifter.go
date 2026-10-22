package shonin

import (
	"fmt"
	"log/slog"
	"path"
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
func GetFileRuleChain(
	repoRules map[string]kitfile.Rules,
	files []string,
	events *ReviewStateBuilder,
	log *slog.Logger,
) RuleChain {
	chainMap := make(map[string][]RuleLayer, len(files))
	patterns := patternCache{}

	for _, file := range files {
		layers := getFileRuleLayers(repoRules, patterns, file, events, log)
		chainMap[file] = layers
		events.AddEvent("sift", file, "", "", StatusDone,
			fmt.Sprintf("matched %d rule layer(s)", len(layers)))
	}

	return RuleChain{FileChains: chainMap}
}

// patternCache compiles each rule pattern once per sift rather than once per
// file, keeping the compile error so every file it applies to still reports
// it.
type patternCache map[string]compiledPattern

type compiledPattern struct {
	re  *regexp.Regexp
	err error
}

func (c patternCache) compile(pattern string) (*regexp.Regexp, error) {
	p, ok := c[pattern]
	if !ok {
		p.re, p.err = regexp.Compile(pattern)
		c[pattern] = p
	}
	return p.re, p.err
}

func getFileRuleLayers(
	repoRules map[string]kitfile.Rules,
	patterns patternCache,
	fileName string,
	events *ReviewStateBuilder,
	log *slog.Logger,
) []RuleLayer {
	var layers []RuleLayer

	subdir, _ := filepath.Split(fileName)
	subdirParts := strings.Split(filepath.ToSlash(subdir), "/")
	// A leading "/" (absolute input paths) would visit the root layer twice
	// through path.Join's empty-element handling, duplicating the root
	// requirements. Trailing empty segments are kept: they name the file's
	// own directory layer. A relative root-level file (e.g. "main.go") would
	// leave an empty part and skip every layer; keep its single empty
	// element so the root layer is visited exactly once.
	if len(subdirParts) > 1 && subdirParts[0] == "" {
		subdirParts = subdirParts[1:]
	}

	// Loop down from the repo root to the target file's depth.
	for i := range subdirParts {
		layer := RuleLayer{DirectoryPath: path.Join(subdirParts[:i]...)}
		configFilePath := layer.KitFile()

		// Pull out rules registered exactly at this configuration file node
		matchedRules := repoRules[configFilePath]
		if len(matchedRules) == 0 {
			continue
		}

		truncateChain := false

		for _, rule := range matchedRules {
			// Check pattern matching against the file.
			re, err := patterns.compile(rule.Pattern)
			if err != nil {
				patternErr := fmt.Errorf(
					"invalid rule pattern %q in %s for file %q: %w",
					rule.Pattern, configFilePath, fileName, err,
				)
				log.Error("sift: skipping rule with invalid pattern",
					"component", "sifter", "phase", "sift",
					"file", fileName, "kit_file", configFilePath,
					"rule", rule.Name, "pattern", rule.Pattern, "error", err)
				events.AddEvent("sift", fileName, layer.DirectoryPath, rule.Name, StatusError,
					fmt.Sprintf("invalid pattern %q: %v", rule.Pattern, err))
				events.AddError(patternErr)
				continue
			}
			if !re.MatchString(fileName) {
				continue
			}

			// Constraint: Scope Exclusion (current_directory_only)
			// Discard the rule if it is scoped locally, but this current loop layer
			// is a strict ancestor of the file's own directory — the layer for
			// the file's immediate directory itself still applies.
			if rule.CurrentDirectoryOnly && i < len(subdirParts)-1 {
				continue
			}

			// Constraint: The Break Signal (ignore_parent_dirs)
			// Track if any valid matching rule explicitly orders us to throw away
			// parent rules.
			if rule.IgnoreParentDirs {
				truncateChain = true
			}

			// Wrap the configured rule at the sifter boundary: from this
			// point on the pipeline only sees the algorithm-level Rule
			// type.
			layer.Rules = append(layer.Rules, newRule(rule))
		}

		// If a rule dictates truncation, discard all higher rules accumulated so far.
		if truncateChain {
			// Wipes out all parent entries collected from root down to here
			layers = nil
			events.AddEvent("sift", fileName, layer.DirectoryPath, "", StatusActive,
				"ignore_parent_dirs: parent rules discarded")
			log.Debug("sift: truncating parent rule chain",
				"component", "sifter", "phase", "sift",
				"file", fileName, "layer", layer.DirectoryPath)
		}

		// Append this layer if it contains matching, non-scoped out rules
		if len(layer.Rules) > 0 {
			layers = append(layers, layer)
		}
	}

	return layers
}
