package shonin

import (
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
	RepoRoot    string
	HeadRef     string
	Files       map[string]FileState
	Overall     Status
	Events      []Event // chronological trace
	Err         error   // surfaced, not swallowed
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
	DirectoryPath     string
	Rules             []RuleState
	CombinedStrategy  kitfile.RuleCombineStrategy
	ResolvedBy        []string   // active individuals/teams that contributed
	Active            bool
	Status            Status
	Reason            string
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
	Target        string
	RequiredCount int
	SatisfiedCount int
	Satisfied     bool
	ResolvedBy    []string // individuals/teams that contributed to satisfaction
	Status        Status
	Reason        string
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

// ApproverResolver resolves approver targets to actual users/teams.
type ApproverResolver interface {
	Resolve(target string, ctx EvaluationContext) ([]string, bool) // target → active members, ok
}

// DefaultApproverResolver is a stub resolver that returns the target as-is.
// This preserves current behavior during migration.
type DefaultApproverResolver struct{}

func (r *DefaultApproverResolver) Resolve(target string, ctx EvaluationContext) ([]string, bool) {
	return []string{target}, true
}

// ReviewStateBuilder builds the complete review state from the algorithm execution.
type ReviewStateBuilder struct {
	resolver ApproverResolver
	events   []Event
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

// AddEvent adds an event to the review state.
func (b *ReviewStateBuilder) AddEvent(phase, path, layer, rule string, status Status, note string) {
	b.events = append(b.events, Event{
		Phase:  phase,
		Path:   path,
		Layer:  layer,
		Rule:   rule,
		Status: status,
		Note:   note,
	})
}

// BuildReviewState builds a complete review state from the current execution.
func (b *ReviewStateBuilder) BuildReviewState(repoRoot, headRef string, files map[string]FileState, overall Status, err error) *ReviewState {
	return &ReviewState{
		RepoRoot: repoRoot,
		HeadRef:  headRef,
		Files:    files,
		Overall:  overall,
		Events:   b.events,
		Err:      err,
	}
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
