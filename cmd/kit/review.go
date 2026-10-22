package main

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kenin"
	"github.com/alphaeron/kit/internal/shonin"
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
each has, and how every KIT file in its chain contributed.

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
	state, err := runReview(opts, log)
	if err != nil {
		return err
	}
	if mode == modeJSON {
		if err := tui.JSONEncoder(os.Stdout, reviewJSONOf(state)); err != nil {
			return err
		}
	} else {
		rows := make([][]string, 0, len(state.Files))
		for _, fs := range sortedFileStates(state) {
			rows = append(rows, []string{fs.Path, statusLabel(fs.Status), requiredLabel(fs)})
		}
		if err := tui.TablePrinter(os.Stdout, []string{"File", "Status", "Requires"}, rows); err != nil {
			return err
		}
		fmt.Printf("Overall: %s\n", statusLabel(state.Overall))
	}
	if state.Overall == shonin.StatusError {
		return fmt.Errorf("review did not run cleanly: %w", state.Err)
	}
	return nil
}

// runReview selects the files and evaluates them. log receives the
// algorithm's structured output; the TUI discards it, as the state holds
// everything it reports.
func runReview(opts reviewOptions, log *slog.Logger) (*shonin.ReviewState, error) {
	repo, err := getRepo()
	if err != nil {
		return nil, err
	}
	files, err := reviewFiles(repo, opts)
	if err != nil {
		return nil, err
	}
	inactive := map[string]bool{}
	for _, u := range opts.inactive {
		inactive[u] = true
	}
	approvals := map[string]bool{}
	for _, u := range opts.approvers {
		approvals[u] = true
	}
	s := shonin.Shonin{Fetch: &shonin.LocalFetcher{Repo: repo}, Resolver: &shonin.DefaultApproverResolver{}, Log: log}
	return s.Execute(opts.author, approvals, func(u string) bool { return !inactive[u] }, opts.owners, files), nil
}

// reviewFiles is the sorted file universe of a review: the working tree's
// files, or its changed ones, kept when they match any pattern.
func reviewFiles(repo *gitRepo, opts reviewOptions) ([]string, error) {
	res := make([]*regexp.Regexp, len(opts.patterns))
	for i, p := range opts.patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q: %w", p, err)
		}
		res[i] = re
	}
	src := kenin.NewLocalSource(repo, &shonin.LocalFetcher{Repo: repo}, nil)
	var all []string
	var err error
	if opts.changed {
		all, err = src.ChangedFiles()
	} else {
		all, err = src.AllFiles()
	}
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	var files []string
	for _, f := range all {
		if len(res) == 0 || slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(f) }) {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return files, nil
}

// statusOrder ranks file statuses for display, most in need of attention
// first.
func statusOrder(s shonin.Status) int {
	switch s {
	case shonin.StatusError:
		return 0
	case shonin.StatusUnresolved:
		return 1
	case shonin.StatusMissingApprovers:
		return 2
	case shonin.StatusSatisfied:
		return 4
	}
	return 3
}

// sortedFileStates lists a review's files, most in need of attention first,
// then by path.
func sortedFileStates(state *shonin.ReviewState) []shonin.FileState {
	out := make([]shonin.FileState, 0, len(state.Files))
	for _, fs := range state.Files {
		out = append(out, fs)
	}
	slices.SortFunc(out, func(a, b shonin.FileState) int {
		if d := statusOrder(a.Status) - statusOrder(b.Status); d != 0 {
			return d
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// statusLabel is a status for people: "missing approvers".
func statusLabel(s shonin.Status) string { return strings.ReplaceAll(s.String(), "_", " ") }

// requiredLabel is a file's requirement with progress, e.g.
// "org/backend 1/2, lead 0/1", marking the repository-owner fallback.
func requiredLabel(fs shonin.FileState) string {
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

// reviewJSON is the --json form of a review.
type reviewJSON struct {
	Overall string           `json:"overall"`
	Files   []fileReviewJSON `json:"files"`
	Errors  []string         `json:"errors"`
	Events  []eventJSON      `json:"events"`
}

type fileReviewJSON struct {
	Path         string           `json:"path"`
	Status       string           `json:"status"`
	UsedFallback bool             `json:"used_fallback"`
	Required     []targetJSON     `json:"required"`
	Layers       []layerStateJSON `json:"layers"`
}

type targetJSON struct {
	Target        string   `json:"target"`
	RequiredCount int      `json:"required_count"`
	Approved      int      `json:"approved"`
	Status        string   `json:"status"`
	Reason        string   `json:"reason,omitempty"`
	ApprovedBy    []string `json:"approved_by"`
}

type layerStateJSON struct {
	KitFile  string          `json:"kit_file"`
	Status   string          `json:"status"`
	Active   bool            `json:"active"`
	Reason   string          `json:"reason,omitempty"`
	Rules    []ruleStateJSON `json:"rules"`
	Requires []approverJSON  `json:"requires"`
}

type ruleStateJSON struct {
	Pattern   string       `json:"pattern"`
	Name      string       `json:"name,omitempty"`
	Strategy  string       `json:"combine_strategy"`
	Status    string       `json:"status"`
	Reason    string       `json:"reason,omitempty"`
	Approvers []targetJSON `json:"approvers"`
}

type eventJSON struct {
	Phase  string `json:"phase"`
	Path   string `json:"path,omitempty"`
	Layer  string `json:"layer,omitempty"`
	Rule   string `json:"rule,omitempty"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

func reviewJSONOf(state *shonin.ReviewState) reviewJSON {
	out := reviewJSON{Overall: state.Overall.String(), Files: []fileReviewJSON{}, Errors: state.Errors, Events: []eventJSON{}}
	if out.Errors == nil {
		out.Errors = []string{}
	}
	for _, fs := range sortedFileStates(state) {
		f := fileReviewJSON{Path: fs.Path, Status: fs.Status.String(), UsedFallback: fs.UsedFallback, Required: targetsJSON(fs.Required), Layers: []layerStateJSON{}}
		for _, l := range fs.Layers {
			lj := layerStateJSON{KitFile: kitFileOfDir(l.DirectoryPath), Status: l.Status.String(), Active: l.Active, Reason: l.Reason, Rules: []ruleStateJSON{}, Requires: []approverJSON{}}
			for _, r := range l.Rules {
				lj.Rules = append(lj.Rules, ruleStateJSON{
					Pattern: r.Pattern, Name: r.Name, Strategy: r.CombineStrategy.String(),
					Status: r.Status.String(), Reason: r.Reason, Approvers: targetsJSON(r.Approvers),
				})
			}
			for _, t := range l.Required {
				lj.Requires = append(lj.Requires, approverJSON{t.Target, t.RequiredCount})
			}
			f.Layers = append(f.Layers, lj)
		}
		out.Files = append(out.Files, f)
	}
	for _, e := range state.Events {
		out.Events = append(out.Events, eventJSON{Phase: e.Phase, Path: e.Path, Layer: e.Layer, Rule: e.Rule, Status: e.Status.String(), Note: e.Note})
	}
	return out
}

func targetsJSON(states []shonin.ApproverTargetState) []targetJSON {
	out := make([]targetJSON, 0, len(states))
	for _, s := range states {
		by := s.ResolvedBy
		if by == nil {
			by = []string{}
		}
		out = append(out, targetJSON{
			Target: s.Target, RequiredCount: s.RequiredCount, Approved: s.SatisfiedCount,
			Status: s.Status.String(), Reason: s.Reason, ApprovedBy: by,
		})
	}
	return out
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
