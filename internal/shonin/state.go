package shonin

import (
	"fmt"
	"strings"

	"github.com/alphaeron/kit/internal/kitfile"
)

// Status represents the state of a review node in the evaluation tree.
type Status int

const (
	StatusUnknown Status = iota
	StatusUnresolved
	StatusError
	StatusSatisfied
	StatusMissingApprovers
	StatusActive
	StatusInactive
	StatusDone
)

func (s Status) String() string {
	switch s {
	case StatusUnknown:
		return "unknown"
	case StatusUnresolved:
		return "unresolved"
	case StatusError:
		return "error"
	case StatusSatisfied:
		return "satisfied"
	case StatusMissingApprovers:
		return "missing_approvers"
	case StatusActive:
		return "active"
	case StatusInactive:
		return "inactive"
	case StatusDone:
		return "done"
	default:
		return "unknown"
	}
}

// ReviewState is the complete, renderable outcome of one review run.
type ReviewState struct {
	RepoRoot string
	HeadRef  string
	Files    map[string]FileState
	Overall  Status
	Events   []Event  // chronological trace across fetch, sift, and approve
	Errors   []string // every error encountered, so none are invisible
	Err      error    // surfaced, not swallowed
}

// FileState represents the state of a single file in the review.
type FileState struct {
	Path         string
	Layers       []LayerState
	Required     []ApproverTargetState // final per-target requirement for this file
	Status       Status
	UsedFallback bool
}

// LayerState represents the state of a rule layer in the review.
type LayerState struct {
	DirectoryPath string
	Rules         []RuleState
	// Required is the combined rule-target footprint that results from
	// applying this layer's rules to the incoming footprint. The chain
	// consumes it to feed the next layer down, so the layer's downstream
	// target state travels through the layer state only.
	Required   RuleTargets
	ResolvedBy []string // active individuals/teams that contributed
	Active     bool
	Status     Status
	Reason     string
}

// RuleState represents the state of a single rule in the review.
type RuleState struct {
	Pattern              string
	Name                 string
	IgnoreParentDirs     bool
	CombineStrategy      kitfile.RuleCombineStrategy
	CurrentDirectoryOnly bool
	SelfApprove          bool
	Approvers            []ApproverTargetState
	ResolvedBy           []string // individuals/teams that satisfied this rule
	Status               Status
	Reason               string
}

// ApproverTargetState represents the state of an approver target in the review.
type ApproverTargetState struct {
	Target         string
	RequiredCount  int
	SatisfiedCount int
	Satisfied      bool
	ResolvedBy     []string // individuals/teams that contributed to satisfaction
	Status         Status
	Reason         string
}

// Event represents a step in the review process for auditability.
type Event struct {
	Phase  string // "sift" | "approve"
	Path   string
	Layer  string
	Rule   string
	Status Status
	Note   string // e.g. "rule .*\.go combined via union; required += backend-team"
}

// ReviewStateBuilder builds the complete review state from the algorithm
// execution. It accumulates the chronological event trace and every error
// encountered during fetch, sift, and approve so that the resulting
// ReviewState exposes the full failure picture.
type ReviewStateBuilder struct {
	resolver ApproverResolver
	events   []Event
	errors   []string
	repoRoot string
	files    map[string]FileState
	overall  Status
}

// NewReviewStateBuilder creates a new review state builder.
func NewReviewStateBuilder(resolver ApproverResolver) *ReviewStateBuilder {
	if resolver == nil {
		resolver = &DefaultApproverResolver{}
	}
	return &ReviewStateBuilder{
		resolver: resolver,
		events:   make([]Event, 0),
	}
}

// AddEvent adds an event to the review state. It is a no-op on a nil
// builder, which lets pure helpers such as the sifter operate without a
// trace attached.
func (b *ReviewStateBuilder) AddEvent(phase, path, layer, rule string, status Status, note string) {
	if b == nil {
		return
	}
	b.events = append(b.events, Event{
		Phase:  phase,
		Path:   path,
		Layer:  layer,
		Rule:   rule,
		Status: status,
		Note:   note,
	})
}

// AddError records an error encountered during any phase of the review. All
// recorded errors are visible in the final ReviewState (Errors and Err) and
// force the overall approval status to StatusError, so an error anywhere in
// the fetch/sift/approve workflow can never be silently swallowed.
func (b *ReviewStateBuilder) AddError(err error) {
	if b == nil || err == nil {
		return
	}
	b.errors = append(b.errors, err.Error())
}

// WithRepoRoot sets the repository root on the state being built. It is a no-op on a nil builder, mirroring AddEvent, and returns the receiver so the remaining steps chain together.
func (b *ReviewStateBuilder) WithRepoRoot(repoRoot string) *ReviewStateBuilder {
	if b == nil {
		return b
	}
	b.repoRoot = repoRoot
	return b
}

// WithFiles sets the per-file states on the state being built. It returns the receiver so the remaining steps chain together.
func (b *ReviewStateBuilder) WithFiles(files map[string]FileState) *ReviewStateBuilder {
	if b == nil {
		return b
	}
	b.files = files
	return b
}

// WithOverall sets the overall approval status computed by the algorithm on the state being built. It returns the receiver so the remaining steps chain together.
func (b *ReviewStateBuilder) WithOverall(overall Status) *ReviewStateBuilder {
	if b == nil {
		return b
	}
	b.overall = overall
	return b
}

// Build assembles the full review state from the accumulated events, errors, and steps. When errors have accumulated, the overall approval status is forced to StatusError and Err is set to a combined error so callers can always tell that the review did not run cleanly. This is the only step that reads back the state; all earlier steps only write into it.
func (b *ReviewStateBuilder) Build(baseErr error) *ReviewState {
	errors := b.errors
	overall := b.overall
	if len(errors) > 0 {
		overall = StatusError
		if baseErr == nil {
			baseErr = fmt.Errorf("%d error(s) during review: %s", len(errors), strings.Join(errors, "; "))
		}
	}
	return &ReviewState{
		RepoRoot: b.repoRoot,
		Files:    b.files,
		Overall:  overall,
		Events:   b.events,
		Errors:   errors,
		Err:      baseErr,
	}
}

// Resolver returns the ApproverResolver this builder was created with.
func (b *ReviewStateBuilder) Resolver() ApproverResolver {
	return b.resolver
}

// Summarize converts the review state back to the original evaluation result format for backward compatibility.
func (rs *ReviewState) Summarize() map[string]EvaluationResult {
	out := make(map[string]EvaluationResult, len(rs.Files))
	for file, fs := range rs.Files {
		var missing []string
		for _, t := range fs.Required {
			if !t.IsSatisfied() {
				missing = append(missing, t.Target)
			}
		}
		out[file] = EvaluationResult{
			IsApproved:        len(missing) == 0,
			RequiredApprovers: missing,
		}
	}
	return out
}

// IsSatisfied returns true if the approver target is satisfied.
func (ats *ApproverTargetState) IsSatisfied() bool {
	return ats.SatisfiedCount >= ats.RequiredCount
}
