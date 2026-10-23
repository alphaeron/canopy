package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"strconv"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/rulesedit"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
)

// openWorkspace loads the rules files of the repository containing the
// current directory: every rules file in the working tree (committed ones
// that still exist plus new, non-ignored ones), read from disk. It is the
// single source of rules for every command and the TUI, and the same set
// the engine matches with, so they always agree on which rules exist. log
// is as for openEngine.
func openWorkspace(ctx context.Context, log *slog.Logger) (*rulesedit.Workspace, error) {
	repo, engine, err := openLocal(log)
	if err != nil {
		return nil, err
	}
	return loadWorkspace(ctx, repo, engine)
}

// loadWorkspace is the rules files engine reads from repo, sharing the
// engine's fetch, and validated as the engine's platform validates them. A
// rules file that cannot be read or parsed is kept in Broken rather than
// failing the load, so the rules editors and views still work and can
// report it (see warnBroken); only a fetch that read no rules at all is an
// error.
func loadWorkspace(ctx context.Context, repo *gitRepo, engine *review.Engine) (*rulesedit.Workspace, error) {
	root, err := repo.Root()
	if err != nil {
		return nil, fmt.Errorf("get repository root: %w", err)
	}
	rules, err := engine.Rules(ctx)
	failed := approval.RulesFileErrors(err)
	if rules == nil || (err != nil && len(failed) == 0) {
		return nil, fmt.Errorf("load rules files: %w", err)
	}
	broken := make(map[string]error, len(failed))
	for p, e := range failed {
		broken[p] = e
	}
	return &rulesedit.Workspace{Root: root, Rules: rules, Broken: broken, Validator: engine.Source.Evaluator()}, nil
}

// workspaceLog is the logger a rules command opens its workspace with:
// cliLog(), or quietLog when an interactive prompt follows, so no log line
// lands above the prompt. Such a command shows the skipped rules files in
// the prompt (brokenNote) or after it (warnBroken) instead.
func workspaceLog(interactive bool) *slog.Logger {
	if interactive {
		return quietLog
	}
	return cliLog()
}

// warnBroken tells the user, on w (stderr, so stdout stays parseable), which
// rules files s skipped because they cannot be read or parsed.
func warnBroken(w io.Writer, s *rulesedit.Workspace) error {
	for _, p := range s.Paths() {
		if e, ok := s.Broken[p]; ok {
			// The cause alone: the warning already names the rules file.
			if fe, ok := errors.AsType[*approval.RulesFileError](e); ok {
				e = fe.Err
			}
			if _, err := fmt.Fprintf(w, "warning: skipping %s, which cannot be used (see `%s validate`): %v\n", p, brand.Command, e); err != nil {
				return err
			}
		}
	}
	return nil
}

// brokenNote is the status-line note for the rules files s skipped, or ""
// when there are none.
func brokenNote(s *rulesedit.Workspace) string {
	if len(s.Broken) == 0 {
		return ""
	}
	return countOf(len(s.Broken), "rules file") + " skipped: cannot be read or parsed"
}

// resolveFile maps a --file value to a rules file in s (see
// rulesedit.Workspace.Resolve), so `--file cmd/app` means
// cmd/app/<rules file> wherever the command runs. An empty value resolves to
// "", meaning every rules file.
func resolveFile(s *rulesedit.Workspace, flag string) (string, error) {
	p, err := s.Resolve(flag)
	if errors.Is(err, rulesedit.ErrOutsideRepository) {
		return "", fmt.Errorf("--file %w", err)
	}
	return p, err
}

// nearestRulesFile is the rules file closest to the current directory,
// the add form's default (see rulesedit.Workspace.Nearest).
func nearestRulesFile(s *rulesedit.Workspace) string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "" // Nearest falls back to the first rules file.
	}
	return s.Nearest(wd)
}

