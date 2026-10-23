package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
)

func TestReviewOptionsFrom(t *testing.T) {
	v := tui.Values{Args: []string{`\.go$`}, Flags: map[string]string{
		"changed": "true", "author": "alice", "approver": "bob,org/team", "owner": "root",
	}}
	got, err := reviewOptionsFrom(v)
	if err != nil {
		t.Fatal(err)
	}
	want := reviewOptions{patterns: []string{`\.go$`}, changed: true, author: "alice", approvers: []string{"bob", "org/team"}, owners: []string{"root"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reviewOptionsFrom = %+v, want %+v", got, want)
	}
}

// twoLayerFile is a file under svc/ governed by a root rule and a union
// rule in svc's rules file, with one approval of two.
func twoLayerFile() approval.FileState {
	target := func(name string, have, need int, st approval.Status, by ...string) approval.ApproverTargetState {
		return approval.ApproverTargetState{ApproverTarget: rulesfile.ApproverTarget{Target: name, RequiredCount: need}, SatisfiedCount: have, Status: st, ResolvedBy: by}
	}
	sec := target("org/sec", 1, 1, approval.StatusSatisfied, "org/sec")
	backend := target("org/backend", 0, 2, approval.StatusMissingApprovers)
	return approval.FileState{
		Path:   "svc/a.go",
		Status: approval.StatusMissingApprovers,
		Layers: []approval.LayerState{
			{DirectoryPath: "", Status: approval.StatusSatisfied,
				Rules:    []approval.RuleState{{Rule: rulesfile.Rule{Pattern: `.*`, Name: "security", CombineStrategy: rulesfile.RuleCombineStrategyUnion}, Approvers: []approval.ApproverTargetState{sec}, Status: approval.StatusSatisfied}},
				Required: approval.ExecutableTargets{{ApproverTarget: sec.ApproverTarget}}},
			{DirectoryPath: "svc", Status: approval.StatusMissingApprovers,
				Rules:    []approval.RuleState{{Rule: rulesfile.Rule{Pattern: `\.go$`, CombineStrategy: rulesfile.RuleCombineStrategyUnion}, Approvers: []approval.ApproverTargetState{backend}, Status: approval.StatusMissingApprovers}},
				Required: approval.ExecutableTargets{{ApproverTarget: sec.ApproverTarget}, {ApproverTarget: backend.ApproverTarget}}},
		},
		Required: []approval.ApproverTargetState{sec, backend},
	}
}

// TestFileReviewDetail pins the evaluation views: progress per approver in
// the summary; the layer cascade, root first, with each rule's strategy
// effect and the requirement it hands down, in Chain; the file's own events
// in Trace.
func TestFileReviewDetail(t *testing.T) {
	events := []approval.Event{
		{Phase: "approve", Path: "svc/a.go", Layer: "svc", Note: "Applied union"},
		{Phase: "approve", Path: "other.go", Note: "not this file"},
	}
	d := fileReviewDetail(twoLayerFile(), &approval.ReviewState{Events: events})
	if got := requiredLabel(twoLayerFile()); got != "org/sec 1/1, org/backend 0/2" {
		t.Errorf("requiredLabel = %q", got)
	}
	approvers := d.Sections[0].Rows
	if len(approvers) != 2 || *approvers[1].Meter != (tui.Meter{Have: 0, Need: 2}) || approvers[0].Badge != tui.BadgeOK {
		t.Errorf("approver rows = %+v", approvers)
	}
	if len(d.Tabs) != 2 || d.Tabs[0].Title != "Chain" || d.Tabs[1].Title != "Trace" {
		t.Fatalf("tabs = %+v", d.Tabs)
	}
	var chain []string
	for _, r := range d.Tabs[0].Sections[0].Rows {
		chain = append(chain, r.Text+" | "+r.Note)
	}
	text := strings.Join(chain, "\n")
	for _, want := range []string{
		rulesfile.FileName + " | satisfied",
		"security: .* | union: establishes the required approvers",
		rulesfile.PathInDir("svc") + " | missing approvers",
		`\.go$ | union: adds its approvers to those required above`,
		"→ requires org/sec (1), org/backend (2)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chain missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, rulesfile.PathInDir("svc")) < strings.Index(text, rulesfile.FileName+" | satisfied") {
		t.Errorf("layers must run root first:\n%s", text)
	}
	trace := d.Tabs[1].Sections[0].Rows
	if len(trace) != 1 || trace[0].Note != "Applied union" {
		t.Errorf("trace must hold only the file's events: %+v", trace)
	}
}

func TestStatusBadgeAndOrder(t *testing.T) {
	if statusBadge(approval.StatusSatisfied) != tui.BadgeOK || statusBadge(approval.StatusError) != tui.BadgeFail ||
		statusBadge(approval.StatusMissingApprovers) != tui.BadgePending || statusBadge(approval.StatusInactive) != tui.BadgeOff {
		t.Error("status badges")
	}
	state := &approval.ReviewState{Files: map[string]approval.FileState{
		"b": {Path: "b", Status: approval.StatusSatisfied},
		"a": {Path: "a", Status: approval.StatusSatisfied},
		"z": {Path: "z", Status: approval.StatusError},
		"m": {Path: "m", Status: approval.StatusMissingApprovers},
	}}
	var got []string
	for _, fs := range state.SortedFiles() {
		got = append(got, fs.Path)
	}
	if want := []string{"z", "m", "a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v (most in need of attention first)", got, want)
	}
}

