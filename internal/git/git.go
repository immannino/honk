// Package git wraps the git CLI for read-mostly inspection of working trees.
//
// It shells out to the git binary rather than using a pure-Go implementation so
// that behaviour (config, credential helpers, hooks, merge machinery) matches
// exactly what the user gets on the command line.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Repo is a handle to a single git working tree.
type Repo struct {
	Path string
}

// ExitError is returned when git runs but exits non-zero.
type ExitError struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = fmt.Sprintf("exit status %d", e.Code)
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// ExitCode returns the git exit code carried by err, or -1 if err is not an *ExitError.
func ExitCode(err error) int {
	if ee, ok := errors.AsType[*ExitError](err); ok {
		return ee.Code
	}
	return -1
}

// baseEnv keeps git non-interactive and avoids taking optional locks so that
// inspecting a repo never interferes with an editor or IDE working in it.
var baseEnv = func() []string {
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
		"GIT_MERGE_AUTOEDIT=no",
	)
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}()

// Run executes git with args inside the repo and returns trimmed stdout.
func (r Repo) Run(ctx context.Context, args ...string) (string, error) {
	return r.RunInput(ctx, "", args...)
}

// RunInput is Run with stdin supplied.
func (r Repo) RunInput(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Path
	cmd.Env = baseEnv
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return strings.TrimRight(stdout.String(), "\n"), &ExitError{
				Args:   args,
				Code:   ee.ExitCode(),
				Stderr: stderr.String(),
			}
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// RefExists reports whether ref resolves to a commit.
func (r Repo) RefExists(ctx context.Context, ref string) bool {
	_, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// Fetch updates remote-tracking refs for the repo's default remote.
func (r Repo) Fetch(ctx context.Context) error {
	_, err := r.Run(ctx, "fetch", "--quiet", "--prune")
	return err
}
