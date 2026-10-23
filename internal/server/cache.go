package server

// The review server's caches live here: the server's entry point builds
// them and hands them to what reads through them (the platform), so every
// cache's bounds and lifetime are set in one place.

import (
	"context"
	"crypto/sha1" //nolint:gosec // git names SHA-1 repositories' objects by their SHA-1
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/alphaeron/canopy/internal/logx"
	"github.com/alphaeron/canopy/internal/parallel"
	"github.com/alphaeron/canopy/internal/rulesfile"
)

// Bounds on the repository contents the server keeps in memory. A tree
// listing can run to many thousands of paths, so few are kept, and one
// larger than maxCachedTreeBlobs is not kept at all, in memory or in the
// store; rules files are small, so a blob larger than maxCachedBlobBytes is
// not kept in memory (the store keeps any rules file). A response larger
// than maxCachedResponseBytes is not kept at all.
const (
	maxCachedTrees         = 8
	maxCachedTreeBlobs     = 200_000
	maxCachedBlobs         = 1024
	maxCachedBlobBytes     = 64 << 10
	maxStoredBlobBytes     = rulesfile.MaxSize
	maxCachedResponses     = 512
	maxCachedResponseBytes = 256 << 10
)

// Bounds on writing to the store in the background: how many writes run at
// once (more are dropped, costing a later read a miss) and how long each
// may take.
const (
	maxStoreWrites    = 16
	storeWriteTimeout = 10 * time.Second
)

// TreeBlob is a blob a tree listing names: its SHA and its size in bytes.
type TreeBlob struct {
	SHA  string
	Size int64
}

// CachedResponse is a platform's answer to a request whose answer can
// change (a team's members, a user), kept with its validator so that the
// platform can be asked whether it changed instead of sending it again
// (HTTP's If-None-Match). It is only ever used once the platform has
// confirmed it is current.
type CachedResponse struct {
	// ETag is the response's entity tag, sent back as If-None-Match.
	ETag string
	// Header holds the response headers a reader of the body needs, such
	// as a listing's Link; never credentials.
	Header http.Header
	Body   []byte
}

// clone is a deep copy of c.
func (c CachedResponse) clone() CachedResponse {
	return CachedResponse{ETag: c.ETag, Header: c.Header.Clone(), Body: slices.Clone(c.Body)}
}

// RepoStore keeps what a RepoCache keeps durably, shared between server
// replicas, behind the cache's memory. Every method takes the cache scope
// (see RepoCache.Scope) as its first key, and must never answer for one
// scope with what was put for another. A miss is (zero, false, nil); an
// error is a failure, which the cache treats as a miss.
//
// What a store returns is trusted no further than it can be checked: the
// cache verifies a blob against its SHA, and a store must make its tree
// listings and responses tamper-evident, so that write access to the store
// alone cannot change the rules a review enforces or the members a team
// has.
type RepoStore interface {
	Tree(ctx context.Context, scope, repo, commit string) (map[string]TreeBlob, bool, error)
	PutTree(ctx context.Context, scope, repo, commit string, listing map[string]TreeBlob) error
	Blob(ctx context.Context, scope, sha string) ([]byte, bool, error)
	PutBlob(ctx context.Context, scope, sha string, data []byte) error
	Response(ctx context.Context, scope, url string) (CachedResponse, bool, error)
	PutResponse(ctx context.Context, scope, url string, r CachedResponse) error
}

// RepoCache keeps what a platform read, across reviews: tree listings and
// blob contents, which are immutable once the platform has them (a listing
// is keyed by its repository and commit, and a blob by its SHA, a hash of
// its contents), so nothing kept goes stale; and responses that can change,
// which are only used once the platform confirms them (see CachedResponse).
// It keeps them in memory, bounded, and in a RepoStore when it has one,
// writing to the store in the background. Entries are read and written
// only through a CacheScope. A nil RepoCache keeps nothing. It is safe for
// concurrent use.
type RepoCache struct {
	trees     *bounded[string, map[string]TreeBlob]
	blobs     *bounded[string, []byte]
	responses *bounded[string, CachedResponse]
	store     RepoStore
	writes    *parallel.Background
	log       *slog.Logger
}

