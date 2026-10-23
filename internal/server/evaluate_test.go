package server

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name  string
		setup func(pr *stubPullRequest)
		want  review.Outcome
	}{
		{
			name:  "approved",
			setup: func(pr *stubPullRequest) { pr.input = approval.ReviewInput{Approvers: []string{"lead"}} },
			want:  review.Outcome{Verdict: review.Passed, Summary: "1 file approved"},
		},
		{
			name:  "awaiting approval",
			setup: func(pr *stubPullRequest) {},
			want:  review.Outcome{Verdict: review.Pending, Summary: "1 file awaits approval", Items: []string{"lead"}},
		},
		{
			name: "an invalid proposed rules file fails even when approved",
			setup: func(pr *stubPullRequest) {
				pr.input = approval.ReviewInput{Approvers: []string{"lead"}}
				pr.changed = []string{"main.go", "old/KIT", "svc/KIT"}
				pr.head = map[string]string{"svc/KIT": "- \".*\":\n  name: Svc\n  approvers:\n    - target: org/team\n"}
			},
			want: review.Outcome{Verdict: review.Failed, Summary: "1 invalid proposed rules file", Items: []string{"svc/KIT"}},
		},
		{
			name:  "too many changed files",
			setup: func(pr *stubPullRequest) { pr.changedErr = fmt.Errorf("listing: %w", review.ErrFileListIncomplete) },
			want:  review.Outcome{Verdict: review.Errored, Summary: "too many changed files to evaluate completely"},
		},
		{
			name:  "truncated base tree",
			setup: func(pr *stubPullRequest) { pr.fetchErr = fmt.Errorf("tree: %w", review.ErrTreeTruncated) },
			want:  review.Outcome{Verdict: review.Errored, Summary: "repository too large to evaluate completely"},
		},
		{
			name: "warnings go with the outcome without changing it",
			setup: func(pr *stubPullRequest) {
				pr.input = approval.ReviewInput{Approvers: []string{"lead"}}
				pr.warnings = []string{"large pull request (3500 files)"}
			},
			want: review.Outcome{Verdict: review.Passed, Summary: "1 file approved", Warnings: []string{"large pull request (3500 files)"}},
		},
		{
			name:  "rate limited",
			setup: func(pr *stubPullRequest) { pr.changedErr = fmt.Errorf("listing: %w", review.ErrRateLimited) },
			want:  review.Outcome{Verdict: review.Errored, Summary: "rate limited by the platform; try again later"},
		},
		{
			name:  "a platform failure is not quoted",
			setup: func(pr *stubPullRequest) { pr.changedErr = errors.New("GET https://api/x?token=secret: 500") },
			want:  review.Outcome{Verdict: review.Errored, Summary: "review could not run; see the server logs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := newStubPullRequest()
			tt.setup(pr)
			if got := evaluate(t.Context(), pr, slog.New(slog.DiscardHandler)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("outcome = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestEvaluateEnforcesTheBaseRules: the review applies the rules the pull
// request's base has, not the ones it proposes.
func TestEvaluateEnforcesTheBaseRules(t *testing.T) {
	pr := newStubPullRequest()
	pr.changed = []string{"main.go", rulesfile.FileName}
	pr.head = map[string]string{rulesfile.FileName: "- \".*\":\n  name: Root\n  approvers:\n    - target: dev\n      required_count: 1\n"}
	pr.input = approval.ReviewInput{Author: "dev", Approvers: []string{"dev"}}
	if got := evaluate(t.Context(), pr, slog.New(slog.DiscardHandler)).Verdict; got != review.Pending {
		t.Errorf("verdict = %s, want pending: the proposed rules must not apply before merge", got)
	}
}

func TestValidateProposed(t *testing.T) {
	pr := newStubPullRequest()
	pr.head = map[string]string{
		"ok/KIT":      "- \".*\":\n  name: Ok\n  approvers:\n    - target: lead\n      required_count: 1\n",
		"broken/KIT":  "- [not: rules\n",
		"badre/KIT":   "- \"(\":\n  name: Bad\n",
		"nocount/KIT": "- \".*\":\n  name: NoCount\n  approvers:\n    - target: org/team\n",
	}
	// deleted/KIT is pending but absent at the head: deleted, so skipped.
	pending := []string{"ok/KIT", "broken/KIT", "badre/KIT", "nocount/KIT", "deleted/KIT"}

	got, err := validateProposed(t.Context(), pr, pending)
	if err != nil {
		t.Fatalf("validateProposed: %v", err)
	}
	for _, p := range []string{"broken/KIT", "badre/KIT", "nocount/KIT"} {
		if len(got[p]) == 0 {
			t.Errorf("%s: no violations, want some", p)
		}
	}
	for _, p := range []string{"ok/KIT", "deleted/KIT"} {
		if len(got[p]) != 0 {
			t.Errorf("%s: violations %v, want none", p, got[p])
		}
	}

	tooLarge := fmt.Errorf("read big/KIT: %w", rulesfile.ErrTooLarge)
	pr.headErr = tooLarge
	if got, err := validateProposed(t.Context(), pr, []string{"big/KIT"}); err != nil || !errors.Is(got["big/KIT"][0], rulesfile.ErrTooLarge) {
		t.Errorf("validateProposed with an oversized file = %v, %v; want it as a violation", got, err)
	}

	unreadable := errors.New("unreadable")
	pr.headErr = unreadable
	if _, err := validateProposed(t.Context(), pr, pending[:1]); !errors.Is(err, unreadable) {
		t.Errorf("validateProposed with an unreadable file err = %v, want the read error", err)
	}
}
