package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/brand"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

// stubSource is an in-memory Source for testing the Engine without touching
// the filesystem or git. Rules map mirrors approval.Fetcher output: rules
// file path to parsed rule list.
type stubSource struct {
	root       string
	rules      map[string]rulesfile.Rules
	err        error
	allFiles   []string
	changedErr error
	changedSet []string
	diffs      map[string]string
	diffCalls  []string
	// resolver, when set, is the platform's resolver; the default
	// pass-through one otherwise.
	resolver approval.ApproverResolver
}

func (s *stubSource) RepoRoot() string { return s.root }

// Evaluator is a local-style platform over the stub's rules.
func (s *stubSource) Evaluator() *approval.Evaluator {
	var resolver approval.ApproverResolver = &approval.DefaultApproverResolver{}
	if s.resolver != nil {
		resolver = s.resolver
	}
	return &approval.Evaluator{
		Fetch:    &stubFetch{rules: s.rules, err: s.err},
		Resolver: resolver,
		Log:      slog.New(slog.DiscardHandler),
	}
}

func (s *stubSource) AllFiles(context.Context) ([]string, error) { return s.allFiles, nil }

// ReadHead has no proposed files; stubChange supplies them.
func (s *stubSource) ReadHead(_ context.Context, path string) ([]byte, error) {
	return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
}

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

var (
	_ Source = (*stubSource)(nil)
	_ Rooted = (*stubSource)(nil)
)

// stubFetch is a Fetcher over a fixed rule map.
type stubFetch struct {
	rules map[string]rulesfile.Rules
	err   error
}

func (s *stubFetch) Execute(context.Context) (rulesfile.Set, error) {
	return s.rules, s.err
}

// shared fixture rules: a root rules file rule matching .go and a nested /src
// rules file with a rule overriding the parent for .go files. The root rule is
// a pass-through, the nested one restricts to current-directory-only so the
// chain is unambiguous per file.
var fixtureRules = map[string]rulesfile.Rules{
	rulesfile.FileName: {
		{Pattern: `.*\.go$`, Name: "top-level go rules", Approvers: []rulesfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}},
	},
	rulesfile.PathInDir("src"): {
		{Pattern: `.*\.go$`, Name: "src go rules", CurrentDirectoryOnly: true, IgnoreParentDirs: true, Approvers: []rulesfile.ApproverTarget{{Target: "team-b", RequiredCount: 1}}},
	},
}

