package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/match"
	"github.com/alphaeron/kit/internal/tui"
)

func TestReviewOptionsFrom(t *testing.T) {
	v := tui.Values{Args: []string{`\.go$`}, Flags: map[string]string{
		"changed": "true", "author": "alice", "approver": "bob,org/team", "owner": "root", "inactive": "carol",
	}}
	got, err := reviewOptionsFrom(v)
	if err != nil {
		t.Fatal(err)
	}
	want := reviewOptions{patterns: []string{`\.go$`}, changed: true, author: "alice", approvers: []string{"bob", "org/team"}, owners: []string{"root"}, inactive: []string{"carol"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reviewOptionsFrom = %+v, want %+v", got, want)
	}
}

// twoLayerFile is a file under svc/ governed by a root rule and a union
// rule in svc/KIT, with one approval of two.
func twoLayerFile() approval.FileState {
	target := func(name string, have, need int, st approval.Status, by ...string) approval.ApproverTargetState {
		return approval.ApproverTargetState{ApproverTarget: kitfile.ApproverTarget{Target: name, RequiredCount: need}, SatisfiedCount: have, Status: st, ResolvedBy: by}
	}
	sec := target("org/sec", 1, 1, approval.StatusSatisfied, "org/sec")
	backend := target("org/backend", 0, 2, approval.StatusMissingApprovers)
	return approval.FileState{
		Path:   "svc/a.go",
		Status: approval.StatusMissingApprovers,
		Layers: []approval.LayerState{
			{DirectoryPath: "", Active: true, Status: approval.StatusSatisfied,
				Rules:    []approval.RuleState{{Rule: kitfile.Rule{Pattern: `.*`, Name: "security", CombineStrategy: kitfile.RuleCombineStrategyUnion}, Approvers: []approval.ApproverTargetState{sec}, Status: approval.StatusSatisfied}},
				Required: approval.RuleTargets{{ApproverTarget: sec.ApproverTarget}}},
			{DirectoryPath: "svc", Active: true, Status: approval.StatusMissingApprovers,
				Rules:    []approval.RuleState{{Rule: kitfile.Rule{Pattern: `\.go$`, CombineStrategy: kitfile.RuleCombineStrategyUnion}, Approvers: []approval.ApproverTargetState{backend}, Status: approval.StatusMissingApprovers}},
				Required: approval.RuleTargets{{ApproverTarget: sec.ApproverTarget}, {ApproverTarget: backend.ApproverTarget}}},
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
	d := fileReviewDetail(twoLayerFile(), events)
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
		"KIT | active",
		"security: .* | union: establishes the required approvers",
		"svc/KIT | active",
		`\.go$ | union: adds its approvers to those required above`,
		"→ requires org/sec (1), org/backend (2)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("chain missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "svc/KIT") < strings.Index(text, "KIT | active") {
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
	rules := kitfile.Rules{{Pattern: `(`, Name: "broken"}, {Pattern: `.*`}}
	errs := []string{`invalid pattern "(": missing )`, `something about the whole file`}
	d := violationsDetail("KIT", rules, errs)
	if len(d.Sections) != 2 || d.Sections[0].Title != "Rule (" || d.Sections[1].Title != "File" {
		t.Fatalf("sections = %+v", d.Sections)
	}
	rows := d.Sections[0].Rows
	if rows[0].Badge != tui.BadgeFail || rows[1].Note != "broken" {
		t.Errorf("rule rows = %+v", rows)
	}
	edit := rows[len(rows)-1]
	if edit.Route == nil || edit.Route.Command != "rules edit" || edit.Route.Values.Flag("file") != "KIT" {
		t.Errorf("the last row must link to editing the rule: %+v", edit)
	}
	if valid := violationsDetail("KIT", rules, nil); len(valid.Sections) != 0 || valid.Fields[1].Badge != tui.BadgeOK {
		t.Errorf("a valid file = %+v", valid)
	}
}

// TestUncoveredSection groups uncovered files by directory, each linking to
// a rule for its nearest KIT file.
func TestUncoveredSection(t *testing.T) {
	s := newTestKitSet(t, map[string]kitfile.Rules{"KIT": {}, "svc/KIT": {}})
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
	if deep.Command != "rules add" || deep.Values.Flag("file") != "svc/KIT" || deep.Values.Flag("pattern") != `^svc/deep/` {
		t.Errorf("svc/deep must add a rule to svc/KIT: %+v", deep.Values)
	}
	if !strings.Contains(dirs[2].Note, "2 files") {
		t.Errorf("note = %q", dirs[2].Note)
	}
	if s.nearestTo("other/dir") != "KIT" || newTestKitSet(t, map[string]kitfile.Rules{"svc/KIT": {}}).nearestTo(".") != "" {
		t.Error("nearestTo must walk up to the root and report none past it")
	}
}

// TestRuleMatchesOverlaps: a rule's overlaps are the other rules matching
// its files, most shared first.
func TestRuleMatchesOverlaps(t *testing.T) {
	root := match.Scope{Pattern: ".*", KitFile: "KIT", Name: "all"}
	goRule := match.Scope{Pattern: `\.go$`, KitFile: "svc/KIT", Name: "go"}
	docs := match.Scope{Pattern: `\.md$`, KitFile: "KIT"}
	m := newRuleMatches(&match.Result{Files: []match.FileMatch{
		{Path: "svc/a.go", Scopes: []match.Scope{root, goRule}},
		{Path: "svc/b.go", Scopes: []match.Scope{root, goRule}},
		{Path: "README.md", Scopes: []match.Scope{root, docs}},
	}}, nil)
	rows := m.overlaps("KIT", ".*")
	if len(rows) != 2 || !strings.HasPrefix(rows[0].Text, "go:") || rows[0].Note != "2 shared files" || rows[1].Note != "1 shared file" {
		t.Fatalf("overlaps = %+v", rows)
	}
	it := ruleItem(kitfile.DeclaredRule{KitFile: "svc/KIT", Rule: kitfile.Rule{Pattern: `\.go$`}}, m)
	if !strings.Contains(it.Description, "2 files") || len(it.Detail.Tabs) != 1 || len(it.Detail.Tabs[0].Sections[0].Rows) != 2 {
		t.Errorf("rule item = %+v", it)
	}
}
