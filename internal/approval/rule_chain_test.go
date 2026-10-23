package approval

import (
	"context"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// --- Test fixtures / builders -------------------------------------------
//
// These small helpers centralise the values that were previously duplicated
// across every test (author, the active-owners predicate, the repository
// owners, and the boilerplate rule/layer/chain construction). Each test now
// only describes what makes it unique.

const testAuthor = "user9"

func approver(target string, count int) rulesfile.ApproverTarget {
	return rulesfile.ApproverTarget{Target: target, RequiredCount: count}
}

// buildContext assembles the shared PullRequest. Tests pass only the
// values that differ from the typical case (current approvers, owner
// activeness, repository owners).
func buildContext(approvers map[string]bool, isActive func(string) bool, repoOwners []string) ReviewInput {
	return ReviewInput{
		Author:    testAuthor,
		Approvers: slices.Collect(maps.Keys(approvers)),
		IsActive:  isActive,
		Owners:    repoOwners,
	}
}

func buildLayer(dir, name string, strategy rulesfile.RuleCombineStrategy, targets ...rulesfile.ApproverTarget) RuleLayer {
	return RuleLayer{
		DirectoryPath: dir,
		Rules:         []ExecutableRule{{Rule: rulesfile.Rule{Name: name, CombineStrategy: strategy, Approvers: targets}}},
	}
}

func buildChain(file string, layers ...RuleLayer) RuleChain {
	return RuleChain{FileChains: map[string][]RuleLayer{file: layers}}
}

func approverSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// identity is the canonical form of a platform that compares names exactly.
func identity(name string) string { return name }

func activeAllOwners() func(string) bool {
	return func(string) bool { return true }
}

// testLogger returns a logger that discards all output so tests stay quiet.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// executeChain runs the chain with a fresh event builder and a quiet logger,
// mirroring how Evaluator.Execute wires the approve phase.
func executeChain(t *testing.T, rc RuleChain, pr ReviewInput) *ReviewState {
	t.Helper()
	return rc.Execute(t.Context(), pr, &DefaultApproverResolver{}, NewReviewStateBuilder(), testLogger())
}

func inactiveAllOwners() func(string) bool {
	return func(string) bool { return false }
}

func TestUnionRecurringTargetCountsPerOccurrence(t *testing.T) {
	// Root requires 1 approval from team-a; /src (union) requires 2 from the
	// same team-a. The default resolver maps target -> [target], so the
	// target name is counted as an approver only if it appears in
	// approvers. With one matching approval, root is satisfied and
	// /src still needs one more. Each target occurrence is counted once
	// per occurrence.
	pr := buildContext(approverSet("team-a"), activeAllOwners(), []string{"zowner"})
	rc := buildChain(
		"src/file.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-a", 1)),
		buildLayer("src", "src", rulesfile.RuleCombineStrategyUnion, approver("team-a", 2)),
	)
	state := executeChain(t, rc, pr)

	fs, ok := state.Files["src/file.go"]
	if !ok {
		t.Fatalf("missing file state for /src/file.go")
	}
	if len(fs.Required) != 2 {
		t.Fatalf("expected 2 approver target occurrences (one per layer), got %d: %+v", len(fs.Required), fs.Required)
	}
	// Per-occurrence evaluation against the same approver pool:
	// occurrence 1 (root, requires 1) is satisfied by the one matching approver.
	if fs.Required[0].Status != StatusSatisfied || fs.Required[0].SatisfiedCount != 1 {
		t.Errorf("root occurrence should be satisfied 1/1, got status=%s count=%d",
			fs.Required[0].Status, fs.Required[0].SatisfiedCount)
	}
	// occurrence 2 (/src, requires 2) still needs one more.
	if fs.Required[1].Status == StatusSatisfied || fs.Required[1].SatisfiedCount != 1 || fs.Required[1].RequiredCount != 2 {
		t.Errorf("src occurrence should be unsatisfied 1/2, got status=%s count=%d required=%d",
			fs.Required[1].Status, fs.Required[1].SatisfiedCount, fs.Required[1].RequiredCount)
	}
	if fs.Status != StatusMissingApprovers {
		t.Errorf("file status = %s, want missing_approvers", fs.Status)
	}
	if state.Overall != StatusMissingApprovers {
		t.Errorf("overall status = %s, want missing_approvers", state.Overall)
	}
	if state.Err != nil {
		t.Errorf("no error expected, got %v", state.Err)
	}
}

