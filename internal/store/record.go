package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphaeron/canopy/internal/server"
)

// RecordDelivery records a verified webhook delivery.
func (s *Store) RecordDelivery(ctx context.Context, d server.DeliveryRecord) error {
	var action, scope, repo, head *string
	var number *int
	if pr := d.PullRequest; pr != nil {
		action, scope, repo, head, number = &pr.Action, &pr.Scope, &pr.Repo, &pr.HeadSHA, &pr.Number
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO webhook_deliveries
		(platform, delivery_id, event, action, scope, repo, pr_number, head_sha, disposition)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		d.Platform, d.ID, d.Event, action, scope, repo, number, head, d.Disposition)
	if err != nil {
		return fmt.Errorf("record delivery %s: %w", d.ID, err)
	}
	return nil
}

// RequestReview records a review of pr being requested and returns its
// order: one more than its last request's, on any replica.
func (s *Store) RequestReview(ctx context.Context, pr server.PullRequestInfo) (int64, error) {
	var order int64
	err := s.pool.QueryRow(ctx, `INSERT INTO pull_requests (scope, repo, pr_number, requested_order) VALUES ($1, $2, $3, 1)
		ON CONFLICT (scope, repo, pr_number) DO UPDATE SET requested_order = pull_requests.requested_order + 1, updated_at = now()
		RETURNING requested_order`, pr.Scope, pr.Repo, pr.Number).Scan(&order)
	if err != nil {
		return 0, fmt.Errorf("record review request for %s#%d: %w", pr.Repo, pr.Number, err)
	}
	return order, nil
}

// StartReview records a review starting and returns its ID.
func (s *Store) StartReview(ctx context.Context, r server.ReviewStart) (int64, error) {
	pr := r.PullRequest
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO reviews
		(delivery_id, platform, scope, repo, pr_number, base_sha, head_sha, author, action, review_key, request_order, queued_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
		r.DeliveryID, r.Platform, pr.Scope, pr.Repo, pr.Number, pr.BaseSHA, pr.HeadSHA, pr.Author, pr.Action,
		r.Key, r.Order, r.QueuedAt).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("record review of %s: %w", r.Key, err)
	}
	return id, nil
}

// recordAfterReportTimeout bounds recording a published report, which
// runs after its caller's context may have ended.
const recordAfterReportTimeout = 5 * time.Second

// ReportInOrder calls report unless a review of pr at order or later has
// reported. It holds a transaction-scoped advisory lock on pr from
// checking the order until the report is recorded, so the reports of one
// pull request, on every replica, are published one at a time and in
// order. The lock is advisory, not on pr's row, so requests for pr are
// not held up by a report in flight. The transaction, and a pooled
// connection, are held while report publishes.
//
// When report succeeds but recording it fails, err says so: the report was
// published, but a review requested earlier may still report after it.
func (s *Store) ReportInOrder(ctx context.Context, pr server.PullRequestInfo, order int64, report func(context.Context) error) (bool, error) {
	name := fmt.Sprintf("%s#%d", pr.Repo, pr.Number)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("order report of %s: %w", name, err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	lock := pr.Scope + "\n" + pr.Repo + "\n" + strconv.Itoa(pr.Number) // text cannot hold NUL
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lock); err != nil {
		return false, fmt.Errorf("order report of %s: %w", name, err)
	}
	var reported int64
	err = tx.QueryRow(ctx, "SELECT reported_order FROM pull_requests WHERE scope = $1 AND repo = $2 AND pr_number = $3",
		pr.Scope, pr.Repo, pr.Number).Scan(&reported)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("order report of %s: %w", name, err)
	}
	if reported >= order {
		return false, nil
	}
	if err := report(ctx); err != nil {
		return true, err
	}
	// The report is published: record it even if ctx ends now (the review
	// timed out, the server is stopping), or an older review could report
	// after it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordAfterReportTimeout)
	defer cancel()
	// A pull request with no row (it was pruned) gets one, so later
	// reports stay ordered after this one.
	_, err = tx.Exec(ctx, `INSERT INTO pull_requests (scope, repo, pr_number, requested_order, reported_order) VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (scope, repo, pr_number) DO UPDATE SET reported_order = $4,
			requested_order = GREATEST(pull_requests.requested_order, $4), updated_at = now()`,
		pr.Scope, pr.Repo, pr.Number, order)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return true, fmt.Errorf("reported, but recording the report of %s failed: %w", name, err)
	}
	return true, nil
}

// reviewInput is the JSON of a review input as recorded.
type reviewInput struct {
	Author    string   `json:"author"`
	Approvers []string `json:"approvers"`
	Owners    []string `json:"owners"`
}

// FinishReview records how review id ended, and the targets it evaluated.
func (s *Store) FinishReview(ctx context.Context, id int64, r server.ReviewEnd) error {
	var input, state []byte
	var err error
	if r.Input != nil {
		if input, err = json.Marshal(reviewInput{Author: r.Input.Author, Approvers: r.Input.Approvers, Owners: r.Input.Owners}); err != nil {
			return fmt.Errorf("encode review %d input: %w", id, err)
		}
	}
	if r.State != nil {
		if state, err = json.Marshal(r.State); err != nil {
			return fmt.Errorf("encode review %d state: %w", id, err)
		}
	}
	var errText *string
	if r.Err != "" {
		errText = &r.Err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE reviews SET status = $2, verdict = $3, summary = $4, items = $5, warnings = $6,
			input = $7, state = $8, error = $9, queued_ms = $10, evaluate_ms = $11, report_ms = $12, finished_at = now()
			WHERE id = $1`,
			id, r.Status, string(r.Outcome.Verdict), r.Outcome.Summary, r.Outcome.Items, r.Outcome.Warnings,
			input, state, errText, millis(r.Queued), millis(r.Evaluate), millis(r.Report))
		if err != nil {
			return fmt.Errorf("record end of review %d: %w", id, err)
		}
		rows := make([][]any, len(r.Targets))
		for i, t := range r.Targets {
			rows[i] = []any{id, t.Target, t.Resolved}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"review_targets"}, []string{"review_id", "target", "resolved"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("record targets of review %d: %w", id, err)
		}
		return nil
	})
}

// millis is d in whole milliseconds, at least 0, at most what an integer
// column holds.
func millis(d time.Duration) int32 {
	return int32(min(max(d.Milliseconds(), 0), 1<<31-1))
}

var _ server.Recorder = (*Store)(nil)
