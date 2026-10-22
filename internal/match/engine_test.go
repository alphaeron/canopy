package match

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/kitfile"
)

// stubSource is an in-memory Source for testing the Engine without touching
// the filesystem or git. Rules map mirrors approval.Fetcher output: KIT
// file path to parsed rule list.
type stubSource struct {
	root       string
	rules      map[string]kitfile.Rules
	err        error
	allFiles   []string
	changedErr error
	changedSet []string
	diffs      map[string]string
	diffCalls  []string
}

func (s *stubSource) RepoRoot() string { return s.root }

// Reviewer is a local-style platform over the stub's rules.
func (s *stubSource) Reviewer() *approval.Reviewer {
	return &approval.Reviewer{
		Fetch:    &stubFetch{root: s.root, rules: s.rules, err: s.err},
		Resolver: &approval.DefaultApproverResolver{},
		Log:      slog.New(slog.DiscardHandler),
	}
}

func (s *stubSource) AllFiles(context.Context) ([]string, error) { return s.allFiles, nil }

func (s *stubSource) ChangedFiles(context.Context) ([]string, error) {
	if s.changedErr != nil {
		return nil, s.changedErr
	}
	return s.changedSet, nil
}

func (s *stubSource) DiffContent(_ context.Context, path string) (string, error) {
	s.diffCalls = append(s.diffCalls, path)
	return s.diffs[path], nil
}

var _ Source = (*stubSource)(nil)

// stubFetch is a Fetcher over a fixed rule map.
type stubFetch struct {
	root  string
	rules map[string]kitfile.Rules
	err   error
}

func (s *stubFetch) Execute(context.Context) (string, kitfile.Set, error) {
	return s.root, s.rules, s.err
}

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
	res, err := e.Match(t.Context(), Options{})
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
	res, err := e.Match(t.Context(), Options{ChangedOnly: true})
	if err != nil {
		t.Fatalf("Match changed-only: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "src/api.go" {
		t.Errorf("Match(ChangedOnly) returned %+v, want only the changed file", res.Files)
	}
}

// TestMatchScopeNameFilter pins --scope semantics: matching still uses
// every rule (src/api.go keeps only its own layer's scope, since
// IgnoreParentDirs breaks the parent chain), files outside the scope are
// dropped, a declared scope matching nothing is an empty result, and an
// undeclared name is ErrUnknownScope.
func TestMatchScopeNameFilter(t *testing.T) {
	universe := []string{"main.go", "src/api.go", "docs/readme.md"}
	tests := []struct {
		name      string
		scope     string
		changed   []string
		wantFiles []string
		wantErr   error
	}{
		{name: "nested scope", scope: "src go rules", wantFiles: []string{"src/api.go"}},
		{name: "root scope", scope: "top-level go rules", wantFiles: []string{"main.go"}},
		{name: "declared but unmatched", scope: "src go rules", changed: []string{"main.go"}, wantFiles: []string{}},
		{name: "undeclared", scope: "no such rule", wantErr: ErrUnknownScope},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &stubSource{root: "/repo", rules: fixtureRules, allFiles: universe, changedSet: tc.changed}
			res, err := New(src, nil).Match(t.Context(), Options{ScopeName: tc.scope, ChangedOnly: tc.changed != nil})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Match error = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			got := []string{}
			for _, fm := range res.Files {
				got = append(got, fm.Path)
				if len(fm.Scopes) != 1 || fm.Scopes[0].Name != tc.scope {
					t.Errorf("%s scopes = %+v, want only %q", fm.Path, fm.Scopes, tc.scope)
				}
			}
			if !reflect.DeepEqual(got, tc.wantFiles) {
				t.Errorf("files = %v, want %v", got, tc.wantFiles)
			}
		})
	}
}

// TestCoverageScopeName mirrors Match: a declared scope narrows the
// breakdown but not the overall numbers, and an undeclared name is
// ErrUnknownScope rather than an empty report.
func TestCoverageScopeName(t *testing.T) {
	src := &stubSource{root: "/repo", rules: fixtureRules, allFiles: []string{"main.go", "src/api.go", "docs/readme.md"}}
	e := New(src, nil)
	full, _, err := e.Coverage(t.Context(), Options{})
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	got, _, err := e.Coverage(t.Context(), Options{ScopeName: "src go rules"})
	if err != nil {
		t.Fatalf("Coverage scoped: %v", err)
	}
	if len(got.PerScope) != 1 || got.PerScope[0].Scope.Name != "src go rules" {
		t.Errorf("breakdown = %+v, want only the requested scope", got.PerScope)
	}
	if got.Universe != full.Universe || got.Covered != full.Covered || !reflect.DeepEqual(got.Uncovered, full.Uncovered) {
		t.Errorf("overall numbers changed: got %+v, full %+v", got, full)
	}
	if _, _, err := e.Coverage(t.Context(), Options{ScopeName: "no such rule"}); !errors.Is(err, ErrUnknownScope) {
		t.Errorf("undeclared scope error = %v, want ErrUnknownScope", err)
	}
}

