package shonin

import (
	"testing"

	"github.com/alphaeron/kit/internal/kitfile"
)

func activeAllOwners() func(string) bool {
	return func(string) bool { return true }
}

func TestUnionRecurringTargetCountsPerOccurrence(t *testing.T) {
	// Root requires 1 approval from team-a; /src (union) requires 2 from the
	// same team-a. The default resolver maps target -> [target], so the
	// target name is counted as an approver only if it appears in
	// CurrentApprovers. With one matching approval, root is satisfied and
	// /src still needs one more. Each target occurrence is counted once
	// per occurrence.
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"team-a": true},
		IsOwnerActive:    activeAllOwners(),
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/file.go": {
			{DirectoryPath: "/", Rules: []kitfile.Rule{
				{Name: "root", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}},
			}},
			{DirectoryPath: "/src", Rules: []kitfile.Rule{
				{Name: "src", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 2}}},
			}},
		},
	}}
	state := rc.Execute(ctx)

	fs, ok := state.Files["/src/file.go"]
	if !ok {
		t.Fatalf("missing file state for /src/file.go")
	}
	if len(fs.Required) != 2 {
		t.Fatalf("expected 2 approver target occurrences (one per layer), got %d: %+v", len(fs.Required), fs.Required)
	}
	// Per-occurrence evaluation against the same approver pool:
	// occurrence 1 (root, requires 1) is satisfied by the one matching approver.
	if !fs.Required[0].Satisfied || fs.Required[0].SatisfiedCount != 1 {
		t.Errorf("root occurrence should be satisfied 1/1, got Satisfied=%v count=%d",
			fs.Required[0].Satisfied, fs.Required[0].SatisfiedCount)
	}
	// occurrence 2 (/src, requires 2) still needs one more.
	if fs.Required[1].Satisfied || fs.Required[1].SatisfiedCount != 1 || fs.Required[1].RequiredCount != 2 {
		t.Errorf("/src occurrence should be unsatisfied 1/2, got Satisfied=%v count=%d required=%d",
			fs.Required[1].Satisfied, fs.Required[1].SatisfiedCount, fs.Required[1].RequiredCount)
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
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"team-a": true, "team-b": true},
		IsOwnerActive:    activeAllOwners(),
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/x.go": {
			{DirectoryPath: "/", Rules: []kitfile.Rule{
				{Name: "root", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}},
			}},
			{DirectoryPath: "/src", Rules: []kitfile.Rule{
				{Name: "src", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}, {Target: "team-b", RequiredCount: 1}}},
			}},
		},
	}}
	state := rc.Execute(ctx)
	fs := state.Files["/src/x.go"]
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
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"b": true},
		IsOwnerActive:    activeAllOwners(),
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/x.go": {
			{DirectoryPath: "/", Rules: []kitfile.Rule{
				{Name: "root", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{
						{Target: "a", RequiredCount: 1},
						{Target: "b", RequiredCount: 1},
					}},
			}},
			{DirectoryPath: "/src", Rules: []kitfile.Rule{
				{Name: "src", CombineStrategy: kitfile.RuleCombineStrategyIntersect,
					Approvers: []kitfile.ApproverTarget{
						{Target: "b", RequiredCount: 1},
						{Target: "c", RequiredCount: 1},
					}},
			}},
		},
	}}
	state := rc.Execute(ctx)
	fs := state.Files["/src/x.go"]
	if len(fs.Required) != 1 {
		t.Fatalf("expected only the shared target b, got %+v", fs.Required)
	}
	if fs.Required[0].Target != "b" || !fs.Required[0].Satisfied {
		t.Errorf("expected b to be present and satisfied, got %+v", fs.Required[0])
	}
	if fs.Status != StatusSatisfied || state.Overall != StatusSatisfied {
		t.Errorf("expected satisfied overall, file=%s overall=%s", fs.Status, state.Overall)
	}
}

func TestOverrideDiscardsParentRules(t *testing.T) {
	// README: override -> subdirectory rules completely replace parent.
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"team-b": true},
		IsOwnerActive:    activeAllOwners(),
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/x.go": {
			{DirectoryPath: "/", Rules: []kitfile.Rule{
				{Name: "root", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}},
			}},
			{DirectoryPath: "/src", Rules: []kitfile.Rule{
				{Name: "src", CombineStrategy: kitfile.RuleCombineStrategyOverride,
					Approvers: []kitfile.ApproverTarget{{Target: "team-b", RequiredCount: 1}}},
			}},
		},
	}}
	state := rc.Execute(ctx)
	fs := state.Files["/src/x.go"]
	if len(fs.Required) != 1 || fs.Required[0].Target != "team-b" {
		t.Fatalf("override should leave only team-b, got %+v", fs.Required)
	}
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
}

