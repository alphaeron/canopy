// Package store is the review server's PostgreSQL database: caches of
// immutable or revalidated platform data (a server.RepoStore), and the
// record of the webhooks the server received and the reviews it ran (a
// server.Recorder, read back for review pages as a server.ReviewHistory).
// schema.sql documents each table and what may and may
// not be read from it.
package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// schemaLockID is the advisory lock ApplySchema holds, so replicas starting
// together apply the schema one at a time: concurrent CREATE ... IF NOT
// EXISTS statements can otherwise fail on each other.
const schemaLockID = 0x63616e6f7079 // "canopy"

// MinKeyBytes is the shortest integrity key Open accepts.
const MinKeyBytes = 32

// Options configure a Store.
type Options struct {
	// URL is the PostgreSQL connection string. It carries credentials:
	// errors never include it.
	URL string
	// Key authenticates the cached rows the store writes (see seal), at
	// least MinKeyBytes long. It must be the same on every replica and
	// never stored in the database: a row a holder of the key did not
	// write is a cache miss. Changing it turns every cached row into a
	// miss.
	Key []byte
	// MinConns is the least the pool's connection limit may be: each
	// report in flight holds a connection while it is published (see
	// ReportInOrder), so it should exceed the reviews the server runs at
	// once. A larger pool_max_conns in URL wins.
	MinConns int32
}

// Store is a connection pool to the review server's database. It is safe
// for concurrent use.
type Store struct {
	pool *pgxpool.Pool
	key  []byte
}

// Open connects to the database and checks that it answers. The caller
// must Close the Store.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if len(opts.Key) < MinKeyBytes {
		return nil, fmt.Errorf("database key: want at least %d bytes, got %d", MinKeyBytes, len(opts.Key))
	}
	cfg, err := pgxpool.ParseConfig(opts.URL)
	if err != nil {
		// pgx's parse errors quote the connection string, redacting
		// passwords only on a best-effort basis.
		return nil, errors.New("parse database URL: not a valid PostgreSQL connection string")
	}
	cfg.MaxConns = max(cfg.MaxConns, opts.MinConns)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return &Store{pool: pool, key: append([]byte(nil), opts.Key...)}, nil
}

// Close closes every connection in the pool.
func (s *Store) Close() { s.pool.Close() }

// ApplySchema creates the tables and indexes schema.sql defines that the
// database does not have yet, in one transaction.
func (s *Store) ApplySchema(ctx context.Context) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(schemaLockID)); err != nil {
			return err
		}
		// Without arguments, Exec uses the simple protocol, which runs
		// every statement in the script.
		_, err := tx.Exec(ctx, schema)
		return err
	})
	if err != nil {
		return fmt.Errorf("apply database schema: %w", err)
	}
	return nil
}

// seal is the HMAC-SHA256, under the store's key, of a row's fields: its
// table and every key and value column, each length-prefixed so no two
// rows' fields run together into the same input. A row whose mac does not
// match was not written by a holder of the key.
func (s *Store) seal(fields ...[]byte) []byte {
	m := hmac.New(sha256.New, s.key)
	for _, f := range fields {
		_, _ = m.Write(binary.AppendUvarint(nil, uint64(len(f))))
		_, _ = m.Write(f)
	}
	return m.Sum(nil)
}

// sealed reports whether mac seals fields.
func (s *Store) sealed(mac []byte, fields ...[]byte) bool {
	return hmac.Equal(mac, s.seal(fields...))
}