func TestUnionPreservesBothOccurrences(t *testing.T) {
	// Union must not dedupe by target name: the same target in two layers
	// yields two required entries with their respective counts.
	pr := buildContext(approverSet("team-a", "team-b"), activeAllOwners(), []string{"zowner"})
	rc := buildChain(
		"src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-a", 1)),
		buildLayer("src", "src", rulesfile.RuleCombineStrategyUnion, approver("team-a", 1), approver("team-b", 1)),
	)
	state := executeChain(t, rc, pr)
	fs := state.Files["src/x.go"]
	if len(fs.Required) != 3 {
		t.Fatalf("expected 3 target occurrences after union, got %d: %+v", len(fs.Required), fs.Required)
	}
	want := map[string]int{"team-a": 2, "team-b": 1}
	got := map[string]int{}
	for _, ats := range fs.Required {
		got[ats.Target]++
	}
	for target, count := range want {
		if got[target] != count {
			t.Errorf("target %s occurrence count = %d, want %d", target, got[target], count)
		}
	}
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
}

func TestIntersectKeepsSharedTargetOnly(t *testing.T) {
	// README: root requires A+B, /src (intersect) requires B+C -> only B.
	pr := buildContext(approverSet("b"), activeAllOwners(), []string{"zowner"})
	rc := buildChain(
		"src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("a", 1), approver("b", 1)),
		buildLayer("src", "src", rulesfile.RuleCombineStrategyIntersect, approver("b", 1), approver("c", 1)),
	)
	state := executeChain(t, rc, pr)
	fs := state.Files["src/x.go"]
	if len(fs.Required) != 1 {
		t.Fatalf("expected only the shared target b, got %+v", fs.Required)
	}
	if fs.Required[0].Target != "b" || fs.Required[0].Status != StatusSatisfied {
		t.Errorf("expected b to be present and satisfied, got %+v", fs.Required[0])
	}
	if fs.Status != StatusSatisfied || state.Overall != StatusSatisfied {
		t.Errorf("expected satisfied overall, file=%s overall=%s", fs.Status, state.Overall)
	}
}

func TestOverrideDiscardsParentRules(t *testing.T) {
	// README: override -> subdirectory rules completely replace parent.
	pr := buildContext(approverSet("team-b"), activeAllOwners(), []string{"zowner"})
	rc := buildChain(
		"src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-a", 1)),
		buildLayer("src", "src", rulesfile.RuleCombineStrategyOverride, approver("team-b", 1)),
	)
	state := executeChain(t, rc, pr)
	fs := state.Files["src/x.go"]
	if len(fs.Required) != 1 || fs.Required[0].Target != "team-b" {
		t.Fatalf("override should leave only team-b, got %+v", fs.Required)
	}
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
}

func TestFallbackSatisfiedByActiveOwnerApproval(t *testing.T) {
	// No matching rules -> repository owner fallback, owner already approved.
	pr := buildContext(approverSet("zowner"), activeAllOwners(), []string{"zowner"})
	rc := buildChain("orphan.go")
	state := executeChain(t, rc, pr)
	fs := state.Files["orphan.go"]
	if !fs.UsedFallback {
		t.Errorf("expected UsedFallback=true")
	}
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
	if state.Err != nil {
		t.Errorf("unexpected error: %v", state.Err)
	}
}

func TestFallbackUnsatisfiableWithNoActiveOwners(t *testing.T) {
	// README terminal state: fallback repo owners list is empty while the
	// file has no matching rules -> the rule is unsatisfiable: status error,
	// overall error, and a non-nil error propagates back to the caller so the
	// PR can be blocked and a warning posted.
	pr := buildContext(approverSet(), activeAllOwners(), []string{}) // no owners at all
	rc := buildChain("orphan.go")
	state := executeChain(t, rc, pr)
	fs := state.Files["orphan.go"]
	if fs.Status != StatusError {
		t.Errorf("file status = %s, want error", fs.Status)
	}
	if !fs.UsedFallback {
		t.Errorf("expected UsedFallback=true")
	}
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error", state.Overall)
	}
	if state.Err == nil {
		t.Errorf("expected non-nil error for unsatisfiable rule")
	}
}

