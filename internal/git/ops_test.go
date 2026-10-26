package git

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// conflictingClone returns a clone on a pushed feature branch that edits the
// same line as a newer commit on origin/main.
func conflictingClone(t *testing.T, f *fixture) string {
	t.Helper()
	dir := f.clone("clash")
	f.git(dir, "checkout", "-qb", "feature")
	f.write(dir, "f.txt", "BRANCH\nb\nc\n")
	f.git(dir, "commit", "-qam", "clash")
	f.advanceMain()
	f.git(dir, "fetch", "-q")
	return dir
}

func TestMergeAndRebaseAbortOnConflict(t *testing.T) {
	ctx := context.Background()
	for _, op := range []string{"merge", "rebase"} {
		t.Run(op, func(t *testing.T) {
			f := newFixture(t)
			repo := Repo{Path: conflictingClone(t, f)}
			before, _ := repo.Head(ctx)

			var err error
			if op == "merge" {
				err = repo.Merge(ctx, "origin/main", false)
			} else {
				err = repo.Rebase(ctx, "origin/main", false)
			}

			ce, ok := errors.AsType[*ConflictError](err)
			if !ok {
				t.Fatalf("err = %v, want *ConflictError", err)
			}
			if !slices.Equal(ce.Files, []string{"f.txt"}) {
				t.Errorf("Files = %v, want [f.txt]", ce.Files)
			}
			s := repo.Inspect(ctx, InspectOptions{})
			after, _ := repo.Head(ctx)
			if s.Operation != "" || s.Dirty() || after != before || s.Branch != "feature" {
				t.Errorf("repo not restored: op=%q dirty=%v head moved=%v branch=%q", s.Operation, s.Dirty(), after != before, s.Branch)
			}
		})
	}
}

func TestMergeCreatesMergeCommitAndPushes(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dir := f.clone("feature")
	f.git(dir, "checkout", "-qb", "feature")
	f.write(dir, "new.txt", "x\n")
	f.git(dir, "add", ".")
	f.git(dir, "commit", "-qm", "feature")
	f.git(dir, "push", "-qu", "origin", "feature")
	f.advanceMain()
	f.git(dir, "fetch", "-q")

	repo := Repo{Path: dir}
	if err := repo.Merge(ctx, "origin/main", false); err != nil {
		t.Fatal(err)
	}
	parents, _ := repo.Run(ctx, "rev-list", "--parents", "-n1", "HEAD")
	if n := len(strings.Fields(parents)); n != 3 {
		t.Errorf("HEAD has %d parents, want a merge commit with 2", n-1)
	}
	msg, _ := repo.Run(ctx, "log", "-1", "--format=%s")
	if want := "Merge branch 'main' of " + strings.TrimSuffix(f.remote, ".git") + " into feature"; msg != want {
		t.Errorf("merge message = %q, want %q", msg, want)
	}

	target, ok := repo.UpstreamOf(ctx, "feature")
	if !ok || target.Remote != "origin" || target.Ref != "refs/heads/feature" {
		t.Fatalf("UpstreamOf = %+v, %v", target, ok)
	}
	if err := repo.Push(ctx, target, false); err != nil {
		t.Fatal(err)
	}
	local, _ := repo.Head(ctx)
	remote, _ := Repo{Path: f.remote}.Run(ctx, "rev-parse", "feature")
	if local != remote {
		t.Errorf("remote feature = %s, want %s", remote, local)
	}
}

func TestForcePushWithLeaseRefusesUnseenCommits(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dir := f.clone("mine")
	f.git(dir, "checkout", "-qb", "feature")
	f.write(dir, "mine.txt", "x\n")
	f.git(dir, "add", ".")
	f.git(dir, "commit", "-qm", "mine")
	f.git(dir, "push", "-qu", "origin", "feature")

	// A coworker pushes to the same branch; we never fetch it.
	other := f.clone("theirs")
	f.git(other, "checkout", "-q", "feature")
	f.write(other, "theirs.txt", "y\n")
	f.git(other, "add", ".")
	f.git(other, "commit", "-qm", "theirs")
	f.git(other, "push", "-q")

	f.git(dir, "commit", "--amend", "-qm", "rewritten")
	repo := Repo{Path: dir}
	target, _ := repo.UpstreamOf(ctx, "feature")
	if err := repo.Push(ctx, target, true); err == nil {
		t.Fatal("force-with-lease push should have been rejected")
	}
}

func TestUpstreamOfMissing(t *testing.T) {
	f := newFixture(t)
	dir := f.clone("local")
	f.git(dir, "checkout", "-qb", "local-only")
	if _, ok := (Repo{Path: dir}).UpstreamOf(context.Background(), "local-only"); ok {
		t.Error("expected no upstream")
	}
}

func TestPullMergeMessageMatchesGitPull(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	mk := func(name string) string {
		dir := f.clone(name)
		f.git(dir, "checkout", "-qb", "feature/x")
		f.write(dir, name+".txt", "x\n")
		f.git(dir, "add", ".")
		f.git(dir, "commit", "-qm", "work")
		return dir
	}
	pulled, merged := mk("pulled"), mk("merged")
	f.advanceMain()

	f.git(pulled, "pull", "-q", "--no-rebase", "--no-edit", "origin", "main")
	want, _ := Repo{Path: pulled}.Run(ctx, "log", "-1", "--format=%B")

	f.git(merged, "fetch", "-q")
	got, err := Repo{Path: merged}.PullMergeMessage(ctx, "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("PullMergeMessage = %q, git pull wrote %q", got, want)
	}

	// With no remote involved, it reads like a plain `git merge main`.
	f.git(merged, "branch", "-f", "main", "origin/main")
	local, err := Repo{Path: merged}.PullMergeMessage(ctx, "main")
	if err != nil || strings.TrimSpace(local) != "Merge branch 'main' into feature/x" {
		t.Errorf("local ref message = %q, %v", local, err)
	}
}

func TestFetchHeadURL(t *testing.T) {
	tests := map[string]string{
		"git@github.com:immannino/honk.git":           "github.com:immannino/honk",
		"https://github.com/immannino/honk.git":       "https://github.com/immannino/honk",
		"https://user:token@github.com/org/repo.git/": "https://github.com/org/repo",
		"ssh://git@github.com/org/repo.git":           "ssh://github.com/org/repo",
		"/srv/git/repo.git":                           "/srv/git/repo",
		"/srv/git/repo/":                              "/srv/git/repo",
	}
	for in, want := range tests {
		if got := fetchHeadURL(in); got != want {
			t.Errorf("fetchHeadURL(%q) = %q, want %q", in, got, want)
		}
	}
}
