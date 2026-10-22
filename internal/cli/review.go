package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/match"
	"github.com/alphaeron/kit/internal/tui"
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
self-approval rules are evaluated as they would be on a pull request.
--inactive marks approvers as unavailable, which prunes their targets. A
file no rule covers falls back to the --owner repository owners.`,
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
	inactive  []string
}

func reviewOptionsFrom(v tui.Values) (reviewOptions, error) {
	o := reviewOptions{patterns: v.Args, changed: v.Bool("changed"), author: v.Flag("author")}
	for name, dst := range map[string]*[]string{"approver": &o.approvers, "owner": &o.owners, "inactive": &o.inactive} {
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
	// Other commands print only their result; the algorithm's trace is
	// for --verbose, as it is in the TUI's Trace view.
	log := quietLog
	if verbose {
		log = slog.Default()
	}
	state, err := runReview(cmd.Context(), opts, log)
	if err != nil {
		return err
	}
	if mode == modeJSON {
		if err := writeJSON(os.Stdout, state); err != nil {
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
	}
	if state.Overall == approval.StatusError {
		return fmt.Errorf("review did not run cleanly: %w", state.Err)
	}
	return nil
}

// runReview selects the files and evaluates them: the working tree's files,
// or its changed ones, kept when they match any pattern. log receives the
// algorithm's structured output; the TUI discards it, as the state holds
// everything it reports.
func runReview(ctx context.Context, opts reviewOptions, log *slog.Logger) (*approval.ReviewState, error) {
	repo, err := getRepo()
	if err != nil {
		return nil, err
	}
	engine := newLocalEngine(repo, log)
	files, err := engine.Files(ctx, match.Options{ChangedOnly: opts.changed, Patterns: opts.patterns})
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	return engine.Review(ctx, files, match.ReviewInput{
		Author:    opts.author,
		Approvers: opts.approvers,
		Inactive:  opts.inactive,
		Owners:    opts.owners,
	}), nil
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

func init() {
	reviewCmd.RunE = runReviewCmd
	reviewCmd.Flags().Bool("changed", false, "review only files changed in the working tree")
	reviewCmd.Flags().String("author", "", "the change's author, whose approvals count only under self-approve rules")
	reviewCmd.Flags().StringSlice("approver", nil, "a user or team that has approved (repeatable)")
	reviewCmd.Flags().StringSlice("owner", nil, "a repository owner, who approves files no rule covers (repeatable)")
	reviewCmd.Flags().StringSlice("inactive", nil, "an approver who is unavailable; their targets are pruned (repeatable)")
	rootCmd.AddCommand(reviewCmd)
}
