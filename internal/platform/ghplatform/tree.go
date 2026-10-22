package ghplatform

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sync"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/approval"
)

// baseTree is a GitHub repository's tree at one revision as an
// approval.Tree, read through the GitHub REST API. The tree is listed once
// and then shared: a Source's AllFiles and its reviewer's fetch read the same
// listing, so a review lists the base tree in one API call.
type baseTree struct {
	client *github.Client
	owner  string
	repo   string
	sha    string

	mu    sync.Mutex
	blobs map[string]string // path to blob SHA; nil until listed
}

// newBaseTree is the tree of owner/repo at sha. client is the authenticated
// GitHub client; the caller owns its token wiring.
func newBaseTree(client *github.Client, owner, repo, sha string) *baseTree {
	return &baseTree{client: client, owner: owner, repo: repo, sha: sha}
}

// Files lists every blob in the tree.
func (b *baseTree) Files(ctx context.Context) ([]string, error) {
	blobs, err := b.list(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(blobs)), nil
}

// ReadFile reads the blob at path. The git blob endpoint returns contents
// base64-encoded; the empty encoding is the raw passthrough some API
// variants use.
func (b *baseTree) ReadFile(ctx context.Context, path string) ([]byte, error) {
	blobs, err := b.list(ctx)
	if err != nil {
		return nil, err
	}
	sha, ok := blobs[path]
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
	}
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

// list returns the tree's blobs, listing them on first use. A failed listing
// is not cached, so a later call retries it. The lock is held across the
// call so concurrent first uses share one listing.
func (b *baseTree) list(ctx context.Context) (map[string]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.blobs != nil {
		return b.blobs, nil
	}
	tree, _, err := b.client.Git.GetTree(ctx, b.owner, b.repo, b.sha, true)
	if err != nil {
		return nil, fmt.Errorf("list tree %s/%s@%s: %w", b.owner, b.repo, b.sha, err)
	}
	blobs := make(map[string]string, len(tree.Entries))
	for _, entry := range tree.Entries {
		if entry.GetType() == "blob" {
			blobs[entry.GetPath()] = entry.GetSHA()
		}
	}
	b.blobs = blobs
	return blobs, nil
}

var _ approval.Tree = (*baseTree)(nil)