// NewRepoCache is an empty RepoCache within the package's bounds, backed by
// store; a nil store keeps entries in memory only. log receives store
// failures, which the cache treats as misses; nil means slog.Default().
func NewRepoCache(store RepoStore, log *slog.Logger) *RepoCache {
	r := &RepoCache{
		trees:     newBounded[string, map[string]TreeBlob](maxCachedTrees),
		blobs:     newBounded[string, []byte](maxCachedBlobs),
		responses: newBounded[string, CachedResponse](maxCachedResponses),
		store:     store,
		log:       logx.OrDefault(log).With("component", "cache"),
	}
	if store != nil {
		r.writes = parallel.NewBackground(maxStoreWrites, storeWriteTimeout)
	}
	return r
}

// Wait blocks until the writes to the store started so far are done, so
// the store can be closed.
func (r *RepoCache) Wait() {
	if r != nil {
		r.writes.Wait()
	}
}

// Scope is r as one reader sees it, e.g. one installation of a platform's
// app: name must identify whatever limits what the reader may see, so no
// reader is served an entry another scope kept.
func (r *RepoCache) Scope(name string) CacheScope { return CacheScope{repo: r, name: name} }

// write stores in the background with put, logging a failure.
func (r *RepoCache) write(what string, put func(ctx context.Context) error) {
	r.writes.Go(func(ctx context.Context) {
		if err := put(ctx); err != nil {
			r.log.Warn("store write failed", "entry", what, "error", err)
		}
	})
}

// stored reads from the store with get, treating a failure as a miss.
func stored[T any](ctx context.Context, r *RepoCache, what string, get func(RepoStore) (T, bool, error)) (T, bool) {
	var zero T
	if r.store == nil {
		return zero, false
	}
	v, ok, err := get(r.store)
	if err != nil {
		r.log.Warn("store read failed", "entry", what, "error", err)
		return zero, false
	}
	return v, ok
}

// CacheScope is a RepoCache seen by one reader (see RepoCache.Scope). The
// zero CacheScope keeps nothing. It is safe for concurrent use.
type CacheScope struct {
	repo *RepoCache
	name string
}

// Name is the scope's name, as RepoCache.Scope was given it.
func (s CacheScope) Name() string { return s.name }

// treeKey names the listing of repo at commit.
func (s CacheScope) treeKey(repo, commit string) string {
	return s.name + "/tree/" + repo + "@" + commit
}

// blobKey names the blob sha, whose contents are the same in every
// repository.
func (s CacheScope) blobKey(sha string) string { return s.name + "/blob/" + sha }

// responseKey names the response to a GET of url.
func (s CacheScope) responseKey(url string) string { return s.name + "/response/" + url }

// Tree returns the kept listing of repo at commit, path to blob. It is
// shared: callers must not modify it.
func (s CacheScope) Tree(ctx context.Context, repo, commit string) (map[string]TreeBlob, bool) {
	if s.repo == nil {
		return nil, false
	}
	key := s.treeKey(repo, commit)
	if listing, ok := s.repo.trees.get(key); ok {
		return listing, true
	}
	listing, ok := stored(ctx, s.repo, key, func(st RepoStore) (map[string]TreeBlob, bool, error) {
		return st.Tree(ctx, s.name, repo, commit)
	})
	if !ok || len(listing) > maxCachedTreeBlobs {
		return nil, false
	}
	s.repo.trees.put(key, listing)
	return listing, true
}