// ruleTarget finds the rule edit and remove act on: the one declaring
// pattern, narrowed by --file and --position (0-based) when set. Several
// are resolved with a tree picker (rules file → rule) when interactive and
// are an error listing them otherwise, so a script never changes a rule it
// did not choose. The rule comes back with its location set.
func ruleTarget(s *rulesedit.Workspace, v tui.Values, pattern string, interactive bool) (rulesfile.Rule, error) {
	only, err := resolveFile(s, v.Flag("file"))
	if err != nil {
		return rulesfile.Rule{}, err
	}
	position, err := positionFlag(v)
	if err != nil {
		return rulesfile.Rule{}, err
	}
	rule, err := s.Locate(pattern, only, position)
	ambiguous, ok := errors.AsType[*rulesedit.AmbiguousRuleError](err)
	if !ok {
		return rule, err
	}
	if !interactive {
		return rulesfile.Rule{}, fmt.Errorf("%w; pass --file and --position to choose one", err)
	}
	return pickRule(ambiguous)
}

// positionFlag is --position, or -1 when it is unset.
func positionFlag(v tui.Values) (int, error) {
	text := v.Flag("position")
	if text == "" {
		return -1, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("--position %q: want a 0-based position in the rules file", text)
	}
	return n, nil
}

// pickRule asks which of the ambiguous rules to act on, as a tree of their
// rules files.
func pickRule(ambiguous *rulesedit.AmbiguousRuleError) (rulesfile.Rule, error) {
	var groups []tui.PickGroup
	for i, r := range ambiguous.Rules {
		if len(groups) == 0 || groups[len(groups)-1].Title != r.RulesFile {
			groups = append(groups, tui.PickGroup{Title: r.RulesFile})
		}
		g := &groups[len(groups)-1]
		g.Choices = append(g.Choices, tui.Choice{Label: ruleChoiceLabel(r), Value: strconv.Itoa(i)})
	}
	picked, err := tui.PickTree(fmt.Sprintf("Pattern %q matches %d rules. Which one?", ambiguous.Pattern, len(ambiguous.Rules)), groups)
	if err != nil {
		return rulesfile.Rule{}, err
	}
	i, _ := strconv.Atoi(picked)
	return ambiguous.Rules[i], nil
}

// ruleChoiceLabel is a rule as a choice under its rules file: its position,
// name or pattern, strategy, and approvers.
func ruleChoiceLabel(r rulesfile.Rule) string {
	return fmt.Sprintf("[%d] %s", r.Position, joinNotes(ruleLabel(r.Name, r.Pattern), joinNotes(strategyName(r), approversLabel(r.Approvers))))
}

// ruleChoicesPage is the TUI's picker for an ambiguous pattern: the rules
// declaring it as a tree of their rules files, each opening route for that
// one rule (v with its --file and --position set).
func ruleChoicesPage(ambiguous *rulesedit.AmbiguousRuleError, v tui.Values, route func(tui.Values) tui.Route) tui.Page {
	var groups []tui.TreeGroup
	for _, r := range ambiguous.Rules {
		if len(groups) == 0 || groups[len(groups)-1].Title != r.RulesFile {
			groups = append(groups, tui.TreeGroup{Title: r.RulesFile})
		}
		chosen := tui.Values{Args: v.Args, Flags: maps.Clone(v.Flags)}
		if chosen.Flags == nil {
			chosen.Flags = map[string]string{}
		}
		chosen.Flags["file"] = r.RulesFile
		chosen.Flags["position"] = strconv.Itoa(r.Position)
		next := route(chosen)
		g := &groups[len(groups)-1]
		g.Items = append(g.Items, tui.Item{Title: ruleChoiceLabel(r), Detail: tui.Detail{Fields: ruleFieldsDetail(r)}, Route: &next})
	}
	return tui.NewTreePage(fmt.Sprintf("Rules %q", ambiguous.Pattern), groups).
		WithSummary(fmt.Sprintf("pattern %q matches %d rules · enter picks one", ambiguous.Pattern, len(ambiguous.Rules)))
}