func TestMatchReturnsAllFilesInUniverse(t *testing.T) {
	src := &stubSource{
		root:     "/repo",
		rules:    fixtureRules,
		allFiles: []string{"main.go", "src/api.go", "docs/readme.md"},
	}
	e := &Engine{Source: src}
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
			if len(fm.Rules) != 1 || fm.Rules[0].Name != "top-level go rules" {
				t.Errorf("main.go scopes = %+v, want one 'top-level go rules'", fm.Rules)
			}
		case "src/api.go":
			if len(fm.Rules) != 1 || fm.Rules[0].Name != "src go rules" {
				t.Errorf("src/api.go scopes = %+v, want one 'src go rules' (parent rule broken by IgnoreParentDirs)", fm.Rules)
			}
		case "docs/readme.md":
			if len(fm.Rules) != 0 {
				t.Errorf("docs/readme.md scopes = %+v, want none (no rule matches)", fm.Rules)
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
	e := &Engine{Source: src}
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
			res, err := (&Engine{Source: src}).Match(t.Context(), Options{ScopeName: tc.scope, ChangedOnly: tc.changed != nil})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Match error = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			got := []string{}
			for _, fm := range res.Files {
				got = append(got, fm.Path)
				if len(fm.Rules) != 1 || fm.Rules[0].Name != tc.scope {
					t.Errorf("%s scopes = %+v, want only %q", fm.Path, fm.Rules, tc.scope)
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
	e := &Engine{Source: src}
	full, _, err := e.Coverage(t.Context(), Options{})
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	got, _, err := e.Coverage(t.Context(), Options{ScopeName: "src go rules"})
	if err != nil {
		t.Fatalf("Coverage scoped: %v", err)
	}
	if len(got.PerRule) != 1 || got.PerRule[0].Name != "src go rules" {
		t.Errorf("breakdown = %+v, want only the requested scope", got.PerRule)
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
	res, err := (&Engine{Source: src}).Match(t.Context(), Options{ScopeName: "src go rules", IncludeDiff: true})
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
	e := &Engine{Source: src}
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
	e := &Engine{Source: src}
	if _, err := e.Match(t.Context(), Options{}); err != errRules {
		t.Errorf("Match must surface the rule-loading error, got %v", err)
	}
}

func TestCoverageUnmatchedFilesAndUnmatchedSet(t *testing.T) {
	src := &stubSource{
		rules:    fixtureRules,
		allFiles: []string{"main.go", "src/api.go", "docs/readme.md"},
	}
	e := &Engine{Source: src}
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
	if len(report.PerRule) != 2 {
		t.Errorf("Coverage: PerRule length=%d, want 2 (one per distinct scope)", len(report.PerRule))
	}
}

func TestCoverageChangedOnly(t *testing.T) {
	src := &stubSource{
		rules:      fixtureRules,
		allFiles:   []string{"a.go", "b.go"},
		changedSet: []string{"a.go"},
	}
	e := &Engine{Source: src}
	report, _, err := e.Coverage(t.Context(), Options{ChangedOnly: true})
	if err != nil {
		t.Fatalf("Coverage changed-only: %v", err)
	}
	if report.Universe != 1 {
		t.Errorf("Coverage changed-only universe=%d, want 1", report.Universe)
	}
}

var errRules = err(brand.Command + " file parsing failed")

type err string

func (e err) Error() string { return string(e) }

func TestFilesNarrowsByPatterns(t *testing.T) {
	src := &stubSource{allFiles: []string{"docs/readme.md", "main.go", "src/api.go"}}
	e := &Engine{Source: src}
	got, err := e.Files(t.Context(), Options{Patterns: []string{`\.md$`, `^main`}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docs/readme.md", "main.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(src.allFiles, []string{"docs/readme.md", "main.go", "src/api.go"}) {
		t.Errorf("Files must not modify the source's slice: %v", src.allFiles)
	}
	if _, err := e.Files(t.Context(), Options{Patterns: []string{"("}}); err == nil {
		t.Error("an invalid pattern must be an error")
	}
}

// TestReviewEvaluatesFiles checks that Review runs the full review over the
// files given: only those files are in the state, and a file whose rule an
// approver satisfies is satisfied.
func TestReviewEvaluatesFiles(t *testing.T) {
	e := &Engine{Source: &stubSource{rules: fixtureRules}, Log: slog.New(slog.DiscardHandler)}
	state := e.Review(t.Context(), []string{"main.go"}, approval.ReviewInput{Approvers: []string{"team-a"}})
	if len(state.Files) != 1 || state.Files["main.go"].Status != approval.StatusSatisfied || state.Overall != approval.StatusSatisfied {
		t.Errorf("Review = %+v, want main.go alone, satisfied", state)
	}
}

func TestRequirementsReportsRequiredTargetsAndFallback(t *testing.T) {
	src := &stubSource{rules: fixtureRules}
	e := &Engine{Source: src, Log: slog.New(slog.DiscardHandler)}
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
	report, match, err := (&Engine{Source: src}).Coverage(t.Context(), Options{ScopeName: "src go rules"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.PerRule) != 1 || report.Covered != 2 {
		t.Errorf("report = %+v, want one scope in the breakdown and 2 covered files", report)
	}
	if len(match.Files) != 3 {
		t.Errorf("match has %d files, want the whole universe of 3", len(match.Files))
	}
}

// unrootedSource hides stubSource's RepoRoot, standing in for a source on a
// hosted platform.
type unrootedSource struct{ Source }

// TestRepoRootOnlyFromRootedSources: the match result and the review state
// carry the repository root of a Rooted source, and none for any other.
func TestRepoRootOnlyFromRootedSources(t *testing.T) {
	stub := &stubSource{root: "/repo", rules: fixtureRules, allFiles: []string{"main.go"}}
	tests := []struct {
		name string
		src  Source
		want string
	}{
		{name: "rooted", src: stub, want: "/repo"},
		{name: "unrooted", src: unrootedSource{stub}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Engine{Source: tt.src}
			res, err := e.Match(t.Context(), Options{})
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			if res.RepoRoot != tt.want {
				t.Errorf("Match RepoRoot = %q, want %q", res.RepoRoot, tt.want)
			}
			if got := e.Review(t.Context(), []string{"main.go"}, approval.ReviewInput{}).RepoRoot; got != tt.want {
				t.Errorf("Review RepoRoot = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExactPattern pins that drilling from a file into `rules get` targets
// that file only, even when the path contains regex metacharacters.
func TestExactPattern(t *testing.T) {
	re := regexp.MustCompile(ExactPattern("pkg/a.go"))
	for path, want := range map[string]bool{
		"pkg/a.go":     true,
		"pkg/aXgo":     false,
		"pkg/a.go.bak": false,
		"x/pkg/a.go":   false,
	} {
		if got := re.MatchString(path); got != want {
			t.Errorf("match %q = %v, want %v", path, got, want)
		}
	}
	several := regexp.MustCompile(ExactPattern("a.go", "b+.go"))
	for path, want := range map[string]bool{"a.go": true, "b+.go": true, "bb.go": false, "a.go/x": false} {
		if got := several.MatchString(path); got != want {
			t.Errorf("match %q of several = %v, want %v", path, got, want)
		}
	}
}

// TestValidateProposed pins the check of the rules files a change proposes,
// shared by the server and canopy review: each is read as proposed, parse
// errors and rule violations are reported, and files that are gone or not
// rules files are skipped.
func TestValidateProposed(t *testing.T) {
	pr := newStubChange()
	pr.head = map[string]string{
		"ok/RULES":      "- \".*\":\n  name: Ok\n  approvers:\n    - target: lead\n      required_count: 1\n",
		"broken/RULES":  "- [not: rules\n",
		"badre/RULES":   "- \"(\":\n  name: Bad\n",
		"nocount/RULES": "- \".*\":\n  name: NoCount\n  approvers:\n    - target: org/team\n",
	}
	// deleted/RULES is pending but absent at the head: deleted, so skipped.
	// main.go is not a rules file, so it is not validated.
	pending := []string{"ok/RULES", "broken/RULES", "badre/RULES", "nocount/RULES", "deleted/RULES", "main.go"}

	got, err := (&Engine{Source: pr}).ValidateProposed(t.Context(), pending)
	if err != nil {
		t.Fatalf("ValidateProposed: %v", err)
	}
	for _, p := range []string{"broken/RULES", "badre/RULES", "nocount/RULES"} {
		if len(got[p]) == 0 {
			t.Errorf("%s: no violations, want some", p)
		}
	}
	for _, p := range []string{"ok/RULES", "deleted/RULES", "main.go"} {
		if len(got[p]) != 0 {
			t.Errorf("%s: violations %v, want none", p, got[p])
		}
	}

	tooLarge := fmt.Errorf("read big/RULES: %w", rulesfile.ErrTooLarge)
	pr.headErr = tooLarge
	if got, err := (&Engine{Source: pr}).ValidateProposed(t.Context(), []string{"big/RULES"}); err != nil || !errors.Is(got["big/RULES"][0], rulesfile.ErrTooLarge) {
		t.Errorf("ValidateProposed with an oversized file = %v, %v; want it as a violation", got, err)
	}

	unreadable := errors.New("unreadable")
	pr.headErr = unreadable
	if _, err := (&Engine{Source: pr}).ValidateProposed(t.Context(), pending[:1]); !errors.Is(err, unreadable) {
		t.Errorf("ValidateProposed with an unreadable file err = %v, want the read error", err)
	}
}

// strictResolver is a platform resolver that rejects targets with "!", as
// GitHub rejects what is not a login or a team.
type strictResolver struct {
	*approval.DefaultApproverResolver
}

func (strictResolver) ValidateTarget(target string) error {
	if strings.Contains(target, "!") {
		return fmt.Errorf("%q: %w", target, approval.ErrInvalidTarget)
	}
	return nil
}

// TestValidateProposedChecksTargetSyntax pins that the platform's target
// syntax is part of validating a proposed rules file.
func TestValidateProposedChecksTargetSyntax(t *testing.T) {
	pr := newStubChange()
	pr.resolver = strictResolver{&approval.DefaultApproverResolver{}}
	pr.head = map[string]string{
		"svc/RULES": "- \".*\":\n  approvers:\n    - target: bad!name\n      required_count: 1\n    - target: lead\n      required_count: 1\n",
	}
	got, err := (&Engine{Source: pr}).ValidateProposed(t.Context(), []string{"svc/RULES"})
	if err != nil {
		t.Fatalf("ValidateProposed: %v", err)
	}
	if errs := got["svc/RULES"]; len(errs) != 1 || !errors.Is(errs[0], approval.ErrInvalidTarget) {
		t.Errorf("violations = %v, want the one invalid target", errs)
	}
}

// TestMatchAndCoverageListsAreNeverNull: an empty universe, a file no rule
// matches, and a report with nothing uncovered all encode as [].
func TestMatchAndCoverageListsAreNeverNull(t *testing.T) {
	e := &Engine{Source: &stubSource{rules: fixtureRules, allFiles: []string{"x.unmatched"}}}
	res, err := e.Match(t.Context(), Options{})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if data, _ := json.Marshal(res); !strings.Contains(string(data), `"rules":[]`) {
		t.Errorf("match json = %s, want an uncovered file's rules as []", data)
	}
	empty := &Engine{Source: &stubSource{rules: fixtureRules, changedSet: []string{}}}
	res, err = empty.Match(t.Context(), Options{ChangedOnly: true})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	report, _, err := empty.Coverage(t.Context(), Options{ChangedOnly: true})
	if err != nil {
		t.Fatalf("Coverage: %v", err)
	}
	for name, v := range map[string]any{"match": res, "coverage": report} {
		if data, _ := json.Marshal(v); strings.Contains(string(data), "null") {
			t.Errorf("%s json = %s, want no null lists", name, data)
		}
	}
}
