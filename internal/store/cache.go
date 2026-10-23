package store

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/alphaeron/canopy/internal/server"
)

// ErrTampered is a cached row whose HMAC does not match: it was not
// written by a holder of the store's key, or was changed since. The row is
// not used.
var ErrTampered = errors.New("cached row fails its integrity check")

// Table names, sealed into each row's HMAC so a row cannot be moved
// between tables.
const (
	tableTrees     = "tree_listings"
	tableResponses = "responses"
)

// Tree returns the listing of repo at commit kept for scope.
func (s *Store) Tree(ctx context.Context, scope, repo, commit string) (map[string]server.TreeBlob, bool, error) {
	var listing, mac []byte
	err := s.pool.QueryRow(ctx, "SELECT listing, mac FROM tree_listings WHERE scope = $1 AND repo = $2 AND commit_sha = $3",
		scope, repo, commit).Scan(&listing, &mac)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read tree listing %s@%s: %w", repo, commit, err)
	}
	if !s.sealed(mac, []byte(tableTrees), []byte(scope), []byte(repo), []byte(commit), listing) {
		return nil, false, fmt.Errorf("tree listing %s@%s: %w", repo, commit, ErrTampered)
	}
	blobs, err := decodeListing(listing)
	if err != nil {
		return nil, false, fmt.Errorf("decode tree listing %s@%s: %w", repo, commit, err)
	}
	return blobs, true, nil
}

// PutTree keeps the listing of repo at commit for scope. A listing already
// kept is left as it is: a commit's listing never changes.
func (s *Store) PutTree(ctx context.Context, scope, repo, commit string, listing map[string]server.TreeBlob) error {
	encoded := encodeListing(listing)
	mac := s.seal([]byte(tableTrees), []byte(scope), []byte(repo), []byte(commit), encoded)
	_, err := s.pool.Exec(ctx, `INSERT INTO tree_listings (scope, repo, commit_sha, blob_count, listing, mac)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`, scope, repo, commit, len(listing), encoded, mac)
	if err != nil {
		return fmt.Errorf("keep tree listing %s@%s: %w", repo, commit, err)
	}
	return nil
}

// Blob returns the contents of blob sha kept for scope. They are not
// sealed: the reader checks them against sha (see server.RepoStore).
func (s *Store) Blob(ctx context.Context, scope, sha string) ([]byte, bool, error) {
	var content []byte
	err := s.pool.QueryRow(ctx, "SELECT content FROM blobs WHERE scope = $1 AND sha = $2", scope, sha).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read blob %s: %w", sha, err)
	}
	return content, true, nil
}

// PutBlob keeps the contents of blob sha for scope.
func (s *Store) PutBlob(ctx context.Context, scope, sha string, data []byte) error {
	_, err := s.pool.Exec(ctx, "INSERT INTO blobs (scope, sha, content) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", scope, sha, data)
	if err != nil {
		return fmt.Errorf("keep blob %s: %w", sha, err)
	}
	return nil
}

// Response returns the response to a GET of url kept for scope.
func (s *Store) Response(ctx context.Context, scope, url string) (server.CachedResponse, bool, error) {
	var (
		etag              string
		header, body, mac []byte
	)
	err := s.pool.QueryRow(ctx, "SELECT etag, header, body, mac FROM responses WHERE scope = $1 AND url = $2",
		scope, url).Scan(&etag, &header, &body, &mac)
	if errors.Is(err, pgx.ErrNoRows) {
		return server.CachedResponse{}, false, nil
	}
	if err != nil {
		return server.CachedResponse{}, false, fmt.Errorf("read response to %s: %w", url, err)
	}
	if !s.sealed(mac, []byte(tableResponses), []byte(scope), []byte(url), []byte(etag), header, body) {
		return server.CachedResponse{}, false, fmt.Errorf("response to %s: %w", url, ErrTampered)
	}
	r := server.CachedResponse{ETag: etag, Body: body}
	if err := json.Unmarshal(header, &r.Header); err != nil {
		return server.CachedResponse{}, false, fmt.Errorf("decode response to %s: %w", url, err)
	}
	return r, true, nil
}

// PutResponse keeps the response to a GET of url for scope, replacing the
// one kept before.
func (s *Store) PutResponse(ctx context.Context, scope, url string, r server.CachedResponse) error {
	header, err := json.Marshal(r.Header)
	if err != nil {
		return fmt.Errorf("encode response to %s: %w", url, err)
	}
	body := r.Body
	if body == nil {
		body = []byte{} // an empty body, not NULL
	}
	mac := s.seal([]byte(tableResponses), []byte(scope), []byte(url), []byte(r.ETag), header, body)
	_, err = s.pool.Exec(ctx, `INSERT INTO responses (scope, url, etag, header, body, mac) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (scope, url) DO UPDATE SET etag = $3, header = $4, body = $5, mac = $6, updated_at = now()`,
		scope, url, r.ETag, header, body, mac)
	if err != nil {
		return fmt.Errorf("keep response to %s: %w", url, err)
	}
	return nil
}

// listingVersion is the encoding encodeListing writes.
const listingVersion = 1

// encodeListing encodes a tree listing compactly: a version byte, then
// each blob, by path, as its length-prefixed path and SHA and its size.
// PostgreSQL compresses the large values it stores, so it is not
// compressed here.
func encodeListing(listing map[string]server.TreeBlob) []byte {
	out := []byte{listingVersion}
	for _, path := range slices.Sorted(maps.Keys(listing)) {
		b := listing[path]
		out = binary.AppendUvarint(out, uint64(len(path)))
		out = append(out, path...)
		out = binary.AppendUvarint(out, uint64(len(b.SHA)))
		out = append(out, b.SHA...)
		out = binary.AppendUvarint(out, uint64(b.Size))
	}
	return out
}

// errCorrupt is a listing encodeListing did not write.
var errCorrupt = errors.New("corrupt listing")

// decodeListing decodes what encodeListing encoded.
func decodeListing(data []byte) (map[string]server.TreeBlob, error) {
	if len(data) == 0 || data[0] != listingVersion {
		return nil, errCorrupt
	}
	data = data[1:]
	next := func() (uint64, bool) {
		v, n := binary.Uvarint(data)
		if n <= 0 {
			return 0, false
		}
		data = data[n:]
		return v, true
	}
	str := func() (string, bool) {
		n, ok := next()
		if !ok || n > uint64(len(data)) {
			return "", false
		}
		s := string(data[:n])
		data = data[n:]
		return s, true
	}
	listing := map[string]server.TreeBlob{}
	for len(data) > 0 {
		path, ok1 := str()
		sha, ok2 := str()
		size, ok3 := next()
		if !ok1 || !ok2 || !ok3 {
			return nil, errCorrupt
		}
		listing[path] = server.TreeBlob{SHA: sha, Size: int64(size)}
	}
	return listing, nil
}

var _ server.RepoStore = (*Store)(nil)
