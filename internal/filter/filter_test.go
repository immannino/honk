package filter

import (
	"testing"
	"time"

	"github.com/immannino/honk/internal/git"
)

func TestParse(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.Local)
	at := func(d time.Duration) *git.Status { return &git.Status{Last: &git.Commit{When: now.Add(-d)}} }
	day := 24 * time.Hour

	tests := []struct {
		expr string
		st   *git.Status
		want bool
	}{
		{"updated > 1d", at(2 * time.Hour), true},
		{"updated > 1d", at(3 * day), false},
		{"updated < 1d", at(3 * day), true},
		{"age > 30d", at(40 * day), true},
		{"age > 30d", at(2 * day), false},
		{"updated > 2026-11-01", at(2 * day), true},
		{"updated > 2026-11-01", at(30 * day), false},
		{"updated > 1d or updated > 2026-01-01", at(30 * day), true},
		{"updated > 1d and age < 1w", at(2 * time.Hour), true},
		{"(updated > 1d or age > 90d) && updated < 1h", at(2 * time.Hour), true},
		{"UPDATED>1d", at(time.Hour), true},
		{"", at(time.Hour), true},
		{"updated > 1d", &git.Status{}, false},
	}
	for _, tt := range tests {
		f, err := Parse(tt.expr)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.expr, err)
		}
		if got := f(tt.st, now); got != tt.want {
			t.Errorf("%q = %v, want %v", tt.expr, got, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, expr := range []string{"updated", "updated >", "updated > soon", "colour > 1d", "age > 2026-01-01", "(updated > 1d", "updated > 1d or", "updated > 1d junk", "updated > 1d & age < 2d", "updated ~ 1d"} {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", expr)
		}
	}
}
