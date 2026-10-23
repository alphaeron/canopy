package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alphaeron/canopy/internal/logx"
)

// How long Prune keeps rows, by what they record. A pruned cache row costs
// one more read from the platform the next time a review needs it:
//
//   - tree listings are of base commits, which a pull request's base
//     branch soon moves past;
//   - rules files rarely change, so their blobs are reread monthly;
//   - responses are kept while they are replaced (a changed answer), and
//     their age does not count a 304 that confirmed them, so a response
//     still current is reread weekly;
//   - a pull request's ordering row is removed once no review has been
//     requested or reported for it for a month. Its next request starts
//     again at order 1, which is safe only because no review of it can
//     still be running: reviews and their reports are bounded by minutes
//     (review_timeout, report_timeout), far less than the retention.
const (
	treeRetention        = 7 * 24 * time.Hour
	blobRetention        = 30 * 24 * time.Hour
	responseRetention    = 7 * 24 * time.Hour
	pullRequestRetention = 30 * 24 * time.Hour
)

// pruneBatch is how many rows one delete removes, so pruning never holds
// many locks or long transactions while reviews read and write.
const pruneBatch = 1000

// prunable is a table Prune removes rows from: those whose column is
// older than retention.
type prunable struct {
	table, column string
	retention     time.Duration
}

var prunables = []prunable{
	{"tree_listings", "created_at", treeRetention},
	{"blobs", "created_at", blobRetention},
	{"responses", "updated_at", responseRetention},
	{"pull_requests", "updated_at", pullRequestRetention},
}

// Prune removes the rows past their retention as of now, in batches, and
// returns how many it removed from each table. Replicas may prune at the
// same time: each batch skips the rows another is removing.
func (s *Store) Prune(ctx context.Context, now time.Time) (map[string]int64, error) {
	pruned := map[string]int64{}
	for _, p := range prunables {
		// Table and column names are the constants above, never input.
		sql := fmt.Sprintf(`DELETE FROM %[1]s WHERE ctid = ANY(ARRAY(
			SELECT ctid FROM %[1]s WHERE %[2]s < $1 LIMIT $2 FOR UPDATE SKIP LOCKED))`, p.table, p.column)
		cutoff := now.Add(-p.retention)
		for {
			tag, err := s.pool.Exec(ctx, sql, cutoff, pruneBatch)
			if err != nil {
				return pruned, fmt.Errorf("prune %s: %w", p.table, err)
			}
			pruned[p.table] += tag.RowsAffected()
			if tag.RowsAffected() < pruneBatch {
				break
			}
		}
	}
	return pruned, nil
}

// PruneEvery prunes now and then every interval until ctx is done, logging
// what it removed and any failure, which the next round retries.
func (s *Store) PruneEvery(ctx context.Context, interval time.Duration, log *slog.Logger) {
	log = logx.OrDefault(log).With("component", "store")
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		pruned, err := s.Prune(ctx, time.Now())
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			log.Warn("pruning the database failed", "error", err)
		default:
			log.Debug("database pruned", "rows", pruned)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
