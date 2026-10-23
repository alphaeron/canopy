package ghplatform

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sync"

	"github.com/google/go-github/v93/github"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/parallel"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/server"
)

// baseTree is a GitHub repository's tree at one revision as an
// approval.Tree, read through the GitHub REST API. The tree is listed once
// and then shared: a Source's AllFiles and its evaluator's fetch read the same
// listing, so a review lists the base tree once (see listBlobs). With a
// cache, listings and blobs are shared across reviews too.
type baseTree struct {
	client *github.Client
	owner  string
	repo   string
	sha    string
	cache  server.CacheScope

	mu    sync.Mutex
	blobs map[string]server.TreeBlob // nil until listed; never mutated, as the cache shares it
}

// newBaseTree is the tree of owner/repo at sha. client is the authenticated
// GitHub client; the caller owns its token wiring. cache shares listings
// and blobs with the installation's other reviews.
func newBaseTree(client *github.Client, owner, repo, sha string, cache server.CacheScope) *baseTree {
	return &baseTree{client: client, owner: owner, repo: repo, sha: sha, cache: cache}
}

// Files lists every blob in the tree.
func (b *baseTree) Files(ctx context.Context) ([]string, error) {
	blobs, err := b.list(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(blobs)), nil
}

// ReadFile reads the blob at path, from the cache when it has the blob.
// Only rules files are read through the tree, so a blob the listing sizes
// over rulesfile.MaxSize is rulesfile.ErrTooLarge, without downloading it.
func (b *baseTree) ReadFile(ctx context.Context, path string) ([]byte, error) {
	blobs, err := b.list(ctx)
	if err != nil {
		return nil, err
	}
	blob, ok := blobs[path]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
	}
	if blob.Size > rulesfile.MaxSize {
		return nil, fmt.Errorf("read %s: %d bytes: %w", path, blob.Size, rulesfile.ErrTooLarge)
	}
	sha := blob.SHA
	if data, ok := b.cache.Blob(sha); ok {
		return data, nil
	}
	data, err := b.readBlob(ctx, path, sha)
	if err != nil {
		return nil, err
	}
	b.cache.PutBlob(sha, data)
	return data, nil
}

// readBlob reads the blob sha, at path in the tree, through the API. The git
// blob endpoint returns contents base64-encoded; the empty encoding is the
// raw passthrough some API variants use.
func (b *baseTree) readBlob(ctx context.Context, path, sha string) ([]byte, error) {
	blob, _, err := b.client.Git.GetBlob(ctx, b.owner, b.repo, sha)
	if err != nil {
		return nil, fmt.Errorf("get blob %s for %s: %w", sha, path, err)
	}
	switch blob.GetEncoding() {
	case "base64":
		data, err := base64.StdEncoding.DecodeString(blob.GetContent())
		if err != nil {
			return nil, fmt.Errorf("decode blob %s for %s: %w", sha, path, err)
		}
		return data, nil
	case "":
		return []byte(blob.GetContent()), nil
	default:
		return nil, fmt.Errorf("unsupported blob %s encoding %q", sha, blob.GetEncoding())
	}
}

// list returns the tree's blobs, listing them on first use unless the cache
// has them (see listBlobs). A tree too large to list is
// review.ErrTreeTruncated, never a partial tree: a missing rules file would
// silently drop required approvers. A failed listing is not cached, so a
// later call retries it. The lock is held across the call so concurrent
// first uses share one listing.
func (b *baseTree) list(ctx context.Context) (map[string]server.TreeBlob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.blobs != nil {
		return b.blobs, nil
	}
	if blobs, ok := b.cache.Tree(b.owner+"/"+b.repo, b.sha); ok {
		b.blobs = blobs
		return blobs, nil
	}
	blobs, err := listBlobs(ctx, b.client, b.owner, b.repo, b.sha)
	if err != nil {
		return nil, err
	}
	b.blobs = blobs
	b.cache.PutTree(b.owner+"/"+b.repo, b.sha, blobs)
	return blobs, nil
}

// maxTreeRequests bounds the requests listBlobs makes for one tree, so a
// pathological tree costs a bounded share of the installation's rate limit.
const maxTreeRequests = 256

// listBlobs lists every blob in owner/repo's tree at sha (a commit or tree
// SHA), path to blob. It asks for the whole tree in one request; only when
// GitHub truncates that (past 100,000 entries or 7 MB) does it list the
// tree's own entries and each subtree in the same way, so a tree too large
// for one listing costs one more request per subtree that is. A tree that
// still cannot be listed (one directory GitHub truncates on its own, or more
// than maxTreeRequests requests) is review.ErrTreeTruncated.
func listBlobs(ctx context.Context, client *github.Client, owner, repo, sha string) (map[string]server.TreeBlob, error) {
	l := &treeLister{client: client, owner: owner, repo: repo, sem: make(chan struct{}, parallel.Limit), blobs: map[string]server.TreeBlob{}}
	if err := l.list(ctx, sha, ""); err != nil {
		return nil, fmt.Errorf("list tree %s/%s@%s: %w", owner, repo, sha, err)
	}
	return l.blobs, nil
}

// treeLister is one listBlobs: its requests, at most parallel.Limit at a
// time, and the blobs found so far.
type treeLister struct {
	client *github.Client
	owner  string
	repo   string
	sem    chan struct{}

	mu       sync.Mutex
	requests int
	blobs    map[string]server.TreeBlob
}

// list adds the blobs of the tree sha, each path under prefix.
func (l *treeLister) list(ctx context.Context, sha, prefix string) error {
	tree, err := l.get(ctx, sha, true)
	if err != nil {
		return err
	}
	if !tree.GetTruncated() {
		l.add(prefix, tree.Entries)
		return nil
	}
	if tree, err = l.get(ctx, sha, false); err != nil {
		return err
	}
	if tree.GetTruncated() {
		return fmt.Errorf("directory %q: %w", prefix, review.ErrTreeTruncated)
	}
	l.add(prefix, tree.Entries)
	var subtrees []*github.TreeEntry
	for _, e := range tree.Entries {
		if e.GetType() == "tree" {
			subtrees = append(subtrees, e)
		}
	}
	errs := make([]error, len(subtrees))
	var wg sync.WaitGroup
	for i, e := range subtrees {
		wg.Go(func() { errs[i] = l.list(ctx, e.GetSHA(), prefix+e.GetPath()+"/") })
	}
	wg.Wait()
	return errors.Join(errs...)
}

// get requests the tree sha, counting the request against maxTreeRequests
// and waiting its turn among parallel.Limit concurrent requests.
func (l *treeLister) get(ctx context.Context, sha string, recursive bool) (*github.Tree, error) {
	l.mu.Lock()
	l.requests++
	over := l.requests > maxTreeRequests
	l.mu.Unlock()
	if over {
		return nil, fmt.Errorf("more than %d requests: %w", maxTreeRequests, review.ErrTreeTruncated)
	}
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-l.sem }()
	tree, _, err := l.client.Git.GetTree(ctx, l.owner, l.repo, sha, recursive)
	return tree, err
}

// add records the blobs among entries, each path under prefix.
func (l *treeLister) add(prefix string, entries []*github.TreeEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range entries {
		if e.GetType() == "blob" {
			l.blobs[prefix+e.GetPath()] = server.TreeBlob{SHA: e.GetSHA(), Size: int64(e.GetSize())}
		}
	}
}

var _ approval.Tree = (*baseTree)(nil)
