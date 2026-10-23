package approval

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// stubFetcher is a controllable Fetcher for exercising the full
// fetch -> sift -> approve workflow without a git repository.
type stubFetcher struct {
	rules map[string]rulesfile.Rules
	err   error
}

func (f *stubFetcher) Execute(context.Context) (rulesfile.Set, error) {
	return f.rules, f.err
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

func TestReviewerExecuteTracesFetchSiftAndApprovePhases(t *testing.T) {
	fetch := &stubFetcher{
		rules: map[string]rulesfile.Rules{
			rulesfile.FileName: {{Name: "root", Pattern: `.*\.go$`, CombineStrategy: rulesfile.RuleCombineStrategyUnion,
				Approvers: []rulesfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}}},
		},
	}
	s := &Evaluator{Fetch: fetch, Resolver: &DefaultApproverResolver{}, Log: testLogger()}
	rs := s.Execute(t.Context(), buildContext(approverSet("team-a"), func(string) bool { return true }, []string{"zowner"}), []string{"src/api.go"})

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
	if !eventAt(rs, "fetch", "", StatusDone) {
		t.Errorf("expected a completed fetch event")
	}
	if !eventAt(rs, "sift", "src/api.go", StatusDone) {
		t.Errorf("expected a sift event for file /src/api.go")
	}
	if !eventAt(rs, "approve", "src/api.go", StatusSatisfied) {
		t.Errorf("expected a satisfied approve event for file /src/api.go")
	}
	if rs.Overall != StatusSatisfied {
		t.Errorf("overall status = %s, want satisfied", rs.Overall)
	}
	if fs := rs.Files["src/api.go"]; fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
}

func TestReviewerExecuteFetchErrorFailsReview(t *testing.T) {
	fetchErr := errors.New("no rules files in repository")
	s := &Evaluator{Fetch: &stubFetcher{err: fetchErr}, Resolver: &DefaultApproverResolver{}, Log: testLogger()}
	rs := s.Execute(t.Context(), buildContext(approverSet(), func(string) bool { return true }, []string{"zowner"}), []string{"src/api.go"})

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
	if !eventAt(rs, "fetch", "", StatusError) {
		t.Errorf("expected a fetch phase error event")
	}
}

func TestReviewerExecuteSiftErrorFailsReview(t *testing.T) {
	// A rules file with an invalid regular expression is a sift-phase error:
	// the state must carry it, log it, and the review must be blocked.
	fetch := &stubFetcher{
		rules: map[string]rulesfile.Rules{
			rulesfile.FileName: {{Name: "broken", Pattern: `(`, CombineStrategy: rulesfile.RuleCombineStrategyUnion,
				Approvers: []rulesfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}}},
		},
	}
	s := &Evaluator{Fetch: fetch, Resolver: &DefaultApproverResolver{}, Log: testLogger()}
	rs := s.Execute(t.Context(), buildContext(approverSet("team-a"), func(string) bool { return true }, []string{"zowner"}), []string{"src/api.go"})

	if rs.Overall != StatusError {
		t.Errorf("overall status = %s, want error after sift failure", rs.Overall)
	}
	if rs.Err == nil {
		t.Fatalf("sift error must be surfaced")
	}
	if len(rs.Errors) == 0 {
		t.Errorf("sift error must be accumulated in Errors, got none: %v", rs.Errors)
	}
	if !eventAt(rs, "sift", "src/api.go", StatusError) {
		t.Errorf("expected a sift phase error event for /src/api.go")
	}
	// The broken rule yields no layers -> repository owner fallback is
	// tried, so the approve phase must still be traced.
	if !eventAt(rs, "approve", "src/api.go", StatusUnresolved) {
		t.Errorf("expected the approve phase to fall back after the sift failure")
	}
}

func TestReviewerExecuteUnresolvableTargetIsTracked(t *testing.T) {
	// A resolver that cannot resolve a target must surface the failure in
	// both the approver target state and the accumulated errors.
	fetch := &stubFetcher{
		rules: map[string]rulesfile.Rules{
			rulesfile.FileName: {{Name: "root", Pattern: `.*\.go$`, CombineStrategy: rulesfile.RuleCombineStrategyUnion,
				Approvers: []rulesfile.ApproverTarget{{Target: "ghost-team", RequiredCount: 1}}}},
		},
	}
	resolver := failingResolver{}
	s := &Evaluator{Fetch: fetch, Resolver: resolver, Log: testLogger()}
	rs := s.Execute(t.Context(), buildContext(approverSet("ghost-team"), func(string) bool { return true }, []string{"zowner"}), []string{"src/api.go"})

	if len(rs.Errors) == 0 {
		t.Errorf("resolution failure must be accumulated in Errors, got none")
	}
	if rs.Overall != StatusError {
		t.Errorf("overall status = %s, want error after resolution failure", rs.Overall)
	}
	fs := rs.Files["src/api.go"]
	if fs.Status != StatusUnresolved {
		t.Errorf("file status = %s, want unresolved", fs.Status)
	}
	if len(fs.Required) != 1 || fs.Required[0].Status != StatusUnresolved {
		t.Errorf("target %q must be marked unresolved, got %+v", "ghost-team", fs.Required)
	}
}

type failingResolver struct{}

func (failingResolver) Resolve(_ context.Context, target string) ([]string, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnknownTarget, target)
}

func (failingResolver) Canonical(name string) string { return name }

// TestBrokenRulesFileErrorsButReviewContinues: an unusable rules file
// errors the review, yet the review still runs over the rules that loaded,
// so it also reports every other error (here an unresolvable target), and
// each file still has its state.
func TestBrokenRulesFileErrorsButReviewContinues(t *testing.T) {
	broken := errors.New("parse rules file svc/KIT: bad yaml")
	ev := &Evaluator{
		Fetch: &stubFetcher{
			rules: rulesfile.Set{rulesfile.FileName: {{Pattern: ".*", Name: "root", Approvers: []rulesfile.ApproverTarget{{Target: "org/down", RequiredCount: 1}}}}},
			err:   broken,
		},
		Resolver: &fakeResolver{failing: map[string]error{"org/down": errors.New("platform down")}},
		Log:      testLogger(),
	}
	state := ev.Execute(t.Context(), ReviewInput{}, []string{"a.go"})

	if state.Overall != StatusError {
		t.Errorf("overall = %s, want %s", state.Overall, StatusError)
	}
	if !errors.Is(state.Err, broken) {
		t.Errorf("Err = %v, want it to wrap the broken rules file's error", state.Err)
	}
	if len(state.Errors) < 2 {
		t.Errorf("errors = %q, want the broken rules file and the unresolvable target", state.Errors)
	}
	if _, ok := state.Files["a.go"]; !ok {
		t.Error("a.go has no state: the review stopped at the broken rules file")
	}
}

// TestFetchThatLoadsNothingStopsTheReview: a fetch with no rules at all
// (a nil set) stops the review with its error.
func TestFetchThatLoadsNothingStopsTheReview(t *testing.T) {
	listing := errors.New("list files: boom")
	ev := &Evaluator{Fetch: &stubFetcher{err: listing}, Resolver: &fakeResolver{}, Log: testLogger()}
	state := ev.Execute(t.Context(), ReviewInput{}, []string{"a.go"})
	if state.Overall != StatusError || !errors.Is(state.Err, listing) || len(state.Files) != 0 {
		t.Errorf("state = %s, %v, %d file(s); want an error with no files", state.Overall, state.Err, len(state.Files))
	}
}
