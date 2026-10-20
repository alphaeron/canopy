package gitutil

import (
	"errors"
	"fmt"
	"os"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// FileContentAt returns the content of a single tracked file at revision
// (typically "HEAD"). The returned boolean is false when the file is not
// present at that revision — an expected case, with a nil error. Errors are
// returned only for unexpected failures to resolve the revision, its commit,
// its tree, or to read the tree's entries. This lets kenin.DiffContent read
// a HEAD blob without materializing a replacement tree.
func (r *KitGitRepository) FileContentAt(path string, revision string) (string, bool, error) {
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
