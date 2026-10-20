package kenin

import (
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
)

// stubSource is an in-memory Source for testing the Engine without touching
// the filesystem or git. Rules map mirrors shonin's Fetcher output: KIT
// file path to parsed rule list.
type stubSource struct {
	root       string
	rules      map[string]kitfile.Rules
	err        error
	allFiles   []string
	changedErr error
	changedSet []string
	diffs      map[string]string
}

func (s *stubSource) RepoRoot() string { return s.root }

func (s *stubSource) KitFiles() (map[string]kitfile.Rules, error) {
	return s.rules, s.err
}

func (s *stubSource) AllFiles() ([]string, error) { return s.allFiles, nil }

func (s *stubSource) ChangedFiles() ([]string, error) {
	if s.changedErr != nil {
		return nil, s.changedErr
	}
	return s.changedSet, nil
}

func (s *stubSource) DiffContent(path string) (string, error) {
	return s.diffs[path], nil
}

var _ Source = (*stubSource)(nil)

// shared fixture rules: a root KIT rule matching .go and a nested /src KIT
// file with a rule overriding the parent for .go files. The root rule is a
// pass-through, the nested one restricts to current-directory-only so the
// chain is unambiguous per file.
var fixtureRules = map[string]kitfile.Rules{
	"KIT": {
		{Pattern: `.*\.go$`, Name: "top-level go rules", Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}},
	},
	"src/KIT": {
		{Pattern: `.*\.go$`, Name: "src go rules", CurrentDirectoryOnly: true, IgnoreParentDirs: true, Approvers: []kitfile.ApproverTarget{{Target: "team-b", RequiredCount: 1}}},
	},
}

func TestMatchReturnsAllFilesInUniverse(t *testing.T) {
	src := &stubSource{
		root:     "/repo",
		rules:    fixtureRules,
		allFiles: []string{"main.go", "src/api.go", "docs/readme.md"},
	}
	e := New(src, nil)
	res, err := e.Match(Options{})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(res.Files) != 3 {
		t.Errorf("Match returned %d files, want 3 (every file in the universe is represented)", len(res.Files))
	}
	for _, fm := range res.Files {
		switch fm.Path {
		case "main.go":
			if len(fm.Scopes) != 1 || fm.Scopes[0].Name != "top-level go rules" {
				t.Errorf("main.go scopes = %+v, want one 'top-level go rules'", fm.Scopes)
			}
		case "src/api.go":
			if len(fm.Scopes) != 1 || fm.Scopes[0].Name != "src go rules" {
				t.Errorf("src/api.go scopes = %+v, want one 'src go rules' (parent rule broken by IgnoreParentDirs)", fm.Scopes)
			}
		case "docs/readme.md":
			if len(fm.Scopes) != 0 {
				t.Errorf("docs/readme.md scopes = %+v, want none (no rule matches)", fm.Scopes)
			}
		}
	}
}

func TestMatchChangesOnlyFilesWhenOptionSet(t *testing.T) {
	src := &stubSource{
		root:       "/repo",
		rules:      fixtureRules,
		allFiles:   []string{"main.go", "src/api.go"},
		changedSet: []string{"src/api.go"},
	}
	e := New(src, nil)
	res, err := e.Match(Options{ChangedOnly: true})
	if err != nil {
		t.Fatalf("Match changed-only: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "src/api.go" {
		t.Errorf("Match(ChangedOnly) returned %+v, want only the changed file", res.Files)
	}
}

func TestMatchScopeNameFilter(t *testing.T) {
	src := &stubSource{
		root:  "/repo",
		rules: fixtureRules,
		// One matching .go file; the nested rule's scope name is "src go rules"
		allFiles: []string{"src/api.go"},
	}
	e := New(src, nil)
	res, err := e.Match(Options{ScopeName: "src go rules"})
	if err != nil {
		t.Fatalf("Match scope filter: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("Match returned %d files, want 1", len(res.Files))
	}
	fm := res.Files[0]
	if len(fm.Scopes) != 1 || fm.Scopes[0].Name != "src go rules" {
		t.Errorf("scope filter did not retain requested scope: %+v", fm.Scopes)
	}
}

func TestMatchIncludeDiffAttachesDiff(t *testing.T) {
	src := &stubSource{
		root:     "/repo",
		rules:    fixtureRules,
		allFiles: []string{"main.go"},
		diffs:    map[string]string{"main.go": "--- a/main.go\n+++ b/main.go\n"},
	}
	e := New(src, nil)
	res, err := e.Match(Options{IncludeDiff: true})
	if err != nil {
		t.Fatalf("Match IncludeDiff: %v", err)
	}
	if res.Files[0].Diff == "" {
		t.Errorf("IncludeDiff did not attach the diff")
	}
	res2, err := e.Match(Options{})
	if err != nil {
		t.Fatalf("Match without diff: %v", err)
	}
	if res2.Files[0].Diff != "" {
		t.Errorf("Diff was attached when IncludeDiff was false: %q", res2.Files[0].Diff)
	}
}

func TestMatchRuleLoadingErrorPropagates(t *testing.T) {
	src := &stubSource{
		rules:    fixtureRules,
		allFiles: []string{"a.go"},
		err:      errRules,
	}
	e := New(src, nil)
	if _, err := e.Match(Options{}); err != errRules {
		t.Errorf("Match must surface the rule-loading error, got %v", err)
	}
}

func TestCoverageUnmatchedFilesAndUnmatchedSet(t *testing.T) {
	src := &stubSource{
		rules:    fixtureRules,
		allFiles: []string{"main.go", "src/api.go", "docs/readme.md"},
	}
	e := New(src, nil)
	report, err := e.Coverage(Options{})
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	// main.go → root rule (go). src/api.go → nested rule. docs/readme.md → no rule.
	if report.Universe != 3 || report.Covered != 2 {
		t.Errorf("Coverage: universe=%d, covered=%d; want 3, 2", report.Universe, report.Covered)
	}
	want := 100.0 * 2 / 3
	d := report.Percent - want
	if d < 0 {
		d = -d
	}
	if d > 1e-9 {
		t.Errorf("Coverage: percent=%.10f, want %.10f", report.Percent, want)
	}
	if len(report.Uncovered) != 1 || report.Uncovered[0] != "docs/readme.md" {
		t.Errorf("Coverage: unmatched=%+v, want [docs/readme.md]", report.Uncovered)
	}
	if len(report.PerScope) != 2 {
		t.Errorf("Coverage: PerScope length=%d, want 2 (one per distinct scope)", len(report.PerScope))
	}
}

func TestCoverageChangedOnly(t *testing.T) {
	src := &stubSource{
		rules:      fixtureRules,
		allFiles:   []string{"a.go", "b.go"},
		changedSet: []string{"a.go"},
	}
	e := New(src, nil)
	report, err := e.Coverage(Options{ChangedOnly: true})
	if err != nil {
		t.Fatalf("Coverage changed-only: %v", err)
	}
	if report.Universe != 1 {
		t.Errorf("Coverage changed-only universe=%d, want 1", report.Universe)
	}
}

var errRules = err("kit file parsing failed")

type err string

func (e err) Error() string { return string(e) }
