package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Mutating operations. Each one leaves the repo as it found it on failure: a
// merge or rebase that stops part-way is aborted before returning.

// Head returns the full commit id of HEAD.
func (r Repo) Head(ctx context.Context) (string, error) {
	return r.Run(ctx, "rev-parse", "HEAD")
}

// ConflictError reports a merge or rebase that stopped on conflicts and was aborted.
type ConflictError struct {
	Op    string
	Files []string
	Err   error
}

func (e *ConflictError) Error() string {
	return e.Op + " stopped on conflicts in " + strings.Join(e.Files, ", ") + " (aborted)"
}

func (e *ConflictError) Unwrap() error { return e.Err }

// Merge merges ref into the current branch. It's the `git pull origin <base>`
// workflow minus the fetch, down to the merge commit message (see PullMergeMessage).
func (r Repo) Merge(ctx context.Context, ref string, autostash bool) error {
	args := []string{"merge", "--no-edit"}
	if msg, err := r.PullMergeMessage(ctx, ref); err == nil && msg != "" {
		args = append(args, "-m", msg)
	}
	if autostash {
		args = append(args, "--autostash")
	}
	if _, err := r.Run(ctx, append(args, ref)...); err != nil {
		return r.abort(ctx, "merge", err)
	}
	return nil
}

// PullMergeMessage returns the message `git pull <remote> <branch>` would write
// when merging ref, e.g. "Merge branch 'main' of github.com:org/repo into feature".
// For a remote-tracking ref it builds the FETCH_HEAD line that pull would have
// produced and hands it to `git fmt-merge-msg`, so merge.log, merge.suppressDest
// and friends are honored just as they are by pull.
func (r Repo) PullMergeMessage(ctx context.Context, ref string) (string, error) {
	sha, err := r.Run(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}

	branch, url := ref, "."
	out, _ := r.Run(ctx, "remote")
	var remote string
	for _, name := range strings.Fields(out) {
		if strings.HasPrefix(ref, name+"/") && len(name) > len(remote) {
			remote = name
		}
	}
	if remote != "" {
		u, err := r.Run(ctx, "remote", "get-url", remote)
		if err != nil {
			return "", err
		}
		branch, url = strings.TrimPrefix(ref, remote+"/"), fetchHeadURL(u)
	}

	line := fmt.Sprintf("%s\t\tbranch '%s' of %s\n", sha, branch, url)
	return r.RunInput(ctx, line, "fmt-merge-msg")
}

// fetchHeadURL shortens a remote URL the way `git fetch` does when writing
// FETCH_HEAD: credentials are dropped, then trailing slashes and one ".git".
func fetchHeadURL(u string) string {
	if scheme, rest, ok := strings.Cut(u, "://"); ok {
		host, path, _ := strings.Cut(rest, "/")
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		u = scheme + "://" + host
		if path != "" {
			u += "/" + path
		}
	} else if at, colon := strings.Index(u, "@"), strings.Index(u, ":"); at >= 0 && colon > at {
		u = u[at+1:] // scp-style user@host:path
	}
	u = strings.TrimRight(u, "/")
	return strings.TrimSuffix(u, ".git")
}

// FastForward moves the current branch to ref, failing if that isn't a fast-forward.
func (r Repo) FastForward(ctx context.Context, ref string, autostash bool) error {
	args := []string{"merge", "--ff-only"}
	if autostash {
		args = append(args, "--autostash")
	}
	_, err := r.Run(ctx, append(args, ref)...)
	return err
}

// Rebase replays the current branch's commits on top of ref.
func (r Repo) Rebase(ctx context.Context, ref string, autostash bool) error {
	args := []string{"rebase"}
	if autostash {
		args = append(args, "--autostash")
	}
	if _, err := r.Run(ctx, append(args, ref)...); err != nil {
		return r.abort(ctx, "rebase", err)
	}
	return nil
}

// abort backs out of a stopped merge or rebase, capturing the conflicted paths first.
func (r Repo) abort(ctx context.Context, op string, cause error) error {
	if r.operation(ctx) != op {
		// Failed before starting (e.g. a hook or a precondition) — nothing to undo.
		return cause
	}
	out, _ := r.Run(ctx, "diff", "--name-only", "--diff-filter=U")
	var files []string
	for _, f := range strings.Split(out, "\n") {
		if f != "" {
			files = append(files, f)
		}
	}
	if _, err := r.Run(ctx, op, "--abort"); err != nil {
		return errors.Join(cause, err)
	}
	return &ConflictError{Op: op, Files: files, Err: cause}
}

// PushTarget is where a branch pushes to: its configured remote and remote branch.
type PushTarget struct {
	Remote string
	Ref    string // e.g. refs/heads/feature/x
}

// UpstreamOf returns the push target configured for branch, or false if it has no upstream.
func (r Repo) UpstreamOf(ctx context.Context, branch string) (PushTarget, bool) {
	remote, _ := r.Run(ctx, "config", "--get", "branch."+branch+".remote")
	ref, _ := r.Run(ctx, "config", "--get", "branch."+branch+".merge")
	if remote == "" || remote == "." || ref == "" {
		return PushTarget{}, false
	}
	return PushTarget{Remote: remote, Ref: ref}, true
}

// Push pushes HEAD to target. With force it uses --force-with-lease, which refuses
// to overwrite remote commits that the local remote-tracking ref hasn't seen.
func (r Repo) Push(ctx context.Context, target PushTarget, force bool) error {
	args := []string{"push", "--quiet"}
	if force {
		args = append(args, "--force-with-lease")
	}
	_, err := r.Run(ctx, append(args, target.Remote, "HEAD:"+target.Ref)...)
	return err
}
