package server

// The review server's caches live here: the server's entry point builds
// them and hands them to what reads through them (the platform), so every
// cache's bounds and lifetime are set in one place.

import "slices"

// Bounds on the repository contents the server keeps. A tree listing can
// run to many thousands of paths, so few are kept, and one larger than
// maxCachedTreeBlobs is not; rules files are small, so a blob larger than
// maxCachedBlobBytes is not kept.
const (
	maxCachedTrees     = 8
	maxCachedTreeBlobs = 200_000
	maxCachedBlobs     = 1024
	maxCachedBlobBytes = 64 << 10
)

// TreeBlob is a blob a tree listing names: its SHA and its size in bytes.
type TreeBlob struct {
	SHA  string
	Size int64
}

// RepoCache keeps repository contents a platform read, across reviews: tree
// listings and blob contents. Both are immutable once the platform has
// them: a listing is keyed by its repository and commit, and a blob by its
// SHA, a hash of its contents. So nothing kept goes stale. Entries are read
// and written only through a CacheScope. A nil RepoCache keeps nothing. It
// is safe for concurrent use.
type RepoCache struct {
	trees *bounded[string, map[string]TreeBlob]
	blobs *bounded[string, []byte]
}

// NewRepoCache is an empty RepoCache within the package's bounds.
func NewRepoCache() *RepoCache {
	return &RepoCache{
		trees: newBounded[string, map[string]TreeBlob](maxCachedTrees),
		blobs: newBounded[string, []byte](maxCachedBlobs),
	}
}

// Scope is r as one reader sees it, e.g. one installation of a platform's
// app: name must identify whatever limits what the reader may see, so no
// reader is served an entry another scope kept.
func (r *RepoCache) Scope(name string) CacheScope { return CacheScope{repo: r, name: name} }

// CacheScope is a RepoCache seen by one reader (see RepoCache.Scope). The
// zero CacheScope keeps nothing. It is safe for concurrent use.
type CacheScope struct {
	repo *RepoCache
	name string
}

// treeKey names the listing of repo at commit.
func (s CacheScope) treeKey(repo, commit string) string {
	return s.name + "/tree/" + repo + "@" + commit
}

// blobKey names the blob sha, whose contents are the same in every
// repository.
func (s CacheScope) blobKey(sha string) string { return s.name + "/blob/" + sha }

// Tree returns the kept listing of repo at commit, path to blob. It is
// shared: callers must not modify it.
func (s CacheScope) Tree(repo, commit string) (map[string]TreeBlob, bool) {
	if s.repo == nil {
		return nil, false
	}
	return s.repo.trees.get(s.treeKey(repo, commit))
}

// PutTree keeps the listing of repo at commit, unless it has more than
// maxCachedTreeBlobs blobs. The listing is shared from then on, so callers
// must not modify it.
func (s CacheScope) PutTree(repo, commit string, listing map[string]TreeBlob) {
	if s.repo != nil && len(listing) <= maxCachedTreeBlobs {
		s.repo.trees.put(s.treeKey(repo, commit), listing)
	}
}

// Blob returns a copy of the kept contents of blob sha.
func (s CacheScope) Blob(sha string) ([]byte, bool) {
	if s.repo == nil {
		return nil, false
	}
	data, ok := s.repo.blobs.get(s.blobKey(sha))
	return slices.Clone(data), ok
}

// PutBlob keeps a copy of the contents of blob sha, unless they are larger
// than maxCachedBlobBytes.
func (s CacheScope) PutBlob(sha string, data []byte) {
	if s.repo != nil && len(data) <= maxCachedBlobBytes {
		s.repo.blobs.put(s.blobKey(sha), slices.Clone(data))
	}
}
