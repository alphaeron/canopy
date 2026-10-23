package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesedit"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesFilesCmd lists the rules files (the files that declare rules) in the
// working tree, every one or only those whose paths a regex matches. A rules
// file that cannot be read or parsed is listed too, and flagged.
var rulesFilesCmd = &cobra.Command{
	Use:         "files [pattern]",
	Short:       "List the rules files, or those whose paths match a regex",
	Args:        cobra.MaximumNArgs(1),
	Annotations: map[string]string{"arg": "regular expression of rules file paths (default: every rules file)"},
}

// runRulesFiles is rulesFilesCmd's RunE, assigned in init: it opens the TUI, whose
// routes refer back to the command, so a RunE literal would be an
// initialization cycle.
func runRulesFiles(cmd *cobra.Command, args []string) error {
	var pattern string
	if len(args) > 0 {
		pattern = args[0]
	}

	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, rulesFilesRoute(valuesFrom(cmd, args)))
	}
	s, err := openWorkspace(cmd.Context(), cliLog())
	if err != nil {
		return err
	}
	files, err := s.PathsMatching(pattern)
	if err != nil {
		return err
	}
	switch mode {
	case modeJSON:
		invalid := []string{}
		for _, f := range files {
			if _, ok := s.Broken[f]; ok {
				invalid = append(invalid, f)
			}
		}
		return writeJSON(os.Stdout, struct {
			Pattern string   `json:"pattern"`
			Files   []string `json:"files"`
			Invalid []string `json:"invalid"`
		}{pattern, files, invalid})
	default:
		for _, f := range files {
			fmt.Println(f)
		}
		if len(files) == 0 {
			fmt.Println("(no rules files match)")
		}
	}
	return warnBroken(os.Stderr, s)
}

// rulesFilesRoute lists the rules files a pattern matches (`.*` for every one)
// and asks for the pattern first when opened from home. Enter opens the rules
// file's rules. A broken rules file is badged as failed, and its detail shows
// why.
func rulesFilesRoute(v tui.Values) tui.Route {
	r := commandRoute(rulesFilesCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		var pattern string
		if len(v.Args) > 0 {
			pattern = v.Args[0]
		}
		repo, engine, err := openLocal(quietLog)
		if err != nil {
			return nil, err
		}
		s, err := loadWorkspace(ctx, repo, engine)
		if err != nil {
			return nil, err
		}
		files, err := s.PathsMatching(pattern)
		if err != nil {
			return nil, err
		}
		match, err := matchWith(ctx, engine, review.Options{})
		matches := ruleMatches{match: match, err: err}
		items := make([]tui.Item, len(files))
		for i, f := range files {
			r := rulesListRoute(tui.Values{Flags: map[string]string{"file": f}})
			desc, badge, d := rulesFileDetail(s, f, pattern, matches)
			items[i] = tui.Item{Title: f, Description: desc, Badge: badge, Detail: d, Route: &r}
		}
		title := "rules files"
		if pattern != "" {
			title += " matching " + pattern
		}
		return tui.NewListPage(title, items).WithSummary(joinNotes(countOf(len(files), "rules file"), brokenNote(s))), nil
	})
	if len(v.Args) > 0 {
		r.Title = "files " + v.Args[0]
	}
	return requirePattern(r)
}

// rulesFileDetail describes a rules file as the working tree has it: its rules,
// each linking to the files it applies to (when matches has them), and the
// rules file above it, whose rules it inherits unless a rule ignores parent
// directories. A broken rules file is described by why it cannot be used
// instead.
func rulesFileDetail(s *rulesedit.Workspace, file, pattern string, matches ruleMatches) (string, tui.Badge, tui.Detail) {
	d := tui.Detail{Fields: []tui.Field{{Label: "Path", Value: file}, {Label: "Matched by", Value: orNone(pattern)}}}
	if e, ok := s.Broken[file]; ok {
		d.Fields = append(d.Fields, tui.Field{Label: "Error", Value: e.Error(), Badge: tui.BadgeFail})
		failure := "unusable"
		if fe, ok := errors.AsType[*approval.RulesFileError](e); ok {
			failure = fe.Op + " failed"
		}
		return "rules file · " + failure + ", skipped", tui.BadgeFail, d
	}
	rules := s.Rules[file]
	parent := "none (repository root)"
	if rulesfile.Dir(file) != "" {
		parent = orNoneLabel(s.Parent(file))
	}
	d.Fields = append(d.Fields, tui.Field{Label: "Rules", Value: strconv.Itoa(len(rules))}, tui.Field{Label: "Parent", Value: parent})
	rows := make([]tui.Row, len(rules))
	for i, rule := range rules {
		rule.RulesFile, rule.Position = file, i
		rows[i] = tui.Row{Text: ruleLabel(rule.Name, rule.Pattern), Note: joinNotes(strategyName(rule), approversLabel(rule.Approvers))}
		if matches.err == nil {
			rows[i].Route = ruleFilesRoute(rule, matches.files(rule.Key()))
		}
	}
	d.Sections = []tui.Section{{Title: "Rules · enter shows the files each applies to", Rows: rows}}
	return "rules file · " + countOf(len(rules), "rule"), tui.BadgeOK, d
}

func init() {
	rulesFilesCmd.RunE = runRulesFiles
	rulesCmd.AddCommand(rulesFilesCmd)
}
