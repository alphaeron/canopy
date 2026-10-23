// Package platformtest is the contract every review platform under
// internal/platform must meet. A platform's tests build its review.Source,
// approval.Tree, and approval.ApproverResolver (and, for a hosted platform,
// its server.PullRequest) over the fixtures
// this package defines, and the suites check them against the documented
// behavior of those interfaces, so a new platform is verified to plug into
// the engine rather than assumed to.
package platformtest

import (
	"errors"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/review"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/server"
)

// Fixture is a repository state: the files at the base revision, and the
// change under review on top of it. Paths are repository-relative and
// slash-separated.
type Fixture struct {
	// Base maps each file at the base revision to its contents.
	Base map[string]string
	// Change is the change to the base revision.
	Change Change
}

// Change is a set of edits to a base revision. A path appears in at most
// one of its fields.
type Change struct {
	// Write maps each added or modified file to its new contents. A path
	// that is not in the base revision is added.
	Write map[string]string
	// Delete lists the base files the change removes.
	Delete []string
	// Rename maps each moved base file's old path to its new one. The
	// contents do not change.
	Rename map[string]string
}

// Revision is which side of a fixture a platform reads its rules and file
// universe (Source.AllFiles) from: a hosted review enforces the base
// revision's, a local review previews the working tree's, the head.
type Revision int

const (
	// BaseRevision is the fixture's base revision.
	BaseRevision Revision = iota
	// HeadRevision is the base revision with the change applied.
	HeadRevision
)

// files is the fixture's file paths at rev, sorted.
func (f Fixture) files(rev Revision) []string {
	paths := maps.Clone(f.Base)
	if rev == HeadRevision {
		for p, content := range f.Change.Write {
			paths[p] = content
		}
		for _, p := range f.Change.Delete {
			delete(paths, p)
		}
		for from, to := range f.Change.Rename {
			paths[to] = paths[from]
			delete(paths, from)
		}
	}
	return slices.Sorted(maps.Keys(paths))
}

// SourceFactory builds the platform's review.Source over f: f.Base as the
// base revision and f.Change as the change under review. It fails t when it
// cannot.
type SourceFactory func(t *testing.T, f Fixture) review.Source

// TreeFactory builds the platform's approval.Tree holding files. It fails t
// when it cannot.
type TreeFactory func(t *testing.T, files map[string]string) approval.Tree

// Rules files in the fixtures. The change never touches them: platforms
// deliberately differ in which revision they read rules from (a local
// review previews the working tree's, a hosted review enforces the base's),
// so the contract only covers rules both sides agree on.
const (
	rootRules = "- \".*\":\n  name: \"Root\"\n"
	svcRules  = "- \".*\\\\.go\":\n  name: \"Svc\"\n"
)

// fixture is the repository state the source contract runs over: one
// modified, one added, one deleted, and one renamed file, next to unchanged
// ones and two rules files.
func fixture() Fixture {
	return Fixture{
		Base: map[string]string{
			rulesfile.FileName:         rootRules,
			rulesfile.PathInDir("svc"): svcRules,
			"main.go":                  "package main\n",
			"svc/api.go":               "package svc\n\nfunc A() {}\n",
			"svc/old.go":               "package svc\n",
			"docs/guide.md":            "# Guide\n",
			"docs/old.md":              "# Old\n",
		},
		Change: Change{
			Write: map[string]string{
				"svc/api.go": "package svc\n\nfunc B() {}\n",
				"svc/new.go": "package svc\n\nvar New = 1\n",
			},
			Delete: []string{"docs/old.md"},
			Rename: map[string]string{"svc/old.go": "legacy/old.go"},
		},
	}
}

