// Package forge looks up the merge request (GitLab) or pull request (GitHub)
// tied to a branch by shelling out to glab or gh, so honk reuses whatever
// authentication and host configuration those tools already have.
package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Kind identifies the hosting service behind a remote.
type Kind string

const (
	GitHub  Kind = "github"
	GitLab  Kind = "gitlab"
	Unknown Kind = ""
)

// State of a review request.
type State string

const (
	None   State = "none" // no MR/PR exists for the branch
	Open   State = "open"
	Merged State = "merged"
	Closed State = "closed"
)

// Review is the MR or PR associated with a branch.
type Review struct {
	Kind   Kind   `json:"kind"`
	State  State  `json:"state"`
	Number int    `json:"number,omitempty"`
	URL    string `json:"url,omitempty"`
	Draft  bool   `json:"draft,omitempty"`
	// Error is set when the lookup couldn't be done (tool missing, not logged in, offline).
	Error string `json:"error,omitempty"`
}

// Label is the short form shown in tables: "!12 merged" for GitLab, "#12 merged" for GitHub.
func (r *Review) Label() string {
	if r.Error != "" {
		return "?"
	}
	if r.State == None {
		return "none"
	}
	sigil := "#"
	if r.Kind == GitLab {
		sigil = "!"
	}
	label := fmt.Sprintf("%s%d %s", sigil, r.Number, r.State)
	if r.Draft {
		label += " (draft)"
	}
	return label
}

// Detect works out the hosting service from a remote URL. Hosts containing
// "gitlab" (including self-hosted ones such as gitlab.example.com) are GitLab.
func Detect(remoteURL string) Kind {
	host := strings.ToLower(hostOf(remoteURL))
	switch {
	case strings.Contains(host, "gitlab"):
		return GitLab
	case strings.Contains(host, "github"):
		return GitHub
	}
	return Unknown
}

func hostOf(remote string) string {
	if strings.Contains(remote, "://") {
		if u, err := url.Parse(remote); err == nil {
			return u.Hostname()
		}
	}
	// scp-like: [user@]host:path
	if i := strings.Index(remote, ":"); i > 0 {
		host := remote[:i]
		if j := strings.LastIndex(host, "@"); j >= 0 {
			host = host[j+1:]
		}
		return host
	}
	return ""
}

// Lookup finds the MR/PR whose source branch is branch, for the repo in dir.
// When several exist it prefers open, then merged, then closed.
func Lookup(ctx context.Context, dir string, kind Kind, branch string) *Review {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	rv := &Review{Kind: kind, State: None}
	var err error
	switch kind {
	case GitLab:
		err = lookupGitLab(ctx, dir, branch, rv)
	case GitHub:
		err = lookupGitHub(ctx, dir, branch, rv)
	default:
		return nil
	}
	if err != nil {
		rv.Error = err.Error()
	}
	return rv
}

var rank = map[State]int{None: 0, Closed: 1, Merged: 2, Open: 3}

func consider(rv *Review, state State, number int, url string, draft bool) {
	if rank[state] > rank[rv.State] {
		*rv = Review{Kind: rv.Kind, State: state, Number: number, URL: url, Draft: draft}
	}
}

func lookupGitLab(ctx context.Context, dir, branch string, rv *Review) error {
	out, err := run(ctx, dir, "glab", "mr", "list", "--source-branch", branch, "--all", "-F", "json")
	if err != nil {
		return err
	}
	var mrs []struct {
		IID    int    `json:"iid"`
		State  string `json:"state"`
		WebURL string `json:"web_url"`
		Draft  bool   `json:"draft"`
	}
	if err := json.Unmarshal(out, &mrs); err != nil {
		return fmt.Errorf("parsing glab output: %w", err)
	}
	for _, m := range mrs {
		st := Closed
		switch m.State {
		case "opened":
			st = Open
		case "merged":
			st = Merged
		}
		consider(rv, st, m.IID, m.WebURL, m.Draft)
	}
	return nil
}

func lookupGitHub(ctx context.Context, dir, branch string, rv *Review) error {
	out, err := run(ctx, dir, "gh", "pr", "list", "--head", branch, "--state", "all",
		"--limit", "20", "--json", "number,state,url,isDraft")
	if err != nil {
		return err
	}
	var prs []struct {
		Number  int    `json:"number"`
		State   string `json:"state"`
		URL     string `json:"url"`
		IsDraft bool   `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return fmt.Errorf("parsing gh output: %w", err)
	}
	for _, p := range prs {
		st := Closed
		switch p.State {
		case "OPEN":
			st = Open
		case "MERGED":
			st = Merged
		}
		consider(rv, st, p.Number, p.URL, p.IsDraft)
	}
	return nil
}

func run(ctx context.Context, dir, tool string, args ...string) ([]byte, error) {
	if _, err := exec.LookPath(tool); err != nil {
		return nil, fmt.Errorf("%s not installed", tool)
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_PROMPT=1", "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: %w", tool, ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		msg, _, _ = strings.Cut(msg, "\n")
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", tool, msg)
	}
	return stdout.Bytes(), nil
}
