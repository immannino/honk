package git

import (
	"bufio"
	"strconv"
	"strings"
)

// worktree is the parsed result of `git status --porcelain=v2 --branch --show-stash`.
type worktree struct {
	Head       string // full commit id; empty for an unborn branch
	Branch     string // empty when detached
	Upstream   string
	HasAB      bool
	Ahead      int
	Behind     int
	Stashes    int
	Staged     int
	Unstaged   int
	Untracked  int
	Conflicted int
}

func parsePorcelainV2(out string) worktree {
	var wt worktree
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case '#':
			parseHeader(&wt, line)
		case '1', '2':
			// "1 XY ..." / "2 XY ..." — X is the index, Y the worktree; '.' means unchanged.
			if len(line) < 4 {
				continue
			}
			if line[2] != '.' {
				wt.Staged++
			}
			if line[3] != '.' {
				wt.Unstaged++
			}
		case 'u':
			wt.Conflicted++
		case '?':
			wt.Untracked++
		}
	}
	return wt
}

func parseHeader(wt *worktree, line string) {
	fields := strings.Fields(strings.TrimPrefix(line, "# "))
	if len(fields) < 2 {
		return
	}
	switch fields[0] {
	case "branch.oid":
		if fields[1] != "(initial)" {
			wt.Head = fields[1]
		}
	case "branch.head":
		if fields[1] != "(detached)" {
			wt.Branch = fields[1]
		}
	case "branch.upstream":
		wt.Upstream = fields[1]
	case "branch.ab":
		if len(fields) < 3 {
			return
		}
		wt.HasAB = true
		wt.Ahead, _ = strconv.Atoi(strings.TrimPrefix(fields[1], "+"))
		wt.Behind, _ = strconv.Atoi(strings.TrimPrefix(fields[2], "-"))
	case "stash":
		wt.Stashes, _ = strconv.Atoi(fields[1])
	}
}