// RunSourceContract checks the review.Source that newSource builds against
// the Source contract: the files at rev, the revision the platform reads
// rules from; every changed path (both sides of a rename); per-file diffs;
// and a complete evaluator that reads the rules files.
func RunSourceContract(t *testing.T, rev Revision, newSource SourceFactory) {
	t.Helper()
	f := fixture()

	t.Run("AllFiles is the rules revision, sorted", func(t *testing.T) {
		got, err := newSource(t, f).AllFiles(t.Context())
		if err != nil {
			t.Fatalf("AllFiles: %v", err)
		}
		if want := f.files(rev); !slices.Equal(got, want) {
			t.Errorf("AllFiles = %v, want %v", got, want)
		}
	})

	t.Run("ChangedFiles lists every changed path, sorted", func(t *testing.T) {
		got, err := newSource(t, f).ChangedFiles(t.Context())
		if err != nil {
			t.Fatalf("ChangedFiles: %v", err)
		}
		want := []string{"docs/old.md", "legacy/old.go", "svc/api.go", "svc/new.go", "svc/old.go"}
		if !slices.Equal(got, want) {
			t.Errorf("ChangedFiles = %v, want %v (a rename lists both paths)", got, want)
		}
	})

	t.Run("ChangedFiles is empty without a change", func(t *testing.T) {
		got, err := newSource(t, Fixture{Base: f.Base}).ChangedFiles(t.Context())
		if err != nil {
			t.Fatalf("ChangedFiles: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("ChangedFiles = %v, want none", got)
		}
	})

	t.Run("DiffContent shows each change", func(t *testing.T) {
		src := newSource(t, f)
		tests := []struct {
			path string
			want []string // substrings of the diff; none means the diff is ""
		}{
			{path: "svc/api.go", want: []string{"-func A() {}", "+func B() {}"}},
			{path: "svc/new.go", want: []string{"+var New = 1"}},
			{path: "docs/old.md", want: []string{"-# Old"}},
			{path: "main.go"},
			{path: "absent.go"},
		}
		for _, tt := range tests {
			diff, err := src.DiffContent(t.Context(), tt.path)
			if err != nil {
				t.Errorf("DiffContent(%s): %v", tt.path, err)
				continue
			}
			if len(tt.want) == 0 && diff != "" {
				t.Errorf("DiffContent(%s) = %q, want empty for an unchanged path", tt.path, diff)
			}
			for _, w := range tt.want {
				if !strings.Contains(diff, w) {
					t.Errorf("DiffContent(%s) = %q, want it to contain %q", tt.path, diff, w)
				}
			}
		}
	})

	t.Run("Evaluator fetches the rules files", func(t *testing.T) {
		rev := newSource(t, f).Evaluator()
		if rev == nil || rev.Fetch == nil || rev.Resolver == nil {
			t.Fatalf("Evaluator = %+v, want a fetcher and a resolver", rev)
		}
		rules, err := rev.Fetch.Execute(t.Context())
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		want := []string{rulesfile.FileName, rulesfile.PathInDir("svc")}
		if got := rules.Paths(); !slices.Equal(got, want) {
			t.Fatalf("rules files = %v, want %v", got, want)
		}
		if got := rules[rulesfile.PathInDir("svc")]; len(got) != 1 || got[0].Name != "Svc" {
			t.Errorf("svc rules = %+v, want the parsed Svc rule", got)
		}
	})
}

// RunTreeContract checks the approval.Tree that newTree builds against the
// Tree contract: every file listed in sorted order, contents read back
// unchanged, and fs.ErrNotExist for a path outside the tree.
func RunTreeContract(t *testing.T, newTree TreeFactory) {
	t.Helper()
	files := map[string]string{
		rulesfile.FileName:                rootRules,
		"a/b.go":                          "package a\n",
		"a/" + rulesfile.FileName + ".md": "# not rules\n",
	}

	t.Run("Files lists every file, sorted", func(t *testing.T) {
		got, err := newTree(t, files).Files(t.Context())
		if err != nil {
			t.Fatalf("Files: %v", err)
		}
		if want := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
			t.Errorf("Files = %v, want %v", got, want)
		}
	})

	t.Run("ReadFile returns the contents", func(t *testing.T) {
		tree := newTree(t, files)
		for path, want := range files {
			got, err := tree.ReadFile(t.Context(), path)
			if err != nil {
				t.Errorf("ReadFile(%s): %v", path, err)
				continue
			}
			if string(got) != want {
				t.Errorf("ReadFile(%s) = %q, want %q", path, got, want)
			}
		}
	})

	t.Run("ReadFile of a missing path is fs.ErrNotExist", func(t *testing.T) {
		_, err := newTree(t, files).ReadFile(t.Context(), "missing.go")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(missing.go) err = %v, want fs.ErrNotExist", err)
		}
	})
}

