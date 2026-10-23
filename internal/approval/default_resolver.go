package approval

import (
	"context"
	"strings"
)

// DefaultApproverResolver is the pass-through resolver for local execution:
// it resolves each approver target to itself, so the target name is what
// gets checked against the current approver set. Local reviews have no
// platform to expand groups against, so a group target stands for itself.
// Names compare case-insensitively, as on GitHub, so a local review agrees
// with a hosted one on who approved. Each hosted platform under
// internal/platform supplies its own resolver.
type DefaultApproverResolver struct{}

// Resolve returns the target unchanged as its own member set.
func (r *DefaultApproverResolver) Resolve(ctx context.Context, target string) ([]string, error) {
	return []string{target}, nil
}

// Canonical returns name in lower case.
func (r *DefaultApproverResolver) Canonical(name string) string { return strings.ToLower(name) }
