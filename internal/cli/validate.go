package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// validateCmd reports integrity violations in every rules file in the
// repository, or in one with --file. The output is the validation error
// table: every violation, with the rules file that carries it. A clean run
// reports every file valid; any violation exits non-zero.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate rules file integrity",
	Long: `Validate rules file structure, rule patterns, and required fields in every rules
file in the repository's working tree. --file narrows the check to one rules file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		s, err := openWorkspace(cmd.Context(), cliLog())
		if err != nil {
			return err
		}
		only, err := resolveFile(s, filePath)
		if err != nil {
			return err
		}
		violations := s.Validate(only)
		total := violationCount(violations)

		mode, _ := resolveOutputMode(cmd)
		if mode == modeJSON {
			return writeJSON(os.Stdout, violationText(violations))
		}
		var header = []string{"Rules File", "Violation"}
		rows := make([][]string, 0, total)
		for _, path := range s.Paths() {
			for _, e := range violations[path] {
				rows = append(rows, []string{path, e.Error()})
			}
		}
		if err := writeTable(os.Stdout, header, rows); err != nil {
			return err
		}
		if err := narrowedNotice(os.Stderr, only, len(s.Paths())); err != nil {
			return err
		}
		if len(violations) == 0 {
			checked := len(s.Paths())
			if only != "" {
				checked = 1
			}
			fmt.Printf("All %d rules file(s) are valid.\n", checked)
			return nil
		}
		return fmt.Errorf("%d violation(s) in %d rules file(s)", total, len(violations))
	},
}

// violationCount is the number of violations across every rules file in
// violations, as Workspace.Validate reports them.
func violationCount(violations map[string][]error) int {
	n := 0
	for _, errs := range violations {
		n += len(errs)
	}
	return n
}

// violationText is violations as message strings, the validate JSON form.
func violationText(violations map[string][]error) map[string][]string {
	out := make(map[string][]string, len(violations))
	for p, errs := range violations {
		for _, e := range errs {
			out[p] = append(out[p], e.Error())
		}
	}
	return out
}

// validateRoute shows every rules file (or the one --file names) with its
// violations; enter opens the file's rules.
func validateRoute(v tui.Values) tui.Route {
	return commandRoute(validateCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		s, err := openWorkspace(ctx, quietLog)
		if err != nil {
			return nil, err
		}
		only, err := resolveFile(s, v.Flag("file"))
		if err != nil {
			return nil, err
		}
		violations := s.Validate(only)
		var items []tui.Item
		for _, path := range s.Paths() {
			if only != "" && path != only {
				continue
			}
			errs := violations[path]
			desc := "valid"
			if len(errs) > 0 {
				desc = countOf(len(errs), "violation")
			}
			r := rulesListRoute(tui.Values{Flags: map[string]string{"file": path}})
			badge := tui.BadgeOK
			if len(errs) > 0 {
				badge = tui.BadgeFail
			}
			items = append(items, tui.Item{
				Title:       path,
				Description: desc,
				Badge:       badge,
				Detail:      violationsDetail(path, s.Rules[path], errs),
				Route:       &r,
			})
		}
		return tui.NewListPage("Validation", items).
			WithSummary(fmt.Sprintf("%s in %s", countOf(violationCount(violations), "violation"), countOf(len(items), "rules file"))), nil
	})
}

// violationsDetail places each violation beside the rule it is about (a
// rulesfile.RuleViolation), with the rule's fields and a link to edit it;
// any other violation is listed for the file as a whole.
func violationsDetail(file string, rules rulesfile.Rules, errs []error) tui.Detail {
	d := tui.Detail{Fields: []tui.Field{{Label: "rules file", Value: file}, {Label: "Violations", Value: strconv.Itoa(len(errs))}}}
	if len(errs) == 0 {
		d.Fields[1].Badge = tui.BadgeOK
		return d
	}
	d.Fields[1].Badge = tui.BadgeFail
	byPosition := map[int][]tui.Row{}
	var rest []tui.Row
	for _, e := range errs {
		row := tui.Row{Text: e.Error(), Badge: tui.BadgeFail}
		if v, ok := errors.AsType[*rulesfile.RuleViolation](e); ok {
			byPosition[v.Position] = append(byPosition[v.Position], row)
		} else {
			rest = append(rest, row)
		}
	}
	for i, rule := range rules {
		rows := byPosition[i]
		if len(rows) == 0 {
			continue
		}
		strategy := strategyName(rule)
		if strategy == "" {
			strategy = "(default)"
		}
		rows = append(rows,
			tui.Row{Indent: 1, Text: "name", Note: orNone(rule.Name)},
			tui.Row{Indent: 1, Text: "strategy", Note: strategy},
			tui.Row{Indent: 1, Text: "approvers", Note: orNoneLabel(approversLabel(rule.Approvers))},
		)
		edit := rulesEditRoute(tui.Values{Args: []string{rule.Pattern}, Flags: map[string]string{"file": file, "position": strconv.Itoa(i)}})
		rows = append(rows, tui.Row{Text: "Edit this rule", Route: &edit})
		d.Sections = append(d.Sections, tui.Section{Title: fmt.Sprintf("Rule [%d] %s", i, rule.Pattern), Rows: rows})
	}
	if len(rest) > 0 {
		d.Sections = append(d.Sections, tui.Section{Title: "File", Rows: rest})
	}
	return d
}

func init() {
	validateCmd.Flags().String("file", "", "validate only this rules file (path from the repository root, or its directory)")
	rootCmd.AddCommand(validateCmd)
}
