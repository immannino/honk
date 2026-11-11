package app

import (
	"slices"
	"testing"
)

func TestNormalizeArgs(t *testing.T) {
	for _, tt := range []struct{ in, want []string }{
		{[]string{"honk", "-q"}, []string{"honk", "--quiet=REPO"}},
		{[]string{"honk", "-q", "~/code"}, []string{"honk", "--quiet=REPO", "~/code"}},
		{[]string{"honk", "-q", "repo,branch", "~/code"}, []string{"honk", "--quiet=repo,branch", "~/code"}},
		{[]string{"honk", "~/code", "--quiet", "AGE"}, []string{"honk", "~/code", "--quiet=AGE"}},
		{[]string{"honk", "--quiet=BRANCH"}, []string{"honk", "--quiet=BRANCH"}},
		{[]string{"honk", "--dirty"}, []string{"honk", "--dirty"}},
	} {
		if got := NormalizeArgs(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("NormalizeArgs(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
