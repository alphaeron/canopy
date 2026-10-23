package local

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/gitrepo"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// worktree is the local working tree as an approval.Tree: committed files
// that still exist plus new, non-ignored ones
// (gitrepo.Repository.WorktreeFiles), read from disk, so uncommitted rule
// changes are what a local review sees.
type worktree struct {
	repo *gitrepo.Repository
}

// Files lists the working tree's files: the listing Source.AllFiles shares.
func (w *worktree) Files(ctx context.Context) ([]string, error) {
	return w.repo.WorktreeFiles(ctx)
}

// ReadFile reads the file at path from disk. The name must match the
// listed casing exactly, so a case-insensitive filesystem cannot serve one
// file under another's name.
func (w *worktree) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if !fs.ValidPath(path) {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrInvalid}
	}
	root, err := w.repo.Root()
	if err != nil {
		return nil, fmt.Errorf("get repository root: %w", err)
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	exists, err := fileExistsCaseSensitive(full)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !exists {
		return nil, &fs.PathError{Op: "read", Path: path, Err: fs.ErrNotExist}
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// Only rules files are read through the tree, so a file over the rules
	// file limit is refused rather than read whole.
	data, err := rulesfile.ReadLimited(f)
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %s: %w", path, closeErr)
	}
	return data, nil
}

// fileExistsCaseSensitive checks if path exists, is a regular file, and
// matches exact character casing on disk.
func fileExistsCaseSensitive(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if info.IsDir() {
		return false, nil
	}

	// The parent directory's entries tell the exact casing.
	dir, expectedFile := filepath.Split(path)
	if dir == "" {
		dir = "."
	} else {
		dir = filepath.Clean(dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() == expectedFile {
			return true, nil
		}
	}

	return false, nil
}

var _ approval.Tree = (*worktree)(nil)
