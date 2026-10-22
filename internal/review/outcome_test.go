package review

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alphaeron/kit/internal/approval"
)

func TestOutcomeOf(t *testing.T) {
	files := map[string]approval.FileState{
		"a.go": {Status: approval.StatusSatisfied},
		"b.go": {Status: approval.StatusMissingApprovers},
	}
	tests := []struct {
		name    string
		state   *approval.ReviewState
		want    Verdict
		summary string
	}{
		{name: "satisfied passes", state: &approval.ReviewState{Overall: approval.StatusSatisfied, Files: files}, want: Passed, summary: "all 2 file(s) approved"},
		{name: "done passes", state: &approval.ReviewState{Overall: approval.StatusDone}, want: Passed},
		{name: "missing approvers is pending", state: &approval.ReviewState{Overall: approval.StatusMissingApprovers, Files: files}, want: Pending, summary: "1 of 2 file(s) awaiting approval"},
		{name: "unresolved is pending", state: &approval.ReviewState{Overall: approval.StatusUnresolved}, want: Pending},
		{name: "unknown is pending", state: &approval.ReviewState{}, want: Pending},
		{name: "error errors", state: &approval.ReviewState{Overall: approval.StatusError, Errors: []string{"x", "y"}}, want: Errored, summary: "review failed with 2 errors"},
		{name: "truncated tree", state: &approval.ReviewState{Overall: approval.StatusError, Err: fmt.Errorf("list: %w", ErrTreeTruncated)}, want: Errored, summary: "repository too large to evaluate completely"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OutcomeOf(tt.state)
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
	got := OutcomeOf(&approval.ReviewState{Overall: approval.StatusError, Errors: []string{secret}, Err: fmt.Errorf("%s", secret)})
	if strings.Contains(got.Summary, "token") {
		t.Errorf("Summary = %q quotes the error", got.Summary)
	}
}
