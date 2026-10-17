package shonin

// ApproverResolver resolves approver targets (usernames or group names) to
// the concrete members who can satisfy an approval requirement. Implementations are implementation-specific, mirroring the
// Fetcher pattern: local execution uses the pass-through default resolver,
// and platform integrations (GitHub/GitLab) provide a remote resolver that
// expands group targets and checks platform membership.
type ApproverResolver interface {
	// Resolve maps a target to its member set. ok is false when the target
	// cannot be resolved at all, in which case the returned members must be
	// ignored.
	Resolve(target string, ctx EvaluationContext) ([]string, bool)
}
