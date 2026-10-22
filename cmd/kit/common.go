package main

import (
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kenin"
	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/shonin"
	"github.com/alphaeron/kit/internal/tui"
)

// newLocalEngine builds the kenin engine bound to the current repository's
// worktree. The same engine type is driven by the REST server with a remote
// source; this constructor is the CLI's fixed point.
func newLocalEngine(repo *gitRepo) (*kenin.Engine, error) {
	source := kenin.NewLocalSource(repo, &shonin.LocalFetcher{Repo: repo}, nil)
	return kenin.New(source, nil), nil
}

// ruleScope is one rule of a rule chain with the files it applies to.
type ruleScope struct {
	key     string // "<pattern> in <KIT file> (<name>)"
	rule    kitfile.Rule
	kitFile string
	files   []string // sorted
}

// ruleScopes flattens a rule chain into "which rules apply to which files":
// one entry per rule in any layer, keyed by its pattern, declaring KIT file
// ("KIT" for a repo-root rule) and name, with every file whose chain included
// the rule listed once. Entries are sorted by key. Each keeps the rule's
// definition and KIT file so the TUI can show where the rule comes from.
func ruleScopes(chain shonin.RuleChain) []ruleScope {
	byKey := map[string]*ruleScope{}
	seen := map[string]map[string]bool{}
	for file, layers := range chain.FileChains {
		for _, layer := range layers {
			kitFile := layerKitFile(layer)
			for _, rule := range layer.Rules {
				key := rule.Pattern + " in " + kitFile
				if rule.Name != "" {
					key += " (" + rule.Name + ")"
				}
				sc, ok := byKey[key]
				if !ok {
					sc = &ruleScope{key: key, rule: rule.Rule, kitFile: kitFile}
					byKey[key] = sc
					seen[key] = map[string]bool{}
				}
				if !seen[key][file] {
					seen[key][file] = true
					sc.files = append(sc.files, file)
				}
			}
		}
	}
	out := make([]ruleScope, 0, len(byKey))
	for _, sc := range byKey {
		slices.Sort(sc.files)
		out = append(out, *sc)
	}
	slices.SortFunc(out, func(a, b ruleScope) int { return strings.Compare(a.key, b.key) })
	return out
}

// layerKitFile is the KIT file that declares a layer's rules.
func layerKitFile(layer shonin.RuleLayer) string {
	if layer.DirectoryPath == "" {
		return "KIT"
	}
	return layer.DirectoryPath + "/KIT"
}

// kitFileFlag registers the shared --file flag on a command that changes a
// KIT file. The value is resolved by kitSet.resolve.
func kitFileFlag(cmd *cobra.Command, usage string) {
	cmd.Flags().String("file", "", usage+" (path from the repository root, or its directory)")
}

// outputMode is the resolved output style for a command invocation. The CLI
// picks one of: --json emits JSON, a TTY renders a bubbletea TUI, and
// otherwise a plain table. Resolving once avoids every subcommand re-deriving
// the same policy.
type outputMode int

const (
	modeJSON outputMode = iota
	modeTUI
	modeTable
)

// resolveOutputMode inspects the command's --json flag and then the attached
// writer. It mirrors the Interactive test against os.Stdout (where bubbletea
// will render) and is the single decision point across all subcommands.
func resolveOutputMode(cmd *cobra.Command) (outputMode, error) {
	jsonFlag, err := cmd.Flags().GetBool("json")
	if err != nil {
		return modeTable, nil
	}
	if jsonFlag {
		return modeJSON, nil
	}
	if tui.Interactive(os.Stdout) {
		return modeTUI, nil
	}
	return modeTable, nil
}
