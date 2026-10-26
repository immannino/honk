// Package scan discovers git working trees beneath a directory.
package scan

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultSkip lists directory names that are never worth descending into.
var DefaultSkip = []string{"node_modules", "vendor", ".venv", "venv", ".terraform", ".cache"}

// Options controls the walk.
type Options struct {
	// MaxDepth limits how many directories below root are searched. 0 means unlimited.
	MaxDepth int
	// Skip is a list of directory names to ignore wherever they appear.
	Skip []string
	// Nested keeps searching inside a repo once found (for repos-within-repos).
	// Submodules are found this way too.
	Nested bool
}

// Find returns the absolute paths of every working tree under root, in walk order.
// A directory counts as a working tree when it contains a .git entry; that entry
// may be a file, as with linked worktrees and submodules.
func Find(root string, opts Options) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New(root + " is not a directory")
	}

	var repos []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable directories are skipped rather than aborting the whole scan.
			if d != nil && d.IsDir() && path != root {
				return filepath.SkipDir
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}

		if path != root {
			name := d.Name()
			if name == ".git" || slices.Contains(opts.Skip, name) {
				return filepath.SkipDir
			}
			if opts.MaxDepth > 0 && depth(root, path) > opts.MaxDepth {
				return filepath.SkipDir
			}
		}

		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			repos = append(repos, path)
			if !opts.Nested {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return repos, err
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}
