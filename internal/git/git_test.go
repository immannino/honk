package git

import (
	"context"
	"strings"
	"testing"
)

// Env changes made after package init (e.g. t.Setenv) must reach git.
func TestRunSeesCurrentEnv(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "honk-env-check")
	t.Setenv("GIT_AUTHOR_EMAIL", "env@example.com")
	out, err := Repo{Path: t.TempDir()}.Run(context.Background(), "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "honk-env-check <env@example.com>") {
		t.Errorf("GIT_AUTHOR_IDENT = %q, want it to use the env set by the test", out)
	}
}