func TestFallbackSatisfiedByActiveOwnerApproval(t *testing.T) {
	// No matching rules -> repository owner fallback, owner already approved.
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"zowner": true},
		IsOwnerActive:    activeAllOwners(),
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{"/orphan.go": {}}}
	state := rc.Execute(ctx)
	fs := state.Files["/orphan.go"]
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
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{},
		IsOwnerActive:    activeAllOwners(),
		RepositoryOwners: []string{}, // no owners at all
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{"/orphan.go": {}}}
	state := rc.Execute(ctx)
	fs := state.Files["/orphan.go"]
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
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{},
		IsOwnerActive:    func(string) bool { return false },
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{"/orphan.go": {}}}
	state := rc.Execute(ctx)
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error", state.Overall)
	}
	if state.Err == nil {
		t.Errorf("expected non-nil error when all owners inactive")
	}
	fs := state.Files["/orphan.go"]
	if fs.Status != StatusError {
		t.Errorf("file status = %s, want error", fs.Status)
	}
}

func TestRulesPrunedToEmptyFallBack(t *testing.T) {
	// Existing rules prune out (inactive target + self-approval) -> empty set
	// -> fallback kicks in and owner approval satisfies the file.
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"zowner": true},
		IsOwnerActive:    func(s string) bool { return s == "zowner" }, // team-a inactive
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/x.go": {
			{DirectoryPath: "/", Rules: []kitfile.Rule{
				{Name: "root", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{{Target: "team-a", RequiredCount: 1}}},
			}},
		},
	}}
	state := rc.Execute(ctx)
	fs := state.Files["/src/x.go"]
	if !fs.UsedFallback {
		t.Errorf("expected fallback after rule pruning to empty")
	}
	if fs.Status != StatusSatisfied {
		t.Errorf("file status = %s, want satisfied", fs.Status)
	}
}

func TestPerRuleStateTrackingInLayers(t *testing.T) {
	// rule_chain.go previously carried the comment "we're not tracking
	// individual rule states in detail"; each LayerState must now expose its
	// per-rule / per-target satisfaction breakdown.
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"team-b": true},
		IsOwnerActive:    activeAllOwners(),
		RepositoryOwners: []string{"zowner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/src/x.go": {
			{DirectoryPath: "/", Rules: []kitfile.Rule{
				{Name: "root", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{
						{Target: "team-a", RequiredCount: 1},
						{Target: "team-b", RequiredCount: 1},
					}},
			}},
		},
	}}
	state := rc.Execute(ctx)
	fs := state.Files["/src/x.go"]
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
	if rule.Approvers[0].Target != "team-a" || rule.Approvers[0].Satisfied {
		t.Errorf("team-a should be tracked as unsatisfied, got %+v", rule.Approvers[0])
	}
	if rule.Approvers[1].Target != "team-b" || !rule.Approvers[1].Satisfied {
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
	ctx := EvaluationContext{
		Author:           "user9",
		CurrentApprovers: map[string]bool{"owner": true},
		IsOwnerActive:    func(string) bool { return false }, // everyone inactive
		RepositoryOwners: []string{"owner"},
	}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/ok.go": {
			{DirectoryPath: "/", Rules: []kitfile.Rule{
				{Name: "root", CombineStrategy: kitfile.RuleCombineStrategyUnion,
					Approvers: []kitfile.ApproverTarget{{Target: "owner", RequiredCount: 1}}},
			}},
		},
		"/orphan.go": {},
	}}
	state := rc.Execute(ctx)
	// /ok.go: rule pruned (owner inactive by IsOwnerActive) -> fallback ->
	// owner inactive -> that file is unsatisfiable too. So the overall
	// status must be unambiguous in error.
	if state.Overall != StatusError {
		t.Errorf("overall status = %s, want error given all owners inactive", state.Overall)
	}
	if state.Err == nil {
		t.Errorf("expected non-nil error")
	}
}