// ResolverHarness is a platform's approval.ApproverResolver with the
// platform-specific targets the resolver contract needs, since target
// syntax differs by platform.
type ResolverHarness struct {
	// Resolver is the platform's resolver.
	Resolver approval.ApproverResolver
	// Group is a group target whose members are those passed to the
	// factory; "" when the platform has no groups.
	Group string
	// UnknownGroup is a well-formed group target the platform does not
	// have; "" when the platform has no groups.
	UnknownGroup string
	// Invalid is a target the platform rejects as malformed; "" when every
	// target is well-formed.
	Invalid string
	// Calls reports how many platform API calls the resolver has made; nil
	// when it makes none.
	Calls func() int
}

// ResolverFactory builds the platform's resolver harness over a group
// holding members. It fails t when it cannot.
type ResolverFactory func(t *testing.T, members []string) ResolverHarness

// RunResolverContract checks a platform's approval.ApproverResolver against
// the resolver contract: a user stands for itself, a group resolves to its
// members, an unknown group is approval.ErrUnknownTarget, a malformed target
// is rejected without an API call, and Canonical is idempotent.
func RunResolverContract(t *testing.T, newHarness ResolverFactory) {
	t.Helper()
	members := []string{"Ann", "bo"}

	t.Run("a user resolves to itself", func(t *testing.T) {
		h := newHarness(t, members)
		got, err := h.Resolver.Resolve(t.Context(), "Dana")
		if err != nil {
			t.Fatalf("Resolve(Dana): %v", err)
		}
		if len(got) != 1 || h.Resolver.Canonical(got[0]) != h.Resolver.Canonical("Dana") {
			t.Errorf("Resolve(Dana) = %v, want Dana alone", got)
		}
	})

	t.Run("Canonical is idempotent", func(t *testing.T) {
		r := newHarness(t, members).Resolver
		for _, name := range []string{"Dana", "dana", "DANA", "ann-b"} {
			if once := r.Canonical(name); r.Canonical(once) != once {
				t.Errorf("Canonical(Canonical(%q)) = %q, want %q", name, r.Canonical(once), once)
			}
		}
	})

	t.Run("a group resolves to its members", func(t *testing.T) {
		h := newHarness(t, members)
		if h.Group == "" {
			t.Skip("the platform has no groups")
		}
		got, err := h.Resolver.Resolve(t.Context(), h.Group)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", h.Group, err)
		}
		if !slices.Equal(canonicalSet(h.Resolver, got), canonicalSet(h.Resolver, members)) {
			t.Errorf("Resolve(%s) = %v, want %v", h.Group, got, members)
		}
	})

	t.Run("an unknown group is ErrUnknownTarget", func(t *testing.T) {
		h := newHarness(t, members)
		if h.UnknownGroup == "" {
			t.Skip("the platform has no groups")
		}
		if _, err := h.Resolver.Resolve(t.Context(), h.UnknownGroup); !errors.Is(err, approval.ErrUnknownTarget) {
			t.Errorf("Resolve(%s) err = %v, want approval.ErrUnknownTarget", h.UnknownGroup, err)
		}
	})

	t.Run("a malformed target is rejected offline", func(t *testing.T) {
		h := newHarness(t, members)
		if h.Invalid == "" {
			t.Skip("the platform accepts every target")
		}
		v, ok := h.Resolver.(approval.TargetValidator)
		if !ok {
			t.Fatal("a platform that rejects targets must implement approval.TargetValidator")
		}
		for _, valid := range []string{"Dana", h.Group} {
			if err := v.ValidateTarget(valid); valid != "" && err != nil {
				t.Errorf("ValidateTarget(%q) = %v, want it valid", valid, err)
			}
		}
		if err := v.ValidateTarget(h.Invalid); !errors.Is(err, approval.ErrInvalidTarget) {
			t.Errorf("ValidateTarget(%q) = %v, want approval.ErrInvalidTarget", h.Invalid, err)
		}
		before := calls(h)
		_, err := h.Resolver.Resolve(t.Context(), h.Invalid)
		if !errors.Is(err, approval.ErrUnknownTarget) || !errors.Is(err, approval.ErrInvalidTarget) {
			t.Errorf("Resolve(%q) err = %v, want approval.ErrUnknownTarget and approval.ErrInvalidTarget", h.Invalid, err)
		}
		if calls(h) != before {
			t.Errorf("Resolve(%q) called the platform API", h.Invalid)
		}
	})
}

