package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphaeron/canopy/internal/store"
)

// testDatabaseEnv names a PostgreSQL URL the end-to-end tests may create
// schemas in. With it, the review server runs with a database (as its
// cache and recorder); without it, it runs without one.
const testDatabaseEnv = "CANOPY_TEST_DATABASE_URL"

// testDatabase is a store over a schema of its own in the test database,
// dropped when the test ends, and a connection to that schema for the
// test's checks; nil, nil without a test database.
func testDatabase(t *testing.T) (*store.Store, *pgx.Conn) {
	t.Helper()
	base := os.Getenv(testDatabaseEnv)
	if base == "" {
		return nil, nil
	}
	name := "canopy_e2e_" + strings.ToLower(rand.Text())
	admin, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect to %s: %v", testDatabaseEnv, err)
	}
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Errorf("drop schema %s: %v", name, err)
		}
		_ = admin.Close(ctx)
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("%s must be a URL: %v", testDatabaseEnv, err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	db, err := store.Open(t.Context(), store.Options{URL: u.String(), Key: bytes.Repeat([]byte("e"), store.MinKeyBytes), MinConns: 8})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplySchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(t.Context(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()); db.Close() })
	return db, conn
}

// waitRecorded waits until n reviews have finished in the record, so the
// server is not stopped while it reads the answer to a report it made.
func (e *env) waitRecorded(n int) {
	e.t.Helper()
	if e.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stepTimeout)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var finished int
		if err := e.db.QueryRow(ctx, "SELECT count(*) FROM reviews WHERE status <> 'running'").Scan(&finished); err != nil {
			e.t.Fatalf("waiting for %d recorded reviews: %v", n, err)
		}
		if finished >= n {
			return
		}
		select {
		case <-ctx.Done():
			e.t.Fatalf("waiting for %d recorded reviews: %v", n, ctx.Err())
		case <-tick.C:
		}
	}
}

// assertRecorded checks the database's record of a finished test: every
// delivery recorded, no review left running, and one reported review per
// check run, all of them ordered.
func (e *env) assertRecorded(deliveries, checkRuns int) {
	e.t.Helper()
	if e.db == nil {
		return
	}
	var recorded, running, reported int
	var tree, blobs int
	err := e.db.QueryRow(context.Background(), `SELECT
		(SELECT count(*) FROM webhook_deliveries),
		(SELECT count(*) FROM reviews WHERE status = 'running'),
		(SELECT count(*) FROM reviews WHERE status = 'reported' AND request_order > 0),
		(SELECT count(*) FROM tree_listings),
		(SELECT count(*) FROM blobs)`).Scan(&recorded, &running, &reported, &tree, &blobs)
	if err != nil {
		e.t.Fatalf("read the record: %v", err)
	}
	if recorded != deliveries || running != 0 || reported != checkRuns {
		e.t.Errorf("record: %d deliveries, %d running, %d reported; want %d, 0, %d", recorded, running, reported, deliveries, checkRuns)
		rows, _ := e.db.Query(context.Background(), "SELECT id, status, request_order, coalesce(verdict, ''), coalesce(error, '') FROM reviews ORDER BY id")
		for rows.Next() {
			var id, order int64
			var status, verdict, msg string
			_ = rows.Scan(&id, &status, &order, &verdict, &msg)
			e.t.Logf("review %d: %s order %d %s %s", id, status, order, verdict, msg)
		}
		rows.Close()
	}
	if tree == 0 || blobs == 0 {
		e.t.Errorf("cached %d tree listings and %d blobs, want the base's", tree, blobs)
	}
}
