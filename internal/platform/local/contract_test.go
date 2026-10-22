package local

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/platform/platformtest"
	"github.com/alphaeron/kit/internal/review"
)

// TestLocalMeetsTheSourceContract runs the platform contract over a scratch
// repository: the base revision committed at HEAD, the change left
// uncommitted in the working tree.
func TestLocalMeetsTheSourceContract(t *testing.T) {
	platformtest.RunSourceContract(t, func(t *testing.T, f platformtest.Fixture) review.Source {
		dir, repo := newScratchRepo(t, f.Base)
		for path, content := range f.Change.Write {
			writeTestFile(t, dir, path, content)
		}
		for _, path := range f.Change.Delete {
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(path))); err != nil {
				t.Fatalf("Remove %s: %v", path, err)
			}
		}
		for from, to := range f.Change.Rename {
			dst := filepath.Join(dir, filepath.FromSlash(to))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.Rename(filepath.Join(dir, filepath.FromSlash(from)), dst); err != nil {
				t.Fatalf("Rename %s: %v", from, err)
			}
		}
		return NewSource(repo, testLogger())
	})
}

func TestLocalWorktreeMeetsTheTreeContract(t *testing.T) {
	platformtest.RunTreeContract(t, func(t *testing.T, files map[string]string) approval.Tree {
		_, repo := newScratchRepo(t, files)
		return &worktree{repo: repo}
	})
}

// TestLocalResolverMeetsTheResolverContract: the local platform has no
// groups and accepts every target, so only the user checks apply.
func TestLocalResolverMeetsTheResolverContract(t *testing.T) {
	platformtest.RunResolverContract(t, func(t *testing.T, _ []string) platformtest.ResolverHarness {
		return platformtest.ResolverHarness{Resolver: NewEvaluator(nil, nil).Resolver}
	})
}