func TestFallbackUnsatisfiableWhenAllOwnersInactive(t *testing.T) {
	// Owners exist but are all inactive -> same unsatisfiable terminal state.
	pr := buildContext(approverSet(), inactiveAllOwners(), []string{"zowner"})
	rc := buildChain("orphan.go")
	state := executeChain(t, rc, pr)
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error", state.Overall)
	}
	if state.Err == nil {
		t.Errorf("expected non-nil error when all owners inactive")
	}
	fs := state.Files["orphan.go"]
	if fs.Status != StatusError {
		t.Errorf("file status = %s, want error", fs.Status)
	}
}

// executeChainWithResolver runs the chain with the given resolver so tests can
// exercise resolution failures.
func executeChainWithResolver(t *testing.T, rc RuleChain, pr ReviewInput, resolver ApproverResolver) *ReviewState {
	t.Helper()
	return rc.Execute(t.Context(), pr, resolver, NewReviewStateBuilder(), testLogger())
}

func TestFallbackUnresolvableOwnerIsUnresolved(t *testing.T) {
	// No matching rules -> fallback to repository owners, but the owners
	// cannot be resolved to members. The file must surface StatusUnresolved
	// and the accumulated error must force the overall status to
	// StatusError, mirroring the non-fallback resolution-failure path.
	pr := buildContext(approverSet(), activeAllOwners(), []string{"zowner"})
	rc := buildChain("orphan.go")
	state := executeChainWithResolver(t, rc, pr, failingResolver{})
	fs := state.Files["orphan.go"]
	if !fs.UsedFallback {
		t.Errorf("expected UsedFallback=true")
	}
	if fs.Status != StatusUnresolved {
		t.Errorf("file status = %s, want unresolved", fs.Status)
	}
	if len(fs.Required) != 1 || fs.Required[0].Status != StatusUnresolved {
		t.Errorf("expected target state with StatusUnresolved, got %+v", fs.Required)
	}
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error", state.Overall)
	}
	if state.Err == nil {
		t.Errorf("expected non-nil error from unresolvable fallback owner")
	}
}

// groupResolver models a remote resolver that expands group targets to
// individual members, while passing other targets through unchanged like
// the default local resolver.
type groupResolver struct{}

// Resolve maps "team-x" and "team-y" to their members, ignoring the supplied
// context; every other target resolves to itself, mirroring the default
// local resolver.
func (groupResolver) Resolve(_ context.Context, target string) ([]string, error) {
	switch target {
	case "team-x":
		return []string{"al", "bo"}, nil
	case "team-y":
		return []string{testAuthor, "bo"}, nil
	}
	return []string{target}, nil
}

func (groupResolver) Canonical(name string) string { return name }

func TestGroupTargetResolvedAgainstMembers(t *testing.T) {
	// team-x resolves to [al, bo]; bo is active and approving, so the group
	// is satisfied even though the group name never appears in
	// approvers.
	pr := buildContext(approverSet("bo"), activeAllOwners(), []string{"zowner"})
	rc := buildChain("src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-x", 1)),
	)
	state := executeChainWithResolver(t, rc, pr, groupResolver{})
	fs := state.Files["src/x.go"]
	if fs.UsedFallback {
		t.Errorf("expected the group rule to satisfy the file, got fallback")
	}
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
	if len(fs.Layers) != 1 || len(fs.Layers[0].Rules) != 1 {
		t.Fatalf("expected one tracked rule layer state, got %+v", fs.Layers)
	}
	ts := fs.Layers[0].Rules[0].Approvers[0]
	if ts.Status != StatusSatisfied {
		t.Errorf("group target should be satisfied, got %+v", ts)
	}
	if len(ts.ResolvedBy) != 1 || ts.ResolvedBy[0] != "bo" {
		t.Errorf("expected resolved member bo, got %v", ts.ResolvedBy)
	}
}

