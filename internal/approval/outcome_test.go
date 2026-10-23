package approval

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

func TestReviewStateOutcome(t *testing.T) {
	files := map[string]FileState{
		"a.go": {Status: StatusSatisfied},
		"b.go": {Status: StatusMissingApprovers},
	}
	tests := []struct {
		name    string
		state   *ReviewState
		want    Verdict
		summary string
	}{
		{name: "satisfied passes", state: &ReviewState{Overall: StatusSatisfied, Files: files}, want: Passed, summary: "all 2 files approved"},
		{name: "done passes", state: &ReviewState{Overall: StatusDone}, want: Passed},
		{name: "missing approvers is pending", state: &ReviewState{Overall: StatusMissingApprovers, Files: files}, want: Pending, summary: "1 of 2 files awaits approval"},
		{name: "unresolved is pending", state: &ReviewState{Overall: StatusUnresolved}, want: Pending},
		{name: "unknown is pending", state: &ReviewState{}, want: Pending},
		{name: "error errors", state: &ReviewState{Overall: StatusError, Errors: []string{"x", "y"}}, want: Errored, summary: "review failed with 2 errors"},
		{name: "truncated tree", state: &ReviewState{Overall: StatusError, Err: fmt.Errorf("list: %w", ErrTreeTruncated)}, want: Errored, summary: "repository too large to evaluate completely"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (tt.state).Outcome()
			if got.Verdict != tt.want {
				t.Errorf("Verdict = %s, want %s", got.Verdict, tt.want)
			}
			if tt.summary != "" && got.Summary != tt.summary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.summary)
			}
		})
	}
}

// TestOutcomeNeverQuotesErrors: a failed review's summary is published on
// the change, so it never carries an error's text.
func TestOutcomeNeverQuotesErrors(t *testing.T) {
	secret := "GET https://api.example.com/x?token=abc: 500"
	got := (&ReviewState{Overall: StatusError, Errors: []string{secret}, Err: fmt.Errorf("%s", secret)}).Outcome()
	if strings.Contains(got.Summary, "token") {
		t.Errorf("Summary = %q quotes the error", got.Summary)
	}
}

// TestOutcomePendingNamesWhoIsNeeded: a pending outcome's items are the
// approvers still needed, each with the most approvals it still owes a file.
func TestOutcomePendingNamesWhoIsNeeded(t *testing.T) {
	need := func(target string, required, satisfied int) ApproverTargetState {
		st := StatusMissingApprovers
		if satisfied >= required {
			st = StatusSatisfied
		}
		return ApproverTargetState{
			ApproverTarget: rulesfile.ApproverTarget{Target: target, RequiredCount: required},
			SatisfiedCount: satisfied, Status: st,
		}
	}
	state := &ReviewState{Overall: StatusMissingApprovers, Files: map[string]FileState{
		"a.go": {Status: StatusMissingApprovers, Required: []ApproverTargetState{need("org/backend", 2, 0), need("lead", 1, 1)}},
		"b.go": {Status: StatusMissingApprovers, Required: []ApproverTargetState{need("org/backend", 2, 1), need("alice", 1, 0)}},
		"c.go": {Status: StatusSatisfied, Required: []ApproverTargetState{need("lead", 1, 1)}},
	}}
	got := (state).Outcome()
	want := Outcome{Verdict: Pending, Summary: "2 of 3 files await approval", Items: []string{"alice", "org/backend ×2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Outcome = %+v, want %+v", got, want)
	}
}

// TestOutcomeDescription: the description fits the platform's limit, keeping
// the summary, then the warnings (shortened to a count), then as many items
// as fit.
func TestOutcomeDescription(t *testing.T) {
	paths := []string{"services/payments/api/RULES", "services/payments/worker/RULES", "services/ledger/RULES", "tools/release/RULES", "docs/RULES"}
	large := "large pull request (4012 files)"
	tests := []struct {
		name  string
		o     Outcome
		limit int
		want  string
	}{
		{name: "summary alone", o: Outcome{Summary: "all 3 files approved"}, limit: 140, want: "all 3 files approved"},
		{name: "no limit lists everything", o: Outcome{Summary: "5 invalid proposed rules files", Items: paths, Warnings: []string{large}},
			want: "5 invalid proposed rules files (services/payments/api/RULES, services/payments/worker/RULES, services/ledger/RULES, tools/release/RULES, docs/RULES) · warning: " + large},
		{name: "items that do not all fit end with a count", o: Outcome{Summary: "6 invalid proposed rules files", Items: append(paths, "infra/terraform/RULES")}, limit: 148,
			want: "6 invalid proposed rules files (services/payments/api/RULES, services/payments/worker/RULES, services/ledger/RULES, tools/release/RULES +2 more)"},
		{name: "warnings keep their place before items", o: Outcome{Summary: "5 invalid proposed rules files", Items: paths, Warnings: []string{large}}, limit: 148,
			want: "5 invalid proposed rules files (services/payments/api/RULES, services/payments/worker/RULES +3 more) · warning: " + large},
		{name: "a warning too long whole is counted", o: Outcome{Summary: "all 3 files approved", Warnings: []string{strings.Repeat("w", 140)}}, limit: 140,
			want: "all 3 files approved · 1 warning"},
		{name: "no item fits", o: Outcome{Summary: "2 of 3 files await approval", Items: []string{strings.Repeat("t", 140)}}, limit: 140,
			want: "2 of 3 files await approval"},
		{name: "a summary too long alone is cut", o: Outcome{Summary: strings.Repeat("é", 100), Items: paths}, limit: 140,
			want: strings.Repeat("é", 68) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.o.Description(tt.limit)
			if got != tt.want {
				t.Errorf("Description(%d) =\n  %q\nwant\n  %q", tt.limit, got, tt.want)
			}
			if tt.limit > 0 && len(got) > tt.limit {
				t.Errorf("Description(%d) is %d bytes, over the limit", tt.limit, len(got))
			}
		})
	}
}
