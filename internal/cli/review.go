package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/tui"
)

// reviewCmd runs the full review algorithm (fetch, sift, approve) over the
// selected files, as the review server does for a pull request, with the
// approvals and author given as flags instead of read from the platform.
var reviewCmd = &cobra.Command{
	Use:   "review [pattern...]",
	Short: "Evaluate who must approve each file",
	Long: `Run the review algorithm over the repository's files and report, for each
file, its status, the approver targets it requires and how many approvals
each has, and how every rules file in its chain contributed.

Every file in the working tree is reviewed, or only changed files with
--changed; pattern arguments (regular expressions) narrow the files further.
--approver simulates approvals and --author the change's author, so
self-approval rules are evaluated as they would be on a pull or merge request.
Every approver counts as active. A file no rule covers falls back to the
--owner repository owners. --label simulates the change's labels: one that
blocking_labels in the configuration names fails the review.`,
	Annotations: map[string]string{"arg": "regular expressions selecting the files to review (every file when omitted)"},
	// A review that ran into errors is a result, not a usage mistake.
	SilenceUsage: true,
}

// reviewOptions are review's inputs, read from route values so the CLI and
// the TUI parse them identically.
type reviewOptions struct {
	patterns  []string
	changed   bool
	author    string
	approvers []string
	owners    []string
	labels    []string
}

func reviewOptionsFrom(v tui.Values) (reviewOptions, error) {
	o := reviewOptions{patterns: v.Args, changed: v.Bool("changed"), author: v.Flag("author")}
	for name, dst := range map[string]*[]string{"approver": &o.approvers, "owner": &o.owners, "label": &o.labels} {
		list, err := splitList(v.Flag(name))
		if err != nil {
			return reviewOptions{}, fmt.Errorf("--%s: %w", name, err)
		}
		*dst = list
	}
	return o, nil
}

func runReviewCmd(cmd *cobra.Command, args []string) error {
	v := valuesFrom(cmd, args)
	mode, _ := resolveOutputMode(cmd)
	if mode == modeTUI {
		return openTUI(cmd, reviewRoute(v))
	}
	opts, err := reviewOptionsFrom(v)
	if err != nil {
		return err
	}
	res, err := runReview(cmd.Context(), opts, cliLog())
	if err != nil {
		return err
	}
	state := res.state
	if mode == modeJSON {
		if err := writeJSON(os.Stdout, approval.ReviewReport{State: state, Outcome: res.outcome}); err != nil {
			return err
		}
	} else {
		rows := make([][]string, 0, len(state.Files))
		for _, fs := range state.SortedFiles() {
			rows = append(rows, []string{fs.Path, statusLabel(fs.Status), requiredLabel(fs)})
		}
		if err := writeTable(os.Stdout, []string{"File", "Status", "Requires"}, rows); err != nil {
			return err
		}
		fmt.Printf("Overall: %s\n", statusLabel(state.Overall))
		fmt.Printf("Outcome: %s: %s\n", res.outcome.Verdict, res.outcome.Description(0))
	}
	// The exit status is the review's verdict, as a hosted platform would
	// report it, so a script can gate on it.
	switch res.outcome.Verdict {
	case approval.Passed:
		return nil
	case approval.Errored:
		return fmt.Errorf("review did not run cleanly: %w", state.Err)
	}
	return fmt.Errorf("review %s: %s", res.outcome.Verdict, res.outcome.Description(0))
}

// reviewResult is a review and what it means: its state, and its outcome,
// which also covers the rules files the working tree proposes.
type reviewResult struct {
	state   *approval.ReviewState
	outcome approval.Outcome
}

// runReview selects the files and evaluates them: the working tree's files,
// or its changed ones, kept when they match any pattern. log receives the
// algorithm's structured output; the TUI discards it, as the state holds
// everything it reports.
func runReview(ctx context.Context, opts reviewOptions, log *slog.Logger) (reviewResult, error) {
	engine, err := openEngine(log)
	if err != nil {
		return reviewResult{}, err
	}
	return reviewWith(ctx, engine, opts)
}