// TestViolationsDetail: each violation sits with the rule it names, with
// that rule's fields and an edit link; others are the file's.
func TestViolationsDetail(t *testing.T) {
	rules := rulesfile.Rules{{Pattern: `(`, Name: "broken"}, {Pattern: `.*`}}
	errs := append(rules.Validate(), errors.New("something about the whole file"))
	d := violationsDetail(rulesfile.FileName, rules, errs)
	if len(d.Sections) != 2 || d.Sections[0].Title != "Rule [0] (" || d.Sections[1].Title != "File" {
		t.Fatalf("sections = %+v", d.Sections)
	}
	rows := d.Sections[0].Rows
	if rows[0].Badge != tui.BadgeFail || rows[1].Note != "broken" {
		t.Errorf("rule rows = %+v", rows)
	}
	edit := rows[len(rows)-1]
	if edit.Route == nil || edit.Route.Command != "rules edit" || edit.Route.Values.Flag("file") != rulesfile.FileName {
		t.Errorf("the last row must link to editing the rule: %+v", edit)
	}
	if valid := violationsDetail(rulesfile.FileName, rules, nil); len(valid.Sections) != 0 || valid.Fields[1].Badge != tui.BadgeOK {
		t.Errorf("a valid file = %+v", valid)
	}
}

