package server

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/alphaeron/canopy/internal/approval"
)

// Recorder keeps the server's record of the webhooks it received and the
// reviews it ran, shared by every replica of the server, and orders the
// replicas' reports. The record is history for people to read: the server
// never reads a verdict, an approval, or a team's members back from it.
type Recorder interface {
	// RecordDelivery records a verified webhook delivery.
	RecordDelivery(ctx context.Context, d DeliveryRecord) error
	// RequestReview records that a review of pr was requested, and
	// returns the request's order: greater than that of every earlier
	// request to review pr, of any of its head commits, on any replica.
	RequestReview(ctx context.Context, pr PullRequestInfo) (int64, error)
	// StartReview records a review starting and returns its ID.
	StartReview(ctx context.Context, r ReviewStart) (int64, error)
	// ReportInOrder calls report unless a review of pr requested at order
	// or later has already reported, holding off every other report of pr
	// until report returns, and records order as reported when report
	// succeeds. Reports are ordered per pull request rather than per head
	// commit: a review superseded by a newer one of another head only
	// skips reporting on a commit that is no longer the head. called
	// reports whether report was called; if it was, err is report's.
	// Otherwise err means the order could not be checked.
	ReportInOrder(ctx context.Context, pr PullRequestInfo, order int64, report func(context.Context) error) (called bool, err error)
	// FinishReview records how review id ended.
	FinishReview(ctx context.Context, id int64, r ReviewEnd) error
}

// What became of a verified webhook delivery.
const (
	DeliveryQueued    = "queued"     // it started a review
	DeliveryIgnored   = "ignored"    // it does not start reviews
	DeliveryUnusable  = "unusable"   // the platform could not read it
	DeliveryQueueFull = "queue_full" // it was dropped: the queue was full
)

// DeliveryRecord is a verified webhook delivery as the server records it.
type DeliveryRecord struct {
	Platform string
	// ID is the platform's delivery ID (see PRWebhook.DeliveryID).
	ID          string
	Event       string
	Disposition string
	// PullRequest is the pull request the delivery names; nil when it
	// names none the server could read.
	PullRequest *PullRequestInfo
}

// ReviewStart is a review as the server records it when it starts.
type ReviewStart struct {
	Platform    string
	DeliveryID  string
	PullRequest PullRequestInfo
	Key         string
	// Order is the review's request order (see Recorder.RequestReview);
	// 0 when the request could not be recorded.
	Order    int64
	QueuedAt time.Time
}

// How a review ended.
const (
	ReviewReported     = "reported"      // its outcome was published
	ReviewSuperseded   = "superseded"    // a newer review of its key replaced it
	ReviewCancelled    = "cancelled"     // the server stopped
	ReviewReportFailed = "report_failed" // publishing its outcome failed
)

// ReviewEnd is how a review ended, as the server records it.
type ReviewEnd struct {
	// Status is one of the Review* constants.
	Status string
	// Outcome is the outcome the review reached, or the one it published
	// when that differs (see Server.report).
	Outcome approval.Outcome
	// Input is the review input the review used; nil when it was not read.
	Input *approval.ReviewInput
	// State is the review's state; nil when the review did not run.
	State *approval.ReviewState
	// Targets is every approver target the review evaluated.
	Targets []TargetRecord
	// Err is why the review could not run, or its report failed, for
	// operators; "" when nothing failed.
	Err      string
	Queued   time.Duration // from the request to the review starting
	Evaluate time.Duration // evaluating the pull request
	Report   time.Duration // publishing the outcome
}

// TargetRecord is an approver target a review evaluated: the pull
// requests whose reviews depend on a team are found by it.
type TargetRecord struct {
	Target string
	// Resolved is false when the target could not be resolved.
	Resolved bool
}

// targetsOf lists every approver target state evaluated, sorted, each
// once: resolved unless every evaluation of it failed to resolve it.
func targetsOf(state *approval.ReviewState) []TargetRecord {
	if state == nil {
		return nil
	}
	resolved := map[string]bool{}
	add := func(ts []approval.ApproverTargetState) {
		for _, t := range ts {
			resolved[t.Target] = resolved[t.Target] || t.Status != approval.StatusUnresolved
		}
	}
	for _, fs := range state.Files {
		add(fs.Required)
		for _, l := range fs.Layers {
			for _, r := range l.Rules {
				add(r.Approvers)
			}
		}
	}
	out := make([]TargetRecord, 0, len(resolved))
	for _, t := range slices.Sorted(maps.Keys(resolved)) {
		out = append(out, TargetRecord{Target: t, Resolved: resolved[t]})
	}
	return out
}
