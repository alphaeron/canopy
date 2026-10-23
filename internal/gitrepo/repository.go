// Package gitrepo wraps a go-git repository with the file listing, content,
// and worktree queries the review needs.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/filesystem"
)

// Repository embeds the go-git repository and adds helpers shared by
// every local consumer: the CLI commands and the local platform
// (internal/platform/local). A repository with no commit at all (a plain-init
// with nothing committed) is first-class state, never an error: HEAD-reading
// call sites ask HasCommits before resolving and behave as if the tracked
// universe is empty, because a repository with no commit has no tracked files
// and no tracked rules.
//
// A Repository is a snapshot: it reads the worktree status, HEAD's file
// list, and the working tree's file list once, on first use, and reuses
// them, since each walks the whole repository. Open a new Repository to see later changes. It is safe for
// concurrent use.
type Repository struct {
	git.Repository

	mu        sync.Mutex
	status    git.Status // nil until read
	headPaths []string   // nil until listed
	worktree  []string   // nil until listed
}

// HasCommits reports whether HEAD resolves: false means the repository has
// no commit yet, whether HEAD is unborn or points to a ref that does not
// exist. Callers use it to degrade HEAD-reading operations to an empty
// result instead of a reference-not-found error.
func (r *Repository) HasCommits() bool {
	_, err := r.Head()
	return err == nil
}

// Root returns the repository's directory on local disk: the worktree root,
// or the storage directory for a bare repository. It errors when neither
// filesystem exposes a local path, as with an in-memory repository.
func (r *Repository) Root() (string, error) {
	if wt, err := r.Worktree(); err == nil {
		if hasRoot, ok := wt.Filesystem().(interface{ Root() string }); ok {
			return hasRoot.Root(), nil
		}
	}

	// A bare repository has only its storage directory.
	if s, ok := r.Storer.(*filesystem.Storage); ok {
		if hasRoot, ok := s.Filesystem().(interface{ Root() string }); ok {
			return hasRoot.Root(), nil
		}
	}

	return "", errors.New("underlying filesystem does not expose a local disk root")
}

// HeadFiles lists every file in HEAD's tree, sorted, listing them on first
// use (see Repository). The result is shared: callers must not modify it.
func (r *Repository) HeadFiles(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.headPaths != nil {
		return r.headPaths, nil
	}
	head, err := r.Head()
	if err != nil {
		return nil, err
	}
	paths := []string{}
	if err := r.walkFiles(ctx, head.Hash(), func(name string) { paths = append(paths, name) }); err != nil {
		return nil, err
	}
	slices.Sort(paths)
	r.headPaths = paths
	return paths, nil
}

// walkFiles calls visit with the path of every file (not directory or
// submodule) in commit's tree. It reads tree entries only, never a file's
// contents, and stops when ctx is done.
func (r *Repository) walkFiles(ctx context.Context, commit plumbing.Hash, visit func(path string)) error {
	c, err := r.CommitObject(commit)
	if err != nil {
		return err
	}
	tree, err := c.Tree()
	if err != nil {
		return err
	}
	w := object.NewTreeWalker(tree, true, nil)
	defer w.Close()
	for i := 0; ; i++ {
		if i%walkCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		name, entry, err := w.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Mode.IsFile() {
			visit(name)
		}
	}
}

// walkCheckEvery is how many tree entries a walk reads between checks of
// its context: often enough to stop promptly, rarely enough to cost nothing.
const walkCheckEvery = 1024

// Status is the worktree's status, read on first use (see Repository). The
// result is shared: callers must not modify it.
func (r *Repository) Status() (git.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status != nil {
		return r.status, nil
	}
	wt, err := r.Worktree()
	if err != nil {
		return nil, fmt.Errorf("open worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return nil, err
	}
	r.status = status
	return status, nil
}

// WorktreeFiles lists the repository-relative, slash-separated paths of the
// files in the working tree, sorted: files tracked at HEAD that have not
// been deleted, plus new files (staged or untracked) that are not ignored.
// It is the file set a local, pre-pull-request view should see: uncommitted
// additions count and uncommitted deletions do not. A repository with no
// commit yields only new files. The list is made on first use (see
// Repository) and shared: callers must not modify it.
func (r *Repository) WorktreeFiles(ctx context.Context) ([]string, error) {
	head := []string{}
	if r.HasCommits() {
		var err error
		if head, err = r.HeadFiles(ctx); err != nil {
			return nil, fmt.Errorf("list files at HEAD: %w", err)
		}
	}
	// Status leaves out ignored files, so ignored rules files never appear.
	status, err := r.Status()
	if err != nil {
		return nil, fmt.Errorf("worktree status: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.worktree != nil {
		return r.worktree, nil
	}
	set := make(map[string]bool, len(head))
	for _, p := range head {
		set[p] = true
	}
	for p, st := range status {
		switch {
		case st.Worktree == git.Deleted || st.Staging == git.Deleted:
			delete(set, p)
		case st.Worktree == git.Untracked || st.Staging == git.Added:
			set[p] = true
		}
	}
	r.worktree = slices.Sorted(maps.Keys(set))
	return r.worktree, nil
}

// FileAt is the file at path in the commit that revision (typically
// "HEAD") resolves to, looked up directly rather than by walking the tree,
// or nil when the commit has no file there. Its contents are not read
// until asked for (object.File.Contents, Reader), and Size is known
// without reading them. Errors are returned only for unexpected failures to
// resolve the revision, its commit, or its tree.
func (r *Repository) FileAt(path, revision string) (*object.File, error) {
	h, err := r.ResolveRevision(plumbing.Revision(revision))
	if err != nil {
		return nil, fmt.Errorf("resolve revision %q: %w", revision, err)
	}
	commit, err := r.CommitObject(*h)
	if err != nil {
		return nil, fmt.Errorf("resolve commit at %s: %w", revision, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("read tree at %s: %w", revision, err)
	}
	entry, err := tree.FindEntry(path)
	if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find %s at %s: %w", path, revision, err)
	}
	if !entry.Mode.IsFile() {
		return nil, nil
	}
	f, err := tree.TreeEntryFile(entry)
	if err != nil {
		return nil, fmt.Errorf("read blob for %s at %s: %w", path, revision, err)
	}
	f.Name = path
	return f, nil
}