// canonicalSet is names in canonical form, sorted and deduplicated.
func canonicalSet(r approval.ApproverResolver, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, r.Canonical(n))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// calls is the harness's API call count, or 0 when it makes none.
func calls(h ResolverHarness) int {
	if h.Calls == nil {
		return 0
	}
	return h.Calls()
}

// Decision is a reviewer's decision in a review scenario.
type Decision string

// The decisions a reviewer can make.
const (
	Approve        Decision = "approve"
	RequestChanges Decision = "request_changes"
	Dismiss        Decision = "dismiss" // dismisses the reviewer's earlier approval
	Comment        Decision = "comment"
)

// Review is one review in a scenario: who reviewed and what they decided.
type Review struct {
	Reviewer string
	Decision Decision
}

// Reviews is a change's review history: its author and its reviews, in the
// order they were made.
type Reviews struct {
	Author  string
	Reviews []Review
}

// ReviewInputFactory builds the platform's server.PullRequest over a pull
// request whose author and review history are r. It fails t when it cannot.
type ReviewInputFactory func(t *testing.T, r Reviews) server.PullRequest

// RunReviewInputContract checks a hosted platform's
// PullRequest.ReviewInput: the author is reported; each reviewer's latest
// decision counts, so a later change request or dismissal cancels an
// approval and a comment changes nothing; approvals are sorted and
// deduplicated; and the author's own approval is left to self_approve
// rules.
func RunReviewInputContract(t *testing.T, newReader ReviewInputFactory) {
	t.Helper()
	scenario := Reviews{
		Author: "Author",
		Reviews: []Review{
			{"ann", Approve},
			{"bo", Approve},
			{"ann", Approve},
			{"cy", Approve},
			{"cy", RequestChanges},
			{"dee", Approve},
			{"dee", Dismiss},
			{"eve", Comment},
			{"fay", Approve},
			{"fay", Comment},
			{"gil", RequestChanges},
			{"gil", Approve},
			{"Author", Approve},
		},
	}

	t.Run("the author is reported", func(t *testing.T) {
		in, err := newReader(t, scenario).ReviewInput(t.Context())
		if err != nil {
			t.Fatalf("ReviewInput: %v", err)
		}
		if !strings.EqualFold(in.Author, "Author") {
			t.Errorf("Author = %q, want Author", in.Author)
		}
	})

	t.Run("approvals are each reviewer's latest decision", func(t *testing.T) {
		in, err := newReader(t, scenario).ReviewInput(t.Context())
		if err != nil {
			t.Fatalf("ReviewInput: %v", err)
		}
		got := make([]string, len(in.Approvers))
		for i, a := range in.Approvers {
			got[i] = strings.ToLower(a)
		}
		if want := []string{"ann", "bo", "fay", "gil"}; !slices.Equal(got, want) {
			t.Errorf("Approvers = %v, want %v (sorted, deduplicated; change requests and dismissals cancel, comments don't, the author's own approval is left out)", in.Approvers, want)
		}
	})

	t.Run("no reviews, no approvals", func(t *testing.T) {
		in, err := newReader(t, Reviews{Author: "Author"}).ReviewInput(t.Context())
		if err != nil {
			t.Fatalf("ReviewInput: %v", err)
		}
		if len(in.Approvers) != 0 {
			t.Errorf("Approvers = %v, want none", in.Approvers)
		}
	})
}

