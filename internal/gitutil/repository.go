package gitutil

import (
	"errors"
	"path"
	"regexp"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/filesystem"
)

type KitGitRepository struct {
	git.Repository
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

func (r *KitGitRepository) FindFiles(pattern string, revision *plumbing.Reference, isAbsolute bool) ([]string, error) {
	var paths []string

	repoRoot, err := r.Root()
	if err != nil {
		return nil, err
	}

	h, err := r.ResolveRevision(plumbing.Revision(revision.Name().String()))
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

	itErr := tree.Files().ForEach( func (file *object.File) error {
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