// reviewWith is runReview on an open engine. As on a hosted platform, the
// rules files the change proposes (here, those the working tree changes)
// are validated, and an invalid one, or a label blockingLabels names, fails
// the review whatever its approvals (review.OutcomeOf).
func reviewWith(ctx context.Context, engine *review.Engine, opts reviewOptions) (reviewResult, error) {
	files, err := engine.Files(ctx, review.Options{ChangedOnly: opts.changed, Patterns: opts.patterns})
	if err != nil {
		return reviewResult{}, fmt.Errorf("list files: %w", err)
	}
	changed, err := engine.Source.ChangedFiles(ctx)
	if err != nil {
		return reviewResult{}, fmt.Errorf("list changed files: %w", err)
	}
	proposed, err := engine.ValidateProposed(ctx, changed)
	if err != nil {
		return reviewResult{}, err
	}
	state := engine.Review(ctx, files, opts.input())
	blocked := approval.BlockingLabels(opts.labels, blockingLabels)
	return reviewResult{state: state, outcome: review.OutcomeOf(state, proposed, blocked)}, nil
}

// input is the review input the options describe. The local platform has
// no user directory, so IsActive is left nil: every user is active.
func (o reviewOptions) input() approval.ReviewInput {
	return approval.ReviewInput{
		Author:    o.author,
		Approvers: o.approvers,
		Owners:    o.owners,
		Labels:    o.labels,
	}
}

// statusLabel is a status for people: "missing approvers".
func statusLabel(s approval.Status) string { return strings.ReplaceAll(s.String(), "_", " ") }

// requiredLabel is a file's requirement with progress, e.g.
// "org/backend 1/2, lead 0/1", marking the repository-owner fallback.
func requiredLabel(fs approval.FileState) string {
	parts := make([]string, len(fs.Required))
	for i, r := range fs.Required {
		parts[i] = fmt.Sprintf("%s %d/%d", r.Target, r.SatisfiedCount, r.RequiredCount)
	}
	label := strings.Join(parts, ", ")
	switch {
	case fs.UsedFallback && label == "":
		return "no active repository owners"
	case fs.UsedFallback:
		return label + " (repository owners)"
	case label == "":
		return "none"
	}
	return label
}

// reviewRoute is `review`: one row per file, most in need of attention
// first, after a summary row for the whole review.
func reviewRoute(v tui.Values) tui.Route {
	return commandRoute(reviewCmd, v, func(ctx context.Context, v tui.Values) (tui.Page, error) {
		opts, err := reviewOptionsFrom(v)
		if err != nil {
			return nil, err
		}
		res, err := runReview(ctx, opts, quietLog)
		if err != nil {
			return nil, err
		}
		return reviewPage(res, opts), nil
	})
}

func reviewPage(res reviewResult, opts reviewOptions) tui.Page {
	state := res.state
	files := state.SortedFiles()
	counts := state.StatusCounts()
	items := make([]tui.Item, 0, len(files)+1)
	items = append(items, tui.Item{
		Title:       "Review",
		Description: statusLabel(state.Overall) + " · " + countOf(len(files), "file"),
		Badge:       statusBadge(state.Overall),
		Detail:      reviewSummaryDetail(res, opts, counts),
	})
	for _, fs := range files {
		r := fileRulesRoute(fs.Path)
		items = append(items, tui.Item{
			Title:       fs.Path,
			Description: statusLabel(fs.Status) + " · " + requiredLabel(fs),
			Badge:       statusBadge(fs.Status),
			Detail:      fileReviewDetail(fs, state),
			Route:       &r,
		})
	}
	var parts []string
	for _, s := range []approval.Status{approval.StatusSatisfied, approval.StatusMissingApprovers, approval.StatusUnresolved, approval.StatusError} {
		if n := counts[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, statusLabel(s)))
		}
	}
	summary := "overall " + statusLabel(state.Overall) + " · " + countOf(len(files), "file")
	if len(parts) > 0 {
		summary += ": " + strings.Join(parts, ", ")
	}
	return tui.NewListPage("Review", items).WithSummary(summary)
}