func TestGroupTargetEligibleOnlyThroughAuthorIsPruned(t *testing.T) {
	// team-y resolves to [author, bo] with bo inactive: the only active
	// member is the author, which cannot count under the default
	// self-approval ban, so the rule prunes to empty and the fallback
	// applies.
	pr := buildContext(approverSet(testAuthor),
		func(s string) bool { return s == testAuthor }, []string{"zowner"})
	rc := buildChain("src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-y", 1)),
	)
	state := executeChainWithResolver(t, rc, pr, groupResolver{})
	fs := state.Files["src/x.go"]
	if !fs.UsedFallback {
		t.Errorf("expected fallback after the group rule pruned to empty")
	}
	// The fallback owner (zowner) is inactive in the context, so nothing
	// can satisfy the file and the run is unsatisfiable.
	if fs.Status != StatusError {
		t.Errorf("file status = %s, want error from unsatisfiable fallback", fs.Status)
	}
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error", state.Overall)
	}
	if state.Err == nil {
		t.Errorf("expected an accumulated error for the unsatisfiable fallback")
	}
}

func TestRuleStateSnapshotsEffectiveCombineStrategy(t *testing.T) {
	// A corrupt or omitted combine_strategy is remapped to the default
	// (override) by the algorithm. The historical review state must report
	// what actually ran, so that rule state snapshots the effective strategy,
	// never the raw config value.
	rule := ExecutableRule{Rule: rulesfile.Rule{
		Name:            "root",
		CombineStrategy: rulesfile.RuleCombineStrategy(0), // invalid sentinel
		Approvers:       []rulesfile.ApproverTarget{approver("team-a", 1)},
	}}
	pr := buildContext(approverSet("team-a"), activeAllOwners(), []string{"zowner"})
	rs := rule.Execute(t.Context(), newParticipants(pr, identity), &DefaultApproverResolver{}, "src/x.go", "", testLogger())
	if rs.CombineStrategy != rulesfile.RuleCombineStrategyOverride {
		t.Errorf("snapshot combine strategy = %s, want override for an invalid config value", rs.CombineStrategy)
	}
	// Raw config values pass through unchanged.
	valid := ExecutableRule{Rule: rulesfile.Rule{
		Name:            "src",
		CombineStrategy: rulesfile.RuleCombineStrategyIntersect,
		Approvers:       []rulesfile.ApproverTarget{approver("team-a", 1)},
	}}
	rs = valid.Execute(t.Context(), newParticipants(pr, identity), &DefaultApproverResolver{}, "src/x.go", "src", testLogger())
	if rs.CombineStrategy != rulesfile.RuleCombineStrategyIntersect {
		t.Errorf("snapshot combine strategy = %s, want intersect pass-through", rs.CombineStrategy)
	}
}

func TestAuthorApprovalIgnoredWhenSelfApproveDisallowed(t *testing.T) {
	// team-y resolves to [author, bo]. With the default self-approval ban,
	// only the author has approved: the group must stay unsatisfied and the
	// author must not appear in the resolved members.
	pr := buildContext(approverSet(testAuthor), activeAllOwners(), []string{"zowner"})
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"src/x.go": {{DirectoryPath: "", Rules: []ExecutableRule{{Rule: rulesfile.Rule{
			Name:            "root",
			CombineStrategy: rulesfile.RuleCombineStrategyUnion,
			SelfApprove:     false,
			Approvers:       []rulesfile.ApproverTarget{approver("team-y", 1)},
		}}}}},
	}}
	state := executeChainWithResolver(t, rc, pr, groupResolver{})
	fs := state.Files["src/x.go"]
	if fs.Status != StatusMissingApprovers {
		t.Errorf("file status = %s, want missing_approvers despite author approval", fs.Status)
	}
	if len(fs.Required) != 1 {
		t.Fatalf("expected 1 required target, got %+v", fs.Required)
	}
	s := fs.Required[0]
	if s.Status == StatusSatisfied || s.SatisfiedCount != 0 {
		t.Errorf("expected the author's approval to be excluded, got %+v", s)
	}
	if len(s.ResolvedBy) != 0 {
		t.Errorf("expected empty resolved members, got %v", s.ResolvedBy)
	}
}

