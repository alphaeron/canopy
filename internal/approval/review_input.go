package approval

// ReviewInput is the state of a change that a review evaluates its files
// against: who wrote it, who has approved it, who can approve at all, and
// the repository owners who decide files no rule covers. It is
// platform-neutral plain data: the CLI builds it from flags, and a hosted
// platform reads it from its API. Names are compared in their platform's
// canonical form (ApproverResolver.Canonical), so they may be given in any
// form the platform accepts.
type ReviewInput struct {
	// Author is the change's author, whose approvals count only under
	// self-approve rules.
	Author string
	// Approvers are the users who have approved the change.
	Approvers []string
	// IsActive reports whether a user can currently approve; inactive
	// users are pruned from every target. It is called with canonical
	// names. nil means every user is active.
	IsActive func(name string) bool
	// Owners are the repository owners: the fallback for a file no rule
	// covers.
	Owners []string
}

// participants is a ReviewInput in the form the algorithm evaluates it:
// every name canonical, approvals as a set.
type participants struct {
	author    string
	approvers map[string]bool
	owners    []string
	isActive  func(name string) bool
	canonical func(name string) string
}

// newParticipants canonicalizes in with canonical, the platform's
// ApproverResolver.Canonical.
func newParticipants(in ReviewInput, canonical func(string) string) participants {
	p := participants{
		author:    canonical(in.Author),
		approvers: make(map[string]bool, len(in.Approvers)),
		owners:    make([]string, 0, len(in.Owners)),
		isActive:  in.IsActive,
		canonical: canonical,
	}
	for _, a := range in.Approvers {
		p.approvers[canonical(a)] = true
	}
	for _, o := range in.Owners {
		p.owners = append(p.owners, canonical(o))
	}
	if p.isActive == nil {
		p.isActive = func(string) bool { return true }
	}
	return p
}