// StatusReport is one status a platform recorded: the name it is reported
// under (later reports under the same name replace earlier ones), the
// platform's state, the summary as published, and the page it links to.
type StatusReport struct {
	Name    string
	State   string
	Summary string
	Link    string
}

// StatusReporterFactory builds the platform's server.PullRequest, and a
// function returning every status the platform's fake has recorded, in
// order. It fails t when it cannot.
type StatusReporterFactory func(t *testing.T) (server.PullRequest, func() []StatusReport)

// RunStatusReporterContract checks a hosted platform's
// PullRequest.Report: each Report publishes exactly one status; a passed,
// a pending, and a failed review are each in a distinct platform state, and
// an errored one is in a state other than passed or pending (a platform
// may report it as failed, told apart by its summary); a later report
// replaces the earlier one; a start is a running status, in a state of its
// own, that a report replaces, and both link the review page; and an
// oversized summary is cut rather than rejected.
func RunStatusReporterContract(t *testing.T, newReporter StatusReporterFactory) {
	t.Helper()

	t.Run("each verdict is one report, and only a pass passes", func(t *testing.T) {
		r, observe := newReporter(t)
		verdicts := []approval.Verdict{approval.Passed, approval.Pending, approval.Failed, approval.Errored}
		for _, v := range verdicts {
			if err := r.Report(t.Context(), approval.Outcome{Verdict: v, Summary: string(v)}, ""); err != nil {
				t.Fatalf("Report(%s): %v", v, err)
			}
		}
		got := observe()
		if len(got) != len(verdicts) {
			t.Fatalf("reports = %d, want one per Report call (%d)", len(got), len(verdicts))
		}
		for i, s := range got {
			if s.Summary != string(verdicts[i]) {
				t.Errorf("report %d summary = %q, want %q", i, s.Summary, verdicts[i])
			}
		}
		passed, pending, failed, errored := got[0].State, got[1].State, got[2].State, got[3].State
		if passed == pending || passed == failed || pending == failed {
			t.Errorf("states = %v, want passed, pending, and failed distinct", got)
		}
		if errored == passed || errored == pending {
			t.Errorf("states = %v, want errored neither passed nor pending", got)
		}
	})

	t.Run("a later report replaces the earlier one", func(t *testing.T) {
		r, observe := newReporter(t)
		for _, v := range []approval.Verdict{approval.Pending, approval.Passed} {
			if err := r.Report(t.Context(), approval.Outcome{Verdict: v, Summary: "s"}, ""); err != nil {
				t.Fatalf("Report(%s): %v", v, err)
			}
		}
		got := observe()
		if len(got) != 2 || got[0].Name == "" || got[0].Name != got[1].Name {
			t.Errorf("reports = %+v, want two under the same non-empty name", got)
		}
	})

	t.Run("a start runs until a report replaces it, both linking the review", func(t *testing.T) {
		r, observe := newReporter(t)
		const link = "https://canopy.example.com/reviews/o/r/1/abc?sig=x"
		if err := r.Start(t.Context(), link); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err := r.Report(t.Context(), approval.Outcome{Verdict: approval.Passed, Summary: "s"}, link); err != nil {
			t.Fatalf("Report: %v", err)
		}
		got := observe()
		if len(got) != 2 || got[0].Name == "" || got[0].Name != got[1].Name || got[0].State == got[1].State {
			t.Fatalf("statuses = %+v, want a start and a report under the same name, in different states", got)
		}
		for i, s := range got {
			if s.Link != link {
				t.Errorf("status %d links %q, want %q", i, s.Link, link)
			}
		}
	})

	t.Run("an oversized summary is cut, not rejected", func(t *testing.T) {
		r, observe := newReporter(t)
		long := strings.Repeat("ü", 100_000)
		if err := r.Report(t.Context(), approval.Outcome{Verdict: approval.Failed, Summary: long}, ""); err != nil {
			t.Fatalf("Report: %v", err)
		}
		got := observe()
		if len(got) != 1 || got[0].Summary == "" || len(got[0].Summary) >= len(long) {
			t.Errorf("reports = %d, want one with a cut, non-empty summary", len(got))
		}
	})
}