func TestAuthorApprovalCountedWhenSelfApproveAllowed(t *testing.T) {
	// Same group, but the rule allows self-approval: the author's own
	// approval now satisfies the target.
	pr := buildContext(approverSet(testAuthor), activeAllOwners(), []string{"zowner"})
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"src/x.go": {{DirectoryPath: "", Rules: []ExecutableRule{{Rule: rulesfile.Rule{
			Name:            "root",
			CombineStrategy: rulesfile.RuleCombineStrategyUnion,
			SelfApprove:     true,
			Approvers:       []rulesfile.ApproverTarget{approver("team-y", 1)},
		}}}}},
	}}
	state := executeChainWithResolver(t, rc, pr, groupResolver{})
	fs := state.Files["src/x.go"]
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied via author self-approval", fs.Status)
	}
	if len(fs.Required) != 1 || fs.Required[0].SatisfiedCount != 1 {
		t.Errorf("expected the author to be counted once, got %+v", fs.Required)
	}
}

// TestAuthorshipIsApproval pins how the author counts on a platform that
// never reports an approval by them (GitHub does not let an author approve
// their own pull request): authoring the change is their approval, which
// satisfies a rule allowing self-approval and nothing else.
func TestAuthorshipIsApproval(t *testing.T) {
	for _, selfApprove := range []bool{true, false} {
		pr := buildContext(approverSet(), activeAllOwners(), []string{"zowner"})
		rc := RuleChain{FileChains: map[string][]RuleLayer{
			"src/x.go": {{DirectoryPath: "", Rules: []ExecutableRule{{Rule: rulesfile.Rule{
				Name:        "root",
				SelfApprove: selfApprove,
				Approvers:   []rulesfile.ApproverTarget{approver("team-y", 1)},
			}}}}},
		}}
		fs := executeChainWithResolver(t, rc, pr, groupResolver{}).Files["src/x.go"]
		want := StatusMissingApprovers
		if selfApprove {
			want = StatusSatisfied
		}
		if fs.Status != want {
			t.Errorf("self_approve %v with no approvals: status = %s, want %s", selfApprove, fs.Status, want)
		}
	}
}

func TestRulesPrunedToEmptyFallBackButStillErrors(t *testing.T) {
	// Existing rules prune out (inactive target + self-approval): the rule
	// is skipped - an error condition - and the owner fallback satisfies the
	// file, but the final state still terminates error.
	pr := buildContext(approverSet("zowner"), func(s string) bool { return s == "zowner" }, []string{"zowner"}) // team-a inactive
	rc := buildChain("src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-a", 1)),
	)
	state := executeChain(t, rc, pr)
	fs := state.Files["src/x.go"]
	if !fs.UsedFallback {
		t.Errorf("expected fallback after rule pruning to empty")
	}
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied via the owner fallback", fs.Status)
	}
	// The skipped rule is an error condition: accumulated in Errors and
	// forcing the overall terminal state to error.
	if len(state.Errors) == 0 || state.Err == nil {
		t.Errorf("expected the skipped rule to be accumulated as a review error, got %v / %v", state.Errors, state.Err)
	}
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error after a rule is skipped", state.Overall)
	}
	// The rule's own state surfaces the skip in the hierarchical breakdown.
	if len(fs.Layers) != 1 || len(fs.Layers[0].Rules) != 1 {
		t.Fatalf("expected one tracked rule layer state, got %+v", fs.Layers)
	}
	rs := fs.Layers[0].Rules[0]
	if rs.Status != StatusError || rs.Reason == "" {
		t.Errorf("expected the skipped rule state to be an error with a reason, got %+v", rs)
	}
}

func TestPrunedRuleErrorsEvenWhenLaterRuleSatisfies(t *testing.T) {
	// Root rule prunes to empty (team-a inactive); the /src union rule then
	// satisfies the file by team-b. The skip is still an error condition:
	// the final state terminates error.
	pr := buildContext(
		approverSet("team-b"),
		func(s string) bool { return s != "team-a" }, // team-a inactive, others active
		[]string{"zowner"},
	)
	rc := buildChain(
		"src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-a", 1)),
		buildLayer("src", "src", rulesfile.RuleCombineStrategyUnion, approver("team-b", 1)),
	)
	state := executeChain(t, rc, pr)
	fs := state.Files["src/x.go"]
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied by the later rule", fs.Status)
	}
	if len(state.Errors) == 0 || state.Err == nil {
		t.Errorf("expected the skipped root rule to be accumulated as a review error")
	}
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error after a rule is skipped", state.Overall)
	}
}

