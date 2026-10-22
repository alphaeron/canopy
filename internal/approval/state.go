package approval

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/alphaeron/kit/internal/rulesfile"
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
	// RepoRoot is the on-disk repository root when the review ran over a
	// local checkout, and empty otherwise. The approval pipeline never
	// knows it: the caller that does (review.Engine) sets it.
	RepoRoot string
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
// It embeds the rule's configuration by value - a historical snapshot of the
// rule at the time the algorithm ran for this iteration - so a review state
// can be traced back to the rule that produced it without duplicating fields.
// The embedded copy captures the effective configuration that was actually
// applied, not the raw config: a corrupt or omitted combine_strategy is
// rewritten to the default (override) so the state is accurate about what
// ran, even if the config file later changes.
//
// The Approvers field shadows the embedded config's approver list; where the
// type differs (state, not config), read it directly, and the raw
// configuration target list is still available on Rule.Approvers.
type RuleState struct {
	rulesfile.Rule
	Approvers  []ApproverTargetState
	ResolvedBy []string // individuals/teams that satisfied this rule
	Status     Status
	Reason     string
}

// ApproverTargetState represents the state of an approver target in the
// review. It embeds the approver target's configuration by value,
// preserving the historical snapshot semantics of RuleState.
//
// Satisfied is a convenience boolean for rendering and test assertions; the
// canonical fact of satisfaction is Status == StatusSatisfied, and every
// constructor of this type keeps the two in sync. All status escalation in
// the hierarchy (rule, layer, file status) therefore reads Status, never the
// boolean, so the algorithm has exactly one definition of "unsatisfied":
// Status != StatusSatisfied.
type ApproverTargetState struct {
	rulesfile.ApproverTarget
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
	events  []Event
	errors  []string
	files   map[string]FileState
	overall Status
}

// NewReviewStateBuilder creates a new review state builder.
func NewReviewStateBuilder() *ReviewStateBuilder {
	return &ReviewStateBuilder{events: make([]Event, 0)}
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
		Files:   b.files,
		Overall: overall,
		Events:  b.events,
		Errors:  errors,
		Err:     baseErr,
	}
}

// firstMatching returns the first element of items for which pred holds, or
// the zero value and false when none do. It is a generic first-match
// predicate over any state type the hierarchy holds (rules, layers, files,
// approver targets), so the per-level status escalation and any other
// "find the first item that matches this test" walk share one helper and
// one definition of what "matching" means. The canonical unsatisfied test
// across state levels is Status != StatusSatisfied; a site that looks for a
// strictly-worse status (for example Unresolved, which beats
// StatusMissingApprovers) is a severity query and passes its own predicate
// instead of the canonical one.
func firstMatching[T any](items []T, pred func(T) bool) (T, bool) {
	for _, item := range items {
		if pred(item) {
			return item, true
		}
	}
	var zero T
	return zero, false
}

// attention ranks a file status by how much it needs attention; a lower
// rank needs more: errors, then unresolved targets, then missing approvals,
// then anything else, then satisfied.
func attention(s Status) int {
	switch s {
	case StatusError:
		return 0
	case StatusUnresolved:
		return 1
	case StatusMissingApprovers:
		return 2
	case StatusSatisfied:
		return 4
	default:
		return 3
	}
}

// SortedFiles lists the review's files, most in need of attention first,
// then by path.
func (rs *ReviewState) SortedFiles() []FileState {
	out := slices.Collect(maps.Values(rs.Files))
	slices.SortFunc(out, func(a, b FileState) int {
		return cmp.Or(cmp.Compare(attention(a.Status), attention(b.Status)), cmp.Compare(a.Path, b.Path))
	})
	return out
}

// RequiredTargets is every approver target the review's files require, each
// with the most approvals any one file needs from it, sorted by target, and
// how many files fall back to the repository owners instead.
func (rs *ReviewState) RequiredTargets() (targets rulesfile.ApproverTargets, fallback int) {
	need := map[string]int{}
	for _, fs := range rs.Files {
		if fs.UsedFallback {
			fallback++
			continue
		}
		for _, r := range fs.Required {
			need[r.Target] = max(need[r.Target], r.RequiredCount)
		}
	}
	for _, t := range slices.Sorted(maps.Keys(need)) {
		targets = append(targets, rulesfile.ApproverTarget{Target: t, RequiredCount: need[t]})
	}
	return targets, fallback
}
