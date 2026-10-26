package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// fixture is a bare "remote" plus helpers for making clones in specific states.
type fixture struct {
	t      *testing.T
	dir    string
	remote string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "test", "GIT_COMMITTER_EMAIL": "test@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}

	f := &fixture{t: t, dir: t.TempDir()}
	f.remote = filepath.Join(f.dir, "remote.git")
	f.git(f.dir, "init", "--bare", "-b", "main", f.remote)

	seed := f.clone("seed")
	f.write(seed, "f.txt", "a\nb\nc\n")
	f.git(seed, "add", ".")
	f.git(seed, "commit", "-m", "initial")
	f.git(seed, "push", "origin", "main")
	return f
}

func (f *fixture) git(dir string, args ...string) {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (f *fixture) write(dir, name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) clone(name string) string {
	f.t.Helper()
	dir := filepath.Join(f.dir, name)
	f.git(f.dir, "clone", "-q", f.remote, dir)
	return dir
}

// advanceMain pushes a commit to the remote's main that changes line 1 of f.txt.
func (f *fixture) advanceMain() {
	f.t.Helper()
	seed := filepath.Join(f.dir, "seed")
	f.write(seed, "f.txt", "MAIN\nb\nc\n")
	f.git(seed, "commit", "-qam", "main moves on")
	f.git(seed, "push", "-q")
}

func TestInspect(t *testing.T) {
	f := newFixture(t)

	upToDate := f.clone("uptodate")

	behind := f.clone("behind")

	ready := f.clone("ready")
	f.git(ready, "checkout", "-qb", "feature/ok")
	f.write(ready, "new.txt", "x\n")
	f.git(ready, "add", ".")
	f.git(ready, "commit", "-qm", "feature")

	conflict := f.clone("conflict")
	f.git(conflict, "checkout", "-qb", "feature/clash")
	f.write(conflict, "f.txt", "BRANCH\nb\nc\n")
	f.git(conflict, "commit", "-qam", "clash")

	dirty := f.clone("dirty")
	f.git(dirty, "checkout", "-qb", "wip")
	f.write(dirty, "f.txt", "a\nb\nc\nd\n")
	f.write(dirty, "untracked.txt", "?\n")

	detached := f.clone("detached")
	f.git(detached, "checkout", "-q", "--detach")

	f.advanceMain()
	// uptodate pulls; the others remain on the old main until fetched.
	f.git(upToDate, "pull", "-q")

	ctx := context.Background()
	tests := []struct {
		name       string
		path       string
		want       RebaseState
		baseBehind int
		check      func(t *testing.T, s *Status)
	}{
		{name: "up to date", path: upToDate, want: RebaseUpToDate},
		{name: "fast-forward", path: behind, want: RebaseFastForward, baseBehind: 1, check: func(t *testing.T, s *Status) {
			if s.Behind != 1 || s.Upstream != "origin/main" {
				t.Errorf("upstream = %q behind %d, want origin/main behind 1", s.Upstream, s.Behind)
			}
		}},
		{name: "ready", path: ready, want: RebaseReady, baseBehind: 1},
		{name: "conflicts", path: conflict, want: RebaseConflicts, baseBehind: 1, check: func(t *testing.T, s *Status) {
			if !slices.Equal(s.ConflictFiles, []string{"f.txt"}) {
				t.Errorf("ConflictFiles = %v, want [f.txt]", s.ConflictFiles)
			}
		}},
		{name: "dirty", path: dirty, want: RebaseDirty, baseBehind: 1, check: func(t *testing.T, s *Status) {
			if s.Unstaged != 1 || s.Untracked != 1 {
				t.Errorf("unstaged=%d untracked=%d, want 1/1", s.Unstaged, s.Untracked)
			}
			// Prediction covers committed history only; the uncommitted edit isn't considered.
			if len(s.ConflictFiles) != 0 {
				t.Errorf("ConflictFiles = %v, want none", s.ConflictFiles)
			}
		}},
		{name: "detached", path: detached, want: RebaseDetached, check: func(t *testing.T, s *Status) {
			if !s.Detached || s.Branch != "" {
				t.Errorf("Detached=%v Branch=%q", s.Detached, s.Branch)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fetch so every clone sees the advanced main.
			s := Repo{Path: tt.path}.Inspect(ctx, InspectOptions{Fetch: true})
			if s.Error != "" || s.FetchError != "" {
				t.Fatalf("error=%q fetchError=%q", s.Error, s.FetchError)
			}
			if s.Rebase != tt.want {
				t.Errorf("Rebase = %q, want %q", s.Rebase, tt.want)
			}
			if s.BaseBehind != tt.baseBehind {
				t.Errorf("BaseBehind = %d, want %d", s.BaseBehind, tt.baseBehind)
			}
			if s.Last == nil || s.Last.Hash == "" {
				t.Errorf("Last commit missing")
			}
			if tt.check != nil {
				tt.check(t, s)
			}
		})
	}
}

func TestInspectMergeInProgress(t *testing.T) {
	f := newFixture(t)
	busy := f.clone("busy")
	f.git(busy, "checkout", "-qb", "feature")
	f.write(busy, "f.txt", "BRANCH\nb\nc\n")
	f.git(busy, "commit", "-qam", "clash")
	f.advanceMain()
	f.git(busy, "fetch", "-q")
	// Expected to fail with a conflict, leaving MERGE_HEAD behind.
	_ = exec.Command("git", "-C", busy, "merge", "origin/main").Run()

	s := Repo{Path: busy}.Inspect(context.Background(), InspectOptions{})
	if s.Operation != "merge" || s.Rebase != RebaseBusy || s.Conflicted != 1 {
		t.Errorf("Operation=%q Rebase=%q Conflicted=%d, want merge/busy/1", s.Operation, s.Rebase, s.Conflicted)
	}
}

func TestInspectBaseOverride(t *testing.T) {
	f := newFixture(t)
	seed := filepath.Join(f.dir, "seed")
	f.git(seed, "push", "-q", "origin", "main:develop")

	repo := f.clone("repo")
	f.git(repo, "checkout", "-qb", "feature")
	ctx := context.Background()

	if s := (Repo{Path: repo}).Inspect(ctx, InspectOptions{Base: "develop"}); s.Base != "origin/develop" {
		t.Errorf("Base = %q, want origin/develop", s.Base)
	}
	if s := (Repo{Path: repo}).Inspect(ctx, InspectOptions{Base: "missing"}); s.Rebase != RebaseNoBase {
		t.Errorf("Rebase = %q, want no-base", s.Rebase)
	}
}

func TestInspectNotARepo(t *testing.T) {
	s := Repo{Path: t.TempDir()}.Inspect(context.Background(), InspectOptions{})
	if s.Error == "" {
		t.Error("expected an error for a non-repo directory")
	}
}

func TestInspectUpstreamNew(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	dir := f.clone("mine")
	f.git(dir, "checkout", "-qb", "feature")
	f.write(dir, "mine.txt", "x\n")
	f.git(dir, "add", ".")
	f.git(dir, "commit", "-qm", "mine")
	f.git(dir, "push", "-qu", "origin", "feature")
	f.advanceMain()
	f.git(dir, "fetch", "-q")

	// Rebasing rewrites the pushed commit; upstream's copy has a local equivalent.
	f.git(dir, "rebase", "-q", "origin/main")
	s := Repo{Path: dir}.Inspect(ctx, InspectOptions{})
	if s.Behind != 1 || s.UpstreamNew != 0 {
		t.Fatalf("after rebase: Behind=%d UpstreamNew=%d, want 1/0", s.Behind, s.UpstreamNew)
	}

	// A coworker's commit on the branch has no local equivalent.
	other := f.clone("theirs")
	f.git(other, "checkout", "-q", "feature")
	f.write(other, "theirs.txt", "y\n")
	f.git(other, "add", ".")
	f.git(other, "commit", "-qm", "theirs")
	f.git(other, "push", "-q")
	f.git(dir, "fetch", "-q")
	s = Repo{Path: dir}.Inspect(ctx, InspectOptions{})
	if s.Behind != 2 || s.UpstreamNew != 1 {
		t.Fatalf("after coworker push: Behind=%d UpstreamNew=%d, want 2/1", s.Behind, s.UpstreamNew)
	}
}
