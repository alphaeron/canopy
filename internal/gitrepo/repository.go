// Package gitrepo wraps a go-git repository with the file listing, content,
// and worktree queries the review needs.
package gitrepo

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"

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
type Repository struct {
	git.Repository
}

// HasCommits reports whether HEAD resolves: false means the repository has
// no commit yet, whether HEAD is unborn or points to a ref that does not
// exist. Callers use it to degrade HEAD-reading operations to an empty
// result instead of a reference-not-found error.
func (r *Repository) HasCommits() bool {
	_, err := r.Head()
	return err == nil
}

func (r *Repository) Root() (string, error) {
	// 1. Check if the worktree filesystem exposes a Root path
	if wt, err := r.Worktree(); err == nil {
		if hasRoot, ok := wt.Filesystem().(interface{ Root() string }); ok {
			return hasRoot.Root(), nil
		}
	}

	// 2. Fallback for bare repositories (inspect storage filesystem)
	if s, ok := r.Storer.(*filesystem.Storage); ok {
		if hasRoot, ok := s.Filesystem().(interface{ Root() string }); ok {
			return hasRoot.Root(), nil
		}
	}

	return "", errors.New("underlying filesystem does not expose a local disk root")
}

// FindFiles lists every file at the given reference whose path matches
// pattern (a regular expression). It delegates to FindFilesAt with the
// reference's name as the revision to resolve.
func (r *Repository) FindFiles(pattern string, revision *plumbing.Reference, isAbsolute bool) ([]string, error) {
	return r.FindFilesAt(pattern, plumbing.Revision(revision.Name()), isAbsolute)
}

// FindFilesAt lists every file at the commit that rev resolves to (empty
// rev means HEAD) whose path matches pattern (a regular expression). It
// resolves the revision before touching the tree, so a revision that does
// not exist is an error, never a silently-empty file list.
func (r *Repository) FindFilesAt(pattern string, rev plumbing.Revision, isAbsolute bool) ([]string, error) {
	var paths []string

	repoRoot, err := r.Root()
	if err != nil {
		return nil, err
	}

	hash := plumbing.Revision("HEAD")
	if rev != "" {
		hash = rev
	}
	h, err := r.ResolveRevision(hash)
	if err != nil {
		return nil, err
	}

	commit, err := r.CommitObject(*h)
	if err != nil {
		return nil, err
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	itErr := tree.Files().ForEach(func(file *object.File) error {
		isMatch, err := regexp.MatchString(pattern, file.Name)
		if err == nil {
			if isMatch {
				var finalPath string
				if isAbsolute {
					finalPath = path.Join(repoRoot, file.Name)
				} else {
					finalPath = file.Name
				}
				paths = append(paths, finalPath)
			}
		} else {
			return err
		}
		return nil
	})

	if itErr != nil {
		return nil, itErr
	}
	return paths, nil
}

// WorktreeFiles lists the repository-relative, slash-separated paths of the
// files in the working tree that satisfy keep, sorted: files tracked at HEAD
// that have not been deleted, plus new files (staged or untracked) that are
// not ignored. It is the file set a local, pre-pull-request view should see:
// uncommitted additions count and uncommitted deletions do not. A repository
// with no commit yields only new files.
func (r *Repository) WorktreeFiles(keep func(path string) bool) ([]string, error) {
	set := map[string]bool{}
	if r.HasCommits() {
		if err := r.headFiles(func(p string) {
			if keep(p) {
				set[p] = true
			}
		}); err != nil {
			return nil, fmt.Errorf("list files at HEAD: %w", err)
		}
	}
	wt, err := r.Worktree()
	if err != nil {
		return nil, fmt.Errorf("open worktree: %w", err)
	}
	// Status leaves out ignored files, so ignored rules files never appear.
	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("worktree status: %w", err)
	}
	for p, st := range status {
		if !keep(p) {
			continue
		}
		switch {
		case st.Worktree == git.Deleted || st.Staging == git.Deleted:
			delete(set, p)
		case st.Worktree == git.Untracked || st.Staging == git.Added:
			set[p] = true
		}
	}
	return slices.Sorted(maps.Keys(set)), nil
}

// headFiles calls visit with the path of every file in HEAD's tree.
func (r *Repository) headFiles(visit func(path string)) error {
	head, err := r.Head()
	if err != nil {
		return err
	}
	commit, err := r.CommitObject(head.Hash())
	if err != nil {
		return err
	}
	tree, err := commit.Tree()
	if err != nil {
		return err
	}
	return tree.Files().ForEach(func(f *object.File) error {
		visit(f.Name)
		return nil
	})
}

// FileContentAt returns the content of a single tracked file at revision
// (typically "HEAD"). The returned boolean is false when the file is not
// present at that revision — an expected case, with a nil error. Errors are
// returned only for unexpected failures to resolve the revision, its commit,
// its tree, or to read the tree's entries. This lets review.Source.DiffContent read
// a HEAD blob without materializing a replacement tree.
func (r *Repository) FileContentAt(path string, revision string) (string, bool, error) {
	h, err := r.ResolveRevision(plumbing.Revision(revision))
	if err != nil {
		return "", false, fmt.Errorf("resolve revision %q: %w", revision, err)
	}
	commit, err := r.CommitObject(*h)
	if err != nil {
		return "", false, fmt.Errorf("resolve commit at %s: %w", revision, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", false, fmt.Errorf("read tree at %s: %w", revision, err)
	}

	var content string
	found := false
	err = tree.Files().ForEach(func(file *object.File) error {
		if found || file.Name != path {
			return nil
		}
		fileContent, err := file.Contents()
		if err != nil {
			return fmt.Errorf("read blob for %s: %w", path, err)
		}
		content = fileContent
		found = true
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return content, found, nil
}

// WrapReadError wraps a filesystem read error with the file path so callers
// can diagnose exactly which file failed to be read.
func WrapReadError(path string, err error) error {
	return fmt.Errorf("read %s: %w", path, err)
}

// IsNotExistErr reports whether err is the operating system's not-exist
// error. Matching on os.ErrNotExist (not string equality) is correct even
// when the error has been carried through a billy filesystem wrapper, which
// wraps the underlying OS error.
func IsNotExistErr(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
