package gitutil

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/filesystem"
)

// KitGitRepository embeds the go-git repository and adds helpers shared by
// every local consumer: the CLI commands, the kenin LocalSource, and the
// shonin LocalFetcher. A repository with no commit at all (a plain-init
// with nothing committed) is first-class state, never an error: HEAD-reading
// call sites ask HasCommits before resolving and behave as if the tracked
// universe is empty, because a repository with no commit has no tracked files
// and no tracked rules.
type KitGitRepository struct {
	git.Repository
}

// HasCommits reports whether HEAD resolves: false means the repository has
// no commit yet, whether HEAD is unborn or points to a ref that does not
// exist. Callers use it to degrade HEAD-reading operations to an empty
// result instead of a reference-not-found error.
func (r *KitGitRepository) HasCommits() bool {
	_, err := r.Head()
	return err == nil
}

func (r *KitGitRepository) Root() (string, error) {
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
func (r *KitGitRepository) FindFiles(pattern string, revision *plumbing.Reference, isAbsolute bool) ([]string, error) {
	return r.FindFilesAt(pattern, plumbing.Revision(revision.Name()), isAbsolute)
}

// FindFilesAt lists every file at the commit that rev resolves to (empty
// rev means HEAD) whose path matches pattern (a regular expression). It
// resolves the revision before touching the tree, so a revision that does
// not exist is an error, never a silently-empty file list.
func (r *KitGitRepository) FindFilesAt(pattern string, rev plumbing.Revision, isAbsolute bool) ([]string, error) {
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
func (r *KitGitRepository) WorktreeFiles(keep func(path string) bool) ([]string, error) {
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
	// Status leaves out ignored files, so ignored KIT files never appear.
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
func (r *KitGitRepository) headFiles(visit func(path string)) error {
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