// PutTree keeps the listing of repo at commit, unless it has more than
// maxCachedTreeBlobs blobs. The listing is shared from then on, so callers
// must not modify it.
func (s CacheScope) PutTree(repo, commit string, listing map[string]TreeBlob) {
	if s.repo == nil || len(listing) > maxCachedTreeBlobs {
		return
	}
	key := s.treeKey(repo, commit)
	s.repo.trees.put(key, listing)
	if s.repo.store != nil {
		s.repo.write(key, func(ctx context.Context) error { return s.repo.store.PutTree(ctx, s.name, repo, commit, listing) })
	}
}

// Blob returns a copy of the kept contents of blob sha. Contents read from
// the store are used only if they hash to sha.
func (s CacheScope) Blob(ctx context.Context, sha string) ([]byte, bool) {
	if s.repo == nil {
		return nil, false
	}
	key := s.blobKey(sha)
	if data, ok := s.repo.blobs.get(key); ok {
		return slices.Clone(data), true
	}
	data, ok := stored(ctx, s.repo, key, func(st RepoStore) ([]byte, bool, error) { return st.Blob(ctx, s.name, sha) })
	if !ok {
		return nil, false
	}
	if !blobHasSHA(data, sha) {
		s.repo.log.Warn("stored blob does not match its SHA; ignoring it", "entry", key)
		return nil, false
	}
	if len(data) <= maxCachedBlobBytes {
		s.repo.blobs.put(key, slices.Clone(data))
	}
	return data, true
}

// PutBlob keeps a copy of the contents of blob sha: in memory unless they
// are larger than maxCachedBlobBytes, and in the store unless they are
// larger than a rules file may be.
func (s CacheScope) PutBlob(sha string, data []byte) {
	if s.repo == nil {
		return
	}
	key := s.blobKey(sha)
	if len(data) <= maxCachedBlobBytes {
		s.repo.blobs.put(key, slices.Clone(data))
	}
	if s.repo.store != nil && len(data) <= maxStoredBlobBytes {
		data = slices.Clone(data)
		s.repo.write(key, func(ctx context.Context) error { return s.repo.store.PutBlob(ctx, s.name, sha, data) })
	}
}

// Response returns a copy of the kept response to a GET of url. The caller
// must have the platform confirm it is current (its ETag) before using it.
func (s CacheScope) Response(ctx context.Context, url string) (CachedResponse, bool) {
	if s.repo == nil {
		return CachedResponse{}, false
	}
	key := s.responseKey(url)
	if r, ok := s.repo.responses.get(key); ok {
		return r.clone(), true
	}
	r, ok := stored(ctx, s.repo, key, func(st RepoStore) (CachedResponse, bool, error) { return st.Response(ctx, s.name, url) })
	if !ok || r.ETag == "" {
		return CachedResponse{}, false
	}
	s.repo.responses.put(key, r.clone())
	return r, true
}

// PutResponse keeps a copy of the response to a GET of url, unless it has
// no ETag or its body is larger than maxCachedResponseBytes.
func (s CacheScope) PutResponse(url string, r CachedResponse) {
	if s.repo == nil || r.ETag == "" || len(r.Body) > maxCachedResponseBytes {
		return
	}
	key := s.responseKey(url)
	r = r.clone()
	s.repo.responses.put(key, r)
	if s.repo.store != nil {
		s.repo.write(key, func(ctx context.Context) error { return s.repo.store.PutResponse(ctx, s.name, url, r) })
	}
}

// blobHasSHA reports whether data is the contents of the git blob sha: a
// SHA-1 (40 hex digits) or SHA-256 (64) of the blob's header and contents.
func blobHasSHA(data []byte, sha string) bool {
	var h hash.Hash
	switch len(sha) {
	case 2 * sha1.Size:
		h = sha1.New() //nolint:gosec // git's object ID, not a security boundary on its own
	case 2 * sha256.Size:
		h = sha256.New()
	default:
		return false
	}
	_, _ = fmt.Fprintf(h, "blob %d\x00", len(data))
	_, _ = h.Write(data)
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), sha)
}
