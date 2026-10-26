// Package syncer decides how to bring each repo's branch up to date with its
// base branch, and carries that out.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/immannino/honk/internal/git"
)

// Strategy is how a stale branch picks up new base commits.
type Strategy string

const (
	// StrategyMerge merges the base into the branch, like `git pull origin <base>`.
	StrategyMerge Strategy = "merge"
	// StrategyRebase replays the branch on top of the base and force-pushes with lease.
	StrategyRebase Strategy = "rebase"
)

// ParseStrategy validates a user-supplied strategy name.
func ParseStrategy(s string) (Strategy, error) {
	switch Strategy(s) {
	case StrategyMerge, StrategyRebase:
		return Strategy(s), nil
	}
	return "", fmt.Errorf("unknown strategy %q (want merge or rebase)", s)
}

// Action is what will be done to a repo.
type Action string

const (
	ActionMerge       Action = "merge"
	ActionRebase      Action = "rebase"
	ActionFastForward Action = "fast-forward"
	ActionPush        Action = "push" // up to date with base, but has unpushed commits
	ActionSkip        Action = "skip" // needs attention; Reason says why
	ActionNone        Action = "none" // already up to date
)

// Options controls planning.
type Options struct {
	Strategy Strategy
	// Push publishes updated branches that have an upstream. Off unless asked for.
	Push      bool
	Autostash bool
}

// Step is the planned action for one repo.
type Step struct {
	Status    *git.Status `json:"status"`
	Action    Action      `json:"action"`
	Reason    string      `json:"reason,omitempty"`
	Push      bool        `json:"push"`
	ForcePush bool        `json:"force_push"`
	// NoUpstream is set when a push was requested but the branch has never been pushed.
	NoUpstream bool `json:"no_upstream,omitempty"`
	Autostash  bool `json:"autostash"`
}

// Actionable reports whether the step changes anything.
func (s Step) Actionable() bool {
	return s.Action != ActionSkip && s.Action != ActionNone
}

// Plan decides what to do with a repo based on its status. It's pure: nothing is run.
func Plan(st *git.Status, opts Options) Step {
	step := Step{Status: st}
	skip := func(format string, args ...any) Step {
		step.Action, step.Reason = ActionSkip, fmt.Sprintf(format, args...)
		return step
	}

	switch {
	case st.Error != "":
		return skip("inspection failed: %s", firstLine(st.Error))
	case st.FetchError != "":
		return skip("fetch failed: %s", firstLine(st.FetchError))
	case st.Rebase == git.RebaseBusy:
		return skip("%s in progress", st.Operation)
	case st.Rebase == git.RebaseDetached:
		return skip("detached HEAD")
	case st.Rebase == git.RebaseNoBase:
		return skip("no base branch found (try --base)")
	case st.Rebase == git.RebaseUnknown:
		return skip("could not determine sync state")
	case st.Rebase == git.RebaseUpToDate && !opts.Push:
		step.Action = ActionNone
		return step
	}

	onBase := st.Upstream != "" && st.Upstream == st.Base
	// The branch and its upstream have diverged. Either someone else pushed to it,
	// or it was rewritten locally (rebased) since the last push, in which case every
	// upstream-only commit has a patch-equivalent copy here.
	diverged := !onBase && st.Behind > 0
	rewritten := diverged && st.UpstreamNew == 0

	if diverged && !rewritten {
		// Pushing would be rejected (merge) or discard their commits (rebase).
		return skip("%d new commit(s) on %s; pull your branch first", st.UpstreamNew, st.Upstream)
	}
	if rewritten && opts.Strategy != StrategyRebase {
		return skip("rewritten since last push (rebased?); use --rebase to force-push it")
	}

	if st.Rebase == git.RebaseUpToDate {
		return planPushOnly(step, st, opts, onBase, rewritten)
	}

	switch {
	case onBase && st.BaseAhead > 0:
		return skip("local commits on %s, which is behind %s; reconcile by hand", st.Branch, st.Base)
	case st.Dirty() && !opts.Autostash:
		return skip("uncommitted changes (commit, stash, or use --autostash)")
	case len(st.ConflictFiles) > 0:
		return skip("would conflict in %s", summarizeFiles(st.ConflictFiles))
	}

	step.Autostash = opts.Autostash && st.Dirty()
	switch {
	case st.BaseAhead == 0:
		step.Action = ActionFastForward
	case opts.Strategy == StrategyRebase:
		step.Action = ActionRebase
	default:
		step.Action = ActionMerge
	}
	step.ForcePush = step.Action == ActionRebase || rewritten

	// A branch with no upstream has never been pushed, so there's nothing to update.
	// On the base branch itself, fast-forwarding just catches up with the remote.
	step.Push = opts.Push && st.Upstream != "" && !onBase
	step.NoUpstream = opts.Push && st.Upstream == ""
	if !step.Push {
		step.ForcePush = false
	}
	return step
}

// planPushOnly handles a branch that already contains its base. With --push it
// publishes commits that an earlier sync (or the user) left unpushed. Note this
// pushes all unpushed commits on the branch, not just merge commits.
func planPushOnly(step Step, st *git.Status, opts Options, onBase, rewritten bool) Step {
	step.Action = ActionNone
	if !opts.Push || onBase || st.Ahead == 0 {
		return step
	}
	if st.Upstream == "" {
		return step // never pushed; publishing a new branch is left to the user
	}
	step.Action, step.Push, step.ForcePush = ActionPush, true, rewritten
	return step
}

// Result is the outcome of applying a Step.
type Result struct {
	Step      Step     `json:"step"`
	OldHead   string   `json:"old_head,omitempty"`
	NewHead   string   `json:"new_head,omitempty"`
	Pushed    bool     `json:"pushed"`
	Conflicts []string `json:"conflicts,omitempty"`
	Error     string   `json:"error,omitempty"`
	// PushError is set when the local update succeeded but the push didn't.
	PushError string `json:"push_error,omitempty"`
}

// OK reports whether the step fully succeeded.
func (r Result) OK() bool { return r.Error == "" && r.PushError == "" }

// Apply carries out a step. A failed merge or rebase is aborted so the repo is
// left as it was; a failed push leaves the local update in place.
func Apply(ctx context.Context, step Step) Result {
	res := Result{Step: step}
	if !step.Actionable() {
		return res
	}
	st := step.Status
	repo := git.Repo{Path: st.Path}

	old, err := repo.Head(ctx)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OldHead = old

	switch step.Action {
	case ActionPush:
		// Nothing to do locally.
	case ActionMerge:
		err = repo.Merge(ctx, st.Base, step.Autostash)
	case ActionRebase:
		err = repo.Rebase(ctx, st.Base, step.Autostash)
	case ActionFastForward:
		err = repo.FastForward(ctx, st.Base, step.Autostash)
	}
	if err != nil {
		if ce, ok := errors.AsType[*git.ConflictError](err); ok {
			res.Conflicts = ce.Files
		}
		res.Error = err.Error()
		return res
	}
	res.NewHead, _ = repo.Head(ctx)

	if step.Push {
		target, ok := repo.UpstreamOf(ctx, st.Branch)
		if !ok {
			res.PushError = "no upstream configured"
			return res
		}
		if err := repo.Push(ctx, target, step.ForcePush); err != nil {
			res.PushError = err.Error()
			return res
		}
		res.Pushed = true
	}
	return res
}

func summarizeFiles(files []string) string {
	const show = 3
	if len(files) <= show {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:show], ", "), len(files)-show)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
