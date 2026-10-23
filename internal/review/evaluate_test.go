package review

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"reflect"
	"testing"

	"github.com/alphaeron/canopy/internal/approval"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

// stubChange is an in-memory Change: rules at the base, rules files it
// proposes, its changed files, and its review input.
type stubChange struct {
	stubSource
	head     map[string]string
	headErr  error // makes ReadHead fail
	input    approval.ReviewInput
	warnings []string
}

func newStubChange() *stubChange {
	return &stubChange{stubSource: stubSource{
		rules: rulesfile.Set{rulesfile.FileName: {{Pattern: ".*", Name: "Root",
			Approvers: rulesfile.ApproverTargets{{Target: "lead", RequiredCount: 1}}}}},
		changedSet: []string{"main.go"},
	}}
}

func (c *stubChange) ReviewInput(context.Context) (approval.ReviewInput, error) { return c.input, nil }

func (c *stubChange) ReadHead(_ context.Context, path string) ([]byte, error) {
	if c.headErr != nil {
		return nil, c.headErr
	}
	data, ok := c.head[path]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
	}
	return []byte(data), nil
}

func (c *stubChange) Warnings() []string { return c.warnings }

var _ Change = (*stubChange)(nil)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name  string
		setup func(pr *stubChange)
		want  approval.Outcome
	}{
		{
			name:  "approved",
			setup: func(pr *stubChange) { pr.input = approval.ReviewInput{Approvers: []string{"lead"}} },
			want:  approval.Outcome{Verdict: approval.Passed, Summary: "1 file approved"},
		},
		{
			name:  "awaiting approval",
			setup: func(pr *stubChange) {},
			want:  approval.Outcome{Verdict: approval.Pending, Summary: "1 file awaits approval", Items: []string{"lead"}},
		},
		{
			name: "an invalid proposed rules file fails even when approved",
			setup: func(pr *stubChange) {
				pr.input = approval.ReviewInput{Approvers: []string{"lead"}}
				pr.changedSet = []string{"main.go", "old/RULES", "svc/RULES"}
				pr.head = map[string]string{"svc/RULES": "- \".*\":\n  name: Svc\n  approvers:\n    - target: org/team\n"}
			},
			want: approval.Outcome{Verdict: approval.Failed, Summary: "1 invalid proposed rules file", Items: []string{"svc/RULES"}},
		},
		{
			name:  "too many changed files",
			setup: func(pr *stubChange) { pr.changedErr = fmt.Errorf("listing: %w", approval.ErrFileListIncomplete) },
			want:  approval.Outcome{Verdict: approval.Errored, Summary: "too many changed files to evaluate completely"},
		},
		{
			name:  "truncated base tree",
			setup: func(pr *stubChange) { pr.err = fmt.Errorf("tree: %w", approval.ErrTreeTruncated) },
			want:  approval.Outcome{Verdict: approval.Errored, Summary: "repository too large to evaluate completely"},
		},
		{
			name: "warnings go with the outcome without changing it",
			setup: func(pr *stubChange) {
				pr.input = approval.ReviewInput{Approvers: []string{"lead"}}
				pr.warnings = []string{"large pull request (3500 files)"}
			},
			want: approval.Outcome{Verdict: approval.Passed, Summary: "1 file approved", Warnings: []string{"large pull request (3500 files)"}},
		},
		{
			name: "an uncovered file waits for an owner other than the author, and is warned of",
			setup: func(pr *stubChange) {
				pr.changedSet = []string{"docs/a.md"}
				pr.rules = rulesfile.Set{rulesfile.FileName: {{Pattern: `^src/`, Approvers: rulesfile.ApproverTargets{{Target: "lead", RequiredCount: 1}}}}}
				pr.input = approval.ReviewInput{Author: "alice", Owners: []string{"alice"}}
				pr.warnings = []string{"large pull request (3500 files)"}
			},
			want: approval.Outcome{Verdict: approval.Pending, Summary: "1 file awaits approval", Items: []string{"alice"},
				Warnings: []string{approval.UncoveredWarning(1), "large pull request (3500 files)"}},
		},
		{
			name:  "rate limited",
			setup: func(pr *stubChange) { pr.changedErr = fmt.Errorf("listing: %w", approval.ErrRateLimited) },
			want:  approval.Outcome{Verdict: approval.Errored, Summary: "rate limited by the platform; try again later"},
		},
		{
			name:  "a platform failure is not quoted",
			setup: func(pr *stubChange) { pr.changedErr = errors.New("GET https://api/x?token=secret: 500") },
			want:  approval.Outcome{Verdict: approval.Errored, Summary: "review could not run; see the server logs"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := newStubChange()
			tt.setup(pr)
			if got := Evaluate(t.Context(), pr, slog.New(slog.DiscardHandler)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("outcome = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestEvaluateEnforcesTheBaseRules: the review applies the rules the pull
// request's base has, not the ones it proposes.
func TestEvaluateEnforcesTheBaseRules(t *testing.T) {
	pr := newStubChange()
	pr.changedSet = []string{"main.go", rulesfile.FileName}
	pr.head = map[string]string{rulesfile.FileName: "- \".*\":\n  name: Root\n  approvers:\n    - target: dev\n      required_count: 1\n"}
	pr.input = approval.ReviewInput{Author: "dev", Approvers: []string{"dev"}}
	if got := Evaluate(t.Context(), pr, slog.New(slog.DiscardHandler)).Verdict; got != approval.Pending {
		t.Errorf("verdict = %s, want pending: the proposed rules must not apply before merge", got)
	}
}
