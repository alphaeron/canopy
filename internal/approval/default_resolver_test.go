package approval

import (
	"testing"

	"github.com/alphaeron/canopy/internal/rulesfile"
)

// TestDefaultResolverIgnoresCase pins that a local review compares names
// as GitHub does, case-insensitively: an approval by "Lead" satisfies a
// target "lead", and a target stands for itself.
func TestDefaultResolverIgnoresCase(t *testing.T) {
	r := &DefaultApproverResolver{}
	if got := r.Canonical("Org/Lead"); got != "org/lead" {
		t.Errorf("Canonical = %q, want org/lead", got)
	}
	if got, err := r.Resolve(t.Context(), "org/team"); err != nil || len(got) != 1 || got[0] != "org/team" {
		t.Errorf("Resolve = %v, %v; want the target itself", got, err)
	}

	pr := ReviewInput{Author: "someone", Approvers: []string{"Lead"}}
	rc := buildChain("src/x.go", buildLayer("", "root", rulesfile.RuleCombineStrategyUnion, approver("lead", 1)))
	if fs := executeChain(t, rc, pr).Files["src/x.go"]; fs.Status != StatusSatisfied {
		t.Errorf("approval by Lead for target lead: status = %s, want satisfied", fs.Status)
	}
}