// reviewSummaryDetail is the review as a whole: the inputs it ran with, the
// file counts by status, and every error it collected.
func reviewSummaryDetail(res reviewResult, opts reviewOptions, counts map[approval.Status]int) tui.Detail {
	state := res.state
	d := tui.Detail{Fields: []tui.Field{
		{Label: "Overall", Value: statusLabel(state.Overall), Badge: statusBadge(state.Overall)},
		{Label: "Outcome", Value: res.outcome.Description(0), Badge: verdictBadge(res.outcome.Verdict)},
		{Label: "Files", Value: filesLabel(opts)},
		{Label: "Author", Value: opts.author},
		{Label: "Approvals", Value: strings.Join(opts.approvers, ", ")},
		{Label: "Owners", Value: strings.Join(opts.owners, ", ")},
		{Label: "Labels", Value: strings.Join(opts.labels, ", ")},
	}}
	var rows []tui.Row
	for _, s := range []approval.Status{approval.StatusError, approval.StatusUnresolved, approval.StatusMissingApprovers, approval.StatusSatisfied} {
		if n := counts[s]; n > 0 {
			rows = append(rows, tui.Row{Text: statusLabel(s), Badge: statusBadge(s), Note: countOf(n, "file")})
		}
	}
	d.Sections = append(d.Sections, tui.Section{Title: "Files by status", Rows: rows})
	if len(state.Errors) > 0 {
		errs := make([]tui.Row, len(state.Errors))
		for i, e := range state.Errors {
			errs[i] = tui.Row{Text: e, Badge: tui.BadgeFail}
		}
		d.Sections = append(d.Sections, tui.Section{Title: "Errors", Rows: errs})
	}
	d.Tabs = []tui.Tab{{Title: "Trace", Sections: []tui.Section{{Title: "Events", Rows: eventRows(state.Events, true)}}}}
	return d
}

func filesLabel(opts reviewOptions) string {
	label := "every file"
	if opts.changed {
		label = "changed files"
	}
	if len(opts.patterns) > 0 {
		label += " matching " + strings.Join(opts.patterns, " or ")
	}
	return label
}

// fileReviewDetail is one file's evaluation: its requirement and approval
// progress, then tabs for the chain of rule layers that produced it and the
// events the algorithm recorded for the file.
func fileReviewDetail(fs approval.FileState, state *approval.ReviewState) tui.Detail {
	fallback := "no"
	if fs.UsedFallback {
		fallback = "yes: no rule requires approvers, so the repository owners do"
	}
	d := tui.Detail{
		Fields: []tui.Field{
			{Label: "File", Value: fs.Path},
			{Label: "Status", Value: statusLabel(fs.Status), Badge: statusBadge(fs.Status)},
			{Label: "Requires", Value: requiredLabel(fs)},
			{Label: "Fallback", Value: fallback},
		},
		Sections: []tui.Section{{Title: "Approvers", Rows: targetRows(fs.Required, 0)}},
		Tabs:     chainTabs(fs, state),
	}
	return d
}

func init() {
	reviewCmd.RunE = runReviewCmd
	reviewCmd.Flags().Bool("changed", false, "review only files changed in the working tree")
	reviewCmd.Flags().String("author", "", "the change's author, whose approvals count only under self-approve rules")
	reviewCmd.Flags().StringSlice("approver", nil, "a user or group that has approved (repeatable)")
	reviewCmd.Flags().StringSlice("owner", nil, "a repository owner, who approves files no rule covers (repeatable)")
	reviewCmd.Flags().StringSlice("label", nil, "a label on the change; one in blocking_labels fails the review (repeatable)")
	rootCmd.AddCommand(reviewCmd)
}
