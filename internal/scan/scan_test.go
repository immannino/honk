package scan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFind(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, gitFile bool) {
		t.Helper()
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if gitFile {
			// Linked worktrees and submodules have a .git file, not a directory.
			if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", false)
	mk("a/inner", false)
	mk("group/b", false)
	mk("group/worktree", true)
	mk("deep/1/2/3/c", false)
	mk("node_modules/pkg", false)
	if err := os.MkdirAll(filepath.Join(root, "plain/dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	rel := func(paths []string) []string {
		var out []string
		for _, p := range paths {
			r, _ := filepath.Rel(root, p)
			out = append(out, r)
		}
		slices.Sort(out)
		return out
	}

	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{"defaults", Options{Skip: DefaultSkip}, []string{"a", "deep/1/2/3/c", "group/b", "group/worktree"}},
		{"depth limited", Options{MaxDepth: 2, Skip: DefaultSkip}, []string{"a", "group/b", "group/worktree"}},
		{"nested", Options{Nested: true, Skip: DefaultSkip}, []string{"a", "a/inner", "deep/1/2/3/c", "group/b", "group/worktree"}},
		{"no skips", Options{}, []string{"a", "deep/1/2/3/c", "group/b", "group/worktree", "node_modules/pkg"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Find(root, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if r := rel(got); !slices.Equal(r, tt.want) {
				t.Errorf("Find() = %v, want %v", r, tt.want)
			}
		})
	}
}

func TestFindRootIsRepo(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Find() = %v, want just the root", got)
	}
}

func TestFindNotADir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(f, Options{}); err == nil {
		t.Error("expected error for a file root")
	}
}