func TestPerRuleStateTrackingInLayers(t *testing.T) {
	// rule_chain.go previously carried the comment "we're not tracking
	// individual rule states in detail"; each LayerState must now expose its
	// per-rule / per-target satisfaction breakdown.
	pr := buildContext(approverSet("team-b"), activeAllOwners(), []string{"zowner"})
	rc := buildChain("src/x.go",
		buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("team-a", 1), approver("team-b", 1)),
	)
	state := executeChain(t, rc, pr)
	fs := state.Files["src/x.go"]
	if len(fs.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(fs.Layers))
	}
	layer := fs.Layers[0]
	if len(layer.Rules) != 1 {
		t.Fatalf("expected 1 tracked rule state in the layer, got %d", len(layer.Rules))
	}
	rule := layer.Rules[0]
	if rule.Name != "root" {
		t.Errorf("rule name = %s, want root", rule.Name)
	}
	if len(rule.Approvers) != 2 {
		t.Fatalf("expected 2 approver target states, got %d", len(rule.Approvers))
	}
	// team-a unsatisfied (1 required, 0 matching), team-b satisfied.
	if rule.Approvers[0].Target != "team-a" || rule.Approvers[0].Status == StatusSatisfied {
		t.Errorf("team-a should be tracked as unsatisfied, got %+v", rule.Approvers[0])
	}
	if rule.Approvers[1].Target != "team-b" || rule.Approvers[1].Status != StatusSatisfied {
		t.Errorf("team-b should be tracked as satisfied, got %+v", rule.Approvers[1])
	}
	if rule.Status != StatusMissingApprovers {
		t.Errorf("rule status = %s, want missing_approvers", rule.Status)
	}
	if layer.Status != StatusMissingApprovers {
		t.Errorf("layer status = %s, want missing_approvers", layer.Status)
	}
}

func TestFallbackUnsatisfiableBeatsSatisfiedFiles(t *testing.T) {
	// An unsatisfiable file must force the overall status to the error
	// terminal state even when other files are fully satisfied.
	pr := buildContext(approverSet("owner"), inactiveAllOwners(), []string{"owner"})
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"ok.go":     {buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("owner", 1))},
		"orphan.go": {},
	}}
	state := executeChain(t, rc, pr)
	// /ok.go: rule pruned (owner inactive by IsActive) -> fallback ->
	// owner inactive -> that file is unsatisfiable too. So the overall
	// status must be unambiguous in error.
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error given all owners inactive", state.Overall)
	}
	if state.Err == nil {
		t.Errorf("expected non-nil error")
	}
}

// TestSharedChainIsTracedPerFile: files that share a chain are evaluated
// once, share its states, and each still gets the chain's events under its
// own path and its own errors.
func TestSharedChainIsTracedPerFile(t *testing.T) {
	// The rule's only target is inactive, so the rule is skipped (an
	// error), and with no repository owners the fallback is unsatisfiable.
	layers := []RuleLayer{buildLayer("", "r", 0, approver("ghost", 1))}
	rc := RuleChain{FileChains: map[string][]RuleLayer{"a.go": layers, "b.go": layers}}
	state := executeChain(t, rc, buildContext(approverSet(), inactiveAllOwners(), nil))

	a, b := state.Files["a.go"], state.Files["b.go"]
	if a.Status != StatusError || b.Status != StatusError {
		t.Fatalf("statuses = %s, %s; want error, error", a.Status, b.Status)
	}
	if &a.Layers[0] != &b.Layers[0] {
		t.Errorf("files sharing a chain do not share its layer states")
	}
	events := map[string]int{}
	for _, e := range state.Events {
		events[e.Path]++
	}
	if events["a.go"] == 0 || events["a.go"] != events["b.go"] {
		t.Errorf("events per file = %v, want the same nonzero count for each", events)
	}
	if got := len(state.Errors); got != 4 {
		t.Fatalf("errors = %d (%q), want a skipped rule and an unsatisfiable fallback per file", got, state.Errors)
	}
	for _, file := range []string{"a.go", "b.go"} {
		if !slices.ContainsFunc(state.Errors, func(e string) bool { return strings.Contains(e, "unsatisfiable rules for "+file) }) {
			t.Errorf("no unsatisfiable error names %s: %q", file, state.Errors)
		}
	}
}

