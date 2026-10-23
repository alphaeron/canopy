package review

import (
	"reflect"
	"testing"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

func TestFilterScope(t *testing.T) {
	core := rulesfile.Rule{Name: "core", Pattern: `.*\.go`, RulesFile: rulesfile.FileName}
	nested := rulesfile.Rule{Name: "core", Pattern: `.*`, RulesFile: rulesfile.PathInDir("svc")}
	docs := rulesfile.Rule{Name: "docs", Pattern: `.*\.md`, RulesFile: rulesfile.FileName}
	in := &Result{RepoRoot: "/repo", Files: []FileMatch{
		{Path: "a.go", Rules: rulesfile.Rules{core, docs}, Diff: "+a"},
		{Path: "svc/b.go", Rules: rulesfile.Rules{nested}},
		{Path: "c.md", Rules: rulesfile.Rules{docs}},
		{Path: "d.txt"},
	}}
	tests := []struct {
		name string
		want []FileMatch
	}{
		{name: "core", want: []FileMatch{
			{Path: "a.go", Rules: rulesfile.Rules{core}, Diff: "+a"},
			{Path: "svc/b.go", Rules: rulesfile.Rules{nested}},
		}},
		{name: "docs", want: []FileMatch{
			{Path: "a.go", Rules: rulesfile.Rules{docs}, Diff: "+a"},
			{Path: "c.md", Rules: rulesfile.Rules{docs}},
		}},
		{name: "absent", want: []FileMatch{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := in.FilterScope(tc.name)
			if got.RepoRoot != in.RepoRoot || !reflect.DeepEqual(got.Files, tc.want) {
				t.Errorf("FilterScope(%q) = %+v, want %+v", tc.name, got.Files, tc.want)
			}
		})
	}
	if len(in.Files) != 4 || len(in.Files[0].Rules) != 2 {
		t.Errorf("FilterScope must not modify its receiver: %+v", in.Files)
	}
}

// TestGroupByRule pins the by-rule view: one group per rule (by location,
// so a repeated pattern is its own group), ordered by name, then location,
// each with its sorted files.
func TestGroupByRule(t *testing.T) {
	core := rulesfile.Rule{Name: "core", Pattern: `.*\.go`, RulesFile: rulesfile.FileName}
	docs := rulesfile.Rule{Name: "docs", Pattern: `.*\.md`, RulesFile: rulesfile.FileName, Position: 1}
	override := rulesfile.Rule{Name: "override", Pattern: `.*\.go`, RulesFile: rulesfile.FileName, Position: 2}
	nested := rulesfile.Rule{Name: "core", Pattern: `.*`, RulesFile: rulesfile.PathInDir("svc")}
	r := &Result{Files: []FileMatch{
		{Path: "svc/b.go", Rules: rulesfile.Rules{core, override, nested}},
		{Path: "a.go", Rules: rulesfile.Rules{core, docs, override}},
		{Path: "c.md", Rules: rulesfile.Rules{docs}},
		{Path: "d.txt"},
	}}
	want := []approval.RuleFiles{
		{Rule: core, Files: []string{"a.go", "svc/b.go"}},
		{Rule: nested, Files: []string{"svc/b.go"}},
		{Rule: docs, Files: []string{"a.go", "c.md"}},
		{Rule: override, Files: []string{"a.go", "svc/b.go"}},
	}
	if got := r.GroupByRule(); !reflect.DeepEqual(got, want) {
		t.Errorf("GroupByRule() = %+v, want %+v", got, want)
	}
}

func TestResultFilesOfAndOverlaps(t *testing.T) {
	a := rulesfile.Rule{Name: "a", Pattern: ".*", RulesFile: rulesfile.FileName}
	b := rulesfile.Rule{Name: "b", Pattern: `\.go$`, RulesFile: rulesfile.PathInDir("src")}
	c := rulesfile.Rule{Name: "c", Pattern: `api`, RulesFile: rulesfile.PathInDir("src"), Position: 1}
	r := &Result{Files: []FileMatch{
		{Path: "src/api.go", Rules: rulesfile.Rules{a, b, c}},
		{Path: "src/x.go", Rules: rulesfile.Rules{a, b}},
		{Path: "README", Rules: rulesfile.Rules{a}},
	}}
	if got, want := r.FilesOf(b.Key()), []string{"src/api.go", "src/x.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("FilesOf(b) = %v, want %v", got, want)
	}
	want := []RuleOverlap{{Rule: b, Shared: 2}, {Rule: c, Shared: 1}}
	if got := r.Overlaps(a.Key()); !reflect.DeepEqual(got, want) {
		t.Errorf("Overlaps(a) = %+v, want %+v", got, want)
	}
}

func TestResultPathsAndCovered(t *testing.T) {
	r := &Result{Files: []FileMatch{{Path: "a", Rules: rulesfile.Rules{{Pattern: "a"}}}, {Path: "b"}}}
	if got := r.Paths(); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Paths = %v", got)
	}
	if got := r.Covered(); got != 1 {
		t.Errorf("Covered = %d, want 1", got)
	}
}
