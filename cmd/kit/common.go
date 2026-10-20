package main

import (
	"fmt"
	"os"

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

// scopesByRule flattens a shonin RuleChain into the scope-keyed file map the
// TUI tree view renders: every rule in every layer is a key (its pattern with
// the registering layer's KIT file, "KIT" for a repo-root rule), and every
// file whose chain included the rule appears once under that key. This is the
// shared "which rules apply to which files" projection for rules get and
// match --by-scope, so neither command re-derives the grouping.
func scopesByRule(chain shonin.RuleChain) map[string][]string {
	byScope := map[string]map[string]bool{}
	var order []string
	for file, layers := range chain.FileChains {
		for _, layer := range layers {
			kitFile := "KIT"
			if layer.DirectoryPath != "" {
				kitFile = layer.DirectoryPath + "/KIT"
			}
			for _, rule := range layer.Rules {
				key := rule.Pattern + " in " + kitFile
				if rule.Name != "" {
					key += " (" + rule.Name + ")"
				}
				if _, seen := byScope[key]; !seen {
					byScope[key] = map[string]bool{}
					order = append(order, key)
				}
				byScope[key][file] = true
			}
		}
	}
	out := make(map[string][]string, len(order))
	for _, k := range order {
		for f := range byScope[k] {
			out[k] = append(out[k], f)
		}
	}
	return out
}

// allKitContent returns every tracked KIT file's parsed rules, keyed by the
// KIT file path. It reads each file's content from the repository's HEAD blob
// (repo.FileContentAt), matching the tree FindFiles enumerates: a file that is
// tracked but absent from the worktree disk still resolves, and a file that has
// never been committed never appears. This keeps list-type commands consistent
// regardless of worktree hygiene.
func allKitContent(repo *gitRepo) (map[string]kitfile.Rules, error) {
	// A repository with no commit yet has no tracked KIT files, so it is
	// valid empty input rather than a fatal missing-HEAD error.
	if !repo.HasCommits() {
		return map[string]kitfile.Rules{}, nil
	}
	ref, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("read HEAD: %w", err)
	}
	paths, err := repo.FindFiles(`KIT`, ref, false)
	if err != nil {
		return nil, fmt.Errorf("find KIT files: %w", err)
	}
	rules := make(map[string]kitfile.Rules, len(paths))
	for _, p := range paths {
		content, exists, err := repo.FileContentAt(p, "HEAD")
		if err != nil {
			return nil, fmt.Errorf("read %s from HEAD: %w", p, err)
		}
		if !exists {
			continue
		}
		list, perr := kitfile.ParseRules([]byte(content))
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", p, perr)
		}
		rules[p] = list
	}
	return rules, nil
}

// kitFilePathFor returns the default KIT file path the user was looking at, or
// the path explicitly passed via --file. An empty --file resolves to the
// repository root's "KIT" file.
func kitFilePathFor(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return "KIT"
}

// kitFileFlag registers the shared --file flag on a command that operates on a
// KIT file. Every command that targets a KIT file reads the flag through
// targetKitFile(cmd).
func kitFileFlag(cmd *cobra.Command) {
	cmd.Flags().String("file", "", "the KIT file to target (defaults to the repo-root KIT)")
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
