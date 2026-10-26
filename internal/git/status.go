package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RebaseState summarises whether the current branch can be brought up to date with its base.
type RebaseState string

const (
	RebaseUpToDate    RebaseState = "up-to-date"   // nothing to pull in from base
	RebaseFastForward RebaseState = "fast-forward" // no local commits; base can be fast-forwarded in
	RebaseReady       RebaseState = "ready"        // clean tree and no predicted conflicts
	RebaseConflicts   RebaseState = "conflicts"    // base and branch touch the same lines
	RebaseDirty       RebaseState = "dirty"        // uncommitted changes must be committed or stashed first
	RebaseBusy        RebaseState = "busy"         // a rebase/merge/cherry-pick is already in progress
	RebaseDetached    RebaseState = "detached"     // HEAD is not on a branch
	RebaseNoBase      RebaseState = "no-base"      // no base branch could be resolved
	RebaseUnknown     RebaseState = "unknown"      // inspection failed part-way
)

// Commit describes a single commit.
type Commit struct {
	Hash    string    `json:"hash"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	When    time.Time `json:"when"`
}

// Status is a point-in-time snapshot of a working tree.
type Status struct {
	Path    string `json:"path"`
	RelPath string `json:"rel_path"`

	Branch   string  `json:"branch,omitempty"`
	Detached bool    `json:"detached"`
	Last     *Commit `json:"last_commit,omitempty"`

	Staged     int `json:"staged"`
	Unstaged   int `json:"unstaged"`
	Untracked  int `json:"untracked"`
	Conflicted int `json:"conflicted"`
	Stashes    int `json:"stashes"`

	// Operation is an in-progress git operation: rebase, am, merge, cherry-pick, revert or bisect.
	Operation string `json:"operation,omitempty"`

	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	// UpstreamNew counts upstream commits with no patch-equivalent locally. When
	// Behind > 0 but this is 0, the branch was rewritten (e.g. rebased) since its last push.
	UpstreamNew int `json:"upstream_new"`

	Base       string `json:"base,omitempty"`
	BaseAhead  int    `json:"base_ahead"`
	BaseBehind int    `json:"base_behind"`

	Rebase        RebaseState `json:"rebase"`
	ConflictFiles []string    `json:"conflict_files,omitempty"`

	Fetched    bool   `json:"fetched"`
	FetchError string `json:"fetch_error,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Dirty reports whether the tree has tracked changes that would block a rebase.
// Untracked files are deliberately excluded; they rarely get in the way.
func (s *Status) Dirty() bool {
	return s.Staged+s.Unstaged+s.Conflicted > 0
}

// HasChanges reports whether anything at all is uncommitted, including untracked files.
func (s *Status) HasChanges() bool {
	return s.Dirty() || s.Untracked > 0
}

// InspectOptions controls how much work Inspect does.
type InspectOptions struct {
	// Fetch runs `git fetch` first so upstream/base counts reflect the remote.
	Fetch        bool
	FetchTimeout time.Duration
	// Base overrides base-branch auto-detection (e.g. "main" or "origin/develop").
	Base string
}

// Inspect collects a Status for the working tree at r.Path. It never returns an
// error; failures are recorded on Status.Error so one bad repo doesn't sink a scan.
func (r Repo) Inspect(ctx context.Context, opts InspectOptions) *Status {
	st := &Status{Path: r.Path, Rebase: RebaseUnknown}

	if opts.Fetch {
		fctx, cancel := ctx, context.CancelFunc(func() {})
		if opts.FetchTimeout > 0 {
			fctx, cancel = context.WithTimeout(ctx, opts.FetchTimeout)
		}
		if err := r.Fetch(fctx); err != nil {
			st.FetchError = err.Error()
		} else {
			st.Fetched = true
		}
		cancel()
	}

	out, err := r.Run(ctx, "status", "--porcelain=v2", "--branch", "--show-stash")
	if err != nil {
		st.Error = err.Error()
		return st
	}
	wt := parsePorcelainV2(out)
	st.Branch = wt.Branch
	st.Detached = wt.Branch == ""
	st.Upstream = wt.Upstream
	st.Ahead, st.Behind = wt.Ahead, wt.Behind
	st.Staged, st.Unstaged = wt.Staged, wt.Unstaged
	st.Untracked, st.Conflicted = wt.Untracked, wt.Conflicted
	st.Stashes = wt.Stashes

	if wt.Head != "" {
		st.Last, _ = r.lastCommit(ctx)
	}
	st.UpstreamNew = st.Behind
	if st.Ahead > 0 && st.Behind > 0 {
		out, err := r.Run(ctx, "rev-list", "--count", "--right-only", "--cherry-pick", "HEAD...@{upstream}")
		if n, convErr := strconv.Atoi(out); err == nil && convErr == nil {
			st.UpstreamNew = n
		}
	}
	st.Operation = r.operation(ctx)

	if st.Detached {
		st.Rebase = RebaseDetached
		if st.Operation != "" {
			st.Rebase = RebaseBusy
		}
		return st
	}

	st.Base = r.resolveBase(ctx, st.Branch, opts.Base)
	if st.Base == "" || wt.Head == "" {
		st.Rebase = RebaseNoBase
		return st
	}

	st.BaseAhead, st.BaseBehind, err = r.aheadBehind(ctx, "HEAD", st.Base)
	if err != nil {
		st.Error = err.Error()
		return st
	}

	st.Rebase, st.ConflictFiles = r.rebaseState(ctx, st)
	return st
}

func (r Repo) rebaseState(ctx context.Context, st *Status) (RebaseState, []string) {
	switch {
	case st.Operation != "":
		return RebaseBusy, nil
	case st.BaseBehind == 0:
		return RebaseUpToDate, nil
	}

	// Predict conflicts even when dirty, so the caller knows what they'd face after stashing.
	conflicts, err := r.PredictConflicts(ctx, "HEAD", st.Base)
	if err != nil {
		return RebaseUnknown, nil
	}

	switch {
	case st.Dirty():
		return RebaseDirty, conflicts
	case len(conflicts) > 0:
		return RebaseConflicts, conflicts
	case st.BaseAhead == 0:
		return RebaseFastForward, nil
	default:
		return RebaseReady, nil
	}
}

// PredictConflicts performs an in-memory merge of a and b (without touching the
// index or worktree) and returns the paths that would conflict. A clean merge is
// a strong, though not perfect, predictor of a clean rebase: rebase replays
// commits one at a time, so intermediate commits can still conflict.
func (r Repo) PredictConflicts(ctx context.Context, a, b string) ([]string, error) {
	out, err := r.Run(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", a, b)
	if err == nil {
		return nil, nil
	}
	if ExitCode(err) != 1 {
		return nil, err
	}
	// Exit 1 means conflicts: first line is the tree id, the rest are paths.
	lines := strings.Split(out, "\n")
	var files []string
	seen := map[string]bool{}
	for _, l := range lines[1:] {
		if l != "" && !seen[l] {
			seen[l] = true
			files = append(files, l)
		}
	}
	return files, nil
}

func (r Repo) lastCommit(ctx context.Context) (*Commit, error) {
	out, err := r.Run(ctx, "log", "-1", "--format=%h%x00%s%x00%an%x00%ct")
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(out, "\x00", 4)
	if len(parts) != 4 {
		return nil, nil
	}
	ts, _ := strconv.ParseInt(parts[3], 10, 64)
	return &Commit{Hash: parts[0], Subject: parts[1], Author: parts[2], When: time.Unix(ts, 0)}, nil
}

// aheadBehind returns how many commits a has that b lacks, and vice versa.
func (r Repo) aheadBehind(ctx context.Context, a, b string) (ahead, behind int, err error) {
	out, err := r.Run(ctx, "rev-list", "--left-right", "--count", a+"..."+b)
	if err != nil {
		return 0, 0, err
	}
	f := strings.Fields(out)
	if len(f) == 2 {
		ahead, _ = strconv.Atoi(f[0])
		behind, _ = strconv.Atoi(f[1])
	}
	return ahead, behind, nil
}

// resolveBase finds the branch that the current branch should be kept up to date with.
// With an explicit override it tries the name as given, then on the branch's remote.
// Otherwise it uses <remote>/HEAD, falling back to common default branch names.
func (r Repo) resolveBase(ctx context.Context, branch, override string) string {
	remote, _ := r.Run(ctx, "config", "--get", "branch."+branch+".remote")
	if remote == "" || remote == "." {
		remote = "origin"
	}

	var candidates []string
	if override != "" {
		candidates = []string{remote + "/" + override, override}
		if strings.Contains(override, "/") {
			candidates = []string{override, remote + "/" + override}
		}
	} else {
		if ref, err := r.Run(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil && ref != "" {
			return ref
		}
		for _, name := range []string{"main", "master"} {
			candidates = append(candidates, remote+"/"+name)
		}
		candidates = append(candidates, "main", "master")
	}

	for _, c := range candidates {
		if r.RefExists(ctx, c) {
			return c
		}
	}
	return ""
}

// operation detects an in-progress multi-step git operation by looking for its
// marker files in the git dir.
func (r Repo) operation(ctx context.Context) string {
	gitDir, err := r.Run(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ""
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("rebase-merge"):
		return "rebase"
	case exists("rebase-apply/applying"):
		return "am"
	case exists("rebase-apply"):
		return "rebase"
	case exists("MERGE_HEAD"):
		return "merge"
	case exists("CHERRY_PICK_HEAD"):
		return "cherry-pick"
	case exists("REVERT_HEAD"):
		return "revert"
	case exists("BISECT_LOG"):
		return "bisect"
	}
	return ""
}
