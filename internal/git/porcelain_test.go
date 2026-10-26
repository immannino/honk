package git

import "testing"

func TestParsePorcelainV2(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want worktree
	}{
		{
			name: "clean tracking branch",
			in: "# branch.oid 1234abcd\n" +
				"# branch.head main\n" +
				"# branch.upstream origin/main\n" +
				"# branch.ab +0 -3\n",
			want: worktree{Head: "1234abcd", Branch: "main", Upstream: "origin/main", HasAB: true, Behind: 3},
		},
		{
			name: "mixed changes and stash",
			in: "# branch.oid 1234abcd\n" +
				"# branch.head wip\n" +
				"# stash 2\n" +
				"1 M. N... 100644 100644 100644 aaa bbb staged.go\n" +
				"1 .M N... 100644 100644 100644 aaa bbb modified.go\n" +
				"1 MM N... 100644 100644 100644 aaa bbb both.go\n" +
				"2 R. N... 100644 100644 100644 aaa bbb R100 new.go\told.go\n" +
				"u UU N... 100644 100644 100644 100644 aaa bbb ccc conflict.go\n" +
				"? untracked.txt\n" +
				"? other.txt\n" +
				"! ignored.log\n",
			want: worktree{Head: "1234abcd", Branch: "wip", Stashes: 2, Staged: 3, Unstaged: 2, Conflicted: 1, Untracked: 2},
		},
		{
			name: "detached",
			in:   "# branch.oid 1234abcd\n# branch.head (detached)\n",
			want: worktree{Head: "1234abcd"},
		},
		{
			name: "unborn branch",
			in:   "# branch.oid (initial)\n# branch.head main\n? README.md\n",
			want: worktree{Branch: "main", Untracked: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePorcelainV2(tt.in); got != tt.want {
				t.Errorf("parsePorcelainV2() =\n  %+v\nwant\n  %+v", got, tt.want)
			}
		})
	}
}