func TestGroupByDir(t *testing.T) {
	got := groupByDir([]string{"svc/b.go", "README", "svc/a.go", "svc/x/c.go"})
	want := []dirFiles{{Dir: ".", Files: []string{"README"}}, {Dir: "svc", Files: []string{"svc/b.go", "svc/a.go"}}, {Dir: "svc/x", Files: []string{"svc/x/c.go"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("groupByDir = %+v, want %+v", got, want)
	}
}

// TestUncoveredSection groups uncovered files by directory, each linking to
// a rule for its nearest rules file.
func TestUncoveredSection(t *testing.T) {
	s := newTestWorkspace(t, map[string]rulesfile.Rules{rulesfile.FileName: {}, rulesfile.PathInDir("svc"): {}})
	sec := uncoveredSection([]string{"README.md", "svc/deep/x.txt", "svc/y.txt", "svc/deep/z.txt"}, s)
	var dirs []tui.Row
	for _, r := range sec.Rows {
		if r.Route != nil {
			dirs = append(dirs, r)
		}
	}
	if len(dirs) != 3 || dirs[0].Text != "repository root" || dirs[1].Text != "svc/" || dirs[2].Text != "svc/deep/" {
		t.Fatalf("directory rows = %+v", dirs)
	}
	deep := dirs[2].Route
	if deep.Command != "rules add" || deep.Values.Flag("file") != rulesfile.PathInDir("svc") || deep.Values.Flag("pattern") != `^svc/deep/` {
		t.Errorf("svc/deep must add a rule to svc's rules file: %+v", deep.Values)
	}
	if !strings.Contains(dirs[2].Note, "2 files") {
		t.Errorf("note = %q", dirs[2].Note)
	}
}

// TestRuleMatchesOverlaps: a rule's overlaps are the other rules matching
// its files, most shared first.
func TestRuleMatchesOverlaps(t *testing.T) {
	root := rulesfile.Rule{Pattern: ".*", RulesFile: rulesfile.FileName, Name: "all"}
	goRule := rulesfile.Rule{Pattern: `\.go$`, RulesFile: rulesfile.PathInDir("svc"), Name: "go"}
	docs := rulesfile.Rule{Pattern: `\.md$`, RulesFile: rulesfile.FileName, Position: 1}
	m := ruleMatches{match: &review.Result{Files: []review.FileMatch{
		{Path: "svc/a.go", Rules: rulesfile.Rules{root, goRule}},
		{Path: "svc/b.go", Rules: rulesfile.Rules{root, goRule}},
		{Path: "README.md", Rules: rulesfile.Rules{root, docs}},
	}}}
	rows := m.overlaps(root.Key())
	if len(rows) != 2 || !strings.HasPrefix(rows[0].Text, "go:") || rows[0].Note != "2 shared files" || rows[1].Note != "1 shared file" {
		t.Fatalf("overlaps = %+v", rows)
	}
	it := ruleItem(goRule, m)
	if !strings.Contains(it.Description, "2 files") || len(it.Detail.Tabs) != 1 || len(it.Detail.Tabs[0].Sections[0].Rows) != 2 {
		t.Errorf("rule item = %+v", it)
	}
	// Enter opens the chains of exactly the rule's own files, whatever else
	// its pattern would match.
	if it.Route == nil || it.Route.Command != "rules get" || !reflect.DeepEqual(it.Route.Values.Args, []string{review.ExactPattern("svc/a.go", "svc/b.go")}) {
		t.Errorf("rule route = %+v, want rules get for the rule's files", it.Route)
	}
	if r := ruleFilesRoute(docs, nil); r != nil {
		t.Errorf("a rule with no files has route %+v, want none", r)
	}
}

// TestReviewWithOutcome runs kit review's core over a real repository: the
// working tree's files are reviewed (a new file counts, a deleted one does
// not), the outcome is the shared verdict, and a rules file the working tree
// changes into an invalid one fails the review, as it would a pull request.
func TestReviewWithOutcome(t *testing.T) {
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	write := func(p, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, p), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	write(rulesfile.FileName, "- \".*\":\n    approvers:\n      - target: lead\n        required_count: 1\n")
	write("old.go", "package old\n")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(0, 0)}}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	write("new.go", "package new\n")
	if err := os.Remove(filepath.Join(root, "old.go")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	res, err := runReview(t.Context(), reviewOptions{}, quietLog)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if _, ok := res.state.Files["new.go"]; !ok {
		t.Errorf("the new file must be reviewed: %v", res.state.SortedFiles())
	}
	if _, ok := res.state.Files["old.go"]; ok {
		t.Errorf("the deleted file must not be reviewed")
	}
	if res.outcome.Verdict != approval.Pending {
		t.Errorf("outcome without approvals = %+v, want pending", res.outcome)
	}
	if res, err := runReview(t.Context(), reviewOptions{approvers: []string{"Lead"}}, quietLog); err != nil || res.outcome.Verdict != approval.Passed {
		t.Errorf("outcome approved by Lead = %+v, %v; want passed (names compare as on GitHub)", res.outcome, err)
	}

	write(rulesfile.FileName, "- \"(\":\n    approvers:\n      - target: lead\n        required_count: 1\n")
	res, err = runReview(t.Context(), reviewOptions{approvers: []string{"lead"}}, quietLog)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if res.outcome.Verdict != approval.Failed || res.outcome.Summary != "1 invalid proposed rules file" {
		t.Errorf("outcome with an invalid changed rules file = %+v, want failed", res.outcome)
	}
}