// TestMatchDiffOnlyForReportedFiles checks that a scope filter also skips
// fetching diffs for the files it drops.
func TestMatchDiffOnlyForReportedFiles(t *testing.T) {
	src := &stubSource{
		root:     "/repo",
		rules:    fixtureRules,
		allFiles: []string{"main.go", "src/api.go", "docs/readme.md"},
		diffs:    map[string]string{"src/api.go": "+x\n"},
	}
	res, err := New(src, nil).Match(t.Context(), Options{ScopeName: "src go rules", IncludeDiff: true})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !reflect.DeepEqual(src.diffCalls, []string{"src/api.go"}) {
		t.Errorf("diffs fetched for %v, want only the reported file", src.diffCalls)
	}
	if len(res.Files) != 1 || res.Files[0].Diff != "+x\n" {
		t.Errorf("reported file must carry its diff: %+v", res.Files)
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
	res, err := e.Match(t.Context(), Options{IncludeDiff: true})
	if err != nil {
		t.Fatalf("Match IncludeDiff: %v", err)
	}
	if res.Files[0].Diff == "" {
		t.Errorf("IncludeDiff did not attach the diff")
	}
	res2, err := e.Match(t.Context(), Options{})
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
	if _, err := e.Match(t.Context(), Options{}); err != errRules {
		t.Errorf("Match must surface the rule-loading error, got %v", err)
	}
}

func TestCoverageUnmatchedFilesAndUnmatchedSet(t *testing.T) {
	src := &stubSource{
		rules:    fixtureRules,
		allFiles: []string{"main.go", "src/api.go", "docs/readme.md"},
	}
	e := New(src, nil)
	report, _, err := e.Coverage(t.Context(), Options{})
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
	report, _, err := e.Coverage(t.Context(), Options{ChangedOnly: true})
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

func TestFilesNarrowsByPatterns(t *testing.T) {
	src := &stubSource{allFiles: []string{"src/api.go", "main.go", "docs/readme.md"}}
	e := New(src, nil)
	got, err := e.Files(t.Context(), Options{Patterns: []string{`\.md$`, `^main`}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docs/readme.md", "main.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(src.allFiles, []string{"src/api.go", "main.go", "docs/readme.md"}) {
		t.Errorf("Files must not modify the source's slice: %v", src.allFiles)
	}
	if _, err := e.Files(t.Context(), Options{Patterns: []string{"("}}); err == nil {
		t.Error("an invalid pattern must be an error")
	}
}

func TestRequirementsReportsRequiredTargetsAndFallback(t *testing.T) {
	src := &stubSource{rules: fixtureRules}
	e := New(src, slog.New(slog.DiscardHandler))
	chain, err := e.RuleChain(t.Context(), []string{"src/api.go", "docs/readme.md"})
	if err != nil {
		t.Fatal(err)
	}
	state := e.Requirements(t.Context(), chain)
	api := state.Files["src/api.go"]
	if api.UsedFallback || len(api.Required) != 1 || api.Required[0].Target != "team-b" {
		t.Errorf("src/api.go requires %+v, want team-b", api.Required)
	}
	if !state.Files["docs/readme.md"].UsedFallback {
		t.Error("a file no rule covers must fall back to the repository owners")
	}
}

func TestCoverageKeepsTheFullMatch(t *testing.T) {
	src := &stubSource{rules: fixtureRules, allFiles: []string{"main.go", "src/api.go", "docs/readme.md"}}
	report, match, err := New(src, nil).Coverage(t.Context(), Options{ScopeName: "src go rules"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PerScope) != 1 || report.Covered != 2 {
		t.Errorf("report = %+v, want one scope in the breakdown and 2 covered files", report)
	}
	if len(match.Files) != 3 {
		t.Errorf("match has %d files, want the whole universe of 3", len(match.Files))
	}
}
