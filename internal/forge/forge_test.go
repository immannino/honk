package forge

import "testing"

func TestDetect(t *testing.T) {
	for url, want := range map[string]Kind{
		"https://github.com/a/b.git":             GitHub,
		"git@github.com:a/b.git":                 GitHub,
		"https://gitlab.com/a/b.git":             GitLab,
		"git@gitlab.example.com:group/sub/b.git": GitLab,
		"ssh://git@gitlab.corp.io:2222/a/b.git":  GitLab,
		"https://example.com/a/b.git":            Unknown,
		"/some/local/path":                       Unknown,
		"https://x.com/gitlab/repo.git":          Unknown, // only the host counts
	} {
		if got := Detect(url); got != want {
			t.Errorf("Detect(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestLabel(t *testing.T) {
	for _, tt := range []struct {
		r    Review
		want string
	}{
		{Review{Kind: GitLab, State: Merged, Number: 12}, "!12 merged"},
		{Review{Kind: GitHub, State: Open, Number: 7, Draft: true}, "#7 open (draft)"},
		{Review{Kind: GitHub, State: None}, "none"},
		{Review{Kind: GitHub, State: None, Error: "boom"}, "?"},
	} {
		if got := tt.r.Label(); got != tt.want {
			t.Errorf("Label() = %q, want %q", got, tt.want)
		}
	}
}

func TestConsiderPrefersOpenThenMerged(t *testing.T) {
	rv := &Review{Kind: GitLab, State: None}
	consider(rv, Closed, 1, "", false)
	consider(rv, Merged, 2, "", false)
	consider(rv, Closed, 3, "", false)
	if rv.Number != 2 {
		t.Fatalf("got !%d, want !2", rv.Number)
	}
	consider(rv, Open, 4, "", false)
	if rv.Number != 4 || rv.State != Open {
		t.Fatalf("got %+v, want open !4", rv)
	}
}
