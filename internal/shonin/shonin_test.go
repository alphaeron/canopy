package shonin

import (
	"errors"
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
)

// stubFetcher is a controllable Fetcher for exercising the full
// fetch -> sift -> approve workflow without a git repository.
type stubFetcher struct {
	root  string
	rules map[string]kitfile.Rules
	err   error
}

func (f *stubFetcher) GetRules() (string, map[string]kitfile.Rules, error) {
	return f.root, f.rules, f.err
}

// phaseSet returns the set of distinct phases present in the event trace.
func phaseSet(t *testing.T, rs *ReviewState) map[string]bool {
	t.Helper()
	phases := make(map[string]bool)
	for _, e := range rs.Events {
		phases[e.Phase] = true
	}
	return phases
}

// eventAt returns true when at least one event matches the given predicates
// (any empty string predicate is ignored).
func eventAt(rs *ReviewState, phase, path string, status Status) bool {
	for _, e := range rs.Events {
		if (phase == "" || e.Phase == phase) &&
			(path == "" || e.Path == path) &&
			(status == StatusUnknown || e.Status == status) {
			return true
		}
	}
	return false
}

func TestShoninExecuteTracesFetchSiftAndApprovePhases(t *testing.T) {
	fetch := &stubFetcher{
		root: "/repo",
		rules: map[string]kitfile.Rules{
			"KIT": {{Name: "root", Pattern: `.*\.go$`, CombineStrategy: kitfile.RuleCombineStrategyUnion,
				Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}}},
		},
	}
	s := &Shonin{Fetch: fetch, Log: testLogger()}
	rs := s.Execute("user9", approverSet("team-a"), func(string) bool { return true },
		[]string{"zowner"}, []string{"/src/api.go"})

	if rs.Err != nil {
		t.Fatalf("clean run should not produce an error, got %v", rs.Err)
	}
	if len(rs.Errors) != 0 {
		t.Errorf("clean run should not accumulate errors, got %v", rs.Errors)
	}
	phases := phaseSet(t, rs)
	for _, want := range []string{"fetch", "sift", "approve"} {
		if !phases[want] {
			t.Errorf("expected events for phase %q, got phases %v", want, phases)
		}
	}
	if !eventAt(rs, "fetch", "/repo", StatusDone) {
		t.Errorf("expected a completed fetch event for repo root /repo")
	}
	if !eventAt(rs, "sift", "/src/api.go", StatusDone) {
		t.Errorf("expected a sift event for file /src/api.go")
	}
	if !eventAt(rs, "approve", "/src/api.go", StatusSatisfied) {
		t.Errorf("expected a satisfied approve event for file /src/api.go")
	}
	if rs.Overall != StatusSatisfied {
		t.Errorf("overall status = %s, want satisfied", rs.Overall)
	}
	if rs.RepoRoot != "/repo" {
		t.Errorf("repo root = %q, want /repo", rs.RepoRoot)
	}
	if fs := rs.Files["/src/api.go"]; fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
}

func TestShoninExecuteFetchErrorFailsReview(t *testing.T) {
	fetchErr := errors.New("no KIT files in repository")
	s := &Shonin{Fetch: &stubFetcher{root: "/repo", err: fetchErr}, Log: testLogger()}
	rs := s.Execute("user9", approverSet(), func(string) bool { return true },
		[]string{"zowner"}, []string{"/src/api.go"})

	if rs.Overall != StatusError {
		t.Errorf("overall status = %s, want error", rs.Overall)
	}
	if rs.Err == nil {
		t.Fatalf("fetched fetch error must be surfaced")
	}
	if !errors.Is(rs.Err, fetchErr) {
		t.Errorf("review error %v does not wrap the fetcher error %v", rs.Err, fetchErr)
	}
	if len(rs.Errors) == 0 {
		t.Errorf("fetched fetch error must be accumulated in Errors, got none")
	}
	if !eventAt(rs, "fetch", "/repo", StatusError) {
		t.Errorf("expected a fetch phase error event")
	}
}

func TestShoninExecuteSiftErrorFailsReview(t *testing.T) {
	// A KIT file with an invalid regular expression is a sift-phase error:
	// the state must carry it, log it, and the review must be blocked.
	fetch := &stubFetcher{
		root: "/repo",
		rules: map[string]kitfile.Rules{
			"KIT": {{Name: "broken", Pattern: `(`, CombineStrategy: kitfile.RuleCombineStrategyUnion,
				Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}}},
		},
	}
	s := &Shonin{Fetch: fetch, Log: testLogger()}
	rs := s.Execute("user9", approverSet("team-a"), func(string) bool { return true },
		[]string{"zowner"}, []string{"/src/api.go"})

	if rs.Overall != StatusError {
		t.Errorf("overall status = %s, want error after sift failure", rs.Overall)
	}
	if rs.Err == nil {
		t.Fatalf("sift error must be surfaced")
	}
	if len(rs.Errors) == 0 {
		t.Errorf("sift error must be accumulated in Errors, got none: %v", rs.Errors)
	}
	if !eventAt(rs, "sift", "/src/api.go", StatusError) {
		t.Errorf("expected a sift phase error event for /src/api.go")
	}
	// The broken rule yields no layers -> repository owner fallback is
	// tried, so the approve phase must still be traced.
	if !eventAt(rs, "approve", "/src/api.go", StatusUnresolved) {
		t.Errorf("expected the approve phase to fall back after the sift failure")
	}
	if rs.RepoRoot != "/repo" {
		t.Errorf("repo root = %q, want /repo", rs.RepoRoot)
	}
}

func TestShoninExecuteUnresolvableTargetIsTracked(t *testing.T) {
	// A resolver that cannot resolve a target must surface the failure in
	// both the approver target state and the accumulated errors.
	fetch := &stubFetcher{
		root: "/repo",
		rules: map[string]kitfile.Rules{
			"KIT": {{Name: "root", Pattern: `.*\.go$`, CombineStrategy: kitfile.RuleCombineStrategyUnion,
				Approvers: []kitfile.ApproverTarget{{Target: "ghost-team", RequiredCount: 1}}}},
		},
	}
	resolver := failingResolver{}
	s := &Shonin{Fetch: fetch, Resolver: resolver, Log: testLogger()}
	rs := s.Execute("user9", approverSet("ghost-team"), func(string) bool { return true },
		[]string{"zowner"}, []string{"/src/api.go"})

	if len(rs.Errors) == 0 {
		t.Errorf("resolution failure must be accumulated in Errors, got none")
	}
	if rs.Overall != StatusError {
		t.Errorf("overall status = %s, want error after resolution failure", rs.Overall)
	}
	fs := rs.Files["/src/api.go"]
	if fs.Status != StatusUnresolved {
		t.Errorf("file status = %s, want unresolved", fs.Status)
	}
	if len(fs.Required) != 1 || fs.Required[0].Status != StatusUnresolved {
		t.Errorf("target %q must be marked unresolved, got %+v", "ghost-team", fs.Required)
	}
}

type failingResolver struct{}

func (failingResolver) Resolve(string, EvaluationContext) ([]string, bool) {
	return nil, false
}
