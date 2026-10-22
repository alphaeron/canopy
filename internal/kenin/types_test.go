package kenin

import (
	"reflect"
	"testing"
)

func TestFilterScope(t *testing.T) {
	core := Scope{Name: "core", Pattern: `.*\.go`, KITFile: "KIT"}
	nested := Scope{Name: "core", Pattern: `.*`, Directory: "svc", KITFile: "svc/KIT"}
	docs := Scope{Name: "docs", Pattern: `.*\.md`, KITFile: "KIT"}
	in := &MatchResult{RepoRoot: "/repo", Files: []FileMatch{
		{Path: "a.go", Scopes: []Scope{core, docs}, Diff: "+a"},
		{Path: "svc/b.go", Scopes: []Scope{nested}},
		{Path: "c.md", Scopes: []Scope{docs}},
		{Path: "d.txt"},
	}}
	tests := []struct {
		name string
		want []FileMatch
	}{
		{name: "core", want: []FileMatch{
			{Path: "a.go", Scopes: []Scope{core}, Diff: "+a"},
			{Path: "svc/b.go", Scopes: []Scope{nested}},
		}},
		{name: "docs", want: []FileMatch{
			{Path: "a.go", Scopes: []Scope{docs}, Diff: "+a"},
			{Path: "c.md", Scopes: []Scope{docs}},
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
	if len(in.Files) != 4 || len(in.Files[0].Scopes) != 2 {
		t.Errorf("FilterScope must not modify its receiver: %+v", in.Files)
	}
}

// TestCoverageFilterScope pins that narrowing the breakdown keeps the
// overall numbers, which describe every rule, and leaves the receiver alone.
func TestCoverageFilterScope(t *testing.T) {
	report := &CoverageReport{
		Universe: 10,
		Covered:  7,
		Percent:  70.0,
		PerScope: []ScopeCoverage{
			{Scope: Scope{Name: "core"}, Files: 5, Percent: 50},
			{Scope: Scope{Name: "sec"}, Files: 2, Percent: 20},
		},
		Uncovered: []string{"x.go"},
	}
	got := report.FilterScope("sec")
	want := *report
	want.PerScope = []ScopeCoverage{{Scope: Scope{Name: "sec"}, Files: 2, Percent: 20}}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("FilterScope = %+v, want %+v", *got, want)
	}
	if len(report.PerScope) != 2 {
		t.Errorf("FilterScope must not modify its receiver: %+v", report.PerScope)
	}
}