// TestRulesByFile pins the chain as rules per file, in chain order (the
// repository root first), each located by rules file and position, with
// files no rule applies to present and empty.
func TestRulesByFile(t *testing.T) {
	rules := rulesfile.Set{
		rulesfile.FileName:         {{Pattern: `.*\.go$`}, {Pattern: `.*\.go$`, Position: 1}},
		rulesfile.PathInDir("src"): {{Pattern: `api`}},
	}
	chain, err := GetFileRuleChain(t.Context(), rules, []string{"main.go", "src/api.go", "docs/readme.md"}, nil, testLogger())
	if err != nil {
		t.Fatalf("GetFileRuleChain: %v", err)
	}
	keys := map[string][]rulesfile.RuleKey{}
	for file, rs := range chain.RulesByFile() {
		keys[file] = []rulesfile.RuleKey{}
		for _, r := range rs {
			keys[file] = append(keys[file], r.Key())
		}
	}
	root0, root1 := rulesfile.RuleKey{RulesFile: rulesfile.FileName}, rulesfile.RuleKey{RulesFile: rulesfile.FileName, Position: 1}
	want := map[string][]rulesfile.RuleKey{
		"main.go":        {root0, root1},
		"src/api.go":     {root0, root1, {RulesFile: rulesfile.PathInDir("src")}},
		"docs/readme.md": {},
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("RulesByFile keys = %v, want %v", keys, want)
	}
}

// TestFallbackAuthorshipIsNotAnOwnerReview: a file no rule covers needs a
// repository owner's review; no rule allows self-approval there, so an
// owner authoring the change does not count, and the review warns that the
// file is uncovered.
func TestFallbackAuthorshipIsNotAnOwnerReview(t *testing.T) {
	rc := buildChain("docs/readme.md")
	in := ReviewInput{Author: "Alice", Owners: []string{"alice"}}

	state := executeChain(t, rc, in)
	fs := state.Files["docs/readme.md"]
	if !fs.UsedFallback || fs.Status != StatusMissingApprovers || state.Overall != StatusMissingApprovers {
		t.Fatalf("file = %+v, overall = %s; want the owners still to review", fs, state.Overall)
	}
	for _, r := range fs.Required {
		if len(r.ResolvedBy) != 0 {
			t.Errorf("owner %s counts %v; authorship must not count", r.Target, r.ResolvedBy)
		}
	}
	if want := []string{UncoveredWarning(1)}; !reflect.DeepEqual(state.Warnings, want) {
		t.Errorf("warnings = %q, want %q", state.Warnings, want)
	}
	if o := state.Outcome(); o.Verdict != Pending || !reflect.DeepEqual(o.Warnings, state.Warnings) {
		t.Errorf("outcome = %+v, want pending with the review's warnings", o)
	}

	other := ReviewInput{Author: "carol", Approvers: []string{"alice"}, Owners: []string{"alice"}}
	if got := executeChain(t, rc, other).Files["docs/readme.md"].Status; got != StatusSatisfied {
		t.Errorf("approved by the owner: status = %s, want satisfied", got)
	}
}

// TestCoveredFilesDoNotWarn: only files no rule covers are warned of; a
// rule that prunes away falls back without the warning (it errors instead).
func TestCoveredFilesDoNotWarn(t *testing.T) {
	rc := buildChain("src/x.go", buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("bob", 1)))
	if w := executeChain(t, rc, buildContext(approverSet("bob"), activeAllOwners(), []string{"zowner"})).Warnings; len(w) != 0 {
		t.Errorf("warnings = %q, want none", w)
	}
}
