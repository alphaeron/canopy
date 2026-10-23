package approval

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// GetFileRuleChain builds the chain of rules that applies for each specified
// file. Errors encountered while matching rules (e.g. invalid patterns) are
// recorded as "sift" phase events and accumulated in the supplied builder so
// they surface in the final review state; they are also logged as structured
// errors. A nil events builder is allowed for callers that only want the
// chain without a trace. It stops with ctx's error once ctx is done.
//
// Files that match the same rules share one []RuleLayer (see RuleChain), so
// a large review holds one copy of each distinct chain.
func GetFileRuleChain(
	ctx context.Context,
	repoRules rulesfile.Set,
	files []string,
	events *ReviewStateBuilder,
	log *slog.Logger,
) (RuleChain, error) {
	chainMap := make(map[string][]RuleLayer, len(files))
	s := newSifter(repoRules)
	for i, file := range files {
		if i%siftCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return RuleChain{}, err
			}
		}
		layers := s.layers(file, events, log)
		chainMap[file] = layers
		events.AddEvent("sift", file, "", "", StatusDone, s.note(len(layers)))
	}
	return RuleChain{FileChains: chainMap}, nil
}

// siftCheckEvery is how many files a sift matches between checks of its
// context.
const siftCheckEvery = 1024

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

// sifter matches files against one rule set. It does the work that does not
// depend on a file's name once: the layers a directory's files can match
// are found once per directory, each rule is wrapped once, and each distinct
// set of matched rules becomes one []RuleLayer that every file matching it
// shares. Only the pattern matching itself runs per file.
type sifter struct {
	patterns patternCache
	repo     rulesfile.Set
	rules    map[string][]Rule      // wrapped rules, by rules file
	dirs     map[string][]candidate // candidate layers, by a file's directory
	chains   map[string][]RuleLayer // shared chains, by chainKey
	notes    map[int]string         // the sift event's note, by layer count
	matched  []match                // the current file's matches, reused
	key      []byte                 // the current file's chainKey, reused
}

// candidate is a layer whose rules file declares rules, on the path from
// the root to a directory.
type candidate struct {
	dir       string // the layer's DirectoryPath
	rulesFile string
	rules     []Rule
	// own is whether the layer is the directory's own, the only layer
	// where a current_directory_only rule applies.
	own bool
}

// match is a matched rule: candidate c's rule r.
type match struct{ c, r int }

func newSifter(repo rulesfile.Set) *sifter {
	return &sifter{
		patterns: patternCache{},
		repo:     repo,
		rules:    map[string][]Rule{},
		dirs:     map[string][]candidate{},
		chains:   map[string][]RuleLayer{},
		notes:    map[int]string{},
	}
}

// layers is the chain of rule layers that applies to fileName, from the
// root down. The result is shared with every file that matches the same
// rules, so it must not be modified.
func (s *sifter) layers(fileName string, events *ReviewStateBuilder, log *slog.Logger) []RuleLayer {
	subdir, _ := filepath.Split(fileName)
	cands := s.candidates(subdir)

	s.matched = s.matched[:0]
	for ci, c := range cands {
		truncateChain := false

		for ri, rule := range c.rules {
			// Check pattern matching against the file.
			re, err := s.patterns.compile(rule.Pattern)
			if err != nil {
				patternErr := fmt.Errorf(
					"invalid rule pattern %q in %s for file %q: %w",
					rule.Pattern, c.rulesFile, fileName, err,
				)
				log.Error("sift: skipping rule with invalid pattern",
					"component", "sifter", "phase", "sift",
					"file", fileName, "rules_file", c.rulesFile,
					"rule", rule.Name, "pattern", rule.Pattern, "error", err)
				events.AddEvent("sift", fileName, c.dir, rule.Name, StatusError,
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
			if rule.CurrentDirectoryOnly && !c.own {
				continue
			}

			// Constraint: The Break Signal (ignore_parent_dirs)
			// Track if any valid matching rule explicitly orders us to throw away
			// parent rules.
			if rule.IgnoreParentDirs {
				truncateChain = true
			}
			s.matched = append(s.matched, match{c: ci, r: ri})
		}

		// If a rule dictates truncation, discard all higher rules accumulated so far.
		if truncateChain {
			// Wipes out all parent entries collected from root down to here
			n := 0
			for _, m := range s.matched {
				if m.c == ci {
					s.matched[n] = m
					n++
				}
			}
			s.matched = s.matched[:n]
			events.AddEvent("sift", fileName, c.dir, "", StatusActive,
				"ignore_parent_dirs: parent rules discarded")
			log.Debug("sift: truncating parent rule chain",
				"component", "sifter", "phase", "sift",
				"file", fileName, "layer", c.dir)
		}
	}
	return s.chain(cands)
}

// candidates are the layers the files directly in subdir (a directory as
// filepath.Split returns it) can match, from the root down.
func (s *sifter) candidates(subdir string) []candidate {
	if cands, ok := s.dirs[subdir]; ok {
		return cands
	}
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
	var cands []candidate
	for i := range subdirParts {
		dir := path.Join(subdirParts[:i]...)
		rulesFile := rulesfile.PathInDir(dir)
		// Pull out rules registered exactly at this configuration file node
		if len(s.repo[rulesFile]) == 0 {
			continue
		}
		cands = append(cands, candidate{
			dir:       dir,
			rulesFile: rulesFile,
			rules:     s.wrapped(rulesFile),
			own:       i == len(subdirParts)-1,
		})
	}
	s.dirs[subdir] = cands
	return cands
}

// wrapped is the rules file's rules, wrapped at the sifter boundary: from
// this point on the pipeline only sees the algorithm-level Rule type.
func (s *sifter) wrapped(rulesFile string) []Rule {
	rules, ok := s.rules[rulesFile]
	if !ok {
		for _, rule := range s.repo[rulesFile] {
			rules = append(rules, newRule(rule))
		}
		s.rules[rulesFile] = rules
	}
	return rules
}

// chain is the shared chain of the current file's matches among cands,
// built the first time a file matches those rules. Its key names each
// matched rule by rules file and position, so files in different
// directories that match the same rules share it too.
func (s *sifter) chain(cands []candidate) []RuleLayer {
	s.key = s.key[:0]
	for _, m := range s.matched {
		s.key = append(s.key, cands[m.c].rulesFile...)
		s.key = append(s.key, 0)
		s.key = binary.AppendUvarint(s.key, uint64(m.r))
	}
	if layers, ok := s.chains[string(s.key)]; ok {
		return layers
	}
	var layers []RuleLayer
	for i, m := range s.matched {
		c := cands[m.c]
		if i == 0 || s.matched[i-1].c != m.c {
			layers = append(layers, RuleLayer{DirectoryPath: c.dir})
		}
		last := &layers[len(layers)-1]
		last.Rules = append(last.Rules, c.rules[m.r])
	}
	s.chains[string(s.key)] = layers
	return layers
}

// note is the sift event's note for a file that matched n layers.
func (s *sifter) note(n int) string {
	note, ok := s.notes[n]
	if !ok {
		note = fmt.Sprintf("matched %d rule layer(s)", n)
		s.notes[n] = note
	}
	return note
}
