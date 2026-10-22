package approval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// fakeResolver is a platform resolver with case-insensitive names, groups
// keyed by name, a set of targets that fail operationally, and a count of
// Resolve calls per target.
type fakeResolver struct {
	groups  map[string][]string
	failing map[string]error

	mu    sync.Mutex
	calls map[string]int
}

func (f *fakeResolver) Resolve(_ context.Context, target string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[target]++
	if err, ok := f.failing[target]; ok {
		return nil, err
	}
	if strings.Contains(target, "/") {
		members, ok := f.groups[target]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownTarget, target)
		}
		return members, nil
	}
	return []string{target}, nil
}

func (f *fakeResolver) Canonical(name string) string { return strings.ToLower(name) }

func singleRuleChain(file string, selfApprove bool, targets ...rulesfile.ApproverTarget) RuleChain {
	return RuleChain{FileChains: map[string][]RuleLayer{file: {{
		DirectoryPath: "",
		Rules:         []Rule{newRule(rulesfile.Rule{Name: "root", SelfApprove: selfApprove, Approvers: targets})},
	}}}}
}

// TestNamesCompareInCanonicalForm pins that a platform's identity rules
// apply everywhere a name is compared: rules file targets, approvals, the
// author, and resolved group members.
func TestNamesCompareInCanonicalForm(t *testing.T) {
	resolver := &fakeResolver{groups: map[string][]string{"org/team": {"Bo", "USER9"}}}
	tests := []struct {
		name   string
		target string
		in     ReviewInput
		want   Status
	}{
		{name: "user target matches approval in another case", target: "Alice",
			in: ReviewInput{Approvers: []string{"alice"}}, want: StatusSatisfied},
		{name: "group member matches approval in another case", target: "org/team",
			in: ReviewInput{Approvers: []string{"bo"}}, want: StatusSatisfied},
		{name: "author is excluded whatever the case", target: "org/team",
			in: ReviewInput{Author: "user9", Approvers: []string{"User9"}}, want: StatusMissingApprovers},
		{name: "inactive check receives canonical names", target: "Alice",
			in: ReviewInput{Approvers: []string{"ALICE"}, IsActive: func(n string) bool { return n == "alice" }}, want: StatusSatisfied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := singleRuleChain("/x.go", false, approver(tt.target, 1))
			state := rc.Execute(t.Context(), tt.in, resolver, NewReviewStateBuilder(), testLogger())
			if got := state.Files["/x.go"].Status; got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestOperationalResolveErrorFailsReview pins that a platform failure while
// resolving a target fails the review with its cause, and is never treated
// as an unknown target that could be pruned to a weaker requirement.
func TestOperationalResolveErrorFailsReview(t *testing.T) {
	outage := errors.New("rate limited")
	resolver := &fakeResolver{failing: map[string]error{"org/team": outage}}
	rc := singleRuleChain("/x.go", false, approver("org/team", 1))

	state := rc.Execute(t.Context(), ReviewInput{Owners: []string{"zowner"}, Approvers: []string{"zowner"}}, resolver, NewReviewStateBuilder(), testLogger())
	fs := state.Files["/x.go"]
	if fs.UsedFallback {
		t.Error("an operational failure must not prune the target and fall back to the owners")
	}
	if state.Overall != StatusError {
		t.Errorf("overall = %s, want error", state.Overall)
	}
	if !strings.Contains(strings.Join(state.Errors, "\n"), "rate limited") {
		t.Errorf("errors = %v, want the platform failure's cause", state.Errors)
	}
}

// TestUnknownTargetIsPruned keeps the documented behavior for a target the
// platform does not know: the rule is skipped, and the review errors.
func TestUnknownTargetIsPruned(t *testing.T) {
	resolver := &fakeResolver{}
	rc := singleRuleChain("/x.go", false, approver("org/ghosts", 1))

	state := rc.Execute(t.Context(), ReviewInput{Owners: []string{"zowner"}, Approvers: []string{"zowner"}}, resolver, NewReviewStateBuilder(), testLogger())
	if !state.Files["/x.go"].UsedFallback {
		t.Error("an unknown target must be pruned, falling back to the owners")
	}
	if state.Overall != StatusError {
		t.Errorf("overall = %s, want error for the skipped rule", state.Overall)
	}
}

// TestReviewResolvesEachTargetOnce: a review resolves a target once however
// many files, rules, and phases use it, so platforms need not cache.
func TestReviewResolvesEachTargetOnce(t *testing.T) {
	resolver := &fakeResolver{groups: map[string][]string{"org/team": {"bo"}}}
	rule := []Rule{newRule(rulesfile.Rule{Name: "root", Approvers: []rulesfile.ApproverTarget{approver("org/team", 1)}})}
	rc := RuleChain{FileChains: map[string][]RuleLayer{
		"/a.go": {{Rules: rule}},
		"/b.go": {{Rules: rule}},
	}}

	rc.Execute(t.Context(), ReviewInput{Approvers: []string{"bo"}}, resolver, NewReviewStateBuilder(), testLogger())
	if got := resolver.calls["org/team"]; got != 1 {
		t.Errorf("resolved org/team %d times, want 1", got)
	}
}
