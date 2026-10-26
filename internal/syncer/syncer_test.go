package syncer

import (
	"strings"
	"testing"

	"github.com/immannino/honk/internal/git"
)

func TestPlan(t *testing.T) {
	// feature returns a pushed feature branch that is 2 behind and 1 ahead of origin/main.
	feature := func(mod func(*git.Status)) *git.Status {
		s := &git.Status{
			Branch: "feature/x", Upstream: "origin/feature/x",
			Base: "origin/main", BaseAhead: 1, BaseBehind: 2,
			Rebase: git.RebaseReady,
		}
		if mod != nil {
			mod(s)
		}
		return s
	}
	merge := Options{Strategy: StrategyMerge, Push: true}
	rebase := Options{Strategy: StrategyRebase, Push: true}

	tests := []struct {
		name      string
		st        *git.Status
		opts      Options
		want      Action
		push      bool
		force     bool
		reasonHas string
		autostash bool
	}{
		{name: "merge and push", st: feature(nil), opts: merge, want: ActionMerge, push: true},
		{name: "rebase and force push", st: feature(nil), opts: rebase, want: ActionRebase, push: true, force: true},
		{name: "push is opt-in", st: feature(nil), opts: Options{Strategy: StrategyRebase}, want: ActionRebase},
		{name: "no upstream stays local", st: feature(func(s *git.Status) { s.Upstream = "" }), opts: rebase, want: ActionRebase},
		{name: "fast-forward with no local commits", opts: merge, want: ActionFastForward, push: true,
			st: feature(func(s *git.Status) { s.BaseAhead = 0; s.Rebase = git.RebaseFastForward })},
		{name: "fast-forward never force pushes", opts: rebase, want: ActionFastForward, push: true,
			st: feature(func(s *git.Status) { s.BaseAhead = 0; s.Rebase = git.RebaseFastForward })},
		{name: "base branch fast-forwards without push", opts: merge, want: ActionFastForward,
			st: &git.Status{Branch: "main", Upstream: "origin/main", Behind: 2, Base: "origin/main", BaseBehind: 2, Rebase: git.RebaseFastForward}},
		{name: "base branch with local commits", opts: merge, want: ActionSkip, reasonHas: "local commits",
			st: &git.Status{Branch: "main", Upstream: "origin/main", Ahead: 1, Behind: 2, Base: "origin/main", BaseAhead: 1, BaseBehind: 2, Rebase: git.RebaseReady}},
		{name: "up to date", opts: merge, want: ActionNone,
			st: feature(func(s *git.Status) { s.BaseBehind = 0; s.Rebase = git.RebaseUpToDate })},
		{name: "behind own upstream", opts: rebase, want: ActionSkip, reasonHas: "pull your branch first",
			st: feature(func(s *git.Status) { s.Behind = 1; s.UpstreamNew = 1 })},
		{name: "rewritten branch needs rebase strategy", opts: merge, want: ActionSkip, reasonHas: "--rebase",
			st: feature(func(s *git.Status) { s.Ahead = 2; s.Behind = 1 })},
		{name: "rewritten branch rebases and force pushes", opts: rebase, want: ActionRebase, push: true, force: true,
			st: feature(func(s *git.Status) { s.Ahead = 2; s.Behind = 1 })},
		{name: "push-only for unpushed sync", opts: merge, want: ActionPush, push: true,
			st: feature(func(s *git.Status) { s.Ahead = 2; s.BaseBehind = 0; s.Rebase = git.RebaseUpToDate })},
		{name: "push-only after local rebase force pushes", opts: rebase, want: ActionPush, push: true, force: true,
			st: feature(func(s *git.Status) { s.Ahead = 2; s.Behind = 1; s.BaseBehind = 0; s.Rebase = git.RebaseUpToDate })},
		{name: "push-only needs --push", opts: Options{Strategy: StrategyMerge}, want: ActionNone,
			st: feature(func(s *git.Status) { s.Ahead = 2; s.BaseBehind = 0; s.Rebase = git.RebaseUpToDate })},
		{name: "push-only needs an upstream", opts: merge, want: ActionNone,
			st: feature(func(s *git.Status) { s.Upstream = ""; s.Ahead = 0; s.BaseBehind = 0; s.Rebase = git.RebaseUpToDate })},
		{name: "push-only works with a dirty tree", opts: merge, want: ActionPush, push: true,
			st: feature(func(s *git.Status) { s.Ahead = 1; s.Unstaged = 2; s.BaseBehind = 0; s.Rebase = git.RebaseUpToDate })},
		{name: "up to date and behind upstream is quiet without --push", opts: Options{Strategy: StrategyMerge}, want: ActionNone,
			st: feature(func(s *git.Status) { s.Behind = 3; s.UpstreamNew = 3; s.BaseBehind = 0; s.Rebase = git.RebaseUpToDate })},
		{name: "dirty", opts: merge, want: ActionSkip, reasonHas: "--autostash",
			st: feature(func(s *git.Status) { s.Unstaged = 1; s.Rebase = git.RebaseDirty })},
		{name: "dirty with autostash", opts: Options{Strategy: StrategyMerge, Autostash: true}, want: ActionMerge, autostash: true,
			st: feature(func(s *git.Status) { s.Unstaged = 1; s.Rebase = git.RebaseDirty })},
		{name: "untracked files don't need autostash", opts: merge, want: ActionMerge, push: true,
			st: feature(func(s *git.Status) { s.Untracked = 3 })},
		{name: "dirty and conflicting", opts: Options{Strategy: StrategyMerge, Autostash: true}, want: ActionSkip, reasonHas: "would conflict",
			st: feature(func(s *git.Status) { s.Unstaged = 1; s.Rebase = git.RebaseDirty; s.ConflictFiles = []string{"a.go"} })},
		{name: "conflicts", opts: merge, want: ActionSkip, reasonHas: "a.go, b.go, c.go and 1 more",
			st: feature(func(s *git.Status) {
				s.Rebase = git.RebaseConflicts
				s.ConflictFiles = []string{"a.go", "b.go", "c.go", "d.go"}
			})},
		{name: "busy", opts: merge, want: ActionSkip, reasonHas: "merge in progress",
			st: feature(func(s *git.Status) { s.Rebase = git.RebaseBusy; s.Operation = "merge" })},
		{name: "detached", opts: merge, want: ActionSkip, reasonHas: "detached",
			st: &git.Status{Detached: true, Rebase: git.RebaseDetached}},
		{name: "no base", opts: merge, want: ActionSkip, reasonHas: "--base",
			st: &git.Status{Branch: "x", Rebase: git.RebaseNoBase}},
		{name: "fetch failed", opts: merge, want: ActionSkip, reasonHas: "fetch failed",
			st: feature(func(s *git.Status) { s.FetchError = "could not read from remote\nmore" })},
		{name: "inspection failed", opts: merge, want: ActionSkip, reasonHas: "inspection failed",
			st: &git.Status{Error: "boom", Rebase: git.RebaseUnknown}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Plan(tt.st, tt.opts)
			if got.Action != tt.want {
				t.Fatalf("Action = %q (%s), want %q", got.Action, got.Reason, tt.want)
			}
			if got.Push != tt.push || got.ForcePush != tt.force {
				t.Errorf("Push=%v ForcePush=%v, want %v/%v", got.Push, got.ForcePush, tt.push, tt.force)
			}
			if got.Autostash != tt.autostash {
				t.Errorf("Autostash = %v, want %v", got.Autostash, tt.autostash)
			}
			if !strings.Contains(got.Reason, tt.reasonHas) {
				t.Errorf("Reason = %q, want it to contain %q", got.Reason, tt.reasonHas)
			}
		})
	}
}

func TestParseStrategy(t *testing.T) {
	for _, s := range []string{"merge", "rebase"} {
		if _, err := ParseStrategy(s); err != nil {
			t.Errorf("ParseStrategy(%q) = %v", s, err)
		}
	}
	if _, err := ParseStrategy("squash"); err == nil {
		t.Error("ParseStrategy(squash) should fail")
	}
}
